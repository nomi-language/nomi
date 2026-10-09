package lsp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func fmtRange(r protocol.Range) string {
	return fmt.Sprintf("%d:%d-%d:%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

// checkSymbolRanges reports each symbol whose selectionRange lies outside
// its range, whose range is empty, or that lies outside its parent's range.
func checkSymbolRanges(t *testing.T, where string, syms []protocol.DocumentSymbol, parent *protocol.DocumentSymbol) int {
	t.Helper()
	checked := 0
	for i := range syms {
		s := &syms[i]
		checked++
		if !rangeContains(s.Range, s.SelectionRange) {
			t.Errorf("%s: %s: selectionRange %s outside range %s", where, s.Name, fmtRange(s.SelectionRange), fmtRange(s.Range))
		}
		if !posBefore(s.Range.Start, s.Range.End) {
			t.Errorf("%s: %s: empty range %s", where, s.Name, fmtRange(s.Range))
		}
		// Only a bare variant (`Point`) is its own name; anything else
		// equal to its selection means the parser recorded no extent.
		if s.Range == s.SelectionRange && s.Kind != protocol.SymbolKindEnumMember {
			t.Errorf("%s: %s: range %s is just the name; no extent recorded", where, s.Name, fmtRange(s.Range))
		}
		if parent != nil && !rangeContains(parent.Range, s.Range) {
			t.Errorf("%s: %s: range %s outside parent %s's %s", where, s.Name, fmtRange(s.Range), parent.Name, fmtRange(parent.Range))
		}
		checked += checkSymbolRanges(t, where, s.Children, s)
	}
	return checked
}

// Every document symbol in the stdlib and the test corpus has its name inside its range and its children inside it, as LSP
// requires (VS Code drops a response that breaks either rule).
func TestDocumentSymbols_RangesContainSelectionAndChildren(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	for _, dir := range []string{"std", "tests"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content := string(src)
			nodes, _, _ := parser.ParseResilient(lexer.Lex(content))
			syms := nodesToDocumentSymbols(nodes, nil)
			toUTF16DocumentSymbols(newLineIndex(content), syms)
			checked += checkSymbolRanges(t, path, syms, nil)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 1000 {
		t.Fatalf("checked only %d symbols; the sweep found too few files", checked)
	}
}

// A declaration's range runs from its first token (after its doc comment)
// through its closing brace; its selection is the name.
func TestDocumentSymbols_RangeIsDeclarationExtent(t *testing.T) {
	input := `/// Adds.
pub fn add(x: Int, y: Int): Int {
  x + y
}

struct User {
  name: String
  /// Years.
  age: Int = 0
}

enum Shape {
  Circle(Float)
  Point
}

interface Named {
  fn name(value: self): String
  fn label(value: self): String
}

impl Named for User {
  fn label(value: User): String {
    value.name
  }
}

once limit = 10

tests "math" {
  test "adds" {
    assert add(1, 2) == 3
  }
}
`
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(input))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	syms := nodesToDocumentSymbols(nodes, nil)
	got := map[string]string{}
	var walk func(prefix string, ss []protocol.DocumentSymbol)
	walk = func(prefix string, ss []protocol.DocumentSymbol) {
		for _, s := range ss {
			got[prefix+s.Name] = fmtRange(s.Range) + " " + fmtRange(s.SelectionRange)
			walk(prefix+s.Name+".", s.Children)
		}
	}
	walk("", syms)
	want := map[string]string{
		"add":                       "1:0-3:1 1:7-1:10",
		"User":                      "5:0-9:1 5:7-5:11",
		"User.name":                 "6:2-6:14 6:2-6:6",
		"User.age":                  "8:2-8:14 8:2-8:5",
		"Shape":                     "11:0-14:1 11:5-11:10",
		"Shape.Circle":              "12:2-12:15 12:2-12:8",
		"Shape.Point":               "13:2-13:7 13:2-13:7",
		"Named":                     "16:0-19:1 16:10-16:15",
		"Named.name":                "17:2-17:30 17:5-17:9",
		"Named.label":               "18:2-18:31 18:5-18:10",
		"impl Named for User":       "21:0-25:1 21:0-21:4",
		"impl Named for User.label": "22:2-24:3 22:5-22:10",
		"limit":                     "27:0-27:15 27:5-27:10",
		"math":                      "29:0-33:1 29:6-29:12",
		"math.adds":                 "30:2-32:3 30:7-30:13",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %q, want %q", k, got[k], w)
		}
	}
	checkSymbolRanges(t, "input", syms, nil)
}
