package lsp

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A function the front end writes for a declaration (a subtype's `try_from`
// and `unchecked`, an opaque type's Debug, a derive's function) stands at a
// synthesized position, which is no line of any file. Each request that
// answers a place for one answers the declaration it was written for
// (analysis.WrittenFor), in the file that declares it.

// writtenTypes declares a subtype, a validated struct, a validated opaque
// type and a struct with a derive, each of which the front end writes
// functions for.
const writtenTypes = `pub subtype Port Int when fn n -> n > 0

pub struct Span
    low: Int
    high: Int
    when fn span -> span.low <= span.high
end

pub opaque type Even Int when fn n -> n % 2 == 0

pub struct Point
    x: Int
end

derive Comparable for Point
`

// writtenUses calls the functions written for writtenTypes' declarations
// from the file that declares them.
const writtenUses = `
pub fn uses()
    _ = Port.try_from(80)
    _ = Port.unchecked(80)
    _ = Span.try_from({low: 1, high: 2})
    _ = Span.unchecked({low: 1, high: 2})
    _ = Even.try_from(2)
    _ = Even.unchecked(2)
    _ = Even.inspect(Even.unchecked(4))
    _ = Point.compare(Point{x: 1}, Point{x: 2})
end
`

// writtenImporter calls them from another file, which may call each but
// the opaque type's and the private `unchecked`.
const writtenImporter = `import ports.{Point, Port, Span}

pub fn uses()
    _ = Port.try_from(80)
    _ = Span.try_from({low: 1, high: 2})
    _ = Point.compare(Point{x: 1}, Point{x: 2})
    _ = Comparable.compare(Point{x: 1}, Point{x: 2})
end
`

// writtenTarget is a call of a written function and the declaration it
// answers: name, as it stands in decl.
type writtenTarget struct{ call, fn, decl, name string }

var writtenInOwnFile = []writtenTarget{
	{"Port.try_from(", "try_from", "subtype Port", "Port"},
	{"Port.unchecked(", "unchecked", "subtype Port", "Port"},
	{"Span.try_from(", "try_from", "struct Span", "Span"},
	{"Span.unchecked(", "unchecked", "struct Span", "Span"},
	{"Even.try_from(", "try_from", "opaque type Even", "Even"},
	{"Even.unchecked(", "unchecked", "opaque type Even", "Even"},
	{"Even.inspect(", "inspect", "opaque type Even", "Even"},
	{"Point.compare(", "compare", "derive Comparable for Point", "Comparable"},
}

var writtenInAnotherFile = []writtenTarget{
	{"Port.try_from(", "try_from", "subtype Port", "Port"},
	{"Span.try_from(", "try_from", "struct Span", "Span"},
	{"Point.compare(", "compare", "derive Comparable for Point", "Comparable"},
}

// positionIn is the position just inside the first name within the first
// within of src.
func positionIn(t *testing.T, src, within, name string) protocol.Position {
	t.Helper()
	at := strings.Index(src, within)
	if at < 0 || !strings.Contains(within, name) {
		t.Fatalf("no %q holding %q in the source", within, name)
	}
	return offsetPosition(src, at+strings.Index(within, name)+1)
}

// rangeIn is the range of the first name within the first within of src.
func rangeIn(t *testing.T, src, within, name string) protocol.Range {
	t.Helper()
	start := positionIn(t, src, within, name)
	start.Character--
	return protocol.Range{Start: start, End: protocol.Position{Line: start.Line, Character: start.Character + uint32(len(name))}}
}

func positionParams(uri string, p protocol.Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: p}
}

// wantWrittenDefinitions asks for the definition at each target's call in the
// document at uri, whose text is src, and requires the target's name in
// declSrc, the text of the file at declURI.
func wantWrittenDefinitions(t *testing.T, s *Server, uri, src, declURI, declSrc string, targets []writtenTarget) {
	t.Helper()
	for _, w := range targets {
		got, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(uri, positionIn(t, src, w.call, w.fn))})
		if err != nil {
			t.Fatalf("%s: %v", w.call, err)
		}
		loc, _ := got.(*protocol.Location)
		if loc == nil {
			t.Errorf("definition of %s): none, want `%s` in `%s`", w.call, w.name, w.decl)
			continue
		}
		if want := rangeIn(t, declSrc, w.decl, w.name); string(loc.URI) != declURI || loc.Range != want {
			t.Errorf("definition of %s): %s %+v, want `%s` in `%s`: %s %+v", w.call, loc.URI, loc.Range, w.name, w.decl, declURI, want)
		}
	}
}

