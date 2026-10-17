package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A list pattern's rest is a pattern: `..rest` binds the tail and `.._`
// ignores it. A bare `..` names nothing, and the error says how to write it,
// in a case arm and on a binding's left side.
func TestListPatternRest_NeedsAName(t *testing.T) {
	for _, c := range []struct{ src, at string }{
		{"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [ce, ..] -> ce\n        _ -> 0\n    }\n}\n", "line 3, col 14"},
		{"fn f(xs: List<Int>) -> Int {\n    [ce, ..] = xs else { return 0 }\n    ce\n}\n", "line 2, col 10"},
	} {
		got := parseError(t, c.src).Error()
		want := c.at + ": a list pattern's rest needs a name: write `..rest` or `.._`"
		if got != want {
			t.Errorf("Parse(%q):\n got %q\nwant %q", c.src, got, want)
		}
	}

	for _, rest := range []string{"..rest", ".._"} {
		lp, ok := caseArmPattern(t, "[ce, "+rest+"]").(*ast.ListPattern)
		if !ok || len(lp.Heads) != 1 || lp.TailSpread == nil {
			t.Fatalf("[ce, %s]: want one head and a rest, got %#v", rest, lp)
		}
		parse(t, "fn f(xs: List<Int>) -> Int {\n    [ce, "+rest+"] = xs else { return 0 }\n    ce\n}\n")
	}
}
