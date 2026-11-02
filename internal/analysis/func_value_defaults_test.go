package analysis_test

import (
	"strings"
	"testing"
)

// A function's defaults apply to a call by name, and to the function named as
// a value where a function type with fewer parameters is expected: the value
// is then the shorter function, whose call fills the rest. Anywhere else the
// value has every parameter, and a call through it passes every argument.

const funcValueDefaultsDecls = `
fn parse(s: String, strict: Bool = False) -> Maybe<Int> {
  if strict { None } else { String.to_int(s) }
}

fn apply(f: (String) -> Maybe<Int>, s: String) -> Maybe<Int> {
  f(s)
}

fn two(a: String, b: Int) -> Int {
  String.length(a) + b
}

fn three(a: String, b: Int, strict: Bool = False) -> Int {
  if strict { 0 } else { String.length(a) + b }
}

fn seed(n: Int = 1) -> Int {
  n
}
`

func TestFuncValueDefaults_FillWhereFewerParametersAreExpected(t *testing.T) {
	for _, stmt := range []string{
		`_ = Maybe.map(Some("1"), parse)`,
		`_ = Iter.map(["1"], parse)`,
		`_ = ["1"] |> Iter.map(parse)`,
		`f: (String) -> Maybe<Int> = parse` + "\n  _ = f(\"1\")",
		`_ = apply(parse, "1")`,
		`_ = apply(f: parse, s: "1")`,
		`f: (String, Int) -> Int = three` + "\n  _ = f(\"a\", 1)",
		`f: () -> Int = seed` + "\n  _ = f()",
		`_ = [["a"], ["b", "c"]] |> Iter.map(String.join)`,
		`_ = Result.map(Ok(["a"]), String.join)`,
	} {
		t.Run(stmt, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + "\nfn main() {\n  " + stmt + "\n}\n")
			expectNoStdlibErrors(t, errs)
		})
	}
}

// The parameters a shorter type leaves out must all have defaults, and a
// pipe's head is checked before the stage it feeds, so nothing is expected of
// it: both are mismatches. The head's hint names the capture to write.
func TestFuncValueDefaults_RejectedWhereADefaultCannotFill(t *testing.T) {
	for _, tc := range []struct{ stmt, want string }{
		{`_ = Iter.map(["1"], two)`, "argument 2: expected (String) -> U, got (String, Int) -> Int"},
		{`_ = parse |> apply("1")`, "`apply` takes (String) -> Maybe<Int> as its first argument, but the pipe passes (String, Bool) -> Maybe<Int>"},
	} {
		t.Run(tc.stmt, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + "\nfn main() {\n  " + tc.stmt + "\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
	_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + "\nfn main() {\n  _ = parse |> apply(\"1\")\n}\n")
	found := false
	for _, e := range errs {
		if strings.Contains(diagText(e), "help: a function value takes its defaults only where its shorter type is expected, so pass a capture that calls it: &parse(&1)") {
			found = true
		}
	}
	if !found {
		t.Errorf("the pipe head's mismatch does not suggest the capture")
	}
}

func TestFuncValueDefaults_AcceptedThroughALambdaOrAPartial(t *testing.T) {
	_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + `
fn main() {
  a: Maybe<Maybe<Int>> = Maybe.map(Some("1"), fn s -> parse(s))
  b: List<Maybe<Int>> = Iter.map(["1"], &parse(&1)) |> Iter.to_list()
  c: List<Maybe<Int>> = Iter.map(["1"], &parse(&1, strict: True)) |> Iter.to_list()
  f: (String) -> Maybe<Int> = fn s -> parse(s)
  g: (String, Bool) -> Maybe<Int> = parse
  _ = #(a, b, c, f("1"), g("1", True), apply(&parse(&1), "2"), parse("3"))
}
`)
	expectNoStdlibErrors(t, errs)
}

// A function's defaults belong to its name and the local names that copy it,
// as a lambda's belong to the name it is bound to. A value reached any other
// way is called with every argument: through an annotated binding,
// parentheses, a parameter, a field, a list element, a `once` or a generic
// function.
func TestFuncValueDefaults_ACallThroughTheValuePassesEveryArgument(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"g: (String, Bool) -> Maybe<Int> = parse\n  _ = g(\"1\")", "expected 2 arguments, got 1"},
		{"fs = [parse]\n  _ = Iter.map(fs, fn f -> f(\"1\")) |> Iter.to_list()", "expected 2 arguments, got 1"},
		{"_ = held(\"1\")", "expected 2 arguments, got 1"},
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
fn pass<T>(x: T) -> T {
  x
}

struct Holder {
  f: (String, Bool) -> Maybe<Int>
}

once held = parse

fn through(f: (String, Bool) -> Maybe<Int>) -> Maybe<Int> {
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

// A direct call fills the defaults, and so does a call through a local name
// that copies a function, a lambda or a partial application: `g = parse` and
// `h = fn x = 1 -> x` both name a function, and their copies keep its
// defaults, owner functions and nested functions included.
func TestFuncValueDefaults_ADirectCallALambdaAndAPartialKeepTheirDefaults(t *testing.T) {
	_, errs := checkSourceWithStdlib(funcValueDefaultsDecls + `
fn main() {
  a = parse("1")
  b = "1" |> parse()
  h = fn x = 1 -> x
  k = &parse(&1)
  g = parse
  g2 = g
  p = Parsing.parse
  fn local(n: Int, m: Int = 5) -> Int {
    n * m
  }
  l = local
  c = fn s: String -> g(s)
  _ = #(a, b, h(), h(2), k("1"), k(s: "1"), g("1", True), "1" |> g(True), g("1"), "1" |> g(), g2("1"), p("1"), l(3), c("1"))
}

struct Parsing {
  n: Int
}

impl Parsing {
  fn parse(s: String, strict: Bool = False) -> Maybe<Int> {
    if strict { None } else { String.to_int(s) }
  }
}
`)
	expectNoStdlibErrors(t, errs)
}
