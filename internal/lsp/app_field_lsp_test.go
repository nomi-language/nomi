package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestAppFieldHoverAndDefinition(t *testing.T) {
	src := `
struct Services {
  context: Context
 port: Int }
fn boot(): Services { Services{context: Context.root(), port: 3} }
fn main() {
 with Services.port = 4
 _ = Services.context
}
`
	uri := "file:///app_field.nomi"
	server := NewServer()
	server.docs.Open(uri, src)
	for _, name := range []string{"port", "context"} {
		pos := appFieldCursor(t, src, "Services."+name, 1)
		if got := appFieldHover(t, server, uri, pos); !strings.Contains(got, name) {
			t.Fatalf("hover %s: %q", name, got)
		}
		result, err := server.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
		if err != nil || result == nil {
			t.Fatalf("definition %s: %v %v", name, result, err)
		}
		if name == "port" {
			loc, ok := result.(*protocol.Location)
			if !ok || loc.Range.Start.Line != 3 {
				t.Fatalf("app field definition must target the field declaration, got %#v", result)
			}
		}
	}
}

// appFieldDefinition drives the real definition entry point and returns the
// single location it answers, failing when there is none.
func appFieldDefinition(t *testing.T, s *Server, uri string, pos protocol.Position) protocol.Location {
	t.Helper()
	result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil {
		t.Fatalf("definition at L%dC%d: %v", pos.Line, pos.Character, err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok || loc == nil {
		t.Fatalf("definition at L%dC%d = %#v, want one location", pos.Line, pos.Character, result)
	}
	return *loc
}

// wantDefinitionAt asserts a definition lands on `name` at 0-based line and
// column, spanning the name.
func wantDefinitionAt(t *testing.T, what string, loc protocol.Location, uri string, line, col uint32, name string) {
	t.Helper()
	if string(loc.URI) != uri || loc.Range.Start.Line != line || loc.Range.Start.Character != col || loc.Range.End.Character != col+uint32(len(name)) {
		t.Fatalf("%s: definition = %s %d:%d-%d, want %s %d:%d-%d", what, loc.URI, loc.Range.Start.Line, loc.Range.Start.Character, loc.Range.End.Character, uri, line, col, col+uint32(len(name)))
	}
}

// The tour's capabilities-and-context example, with a field doc comment: the
// read sits inside string interpolation. The field name is the field; the
// type name is the type.
func TestAppFieldHoverAndDefinition_InInterpolation(t *testing.T) {
	src := `import std/io

/// The running application.
struct App {
  /// The port the server listens on.
  port: Int
  context: Context
}

fn boot(): App {
  App{port: 8080, context: Context.root()}
}

fn main() {
  io.print("listening on :${App.port}")
}
`
	uri := "file:///app_named.nomi"
	server := NewServer()
	server.docs.Open(uri, src)
	name := appFieldCursor(t, src, "App.port", 1)
	owner := appOwnerCursor(t, src, "App.port", 1)

	want := "```nomi\nport: Int\n```\n\n*app field of* `App`\n\nThe port the server listens on."
	if got := appFieldHover(t, server, uri, name); got != want {
		t.Errorf("hover on `port` of `App.port` = %q, want %q", got, want)
	}
	wantDefinitionAt(t, "`port` of `App.port`", appFieldDefinition(t, server, uri, name), uri, 5, 2, "port")

	// The type name hovers exactly as the `App` in boot's return type does.
	typeName := protocol.Position{Line: 9, Character: uint32(strings.Index(strings.Split(src, "\n")[9], "): App") + 3)}
	wantApp := appFieldHover(t, server, uri, typeName)
	if !strings.HasPrefix(wantApp, "```nomi\nstruct App {") || !strings.HasSuffix(wantApp, "The running application.") {
		t.Fatalf("CONTROL: hover on the type name `App` = %q, want the struct and its doc", wantApp)
	}
	if got := appFieldHover(t, server, uri, owner); got != wantApp {
		t.Errorf("hover on `App` of `App.port` = %q, want the type name's hover %q", got, wantApp)
	}
	wantHoverRange(t, "`App`", server, uri, owner, owner.Character, uint32(len("App")))
	wantDefinitionAt(t, "`App` of `App.port`", appFieldDefinition(t, server, uri, owner), uri, 3, 7, "App")
}

// A `with` target and a read passed as a call argument resolve like any other
// read. Renaming the field edits every read and the target; renaming the type
// edits the owner of each.
func TestAppFieldHoverAndDefinition_WithTargetAndCallArgument(t *testing.T) {
	src := `import std/io

struct Logger {
  prefix: String
}

impl Logger {
  pub fn log(logger: Logger, m: String) {
    io.print(logger.prefix ++ m)
  }
}

struct App {
  logger: Logger
  context: Context
}

fn boot(): App {
  App{logger: Logger{prefix: "a"}, context: Context.root()}
}

fn main() {
  with App.logger = Logger{prefix: "b"}
  Logger.log(App.logger, "m")
}
`
	uri := "file:///app_replace.nomi"
	server := NewServer()
	server.docs.Open(uri, src)
	want := "```nomi\nlogger: Logger\n```\n\n*app field of* `App`"
	for idx, site := range []string{"with target", "call argument"} {
		name := appFieldCursor(t, src, "App.logger", idx+1)
		if got := appFieldHover(t, server, uri, name); got != want {
			t.Errorf("hover on the %s `App.logger` = %q, want %q", site, got, want)
		}
		wantDefinitionAt(t, site+" `logger`", appFieldDefinition(t, server, uri, name), uri, 13, 2, "logger")
		owner := appOwnerCursor(t, src, "App.logger", idx+1)
		if got := appFieldHover(t, server, uri, owner); !strings.HasPrefix(got, "```nomi\nstruct App {") {
			t.Errorf("hover on `App` of the %s = %q, want the `App` struct", site, got)
		}
		wantDefinitionAt(t, site+" `App`", appFieldDefinition(t, server, uri, owner), uri, 12, 7, "App")
	}

	doc := server.docs.Snapshot(uri)
	target := appFieldCursor(t, src, "App.logger", 1)
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: int(target.Line) + 1, Col: int(target.Character) + 1})
	if sym == nil {
		t.Fatal("no symbol at the with target's `logger`")
	}
	edits := buildRenameEdits(doc.Analysis, sym, symbolIdentity(sym), "log_sink")
	got := editStarts(edits)
	wantStarts := []string{"13:2", "18:6", posString(appFieldCursor(t, src, "App.logger", 1)), posString(appFieldCursor(t, src, "App.logger", 2))}
	sort.Strings(wantStarts)
	if strings.Join(got, " ") != strings.Join(wantStarts, " ") {
		t.Errorf("field rename edits at %v, want %v", got, wantStarts)
	}

	appSym := doc.Analysis.SymbolAt(analysis.Pos{Line: 13, Col: 8})
	if appSym == nil || appSym.Name != "App" {
		t.Fatalf("symbol at `struct App` = %v, want App", appSym)
	}
	typeEdits := editStarts(buildRenameEdits(doc.Analysis, appSym, symbolIdentity(appSym), "Application"))
	for _, at := range []protocol.Position{appOwnerCursor(t, src, "App.logger", 1), appOwnerCursor(t, src, "App.logger", 2)} {
		found := false
		for _, e := range typeEdits {
			if e == posString(at) {
				found = true
			}
		}
		if !found {
			t.Errorf("renaming `App` edits %v, missing the owner at %s", typeEdits, posString(at))
		}
	}
}

