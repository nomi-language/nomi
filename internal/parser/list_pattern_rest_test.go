package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The rest of a list pattern is a pattern: `..rest` binds the tail and `.._`
// ignores it, and a bare `..` ignores it too, as in a vector pattern. The
// bare form is a wildcard rest the formatter writes as `..` alone.
func TestListPatternRest_MayBeBare(t *testing.T) {
	for _, rest := range []string{"..rest", ".._", ".."} {
		lp, ok := caseArmPattern(t, "[ce, "+rest+"]").(*ast.ListPattern)
		if !ok || len(lp.Heads) != 1 || lp.TailSpread == nil {
			t.Fatalf("[ce, %s]: want one head and a rest, got %#v", rest, lp)
		}
		if lp.BareRest != (rest == "..") {
			t.Errorf("[ce, %s]: BareRest = %v", rest, lp.BareRest)
		}
		if _, wild := lp.TailSpread.(*ast.WildcardPattern); rest == ".." && !wild {
			t.Errorf("[ce, ..]: rest is %T, want the wildcard", lp.TailSpread)
		}
		parse(t, "fn f(xs: List<Int>) -> Int {\n    [ce, "+rest+"] = xs else { return 0 }\n    ce\n}\n")
	}
}

// A `..` must be the last element of a list pattern, bare or not.
func TestListPatternRest_IsLast(t *testing.T) {
	src := "fn f(xs: List<Int>) -> Int {\n    case xs {\n        [.., ce] -> ce\n        _ -> 0\n    }\n}\n"
	got := parseError(t, src).Error()
	want := "line 3, col 10: list pattern spread `..` must be the last element"
	if got != want {
		t.Errorf("Parse:\n got %q\nwant %q", got, want)
	}
}
