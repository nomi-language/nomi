package lsp

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A field of an anonymous struct whose type a type expression in another
// file names is declared there: in a return type, a parameter type, a
// struct field's type, a typealias's target, a `once`'s annotation and a
// generic's type argument. The signature is the contract, so a use reaches
// the type's field even where the body returns a literal labelling it too.
const anonTypeRoutes = `pub typealias Row #{id: Int, tag: String}

pub struct Holder {
    inner: #{id: Int, note: String}
}

pub struct Box<T> {
    value: T
}

pub once settings: #{port: Int} = #{port: 80}

pub fn make(id: Int) -> #{id: Int, label: String} {
    #{id, label: "x"}
}

pub fn take(p: #{id: Int, size: Int}) -> Int {
    p.size
}

pub fn row() -> Row {
    #{id: 1, tag: "t"}
}

pub fn holder() -> Holder {
    Holder{inner: #{id: 1, note: "n"}}
}

pub fn boxed() -> Box<#{id: Int, code: Int}> {
    Box{value: #{id: 1, code: 2}}
}
`

const anonTypeMain = `import std::io
import routes

fn shown(r: routes.Row) -> String {
    r.tag
}

fn main() {
    io.print(routes.make(1).label)
    io.print(routes.row().tag)
    io.print(routes.holder().inner.note)
    io.print("${routes.boxed().value.code}")
    io.print("${routes.settings.port}")
    io.print("${routes.take(#{id: 1, size: 2})}")
    io.print(shown(routes.row()))
    m = routes.make(2)
    io.print(m.label)
    io.print("${routes.row().id} ${routes.make(3).id}")
}
`

func anonTypeProject(t *testing.T, openRoutes bool) (s *Server, mainURI, routesURI string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"routes.nomi": anonTypeRoutes,
		"main.nomi":   anonTypeMain,
	})
	s = NewServer()
	mainURI = fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	routesURI = fileuri.FromPath(filepath.Join(dir, "routes.nomi"))
	if openRoutes {
		s.docs.Open(routesURI, anonTypeRoutes)
	}
	s.docs.Open(mainURI, anonTypeMain)
	if doc := s.docs.Snapshot(mainURI); doc == nil || len(doc.Analysis.TypeErrors) > 0 {
		t.Fatalf("main.nomi does not check: %v", doc.Analysis.TypeErrors)
	}
	return s, mainURI, routesURI
}

// anonTypeCases are the uses in main.nomi and the field in routes.nomi
// each is declared at.
var anonTypeCases = []struct {
	what       string
	line       int
	name       string
	routesLine int
	nth        int
}{
	{what: "a return type's field", line: 8, name: "label", routesLine: 12, nth: 1},
	{what: "a typealias's field", line: 9, name: "tag", routesLine: 0, nth: 1},
	{what: "a typealias's field through a parameter", line: 4, name: "tag", routesLine: 0, nth: 1},
	{what: "a struct field's anonymous type", line: 10, name: "note", routesLine: 3, nth: 1},
	{what: "a generic's anonymous type argument", line: 11, name: "code", routesLine: 28, nth: 1},
	{what: "an annotated once's field", line: 12, name: "port", routesLine: 10, nth: 1},
	{what: "a label of a literal passed to a parameter", line: 13, name: "size", routesLine: 16, nth: 1},
	{what: "a return type's field through a binding", line: 16, name: "label", routesLine: 12, nth: 1},
}

func TestAnonTypeFieldDefinitionAcrossFiles(t *testing.T) {
	for _, open := range []bool{false, true} {
		s, mainURI, routesURI := anonTypeProject(t, open)
		for _, c := range anonTypeCases {
			want := at(t, anonTypeRoutes, c.routesLine, c.name, c.nth)
			loc := definitionResult(t, s, mainURI, at(t, anonTypeMain, c.line, c.name, 1))
			if loc == nil {
				t.Errorf("open routes %v: definition of %s = none, want routes.nomi %v", open, c.what, want)
				continue
			}
			if string(loc.URI) != routesURI || loc.Range.Start != want {
				t.Errorf("open routes %v: definition of %s = %s %v, want routes.nomi %v", open, c.what, loc.URI, loc.Range.Start, want)
			}
		}
	}
}

