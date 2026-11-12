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

// The fields of an anonymous struct are declared where its literal labels
// them, or where the type expression the value was declared with names
// them. A field access, a field accessor and a destructuring pattern refer
// there, across files too.
const anonFieldRoutes = `pub once public = {
    home: "/",
    post_reaction_toggle: "/api/posts/{id}/reactions",
}

pub fn make(n: Int) -> {id: Int, label: String}
    {id: n, label: "x"}
end
`

const anonFieldMain = `import std::io
import std::regex.Regex
import routes

fn show(p: {id: Int}) -> Int
    p.id
end

fn main()
    io.print(routes.public.post_reaction_toggle)
    io.print(routes.public.home)
    local = {count: 1, name: "n"}
    updated = {..local, count: 2}
    io.print(updated.name)
    _ = show({id: local.count})
    m = routes.make(1)
    io.print(m.label)
    io.print([local] |> Iter.map(.name) |> Iter.to_list() |> String.join(","))
    {home:} = routes.public
    io.print(home)
    io.print(updated.count)
    date = String.captures("2024-05", Regex` + "`(?P<year>\\d{4})-(?P<month>\\d\\d)`" + `)!
    io.print(date.year)
end
`

// anonFieldProject writes the routes and main files as a project and opens
// main.nomi, and routes.nomi too when openRoutes is set.
func anonFieldProject(t *testing.T, openRoutes bool) (s *Server, mainURI, routesURI string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"routes.nomi": anonFieldRoutes,
		"main.nomi":   anonFieldMain,
	})
	s = NewServer()
	mainURI = fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	routesURI = fileuri.FromPath(filepath.Join(dir, "routes.nomi"))
	s.docs.Open(mainURI, anonFieldMain)
	if openRoutes {
		s.docs.Open(routesURI, anonFieldRoutes)
	}
	if doc := s.docs.Snapshot(mainURI); doc == nil || len(doc.Analysis.TypeErrors) > 0 {
		t.Fatalf("main.nomi does not check: %v", doc.Analysis.TypeErrors)
	}
	return s, mainURI, routesURI
}

// definitionResult is go-to-definition's answer at pos, nil for none.
func definitionResult(t *testing.T, s *Server, uri string, pos protocol.Position) *protocol.Location {
	t.Helper()
	result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	switch r := result.(type) {
	case nil:
		return nil
	case *protocol.Location:
		return r
	}
	t.Fatalf("definition at %v = %T %+v, want one location or none", pos, result, result)
	return nil
}

func TestAnonFieldDefinition(t *testing.T) {
	for _, open := range []bool{false, true} {
		s, mainURI, routesURI := anonFieldProject(t, open)
		for _, c := range []struct {
			what     string
			at       protocol.Position
			uri      string
			want     protocol.Position
			wantNone bool
		}{
			{what: "a field of an imported once's literal", at: at(t, anonFieldMain, 9, "post_reaction_toggle", 1), uri: routesURI, want: at(t, anonFieldRoutes, 2, "post_reaction_toggle", 1)},
			{what: "another field of it", at: at(t, anonFieldMain, 10, "home", 1), uri: routesURI, want: at(t, anonFieldRoutes, 1, "home", 1)},
			{what: "a field of a local binding's literal", at: at(t, anonFieldMain, 14, "count", 1), uri: mainURI, want: at(t, anonFieldMain, 11, "count", 1)},
			{what: "a field a struct update keeps from its base", at: at(t, anonFieldMain, 13, "name", 1), uri: mainURI, want: at(t, anonFieldMain, 11, "name", 1)},
			{what: "a field a struct update writes", at: at(t, anonFieldMain, 20, "count", 1), uri: mainURI, want: at(t, anonFieldMain, 11, "count", 1)},
			{what: "the label a struct update writes", at: at(t, anonFieldMain, 12, "count", 1), uri: mainURI, want: at(t, anonFieldMain, 11, "count", 1)},
			{what: "a field accessor", at: at(t, anonFieldMain, 17, "name", 1), uri: mainURI, want: at(t, anonFieldMain, 11, "name", 1)},
			{what: "a punned pattern field", at: at(t, anonFieldMain, 18, "home", 1), uri: routesURI, want: at(t, anonFieldRoutes, 1, "home", 1)},
			{what: "a parameter's field, declared by its type", at: at(t, anonFieldMain, 5, "id", 1), uri: mainURI, want: at(t, anonFieldMain, 4, "id", 1)},
			{what: "a field with no known declaration", at: at(t, anonFieldMain, 22, "year", 1), wantNone: true},
		} {
			loc := definitionResult(t, s, mainURI, c.at)
			if c.wantNone {
				if loc != nil {
					t.Errorf("open routes %v: definition of %s = %v, want none", open, c.what, loc)
				}
				continue
			}
			if loc == nil {
				t.Errorf("open routes %v: definition of %s = none, want %s %v", open, c.what, c.uri, c.want)
				continue
			}
			if string(loc.URI) != c.uri || loc.Range.Start != c.want {
				t.Errorf("open routes %v: definition of %s = %s %v, want %s %v", open, c.what, loc.URI, loc.Range.Start, c.uri, c.want)
			}
			if string(loc.URI) == mainURI && loc.Range.Start == c.at {
				t.Errorf("open routes %v: definition of %s is the use itself", open, c.what)
			}
		}
	}
}