func openClean(t *testing.T, files map[string]string, open string) (*Server, string) {
	t.Helper()
	s, uri := openProject(t, files, open)
	if doc := s.docs.Get(uri); len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
		t.Fatalf("%s reports %v %v", open, doc.Errors, doc.Analysis.TypeErrors)
	}
	return s, uri
}

func TestDefinition_AWrittenFunctionAnswersItsDeclaration(t *testing.T) {
	src := writtenTypes + writtenUses
	s, uri := openClean(t, map[string]string{"ports.nomi": src}, "ports.nomi")
	wantWrittenDefinitions(t, s, uri, src, uri, src, writtenInOwnFile)
}

// The declaration is in the file that declares the type: a file of the same
// package, and a file of a package the program depends on.
func TestDefinition_AWrittenFunctionOfAnotherFileAnswersThatFile(t *testing.T) {
	t.Run("a file the document imports", func(t *testing.T) {
		s, uri := openClean(t, map[string]string{"uses.nomi": writtenImporter, "ports.nomi": writtenTypes}, "uses.nomi")
		declURI := strings.TrimSuffix(uri, "uses.nomi") + "ports.nomi"
		wantWrittenDefinitions(t, s, uri, writtenImporter, declURI, writtenTypes, writtenInAnotherFile)
	})
	t.Run("a dependency's file", func(t *testing.T) {
		importer := strings.Replace(writtenImporter, "import ports.", "import fx::ports.", 1)
		s, uri := openClean(t, map[string]string{
			"app/nomi.toml": "[package]\nname = \"app\"\n\n[dependencies]\nfx = { dir = \"../fx\" }\n",
			"app/uses.nomi": importer,
			"fx/nomi.toml":  "[package]\nname = \"fx\"\n",
			"fx/ports.nomi": writtenTypes,
		}, "app/uses.nomi")
		declURI := strings.TrimSuffix(uri, "app/uses.nomi") + "fx/ports.nomi"
		wantWrittenDefinitions(t, s, uri, importer, declURI, writtenTypes, writtenInAnotherFile)
	})
}

// A std file open as a document declares its own types, and a function
// written for one answers that document, not the std module whose written
// function has the same name at the same synthesized position.
func TestDefinition_AWrittenFunctionInAStdDocumentAnswersTheDocument(t *testing.T) {
	s := NewServer()
	s.docs.Open(subtypeStdURI, subtypeStdDoc)
	wantWrittenDefinitions(t, s, subtypeStdURI, subtypeStdDoc, subtypeStdURI, subtypeStdDoc, []writtenTarget{
		{"Positive.unchecked(", "unchecked", "subtype Positive", "Positive"},
		{"Positive.try_from(", "try_from", "subtype Positive", "Positive"},
	})
}

// A function written for a std type answers the std file that declares the
// type, at the type's name.
func TestDefinition_AWrittenFunctionOfAStdTypeAnswersItsStdFile(t *testing.T) {
	src := "pub fn uses()\n    _ = NonEmptyString.try_from(\"a\")\nend\n"
	s, uri := openClean(t, map[string]string{"uses.nomi": src}, "uses.nomi")
	declURI := s.stdFileURI("strings")
	decl, err := os.ReadFile(uriToPath(declURI))
	if err != nil {
		t.Fatal(err)
	}
	wantWrittenDefinitions(t, s, uri, src, declURI, string(decl), []writtenTarget{
		{"NonEmptyString.try_from(", "try_from", "subtype NonEmptyString", "NonEmptyString"},
	})
}

// A declaration of the document's own is the document's, whatever std
// module declares a function of the same name at the same line and column:
// for a user file, and for a std file open as a document.
func TestDefinition_ADocumentsOwnDeclarationIsNotAStdModulesOfThatNameAndPlace(t *testing.T) {
	s := NewServer()
	fn := firstStdFunction(t, s)
	src := strings.Repeat("\n", fn.Line-1) + "pub fn " + fn.Name + "()\nend\n\npub fn uses()\n    " + fn.Name + "()\nend\n"
	target := []writtenTarget{{"    " + fn.Name + "()", fn.Name, "pub fn " + fn.Name, fn.Name}}
	t.Run("a user file", func(t *testing.T) {
		s, uri := openClean(t, map[string]string{"uses.nomi": src}, "uses.nomi")
		wantWrittenDefinitions(t, s, uri, src, uri, src, target)
	})
	t.Run("a std document", func(t *testing.T) {
		const uri = "file:///path/to/std/zz_own.nomi"
		s.docs.Open(uri, src)
		wantWrittenDefinitions(t, s, uri, src, uri, src, target)
	})
}

