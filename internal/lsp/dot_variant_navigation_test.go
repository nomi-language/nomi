package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sourcegraph/jsonrpc2"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// dotVariantMain uses the `.Variant` shorthand in every position the
// checker resolves it from an expected type: an argument to a user
// function, an argument to a std function, a `case` arm, a lambda result,
// the payload and struct forms in a pattern and in an expression, a
// block-local enum, an enum declared in another file whose name this file
// never binds, and a generic enum.
const dotVariantMain = `import tones

/// A paint colour.
enum Color {
    /// The colour of blood.
    Red
    Green
}

enum Shape {
    Circle(Float)
    Rect {
        w: Float
        h: Float
    }
}

enum Slot<T> {
    Full(T)
    Empty
}

fn paint(c: Color): Int {
    case c {
        .Red -> 1
        .Green -> 2
    }
}

fn area(s: Shape): Float {
    case s {
        .Circle(r) -> r * r
        .Rect{w, h} -> w * h
    }
}

fn fill(s: Slot<Int>): Int {
    case s {
        Slot.Full(n) -> n
        Slot.Empty -> 0
    }
}

fn main() {
    _a = paint(.Red)
    _b = Iter.sort([3, 1, 2], .Descending)
    _c = Iter.sort_with([3, 1, 2], |_x, _y| .Less)
    _d = area(.Circle(1.0))
    _e = area(.Rect{w: 1.0, h: 2.0})
    _f = paint(Color.Red)
    _g = area(Shape.Circle(1.0))
    _h = Iter.sort_with([3, 1, 2], |_x, _y| Ordering.Less)
    enum Local {
        One
        Two
    }
    pick = |l: Local| l
    _i = pick(.Two)
    _j = pick(Local.Two)
    _k = tones.tint(.Warm)
    _l = fill(.Full(1))
    _m = fill(Slot.Full(1))
    _n: Maybe<Int> = .Some(1)
    _o: Maybe<Int> = Maybe.Some(1)
}
`

const dotVariantTones = `pub enum Tone {
    Warm
    Cool
}

pub fn tint(t: Tone): Int {
    case t {
        Tone.Warm -> 1
        Tone.Cool -> 2
    }
}
`

type dotVariantProject struct {
	t         *testing.T
	c         *rpcClient
	mainURI   string
	tonesURI  string
	mainLines []string
}

