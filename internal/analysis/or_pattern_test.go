package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `A or B` matches a value either alternative matches.

const orDecls = `enum Shape {
    Circle {r: Float}
    Square {r: Float}
    Point
}

enum Color {
    Red
    Green
    Blue
}

enum Two {
    A Int
    B Int
}

enum Mixed {
    N Int
    S String
}

enum Three {
    A Int
    B Int
    C
}
`

func orErrorsOf(t *testing.T, src string) []string {
	t.Helper()
	return elseErrors(t, orDecls+src)
}

// Every accepted form checks with no error: each position, nested `or`,
// `as` and `when`, and the alternatives covering an enum without `_`.
func TestOrPattern_Accepts(t *testing.T) {
	src := `fn arms(s: Shape, c: Color, t: Two, cs: List<Codepoint>, m: Maybe<Color>, word: String, n: Int) -> Float {
    a = case s {
        #.Circle{r} or #.Square{r} when r > 10.0 -> r
        #.Circle{r} or #.Square{r} -> r * 2.0
        .Point -> 0.0
    }
    b = case c {
        .Red or .Green -> 1
        .Blue -> 2
    }
    d = case t {
        .A(k) or .B(k) as whole -> k + case whole { .A(_) -> 0, .B(_) -> 1 }
    }
    e = case cs {
        [')' or ']' or '}' as closer, ..rest] -> Iter.count(rest) + case closer { ')' -> 1, _ -> 2 }
        _ -> 0
    }
    f = case m {
        Some(.Red or .Green) -> 1
        Some(.Blue) or None -> 2
    }
    g = case word {
        "yes" or "y" -> 1
        _ -> 0
    }
    h = case #(n, n) {
        #(0, x) or #(x, 0) -> x
        #(x, _) -> x
    }
    a + Int.to_float(b + d + e + f + g + h)
}

fn else_binding(t: Three) -> Int {
    .A(k) or .B(k) as _whole = t else { return 0 }
    k
}

fn if_binding(t: Three) -> Int {
    if .A(k) or .B(k) = t { k } else { 0 }
}

fn plain(t: Two) -> Int {
    .A(k) or .B(k) = t
    k
}

fn param(.A(k) or .B(k): Two) -> Int {
    k
}

fn lambda(ts: List<Two>) -> List<Int> {
    ts |> Iter.map(fn .A(k) or .B(k) -> k) |> Iter.to_list()
}
`
	if errs := orErrorsOf(t, src); len(errs) != 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(errs, "\n"))
	}
}

// The name an `or` binds has the type it has in each alternative.
func TestOrPattern_NameHasItsType(t *testing.T) {
	fa, errs := checkSourceWithStdlib(orDecls + `fn f(s: Shape) -> Float {
    case s {
        #.Circle{r} or #.Square{r} -> r
        .Point -> 0.0
    }
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var sites []analysis.Pos
	var sym *analysis.Symbol
	for pos, s := range fa.Definitions {
		if s != nil && s.Name == "r" && s.Kind == analysis.SymbolBinding {
			sites = append(sites, pos)
			if sym != nil && sym != s {
				t.Fatalf("`r` is two symbols; want one with two definition sites")
			}
			sym = s
		}
	}
	if len(sites) != 2 {
		t.Fatalf("want two definition sites of `r`, got %v", sites)
	}
	if sym.Type == nil || sym.Type.String() != "Float" {
		t.Fatalf("r: type %v, want Float", sym.Type)
	}
}

func TestOrPattern_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a name one alternative lacks": {`fn f(s: Shape) -> Float {
    case s {
        #.Circle{r} or .Point -> r
        #.Square{r} -> r
    }
}`, "30:24: `r` is bound by `#.Circle{r}` but not by `.Point`; every alternative of an `or` pattern must bind the same names"},
		"a name only a later alternative binds": {`fn f(s: Shape) -> Float {
    case s {
        .Point or #.Circle{r} -> r
        #.Square{r} -> r
    }
}`, "30:28: `r` is bound by `#.Circle{r}` but not by `.Point`; every alternative of an `or` pattern must bind the same names"},
		"a name at two types": {`fn f(m: Mixed) -> Int {
    case m {
        .N(v) or .S(v) -> case v { _ -> 0 }
    }
}`, "30:21: `v` is an Int in `.N(v)` but a String in `.S(v)`; a name an `or` pattern binds has one type in every alternative"},
		"a name twice in a later alternative": {`fn f(p: #(Int, Int)) -> Int {
    case p {
        #(0, a) or #(a, a) -> a
        _ -> 0
    }
}`, "30:25: `a` is bound twice in one pattern; give each binding its own name"},
		"alternatives that miss a variant": {`fn f(c: Color) -> Int {
    case c {
        .Red or .Green -> 1
    }
}`, "29:5: non-exhaustive case on Color: missing Blue"},
		"nested alternatives that miss a variant": {`fn f(m: Maybe<Color>) -> Int {
    case m {
        Some(.Red or .Green) -> 1
        None -> 2
    }
}`, "29:5: non-exhaustive case on Maybe: missing Some"},
		"a refutable or in a parameter": {`fn f(.Red or .Green: Color) -> Int {
    1
}`, "28:6: refutable pattern in parameter; bind the parameter and use a `case` in the body"},
		"a refutable or in a binding": {`fn f(c: Color) -> Int {
    .Red or .Green = c
    1
}`, "29:5: this pattern can fail to match a value of type Color; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`"},
		"an irrefutable or with else": {`fn f(t: Two) -> Int {
    .A(k) or .B(k) = t else { return 0 }
    k
}`, "29:24: this pattern always matches; remove the else"},
		"a guard that is not Bool": {`fn f(t: Two) -> Int {
    case t {
        .A(k) or .B(k) when k -> k
        _ -> 0
    }
}`, "30:29: `when` guard must be Bool, got Int"},
	} {
		errs := orErrorsOf(t, tc.body+"\n")
		if len(errs) != 1 || errs[0] != tc.want {
			t.Errorf("%s: got:\n%s\nwant one error:\n%s", name, strings.Join(errs, "\n"), tc.want)
		}
	}
}

// A name an `or` pattern binds and never reads is reported once.
func TestOrPattern_UnusedNameReportedOnce(t *testing.T) {
	errs := orErrorsOf(t, `fn f(t: Two) -> Int {
    case t {
        .A(k) or .B(k) -> 0
    }
}
`)
	if len(errs) != 1 || !strings.Contains(errs[0], "binding 'k' is never read") {
		t.Fatalf("got:\n%s", strings.Join(errs, "\n"))
	}
}

// ast.WithoutAs looks through an `as` around an `or`, not into it.
func TestOrPattern_WithoutAsStopsAtTheOr(t *testing.T) {
	or := &ast.OrPattern{Alternatives: []ast.Node{&ast.WildcardPattern{}, &ast.WildcardPattern{}}}
	if ast.WithoutAs(&ast.AsPattern{Pattern: or, Name: "x"}) != or {
		t.Fatal("want the or pattern")
	}
}