// Rename from the field name renames the field; from the type name, the type.
func TestAppFieldRename_FieldAndType(t *testing.T) {
	src := `struct App {
  port: Int
  context: Context
}

fn boot(): App {
  App{port: 8080, context: Context.root()}
}

fn main() {
  _ = App.port
}
`
	// A real project directory: rename scans the project root for files,
	// and main.nomi marks this one as the root.
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	server := NewServer()
	server.docs.Open(uri, src)
	rename := func(pos protocol.Position, newName string) *protocol.WorkspaceEdit {
		t.Helper()
		edit, err := server.textDocumentRename(nil, &protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos},
			NewName:                    newName,
		})
		if err != nil {
			t.Fatalf("rename at L%dC%d: %v", pos.Line, pos.Character, err)
		}
		return edit
	}
	if edit := rename(appFieldCursor(t, src, "App.port", 1), "listen_port"); edit == nil || len(edit.Changes[protocol.DocumentUri(uri)]) != 3 {
		t.Fatalf("rename on `port` of `App.port` = %v, want 3 edits", edit)
	}
	if edit := rename(appOwnerCursor(t, src, "App.port", 1), "Application"); edit == nil || len(edit.Changes[protocol.DocumentUri(uri)]) != 4 {
		t.Fatalf("rename on `App` of `App.port` = %v, want 4 edits", edit)
	}
}

