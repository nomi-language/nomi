package format

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

func TestRenderAttachedTestBody(t *testing.T) {
	nodes, err := parser.Parse(lexer.Lex(`
//! assert answer() == 42
fn answer() -> Int {
  42
}

//! label = #Label{text: "done"}
//! assert Label.render(label) == "[done]"
//! refute Label.render(label) == "[ready]"
fn done_label() -> String {
  Label.render(#Label{text: "done"})
}
`))
	if err != nil {
		t.Fatal(err)
	}

	assertFn := findFunc(t, nodes, "answer")
	if got := RenderAttachedTestBody(assertFn.AttachedTests[0]); got != "assert answer() == 42" {
		t.Fatalf("unexpected assert body:\n%s", got)
	}

	testFn := findFunc(t, nodes, "done_label")
	want := `label = #Label{text: "done"}
assert Label.render(label) == "[done]"
refute Label.render(label) == "[ready]"`
	if got := RenderAttachedTestBody(testFn.AttachedTests[0]); got != want {
		t.Fatalf("unexpected test body:\n%s", got)
	}
}

func findFunc(t *testing.T, nodes []ast.Node, name string) *ast.FuncDef {
	t.Helper()
	for _, node := range nodes {
		if fn, ok := node.(*ast.FuncDef); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function %q not found", name)
	return nil
}

// A reference editor formats its body as the `//!` prompt it came from is
// formatted: at the prompt's width, not the four columns narrower a test
// block's body would have.
func TestFormatAttachedTestBodyMatchesThePrompt(t *testing.T) {
	shown := `assert Uuid.parse("6ba7b81g-9dad-11d1-80b4-00c04fd430c8") == Err(#Error.Digit{index: 7, found: "g"})`
	got, err := FormatAttachedTestBody(shown)
	if err != nil {
		t.Fatal(err)
	}
	if got != shown {
		t.Fatalf("a 98-column assertion that fits a prompt was rewritten:\n%s", got)
	}
	got, err = FormatAttachedTestBody("x = 1\n\nassert   x == 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "x = 1\n\nassert x == 1"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := FormatAttachedTestBody("x = (\n"); err == nil {
		t.Fatal("a body that does not parse formatted without an error")
	}
}
