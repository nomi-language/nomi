package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// An anonymous struct literal checked against an anonymous struct type that a
// signature or annotation writes: each label is a reference to the field the
// type names, as is a field access on a value of that type. A return type,
// a parameter type and a binding annotation.
const anonLabelsSrc = `import std/io

fn make(context: Context): {logger: String, context: Context} {
  {logger: "x", context}
}

fn show(p: {name: String, age: Int}): String {
  p.name
}

fn main() {
  _ = make(Context.root())
  io.print(show({name: "a", age: 3}))
  pt: {x: Int, y: Int} = {x: 1, y: 2}
  io.print(pt.y)
  age = 4
  io.print(show({name: "b", age}))
}
`

// at returns the 0-based position of the nth (1-based) `needle` on 0-based
// line `line` of src.
func at(t *testing.T, src string, line int, needle string, nth int) protocol.Position {
	t.Helper()
	text := strings.Split(src, "\n")[line]
	off := -1
	for range nth {
		next := strings.Index(text[off+1:], needle)
		if next < 0 {
			t.Fatalf("line %d %q has fewer than %d occurrences of %q", line, text, nth, needle)
		}
		off += 1 + next
	}
	return protocol.Position{Line: uint32(line), Character: uint32(off)}
}

// span renders a position and name as referencesAt does.
func span(p protocol.Position, name string) string {
	return itoa(p.Line) + ":" + itoa(p.Character) + "-" + itoa(p.Character+uint32(len(name)))
}

func sorted(xs ...string) []string {
	sort.Strings(xs)
	return xs
}

// renameAt renames the symbol at pos in src (written as a project's
// main.nomi) and returns each edit as "line:col-end=>text", sorted.
func renameAt(t *testing.T, src string, pos protocol.Position, newName string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	server := NewServer()
	server.docs.Open(uri, src)
	edit, err := server.textDocumentRename(nil, &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos},
		NewName:                    newName,
	})
	if err != nil {
		t.Fatalf("rename at L%dC%d: %v", pos.Line, pos.Character, err)
	}
	if edit == nil {
		return nil
	}
	var got []string
	for u, es := range edit.Changes {
		if string(u) != uri {
			t.Errorf("rename edits another file: %s", u)
		}
		for _, e := range es {
			got = append(got, itoa(e.Range.Start.Line)+":"+itoa(e.Range.Start.Character)+"-"+itoa(e.Range.End.Character)+"=>"+e.NewText)
		}
	}
	sort.Strings(got)
	return got
}

func TestAnonLiteralLabels_ReferenceTheAnnotatedField(t *testing.T) {
	src := anonLabelsSrc
	for _, c := range []struct {
		what  string
		name  string
		sites []protocol.Position // every occurrence, the declaration first
	}{
		{"the return type's `logger`", "logger", []protocol.Position{at(t, src, 2, "logger", 1), at(t, src, 3, "logger", 1)}},
		{"the parameter's `name`", "name", []protocol.Position{at(t, src, 6, "name", 1), at(t, src, 7, "name", 1), at(t, src, 12, "name", 1), at(t, src, 16, "name", 1)}},
		{"the parameter's `age`", "age", []protocol.Position{at(t, src, 6, "age", 1), at(t, src, 12, "age", 1), at(t, src, 16, "age", 1)}},
		{"the binding's `y`", "y", []protocol.Position{at(t, src, 13, "y", 1), at(t, src, 13, "y", 2), at(t, src, 14, "y", 1)}},
	} {
		var want []string
		for _, p := range c.sites {
			want = append(want, span(p, c.name))
		}
		want = sorted(want...)
		// From the declaration and from each literal label. The punned `age`
		// token is the variable's read first; see the punned test.
		for i, start := range c.sites {
			if c.name == "age" && start.Line == 16 {
				continue
			}
			if got := referencesAt(t, src, start); strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("references to %s from site %d = %v, want %v", c.what, i, got, want)
			}
		}
		var wantEdits []string
		for _, p := range c.sites {
			text := "sink"
			if p.Line == 16 && c.name == "age" {
				text = "sink: age" // the punned label keeps reading `age`
			}
			wantEdits = append(wantEdits, span(p, c.name)+"=>"+text)
		}
		wantEdits = sorted(wantEdits...)
		for i, start := range c.sites {
			if c.name == "age" && start.Line == 16 {
				continue // the punned token renames the variable; see below
			}
			if got := renameAt(t, src, start, "sink"); strings.Join(got, " ") != strings.Join(wantEdits, " ") {
				t.Errorf("rename of %s from site %d = %v, want %v", c.what, i, got, wantEdits)
			}
		}
	}
}