// editStarts is each edit's start as "line:col", sorted.
func editStarts(edits []protocol.TextEdit) []string {
	out := make([]string, 0, len(edits))
	for _, e := range edits {
		out = append(out, posString(e.Range.Start))
	}
	sort.Strings(out)
	return out
}

func posString(p protocol.Position) string {
	return strings.Join([]string{itoa(p.Line), itoa(p.Character)}, ":")
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// wantHoverRange asserts the hover at pos highlights one line from column col
// for length columns.
func wantHoverRange(t *testing.T, what string, s *Server, uri string, pos protocol.Position, col, length uint32) {
	t.Helper()
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil || res == nil || res.Range == nil {
		t.Fatalf("hover %s: %v %v", what, res, err)
	}
	r := *res.Range
	if r.Start.Line != pos.Line || r.End.Line != pos.Line || r.Start.Character != col || r.End.Character != col+length {
		t.Errorf("hover %s range = %d:%d-%d:%d, want %d:%d-%d", what, r.Start.Line, r.Start.Character, r.End.Line, r.End.Character, pos.Line, col, col+length)
	}
}

// appOwnerCursor is the 0-based position of the idx'th occurrence of read
// (`App.port`): its type name.
func appOwnerCursor(t *testing.T, src, read string, idx int) protocol.Position {
	t.Helper()
	off := -1
	for range idx {
		next := strings.Index(src[off+1:], read)
		if next < 0 {
			t.Fatalf("source has fewer than %d occurrences of %q", idx, read)
		}
		off += 1 + next
	}
	line := strings.Count(src[:off], "\n")
	lineStart := strings.LastIndex(src[:off], "\n") + 1
	return protocol.Position{Line: uint32(line), Character: uint32(off - lineStart)}
}

// appFieldCursor is the position of the field name after the dot of the
// idx'th occurrence of read.
func appFieldCursor(t *testing.T, src, read string, idx int) protocol.Position {
	t.Helper()
	pos := appOwnerCursor(t, src, read, idx)
	pos.Character += uint32(strings.Index(read, ".") + 1)
	return pos
}

// appFieldHover drives the real hover entry point and returns the rendered
// markup, or "" when the server would publish no hover.
func appFieldHover(t *testing.T, s *Server, uri string, pos protocol.Position) string {
	t.Helper()
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil {
		t.Fatalf("hover at L%dC%d: %v", pos.Line, pos.Character, err)
	}
	if res == nil {
		return ""
	}
	markup, ok := res.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("hover contents are %T, want protocol.MarkupContent", res.Contents)
	}
	return markup.Value
}

func appStructLitLabelCursor(t *testing.T, src, name string, idx int) protocol.Position {
	t.Helper()
	needle := name + ":"
	off := -1
	for range idx {
		next := strings.Index(src[off+1:], needle)
		if next < 0 {
			t.Fatalf("source has fewer than %d occurrences of %q", idx, needle)
		}
		off += 1 + next
	}
	line := strings.Count(src[:off], "\n")
	lineStart := strings.LastIndex(src[:off], "\n") + 1
	return protocol.Position{Line: uint32(line), Character: uint32(off - lineStart)}
}