func TestAnonFieldHover(t *testing.T) {
	s, mainURI, _ := anonFieldProject(t, false)
	hover := func(pos protocol.Position) string {
		t.Helper()
		res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(mainURI)},
			Position:     pos,
		}})
		if err != nil || res == nil {
			t.Fatalf("hover at %v: %v %v", pos, res, err)
		}
		return res.Contents.(protocol.MarkupContent).Value
	}
	got := hover(at(t, anonFieldMain, 9, "post_reaction_toggle", 1))
	if !strings.Contains(got, "post_reaction_toggle: String") || !strings.Contains(got, "*declared at* `routes.nomi:3`") {
		t.Errorf("hover on an imported literal's field = %q, want its type and routes.nomi:3", got)
	}
	got = hover(at(t, anonFieldMain, 14, "count", 1))
	if !strings.Contains(got, "count: Int") || strings.Contains(got, "declared at") {
		t.Errorf("hover on a local literal's field = %q, want its type alone", got)
	}
	got = hover(at(t, anonFieldMain, 22, "year", 1))
	if !strings.Contains(got, "year: String") || strings.Contains(got, "declared at") {
		t.Errorf("hover on a field with no known declaration = %q, want its type alone", got)
	}
}

// projectReferences answers references at pos in uri, each as
// "file:line:col", sorted.
func projectReferences(t *testing.T, s *Server, uri string, pos protocol.Position) []string {
	t.Helper()
	locs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos},
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range locs {
		got = append(got, filepath.Base(fileuri.ToPath(string(l.URI)))+":"+itoa(l.Range.Start.Line)+":"+itoa(l.Range.Start.Character))
	}
	sort.Strings(got)
	return got
}

func site(file, src string, line int, name string, nth int, t *testing.T) string {
	p := at(t, src, line, name, nth)
	return file + ":" + itoa(p.Line) + ":" + itoa(p.Character)
}

func TestAnonFieldReferencesAcrossFiles(t *testing.T) {
	for _, open := range []bool{false, true} {
		s, mainURI, routesURI := anonFieldProject(t, open)
		home := sorted(
			site("routes.nomi", anonFieldRoutes, 1, "home", 1, t),
			site("main.nomi", anonFieldMain, 10, "home", 1, t),
			site("main.nomi", anonFieldMain, 18, "home", 1, t),
		)
		for _, from := range []struct {
			uri string
			pos protocol.Position
		}{
			{mainURI, at(t, anonFieldMain, 10, "home", 1)},
			{routesURI, at(t, anonFieldRoutes, 1, "home", 1)},
		} {
			if open == false && from.uri == routesURI {
				continue
			}
			got := projectReferences(t, s, from.uri, from.pos)
			// The punned pattern also binds `home`, which its read on the
			// next line is a reference to; the field's references are the
			// three sites.
			if strings.Join(got, " ") != strings.Join(home, " ") {
				t.Errorf("open routes %v: references to home from %s = %v, want %v", open, filepath.Base(from.uri), got, home)
			}
		}
		count := sorted(
			site("main.nomi", anonFieldMain, 11, "count", 1, t),
			site("main.nomi", anonFieldMain, 12, "count", 1, t),
			site("main.nomi", anonFieldMain, 14, "count", 1, t),
			site("main.nomi", anonFieldMain, 20, "count", 1, t),
		)
		if got := projectReferences(t, s, mainURI, at(t, anonFieldMain, 14, "count", 1)); strings.Join(got, " ") != strings.Join(count, " ") {
			t.Errorf("open routes %v: references to count = %v, want %v", open, got, count)
		}
	}
}

// Rename of a literal's field renames it where it is labelled and every use
// tied to it, in both files; a punned pattern unpuns. A field with no known
// declaration is refused rather than renamed at the one use.
func TestAnonFieldRename(t *testing.T) {
	s, mainURI, routesURI := anonFieldProject(t, false)
	edit := collectWorkspaceRename(context.Background(), s.docs, mainURI, analysis.Pos{Line: 11, Col: 28}, "root")
	if edit == nil {
		t.Fatal("no rename of home")
	}
	render := func(uri string) []string {
		var out []string
		for _, e := range edit.Changes[protocol.DocumentUri(uri)] {
			out = append(out, itoa(e.Range.Start.Line)+":"+itoa(e.Range.Start.Character)+"=>"+e.NewText)
		}
		sort.Strings(out)
		return out
	}
	if got := render(routesURI); strings.Join(got, " ") != "1:4=>root" {
		t.Errorf("routes.nomi edits = %v, want the label renamed", got)
	}
	wantMain := sorted("18:5=>root: home", "10:27=>root")
	if got := render(mainURI); strings.Join(got, " ") != strings.Join(wantMain, " ") {
		t.Errorf("main.nomi edits = %v, want %v", got, wantMain)
	}

	doc := s.docs.Snapshot(mainURI)
	year := at(t, anonFieldMain, 22, "year", 1)
	if _, _, err := renameTarget(s.docs, doc, analysis.Pos{Line: int(year.Line) + 1, Col: int(year.Character) + 1}); err == nil {
		t.Error("rename of a field with no known declaration was accepted")
	}
}
