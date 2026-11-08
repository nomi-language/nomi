package analysis_test

import "testing"

// A pipe's head is checked against the stage's first parameter, as the
// direct call's first argument is, so `x |> f(a)` accepts what `f(x, a)`
// accepts (spec §8 *Pipes*).
func TestPipeHead_IsCheckedAsTheStagesFirstArgument(t *testing.T) {
	const decls = `fn number(i: Int) -> Int {
    i
}

fn word(i: Int) -> String {
    "w${i}"
}

fn show(fs: List<(Int) -> Display>) -> String {
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(",")
}

fn show_with(fs: List<(Int) -> Display>, sep: String) -> String {
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(sep)
}

fn apply(f: (Int) -> Int, x: Int) -> Int {
    f(x)
}

fn total(xs: List<Int>) -> Int {
    Iter.reduce(xs, 0, fn a, b -> a + b)
}

fn low(b: Byte) -> Int {
    Byte.to_int(b)
}

`
	for name, body := range map[string]string{
		"direct":                       `fn f() -> String { show([number, word]) }`,
		"pipe":                         `fn f() -> String { [number, word] |> show() }`,
		"pipe with more arguments":     `fn f() -> String { [number, word] |> show_with(";") }`,
		"pipe with a named argument":   `fn f() -> String { [number, word] |> show_with(sep: ";") }`,
		"then capture":                 `fn f() -> String { [number, word] |> then &show(&1) }`,
		"then capture at a later slot": `fn f() -> String { ";" |> then &show_with([number, word], &1) }`,
		"lambda head":                  `fn f() -> Int { (fn x -> x + 1) |> apply(2) }`,
		"capture head":                 `fn f() -> Int { &(&1 * 3) |> apply(2) }`,
		"empty list head":              `fn f() -> Int { [] |> total() }`,
		"byte literal head":            `fn f() -> Int { 7 |> low() }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body + "\n")
			expectNoErrorsT(t, errs)
		})
	}
}

// A bound value converts at a pipe's head as it does at the call's first
// argument (spec §5 *Type argument conversion*): a `List<(Int) -> Int>` is a
// `List<(Int) -> Display>`, since each function already returns a Display.
func TestPipeHead_ABoundValueConvertsAsTheArgumentDoes(t *testing.T) {
	const decls = `fn number(i: Int) -> Int {
    i
}

fn show(fs: List<(Int) -> Display>) -> String {
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(",")
}

`
	for name, body := range map[string]string{
		"direct": `fn f() -> String {
    ints = [number]
    show(ints)
}
`,
		"pipe": `fn f() -> String {
    ints = [number]
    ints |> show()
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoErrorsT(t, errs)
		})
	}
}

// A partial capture of an interface's function whose result names the
// interface's type parameter is a function of its open slot: where a
// `(Gets<String>) -> String` is expected, T is String, not that function.
func TestPartialCapture_OfAnInterfaceFunctionMeetsTheExpectedFunction(t *testing.T) {
	const src = `interface Gets<T> {
    fn got(value: self) -> T
}

struct Box {
    s: String
}

impl Gets<String> for Box {
    fn got(b: Box) -> String {
        b.s
    }
}

impl Gets<Int> for Box {
    fn got(b: Box) -> Int {
        String.length(b.s)
    }
}

fn f(texts: List<Gets<String>>) -> String {
    String.join(Iter.map(texts, &Gets.got(&1)), ",")
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
}

// A head whose stage's first parameter names a type parameter is checked
// against that parameter as the direct call's first argument is, so a
// function with defaults narrows to the function type the parameter names
// (spec §8 *Pipes*): `parse |> run("1")` checks as `run(parse, "1")` does.
// Each case's call and pipe agree, both accepted or both refused.
func TestPipeHead_AGenericParameterChecksTheHeadAsTheCallDoes(t *testing.T) {
	const decls = `fn run<T>(f: (String) -> T, s: String) -> T {
    f(s)
}

fn run_all<T>(fs: List<(String) -> T>, s: String) -> List<T> {
    fs |> Iter.map(fn f -> f(s)) |> Iter.to_list()
}

fn run_maybe<T>(f: (String) -> Maybe<T>, s: String) -> Maybe<T> {
    f(s)
}

fn run_pairs<K, T>(ps: List<(K, (String) -> T)>, s: String) -> List<(K, T)> {
    ps |> Iter.map(fn p -> (p.first, p.second(s))) |> Iter.to_list()
}

fn parse(s: String, base: Int = 10) -> Maybe<Int> {
    Some(String.length(s) * base)
}

fn parse_hex(s: String) -> Maybe<Int> {
    Some(String.length(s) * 16)
}

fn size(s: String) -> Int {
    String.length(s)
}

fn parse_in(s: String, base: Int) -> Maybe<Int> {
    Some(String.length(s) * base)
}

`
	for _, tc := range []struct {
		name, direct, pipe string
		ok                 bool
	}{
		{"a defaulted function", `run(parse, "1")`, `parse |> run("1")`, true},
		{"a function with no defaults", `run(size, "ab")`, `size |> run("ab")`, true},
		{"a lambda", `run(fn s -> String.length(s), "ab")`, `(fn s -> String.length(s)) |> run("ab")`, true},
		{"a list of functions", `run_all([parse, parse_hex], "10")`, `[parse, parse_hex] |> run_all("10")`, true},
		{"a nested generic", `run_maybe(parse, "1")`, `parse |> run_maybe("1")`, true},
		{"a list of tuples of functions", `run_pairs([("dec", parse), ("hex", parse_hex)], "10")`, `[("dec", parse), ("hex", parse_hex)] |> run_pairs("10")`, true},
		{"a function whose extra parameter has no default", `run(parse_in, "1")`, `parse_in |> run("1")`, false},
	} {
		for form, expr := range map[string]string{"direct": tc.direct, "pipe": tc.pipe} {
			t.Run(tc.name+"/"+form, func(t *testing.T) {
				_, errs := checkSourceWithStdlib(decls + "fn f() {\n    _ = " + expr + "\n}\n")
				if tc.ok {
					expectNoErrorsT(t, errs)
				} else if len(errs) == 0 {
					t.Fatalf("%s checks, but the extra parameter has no default", expr)
				}
			})
		}
	}
}