func TestAnonTypeFieldHoverNamesTheDeclaringFile(t *testing.T) {
	s, mainURI, _ := anonTypeProject(t, false)
	for _, c := range anonTypeCases {
		pos := at(t, anonTypeMain, c.line, c.name, 1)
		res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(mainURI)},
			Position:     pos,
		}})
		if err != nil || res == nil {
			t.Fatalf("hover on %s: %v %v", c.what, res, err)
		}
		got := res.Contents.(protocol.MarkupContent).Value
		where := "*declared at* `routes.nomi:" + itoa(uint32(c.routesLine+1)) + "`"
		if !strings.Contains(got, c.name+": ") || !strings.Contains(got, where) {
			t.Errorf("hover on %s = %q, want its type and %s", c.what, got, where)
		}
	}
}

// References tie the uses of one declared field and no other: `id` in Row
// and `id` in make's return type are two fields.
func TestAnonTypeFieldReferencesAcrossFiles(t *testing.T) {
	for _, open := range []bool{false, true} {
		s, mainURI, routesURI := anonTypeProject(t, open)
		for _, c := range []struct {
			what  string
			from  protocol.Position
			sites []string
		}{
			{"label", at(t, anonTypeMain, 8, "label", 1), sorted(
				site("routes.nomi", anonTypeRoutes, 12, "label", 1, t),
				site("routes.nomi", anonTypeRoutes, 13, "label", 1, t),
				site("main.nomi", anonTypeMain, 8, "label", 1, t),
				site("main.nomi", anonTypeMain, 16, "label", 1, t),
			)},
			{"tag", at(t, anonTypeMain, 4, "tag", 1), sorted(
				site("routes.nomi", anonTypeRoutes, 0, "tag", 1, t),
				site("routes.nomi", anonTypeRoutes, 21, "tag", 1, t),
				site("main.nomi", anonTypeMain, 4, "tag", 1, t),
				site("main.nomi", anonTypeMain, 9, "tag", 1, t),
			)},
			{"Row's id", at(t, anonTypeMain, 17, "id", 1), sorted(
				site("routes.nomi", anonTypeRoutes, 0, "id", 1, t),
				site("routes.nomi", anonTypeRoutes, 21, "id", 1, t),
				site("main.nomi", anonTypeMain, 17, "id", 1, t),
			)},
			{"size", at(t, anonTypeMain, 13, "size", 1), sorted(
				site("routes.nomi", anonTypeRoutes, 16, "size", 1, t),
				site("routes.nomi", anonTypeRoutes, 17, "size", 1, t),
				site("main.nomi", anonTypeMain, 13, "size", 1, t),
			)},
		} {
			got := projectReferences(t, s, mainURI, c.from)
			if strings.Join(got, " ") != strings.Join(c.sites, " ") {
				t.Errorf("open routes %v: references to %s = %v, want %v", open, c.what, got, c.sites)
			}
		}
		if open {
			got := projectReferences(t, s, routesURI, at(t, anonTypeRoutes, 12, "label", 1))
			want := sorted(
				site("routes.nomi", anonTypeRoutes, 12, "label", 1, t),
				site("routes.nomi", anonTypeRoutes, 13, "label", 1, t),
				site("main.nomi", anonTypeMain, 8, "label", 1, t),
				site("main.nomi", anonTypeMain, 16, "label", 1, t),
			)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("references to label from routes.nomi = %v, want %v", got, want)
			}
		}
	}
}

// Rename of a type's field from a use in another file renames the field in
// the type, the literal label checked against it and every use of it, and
// leaves the other types' fields alone.
func TestAnonTypeFieldRenameAcrossFiles(t *testing.T) {
	s, mainURI, routesURI := anonTypeProject(t, false)
	use := at(t, anonTypeMain, 8, "label", 1)
	edit := collectWorkspaceRename(context.Background(), s.docs, mainURI, analysis.Pos{Line: int(use.Line) + 1, Col: int(use.Character) + 1}, "title")
	if edit == nil {
		t.Fatal("no rename of label")
	}
	render := func(uri string) string {
		var out []string
		for _, e := range edit.Changes[protocol.DocumentUri(uri)] {
			out = append(out, itoa(e.Range.Start.Line)+":"+itoa(e.Range.Start.Character)+"=>"+e.NewText)
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	pos := func(src string, line int, name string) string {
		p := at(t, src, line, name, 1)
		return itoa(p.Line) + ":" + itoa(p.Character) + "=>title"
	}
	if got, want := render(routesURI), strings.Join(sorted(pos(anonTypeRoutes, 12, "label"), pos(anonTypeRoutes, 13, "label")), " "); got != want {
		t.Errorf("routes.nomi edits = %v, want %v", got, want)
	}
	if got, want := render(mainURI), strings.Join(sorted(pos(anonTypeMain, 8, "label"), pos(anonTypeMain, 16, "label")), " "); got != want {
		t.Errorf("main.nomi edits = %v, want %v", got, want)
	}
}