func openDotVariantProject(t *testing.T) *dotVariantProject {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.nomi")
	tonesPath := filepath.Join(dir, "tones.nomi")
	for path, src := range map[string]string{mainPath: dotVariantMain, tonesPath: dotVariantTones} {
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &dotVariantProject{
		t:         t,
		c:         startRPC(t, NewServer(), dir),
		mainURI:   pathToURI(mainPath),
		tonesURI:  pathToURI(tonesPath),
		mainLines: strings.Split(dotVariantMain, "\n"),
	}
	p.c.open(p.mainURI, dotVariantMain)
	if got := p.c.waitDiagnostics(p.mainURI); got.n != 0 {
		t.Fatalf("main.nomi has diagnostics: %v", got.msgs)
	}
	return p
}

// at is the position of word's first character on the only line of
// main.nomi containing marker.
func (p *dotVariantProject) at(marker, word string) protocol.Position {
	p.t.Helper()
	found := -1
	for i, l := range p.mainLines {
		if strings.Contains(l, marker) {
			if found >= 0 {
				p.t.Fatalf("marker %q is on lines %d and %d", marker, found, i)
			}
			found = i
		}
	}
	if found < 0 {
		p.t.Fatalf("no line contains %q", marker)
	}
	col := strings.Index(p.mainLines[found], marker) + strings.Index(marker, word)
	if strings.Index(marker, word) < 0 {
		p.t.Fatalf("marker %q does not contain %q", marker, word)
	}
	return protocol.Position{Line: uint32(found), Character: uint32(col)}
}

func (p *dotVariantProject) call(method string, pos protocol.Position, extra map[string]any, out any) error {
	params := map[string]any{"textDocument": map[string]any{"uri": p.mainURI}, "position": pos}
	for k, v := range extra {
		params[k] = v
	}
	return p.c.conn.Call(context.Background(), method, params, out)
}

func (p *dotVariantProject) hover(pos protocol.Position) string {
	p.t.Helper()
	var h *protocol.Hover
	if err := p.call("textDocument/hover", pos, nil, &h); err != nil {
		p.t.Fatalf("hover at %v: %v", pos, err)
	}
	if h == nil {
		return ""
	}
	mc, ok := h.Contents.(protocol.MarkupContent)
	if !ok {
		p.t.Fatalf("hover contents %T", h.Contents)
	}
	return mc.Value
}

func (p *dotVariantProject) definition(pos protocol.Position) []protocol.Location {
	p.t.Helper()
	var locs []protocol.Location
	var raw any
	if err := p.call("textDocument/definition", pos, nil, &raw); err != nil {
		p.t.Fatalf("definition at %v: %v", pos, err)
	}
	switch v := raw.(type) {
	case nil:
	case map[string]any:
		locs = append(locs, locationOf(v))
	case []any:
		for _, l := range v {
			locs = append(locs, locationOf(l.(map[string]any)))
		}
	}
	return locs
}

func locationOf(m map[string]any) protocol.Location {
	r := m["range"].(map[string]any)
	pos := func(x any) protocol.Position {
		pm := x.(map[string]any)
		return protocol.Position{Line: uint32(pm["line"].(float64)), Character: uint32(pm["character"].(float64))}
	}
	return protocol.Location{URI: protocol.DocumentUri(m["uri"].(string)), Range: protocol.Range{Start: pos(r["start"]), End: pos(r["end"])}}
}

// loc renders a location as "file:line:col" with the file's base name.
func loc(uri protocol.DocumentUri, pos protocol.Position) string {
	return filepath.Base(string(uri)) + ":" + itoa(pos.Line) + ":" + itoa(pos.Character)
}

func (p *dotVariantProject) references(pos protocol.Position) []string {
	p.t.Helper()
	var locs []protocol.Location
	if err := p.call("textDocument/references", pos, map[string]any{"context": map[string]any{"includeDeclaration": true}}, &locs); err != nil {
		p.t.Fatalf("references at %v: %v", pos, err)
	}
	var got []string
	for _, l := range locs {
		got = append(got, loc(l.URI, l.Range.Start))
	}
	slices.Sort(got)
	return got
}

func (p *dotVariantProject) rename(pos protocol.Position) ([]string, error) {
	var edit protocol.WorkspaceEdit
	if err := p.call("textDocument/rename", pos, map[string]any{"newName": "Renamed"}, &edit); err != nil {
		return nil, err
	}
	var got []string
	for uri, edits := range edit.Changes {
		for _, e := range edits {
			if e.NewText != "Renamed" {
				p.t.Fatalf("rename edit text %q", e.NewText)
			}
			got = append(got, loc(uri, e.Range.Start))
		}
	}
	slices.Sort(got)
	return got, nil
}

// TestDotVariant_NavigatesLikeTheWrittenVariant: hover, definition,
// references and rename on a `.Variant` answer what they answer on the
// written `Enum.Variant`, and a declaration's references and rename reach
// its shorthand uses.
func TestDotVariant_NavigatesLikeTheWrittenVariant(t *testing.T) {
	p := openDotVariantProject(t)
	m := func(marker, word string) string {
		pos := p.at(marker, word)
		return loc(protocol.DocumentUri(p.mainURI), pos)
	}

	type site struct {
		name    string
		marker  string // the shorthand use
		written string // the written form of the same variant
		word    string
		hover   string // the hover's code line
		decl    string // file:line:col of the variant's declaration
		uses    []string
		std     bool
	}
	redUses := []string{m("        .Red -> 1", "Red"), m("paint(.Red)", "Red"), m("paint(Color.Red)", "Red")}
	circleUses := []string{m(".Circle(r) ->", "Circle"), m("area(.Circle(1.0))", "Circle"), m("Shape.Circle(1.0)", "Circle")}
	rectUses := []string{m(".Rect{w, h}", "Rect"), m("area(.Rect{", "Rect")}
	twoUses := []string{m("pick(.Two)", "Two"), m("pick(Local.Two)", "Two")}
	fullUses := []string{m("Slot.Full(n)", "Full"), m("fill(.Full(1))", "Full"), m("fill(Slot.Full(1))", "Full")}
	sites := []site{
		{name: "user function argument", marker: "paint(.Red)", written: "paint(Color.Red)", word: "Red",
			hover: "variant Red", decl: m("    Red", "Red"), uses: redUses},
		{name: "case arm", marker: "        .Red -> 1", written: "paint(Color.Red)", word: "Red",
			hover: "variant Red", decl: m("    Red", "Red"), uses: redUses},
		{name: "payload expression", marker: "area(.Circle(1.0))", written: "Shape.Circle(1.0)", word: "Circle",
			hover: "variant Circle", decl: m("    Circle(Float)", "Circle"), uses: circleUses},
		{name: "payload pattern", marker: ".Circle(r) ->", word: "Circle",
			hover: "variant Circle", decl: m("    Circle(Float)", "Circle"), uses: circleUses},
		{name: "struct expression", marker: "area(.Rect{", word: "Rect",
			hover: "variant Rect", decl: m("    Rect {", "Rect"), uses: rectUses},
		{name: "struct pattern", marker: ".Rect{w, h}", word: "Rect",
			hover: "variant Rect", decl: m("    Rect {", "Rect"), uses: rectUses},
		{name: "block-local enum", marker: "pick(.Two)", written: "pick(Local.Two)", word: "Two",
			hover: "variant Two", decl: m("        Two", "Two"), uses: twoUses},
		{name: "generic enum", marker: "fill(.Full(1))", written: "fill(Slot.Full(1))", word: "Full",
			hover: "variant Full", decl: m("    Full(T)", "Full"), uses: fullUses},
		{name: "enum of another file", marker: "tones.tint(.Warm)", word: "Warm",
			hover: "variant Warm", decl: "tones.nomi:1:4",
			uses: []string{m("tones.tint(.Warm)", "Warm"), "tones.nomi:7:13"}},
		{name: "std function argument", marker: "[3, 1, 2], .Descending", word: "Descending",
			hover: "variant Descending", decl: "comparable.nomi:20:4", uses: []string{m("[3, 1, 2], .Descending", "Descending")}, std: true},
		{name: "prelude enum", marker: "= .Some(1)", written: "= Maybe.Some(1)", word: "Some",
			hover: "variant Some", decl: "maybe.nomi:20:4", uses: []string{m("= .Some(1)", "Some"), m("= Maybe.Some(1)", "Some")}, std: true},
		{name: "lambda result", marker: "|_x, _y| .Less", written: "|_x, _y| Ordering.Less", word: "Less",
			hover: "variant Less", decl: "comparable.nomi:6:4", uses: []string{m("|_x, _y| .Less", "Less"), m("|_x, _y| Ordering.Less", "Less")}, std: true},
	}
	for _, s := range sites {
		t.Run(s.name, func(t *testing.T) {
			p := &dotVariantProject{t: t, c: p.c, mainURI: p.mainURI, tonesURI: p.tonesURI, mainLines: p.mainLines}
			pos := p.at(s.marker, s.word)

			h := p.hover(pos)
			if !strings.HasPrefix(h, "```nomi\n"+s.hover) {
				t.Errorf("hover = %q, want it to show %q", h, s.hover)
			}
			if s.written != "" {
				if w := p.hover(p.at(s.written, s.word)); h != w {
					t.Errorf("hover = %q, the written form's = %q", h, w)
				}
			}
			if s.word == "Red" {
				if !strings.Contains(h, "The colour of blood.") {
					t.Errorf("hover = %q, want the variant's doc", h)
				}
			}

			defs := p.definition(pos)
			if len(defs) != 1 || loc(defs[0].URI, defs[0].Range.Start) != s.decl {
				t.Errorf("definition = %v, want %s", defs, s.decl)
			}

			want := slices.Clone(s.uses)
			if !s.std {
				want = append(want, s.decl)
			}
			slices.Sort(want)
			if got := p.references(pos); !slices.Equal(got, want) {
				t.Errorf("references = %v, want %v", got, want)
			}

			got, err := p.rename(pos)
			if s.std {
				var rpcErr *jsonrpc2.Error
				if !errors.As(err, &rpcErr) || rpcErr.Message != "'"+s.word+"' is declared in the standard library" {
					t.Errorf("rename = %v, %v; want the standard-library refusal", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rename: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("rename edits = %v, want %v", got, want)
			}
		})
	}

	// From the declaration: references and rename reach every shorthand use.
	declPos := p.at("    Red", "Red")
	want := append(slices.Clone(redUses), m("    Red", "Red"))
	slices.Sort(want)
	if got := p.references(declPos); !slices.Equal(got, want) {
		t.Errorf("references from the declaration = %v, want %v", got, want)
	}
	if got, err := p.rename(declPos); err != nil || !slices.Equal(got, want) {
		t.Errorf("rename from the declaration = %v, %v; want %v", got, err, want)
	}
}
