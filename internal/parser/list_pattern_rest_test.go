package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The rest of a list pattern is a pattern: `..rest` binds the tail, `.._rest`
// names it without binding it, and a bare `..` ignores it, as in a vector
// pattern. The bare form is a wildcard rest the formatter writes as `..`
// alone.
func TestListPatternRest_MayBeBare(t *testing.T) {
	for _, rest := range []string{"..rest", ".._rest", ".."} {
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

// Elements may follow a list pattern's `..`, as they may a vector
// pattern's: they are its Tails, and the rest before them is a name, `_`
// or nothing.
func TestListPatternRest_TailsFollowIt(t *testing.T) {
	for arm, want := range map[string]struct {
		heads, tails int
		bare         bool
	}{
		"[.., ce]":                 {0, 1, true},
		"[first, .., last]":        {1, 1, true},
		"[first, ..mid, last]":     {1, 1, false},
		"[a, b, .._mid, y, z]":     {2, 2, false},
		"[..init, #(k, _)]":        {0, 1, false},
		"[\"a\", ..rest, Some(z)]": {1, 1, false},
	} {
		lp, ok := caseArmPattern(t, arm).(*ast.ListPattern)
		if !ok || lp.TailSpread == nil || len(lp.Heads) != want.heads || len(lp.Tails) != want.tails || lp.BareRest != want.bare {
			t.Errorf("%s: got %#v", arm, lp)
		}
	}
}

// With elements after it, a list pattern's rest is a name or nothing, and
// a pattern takes one `..`.
func TestListPatternRest_BeforeTailsIsAName(t *testing.T) {
	for arm, want := range map[string]string{
		"[a, ..[b], c]":     "a list pattern's `..` takes a name or nothing when elements follow it",
		"[a, .., b, .., c]": "a list pattern takes one `..`",
	} {
		src := "fn f(xs: List<List<Int>>) -> Int {\n    case xs {\n        " + arm + " -> 0\n        _ -> 0\n    }\n}\n"
		if got := parseError(t, src).Error(); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", arm, got, want)
		}
	}
}

// A bare `..` ignores the rest, so `.._` is an error that says to write
// `..`, in a list, vector or set pattern, at the end or before elements.
// `.._name` stays: a labeled discard, as `_name` is.
func TestListPatternRest_DiscardIsBare(t *testing.T) {
	for arm, want := range map[string]string{
		"[a, .._]":      "write `..` to ignore the rest of a list pattern, not `.._`",
		"[a, .._, z]":   "write `..` to ignore the rest of a list pattern, not `.._`",
		"Ids[a, .._]":   "write `..` to ignore the rest of a list pattern, not `.._`",
		"#[a, .._]":     "write `..` to ignore the rest of a vector pattern, not `.._`",
		"#[.._, z]":     "write `..` to ignore the rest of a vector pattern, not `.._`",
		"%[\"a\", .._]": "write `..` to ignore the rest of a set pattern, not `.._`",
	} {
		src := "fn f(xs: List<Int>) -> Int {\n    case xs {\n        " + arm + " -> 0\n        _ -> 0\n    }\n}\n"
		if got := parseError(t, src).Error(); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", arm, got, want)
		}
	}
	for _, arm := range []string{"[a, .._rest]", "[a, .._mid, z]", "#[a, .._rest]"} {
		if rest := restOf(caseArmPattern(t, arm)); rest == nil || rest.Name[0] != '_' {
			t.Errorf("%s: want the labeled rest as a name, got %#v", arm, rest)
		}
	}
}

// restOf answers a list or vector pattern's rest when it is a name.
func restOf(n ast.Node) *ast.IdentPattern {
	switch p := n.(type) {
	case *ast.ListPattern:
		r, _ := p.TailSpread.(*ast.IdentPattern)
		return r
	case *ast.VectorPattern:
		r, _ := p.Rest.(*ast.IdentPattern)
		return r
	}
	return nil
}
