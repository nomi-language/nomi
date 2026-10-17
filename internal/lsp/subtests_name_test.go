package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A `subtests` row's name, `subtests rows, pattern, name { ... }`, reads the
// pattern's bindings: hover, go-to-definition, references and rename reach a
// binding through it, and completion offers the bindings there.
const subtestsNameSrc = `test "leap" {
  cases = [#("leap", 2000, True)]
  subtests cases, #(description, year, expected), "${description} ${year}" {
    assert (year % 400 == 0) == expected
  }
}
`

func TestSubtestsName_HoverDefinitionReferencesAndRename(t *testing.T) {
	src := subtestsNameSrc
	decl := at(t, src, 2, "description", 1)
	use := at(t, src, 2, "description", 2)

	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	s := NewServer()
	s.docs.Open(uri, src)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: int(use.Line) + 1, Col: int(use.Character) + 1})
	if sym == nil {
		t.Fatal("no symbol at the name's `description`")
	}
	if got := renderHover(sym); !strings.Contains(got, "description: String") {
		t.Errorf("hover over the name's `description`:\n%s\nwant it to name the binding's type", got)
	}

	locs := appFieldDefinitions(t, s, uri, use)
	wantDefinitions(t, "the name's `description`", locs, uri, "description", [2]uint32{decl.Line, decl.Character})

	want := sorted(span(decl, "description"), span(use, "description"))
	if got := referencesAt(t, src, decl); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references to `description` = %v, want %v", got, want)
	}
	wantEdits := sorted(span(decl, "description")+"=>label", span(use, "description")+"=>label")
	if got := renameAt(t, src, use, "label"); strings.Join(got, " ") != strings.Join(wantEdits, " ") {
		t.Errorf("rename from the name = %v, want %v", got, wantEdits)
	}

	yearDecl := at(t, src, 2, "year", 1)
	want = sorted(span(yearDecl, "year"), span(at(t, src, 2, "year", 2), "year"), span(at(t, src, 3, "year", 1), "year"))
	if got := referencesAt(t, src, yearDecl); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("references to `year` = %v, want %v", got, want)
	}
}

// Completion inside the name offers the pattern's bindings.
func TestSubtestsName_CompletionOffersThePatternsBindings(t *testing.T) {
	items := complete(t, "test \"t\" {\n  cases = [#(\"a\", 1)]\n  subtests cases, #(description, n), desc"+cursorMark+" {\n    assert n > 0\n  }\n}\n")
	labelsInclude(t, items, "description")
}
