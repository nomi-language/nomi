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

// Only a call of a function by name fills its defaults. Named as a value, it
// is called with every argument, through a binding, a chain of bindings, a
// pipe, parentheses, a parameter, a field or a generic function. The checker
// used to carry the defaults on the value's type, so `g("1")` after
// `g = parse` was admitted and the IR builder then declined the call.
func TestFuncValueDefaults_ACallThroughTheValuePassesEveryArgument(t *testing.T) {
	const held = "expected 2 arguments, got 1: `g` holds the function `parse` as a value, and defaults apply only to a call of `parse` by name" +
		"\nhelp: call `parse` directly, or bind a lambda that leaves the rest to its defaults: `g = |s| parse(s)`"
	for _, tc := range []struct{ body, want string }{
		{"g = parse\n  _ = g(\"1\")", held},
		{"g: (String, Bool) -> Maybe<Int> = parse\n  _ = g(\"1\")", held},
		{"f = parse\n  g = f\n  _ = g(\"1\")", held},
		{"g = parse\n  _ = \"1\" |> g()", "expected 2 arguments, got 1 (counting the piped value): `g` holds the function `parse` as a value, and defaults apply only to a call of `parse` by name"},
		{"_ = (parse)(\"1\")", "expected 2 arguments, got 1"},
		{"_ = \"1\" |> (parse)()", "expected 2 arguments, got 1 (counting the piped value)"},
		{"_ = pass(parse)(\"1\")", "expected 2 arguments, got 1"},
		{"b = Holder{f: parse}\n  _ = b.f(\"1\")", "expected 2 arguments, got 1"},
		{"_ = through(parse)", "expected 2 arguments, got 1 (counting the piped value)"},
		// A function value's parameters have no names.
		{"g = parse\n  _ = g(s: \"1\", strict: True)", "no parameter named 's'"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + `
fn pass<T>(x: T): T {
  x
}

struct Holder {
  f: (String, Bool) -> Maybe<Int>
}

fn through(f: (String, Bool) -> Maybe<Int>): Maybe<Int> {
  "1" |> f()
}

fn main() {
  ` + tc.body + `
}
`)
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// A direct call fills the defaults, and so does a call of a lambda or a
// partial application through its value: their defaults belong to the value.
func TestFuncValueDefaults_ADirectCallALambdaAndAPartialKeepTheirDefaults(t *testing.T) {
	_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + `
fn main() {
  a = parse("1")
  b = "1" |> parse()
  h = |x = 1| x
  k = parse(_)
  g = parse
  _ = (a, b, h(), h(2), k("1"), k(s: "1"), g("1", True), "1" |> g(True))
}
`)
	expectNoStdlibErrors(t, errs)
}
