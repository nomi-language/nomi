package analysis_test

import "testing"

// A function's defaults apply to a call and do not shorten its type, so a
// function with a defaulted parameter named as a value does not fit a function
// type with fewer parameters, in any position. The generic call path used to
// let `Maybe.map(m, parse)` through (the mismatch fell to its permissive
// fallback) while the annotated binding was rejected, and the accepted form
// was BLOCKED at run time.

const funcValueDefaultsDecls = `
fn parse(s: String, strict: Bool = False): Maybe<Int> {
  if strict { None } else { String.to_int(s) }
}

fn apply(f: (String) -> Maybe<Int>, s: String): Maybe<Int> {
  f(s)
}

fn two(a: String, b: Int): Int {
  String.length(a) + b
}
`

func TestFuncValueDefaults_RejectedWhereFewerParametersAreExpected(t *testing.T) {
	const hint = "\nhelp: defaults do not apply to a function value, so pass a lambda that calls it: |a| parse(a)"
	for _, tc := range []struct{ stmt, want string }{
		{`_ = Maybe.map(Some("1"), parse)`, "argument 2: expected (String) -> U, got (String, Bool) -> Maybe<Int>" + hint},
		{`_ = Iter.map(["1"], parse)`, "argument 2: expected (String) -> U, got (String, Bool) -> Maybe<Int>" + hint},
		{`_ = ["1"] |> Iter.map(parse)`, "argument 2: expected (String) -> U, got (String, Bool) -> Maybe<Int>" + hint},
		{`f: (String) -> Maybe<Int> = parse`, "type mismatch: expected (String) -> Maybe<Int>, got (String, Bool) -> Maybe<Int>" + hint},
		{`_ = apply(parse, "1")`, "argument 1: expected (String) -> Maybe<Int>, got (String, Bool) -> Maybe<Int>" + hint},
		{`_ = apply(f: parse, s: "1")`, "argument 'f': expected (String) -> Maybe<Int>, got (String, Bool) -> Maybe<Int>" + hint},
		// No defaults: the generic path reports any arity mismatch now.
		{`_ = Iter.map(["1"], two)`, "argument 2: expected (String) -> U, got (String, Int) -> Int"},
	} {
		t.Run(tc.stmt, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + "\nfn main() {\n  " + tc.stmt + "\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}

func TestFuncValueDefaults_AcceptedThroughALambdaOrAPartial(t *testing.T) {
	_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + `
fn main() {
  a: Maybe<Maybe<Int>> = Maybe.map(Some("1"), |s| parse(s))
  b: List<Maybe<Int>> = Iter.map(["1"], parse(_)) |> Iter.to_list()
  c: List<Maybe<Int>> = Iter.map(["1"], parse(_, strict: True)) |> Iter.to_list()
  f: (String) -> Maybe<Int> = |s| parse(s)
  g: (String, Bool) -> Maybe<Int> = parse
  _ = (a, b, c, f("1"), g("1", True), apply(parse(_), "2"), parse("3"))
}
`)
	expectNoStdlibErrors(t, errs)
}
