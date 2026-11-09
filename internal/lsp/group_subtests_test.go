package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A group's `subtests` block binds its row in the group's setup and tests:
// hover over a use names the binding's type, go-to-definition reaches the
// pattern.
const groupSubtestsSrc = `group "stores" {
  subtests [("memory", 1), ("sqlite", 2)], (store, workers), store {
    setup workers * 2

    test "reads the row", doubled {
      assert doubled == workers * 2
      assert store != ""
    }
  }
}
`

func TestGroupSubtests_HoverAndDefinition(t *testing.T) {
	src := groupSubtestsSrc
	decl := at(t, src, 1, "workers", 1)
	inSetup := at(t, src, 2, "workers", 1)
	inTest := at(t, src, 5, "workers", 1)
	storeUse := at(t, src, 6, "store", 1)

	path := filepath.Join(t.TempDir(), "main_test.nomi")
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
	if len(doc.Analysis.TypeErrors) > 0 {
		t.Fatalf("the source does not check: %v", doc.Analysis.TypeErrors)
	}
	for _, c := range []struct {
		what string
		pos  struct{ Line, Character uint32 }
		want string
	}{
		{"`workers` in the setup", struct{ Line, Character uint32 }{inSetup.Line, inSetup.Character}, "workers: Int"},
		{"`workers` in the test", struct{ Line, Character uint32 }{inTest.Line, inTest.Character}, "workers: Int"},
		{"`store` in the test", struct{ Line, Character uint32 }{storeUse.Line, storeUse.Character}, "store: String"},
	} {
		sym := doc.Analysis.SymbolAt(analysis.Pos{Line: int(c.pos.Line) + 1, Col: int(c.pos.Character) + 1})
		if sym == nil {
			t.Fatalf("no symbol at %s", c.what)
		}
		if got := renderHover(sym); !strings.Contains(got, c.want) {
			t.Errorf("hover over %s:\n%s\nwant it to contain %q", c.what, got, c.want)
		}
	}
	locs := appFieldDefinitions(t, s, uri, inTest)
	wantDefinitions(t, "the test's `workers`", locs, uri, "workers", [2]uint32{decl.Line, decl.Character})

}
