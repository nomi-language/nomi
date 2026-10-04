package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// errorsAt is every error message reported at line:col.
func errorsAt(errs []analysis.TypeError, line, col int) []string {
	var out []string
	for _, e := range errs {
		if e.Line == line && e.Col == col {
			out = append(out, diagText(e))
		}
	}
	return out
}

// TestLambdaArgDiagnostic_ReportedOnce pins that an error inside a lambda is
// reported once when the call holding the lambda is the first argument of an
// interface-qualified call (`Iter.count(Iter.filter(xs, |x| ...))`), or is
// that first argument itself (`Iter.count(xs)` with `xs` unbound). checkCall checks that first argument once to read its
// type for specializing the interface's parameters, and again against its
// parameter; only the second check reports.
func TestLambdaArgDiagnostic_ReportedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"nested call",
			"fn f(): Int {\n    Iter.count(Iter.filter([1, 2, 3], |x| x > nope))\n}\n",
			2, 47, "undefined variable 'nope'",
		},
		{
			"interface call inside a pipe stage",
			"fn f(): Int {\n    limit = 2\n    [1, 2, 3]\n    |> |xs| xs |> Iter.filter(|x| x > Iter.count(xs) - limit)\n    |> Iter.count()\n}\n",
			4, 50, "'xs' is the parameter of the lambda stage on line 4, whose body ends at the next `|>`\nhelp: write `|xs| { ... }` to keep the pipe inside it",
		},
		{
			"plain generic call",
			"fn apply<T>(x: T, f: (T) -> T): T {\n    f(x)\n}\n\nfn f(): Int {\n    apply(1, |n| n + nope)\n}\n",
			6, 22, "undefined variable 'nope'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := errorsAt(errs, tc.line, tc.col)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors at %d:%d = %q, want exactly [%q]; all: %v", tc.line, tc.col, got, tc.want, errs)
			}
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
			}
		})
	}
}

// TestLambdaArgDiagnostic_ValidNestedCallAccepted is the acceptance mirror.
func TestLambdaArgDiagnostic_ValidNestedCallAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(): Int {\n    limit = 1\n    Iter.count(Iter.filter([1, 2, 3], |x| x > limit))\n}\n")
	expectNoStdlibErrors(t, errs)
}

// TestLambdaBodyDiagnostic_ReportedOnce pins that an error inside a lambda's
// body is reported once wherever the lambda stands. A function whose body is
// a lone lambda and whose declared return is a function type used to check
// that lambda twice (once through checkBlockExpectingReturn, then again in
// checkFunc), reporting each error in its body twice.
func TestLambdaBodyDiagnostic_ReportedOnce(t *testing.T) {
	const decls = "fn apply<T>(x: T, f: (T) -> T): T {\n    f(x)\n}\n\nfn plain(x: Int, f: (Int) -> Int): Int {\n    f(x)\n}\n\n"
	for _, tc := range []struct{ name, src string }{
		{"generic function", "fn f(): Int {\n    apply(1, |n| n + nope)\n}\n"},
		{"owner function", "fn f(): Maybe<Int> {\n    Maybe.map(Some(1), |n| n + nope)\n}\n"},
		{"interface owner function", "fn f(): List<Int> {\n    Iter.map([1, 2], |n| n + nope) |> Iter.to_list()\n}\n"},
		{"file function", "fn f(): Int {\n    plain(1, |n| n + nope)\n}\n"},
		{"nested lambdas", "fn f(): Int {\n    apply(1, |n| apply(n, |m| m + nope))\n}\n"},
		{"pipe lambda stage", "fn f(): Int {\n    1 |> |n| n + nope\n}\n"},
		{"pipe call stage", "fn f(): List<Int> {\n    [1, 2] |> Iter.map(|n| n + nope) |> Iter.to_list()\n}\n"},
		{"annotated binding", "fn f(): Int {\n    g: (Int) -> Int = |n| n + nope\n    g(1)\n}\n"},
		{"function body", "fn f(): (Int) -> Int {\n    |n| n + nope\n}\n"},
		{"function body, annotated parameter", "fn f(): (Int) -> Int {\n    |n: Int| n + nope\n}\n"},
		{"nested function body", "fn f(): Int {\n    fn g(): (Int) -> Int {\n        |n| n + nope\n    }\n    g()(1)\n}\n"},
		{"stray placeholder", "fn f(): (Int) -> Int {\n    |n| n + _\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + tc.src)
			want := "undefined variable 'nope'"
			if tc.name == "stray placeholder" {
				want = "`_` has no value here\nhelp: `_` stands for a missing argument only in a call, as in add(1, _)"
			}
			if len(errs) != 1 || diagText(errs[0]) != want {
				t.Fatalf("errors = %v, want exactly one %q", errs, want)
			}
		})
	}
}

// TestLambdaBodyDiagnostic_FunctionBodyLambdaInfersParams is the acceptance
// mirror: a lone lambda body still takes its parameter types from the
// declared return type.
func TestLambdaBodyDiagnostic_FunctionBodyLambdaInfersParams(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(k: Int): (Int) -> Int {\n    |n| n + k\n}\n")
	expectNoStdlibErrors(t, errs)
}
