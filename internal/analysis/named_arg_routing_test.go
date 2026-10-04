package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A named argument is checked against the parameter it names, in a generic
// call and in a pipe, as a non-generic direct call already did. The generic
// direct call used to check it against the parameter at its written position
// (`spread(1, twice: True)` was "argument 2: expected List<Int>, got Bool"),
// and the non-generic pipe did not check its explicit arguments at all.

const namedArgDecls = `
fn spread<T>(x: T, xs: List<T> = [], twice: Bool = False): List<T> {
  base = [x, ..xs]
  if twice { [x, ..base] } else { base }
}

fn g(x: Int, y: Int = 0): Int {
  x + y
}
`

func TestNamedArgRouting_Accepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(namedArgDecls + `
fn main() {
  a: List<Int> = spread(1, twice: True)
  b: List<Int> = 1 |> spread(twice: True)
  c: List<Int> = spread(1, twice: True, xs: [2, 3])
  d: List<Int> = spread(x: 4, xs: [5])
  e: Int = g(1, y: 2)
  f: Int = 1 |> g(y: 2)
  _ = (a, b, c, d, e, f)
}
`)
	expectNoStdlibErrors(t, errs)
}

// expectOneErrorAt asserts exactly one error carries want, at line:col.
func expectOneErrorAt(t *testing.T, errs []analysis.TypeError, want string, line, col int) {
	t.Helper()
	var hits []analysis.TypeError
	for _, e := range errs {
		if strings.Contains(diagText(e), want) {
			hits = append(hits, e)
		}
	}
	if len(hits) != 1 {
		expectStdlibError(t, errs, want)
		t.Fatalf("want exactly one error containing %q, got %d: %v", want, len(hits), hits)
	}
	if hits[0].Line != line || hits[0].Col != col {
		t.Errorf("%q is at %d:%d, want %d:%d", want, hits[0].Line, hits[0].Col, line, col)
	}
}

func TestNamedArgRouting_Rejected(t *testing.T) {
	for _, tc := range []struct {
		call, want string
		col        int
	}{
		{`spread(1, twice: 3)`, "argument 'twice': expected Bool, got Int", 24},
		{`1 |> spread(twice: 3)`, "argument 'twice': expected Bool, got Int", 26},
		{`spread(1, xs: "s")`, "argument 'xs': expected List<Int>, got String", 21},
		{`1 |> g(y: "s")`, "argument 'y': expected Int, got String", 17},
		{`1 |> g("s")`, "argument 2: expected Int, got String", 14},
		{`spread(1, nope: True)`, "no parameter named 'nope'", 17},
		{`1 |> spread(nope: True)`, "no parameter named 'nope'", 19},
		{`g(1, nope: 2)`, "no parameter named 'nope'", 12},
		{`1 |> g(nope: 2)`, "no parameter named 'nope'", 14},
		{`g(1, yy: 2)`, "no parameter named 'yy'\nhelp: did you mean 'y'?", 12},
	} {
		t.Run(tc.call, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(namedArgDecls + "\nfn main() {\n  _ = " + tc.call + "\n}\n")
			// The call is on line 12, after `  _ = `; the error sits on the
			// argument's value, or on the name of an unknown one.
			expectOneErrorAt(t, errs, tc.want, 12, tc.col)
		})
	}
}