// A punned label, `{..., context}`, is both the field's label and a read of
// the variable. References and rename reach it from either; rename unpuns it.
func TestAnonLiteralLabels_PunnedLabelIsFieldAndVariable(t *testing.T) {
	src := anonLabelsSrc
	param := at(t, src, 2, "context", 1)
	field := at(t, src, 2, "context", 2)
	label := at(t, src, 3, "context", 1)

	if got, want := referencesAt(t, src, field), sorted(span(field, "context"), span(label, "context")); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references to the `context` field = %v, want %v", got, want)
	}
	if got, want := referencesAt(t, src, param), sorted(span(param, "context"), span(label, "context")); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references to the `context` parameter = %v, want %v", got, want)
	}
	if got, want := renameAt(t, src, field, "ctx"), sorted(span(field, "context")+"=>ctx", span(label, "context")+"=>ctx: context"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rename of the `context` field = %v, want %v", got, want)
	}
	if got, want := renameAt(t, src, param, "c"), sorted(span(param, "context")+"=>c", span(label, "context")+"=>context: c"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rename of the `context` parameter = %v, want %v", got, want)
	}
	// From the punned token itself, rename takes the variable.
	if got, want := renameAt(t, src, label, "c"), sorted(span(param, "context")+"=>c", span(label, "context")+"=>context: c"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rename from the punned `context` = %v, want %v", got, want)
	}

	// The punned `age` argument renames the variable the same way.
	ageVar := at(t, src, 15, "age", 1)
	ageUse := at(t, src, 16, "age", 1)
	if got, want := renameAt(t, src, ageVar, "years"), sorted(span(ageVar, "age")+"=>years", span(ageUse, "age")+"=>age: years"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rename of the `age` binding = %v, want %v", got, want)
	}
}

// Hover on a label shows the field the type declares, and go-to-definition
// lands on it.
func TestAnonLiteralLabels_HoverAndDefinition(t *testing.T) {
	src := anonLabelsSrc
	uri := "file:///anon_labels.nomi"
	server := NewServer()
	server.docs.Open(uri, src)
	for _, c := range []struct {
		label, decl protocol.Position
		name, hover string
	}{
		{at(t, src, 3, "logger", 1), at(t, src, 2, "logger", 1), "logger", "```nomi\nlogger: String\n```"},
		{at(t, src, 12, "age", 1), at(t, src, 6, "age", 1), "age", "```nomi\nage: Int\n```"},
		{at(t, src, 13, "y", 2), at(t, src, 13, "y", 1), "y", "```nomi\ny: Int\n```"},
	} {
		if got := appFieldHover(t, server, uri, c.label); got != c.hover {
			t.Errorf("hover on the `%s` label = %q, want %q", c.name, got, c.hover)
		}
		locs := appFieldDefinitions(t, server, uri, c.label)
		wantDefinitions(t, "`"+c.name+"` label", locs, uri, c.name, [2]uint32{c.decl.Line, c.decl.Character})
	}
}

// A literal with no declared anonymous type declares its own fields: its
// labels reference nothing else.
func TestAnonLiteralLabels_UntypedLiteralDeclaresItsOwn(t *testing.T) {
	src := `import std/io

fn main() {
  a = {x: 1}
  b: {x: Int} = {x: 2}
  io.print(a.x + b.x)
}
`
	own := at(t, src, 3, "x", 1)
	if got := referencesAt(t, src, own); len(got) != 1 || got[0] != span(own, "x") {
		t.Errorf("references from an untyped literal's label = %v, want only itself", got)
	}
}