// firstStdFunction is a std module's top-level `pub fn`, its name and the
// line the name is on: the first by module name and then by line.
func firstStdFunction(t *testing.T, s *Server) struct {
	Name string
	Line int
} {
	t.Helper()
	modules := make([]string, 0, len(s.std.Files))
	for name := range s.std.Files {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	for _, module := range modules {
		fa := s.std.Files[module]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		best := struct {
			Name string
			Line int
		}{}
		for _, sym := range fa.ModuleScope.Symbols {
			fd, ok := sym.Node.(*ast.FuncDef)
			if !ok || sym.Kind != analysis.SymbolFunction || sym.Resolved != nil || fd.Visibility != ast.Public || sym.Pos.Col != len("pub fn ")+1 {
				continue
			}
			if best.Name == "" || sym.Pos.Line < best.Line {
				best.Name, best.Line = sym.Name, sym.Pos.Line
			}
		}
		if best.Name != "" {
			return best
		}
	}
	t.Fatal("no std module declares a top-level `pub fn`")
	return struct {
		Name string
		Line int
	}{}
}

// Rename refuses a function the source does not write, saying whose
// declaration it comes with: there is no declaration of it to rename with
// its uses.
func TestRename_RefusesAWrittenFunction(t *testing.T) {
	check := func(t *testing.T, s *Server, uri, src, call, fn, want string) {
		t.Helper()
		at := positionParams(uri, positionIn(t, src, call, fn))
		if got, err := s.textDocumentPrepareRename(nil, &protocol.PrepareRenameParams{TextDocumentPositionParams: at}); err == nil || err.Error() != want {
			t.Errorf("prepareRename at %s): %v, %v; want the error %q", call, got, err, want)
		}
		if got, err := s.textDocumentRename(nil, &protocol.RenameParams{TextDocumentPositionParams: at, NewName: "renamed"}); err == nil || err.Error() != want || got != nil {
			t.Errorf("rename at %s): %v, %v; want no edit and the error %q", call, got, err, want)
		}
	}
	t.Run("a user file", func(t *testing.T) {
		src := writtenTypes + writtenUses
		s, uri := openClean(t, map[string]string{"ports.nomi": src}, "ports.nomi")
		check(t, s, uri, src, "Port.try_from(", "try_from", "'try_from' is not written in the source: it comes with the declaration of Port, and cannot be renamed")
		check(t, s, uri, src, "Span.unchecked(", "unchecked", "'unchecked' is not written in the source: it comes with the declaration of Span, and cannot be renamed")
		check(t, s, uri, src, "Point.compare(", "compare", "'compare' implements Comparable.compare; it must keep the interface's name")
	})
	t.Run("a std document", func(t *testing.T) {
		s := NewServer()
		s.docs.Open(subtypeStdURI, subtypeStdDoc)
		check(t, s, subtypeStdURI, subtypeStdDoc, "Positive.unchecked(", "unchecked", "'unchecked' is not written in the source: it comes with the declaration of Positive, and cannot be renamed")
		check(t, s, subtypeStdURI, subtypeStdDoc, "Positive.try_from(", "try_from", "'try_from' is not written in the source: it comes with the declaration of Positive, and cannot be renamed")
	})
}

// Go-to-implementation lists a derived impl at its `derive` entry in the
// file that writes the derive, which need not be the document asked from.
func TestImplementation_ADerivedImplOfAnotherFileAnswersItsDerive(t *testing.T) {
	s, uri := openClean(t, map[string]string{"uses.nomi": writtenImporter, "ports.nomi": writtenTypes}, "uses.nomi")
	got, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{TextDocumentPositionParams: positionParams(uri, positionIn(t, writtenImporter, "Comparable.compare(", "compare"))})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.Location{
		URI:   protocol.DocumentUri(strings.TrimSuffix(uri, "uses.nomi") + "ports.nomi"),
		Range: rangeIn(t, writtenTypes, "derive Comparable for Point", "Comparable"),
	}
	var own []protocol.Location
	locs, _ := got.([]protocol.Location)
	for _, loc := range locs {
		if strings.HasSuffix(string(loc.URI), "ports.nomi") {
			own = append(own, loc)
		}
	}
	if len(own) != 1 || own[0] != want {
		t.Errorf("implementations of Comparable.compare in ports.nomi: %+v, want %+v", own, want)
	}
}
