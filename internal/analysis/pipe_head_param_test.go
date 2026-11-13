package analysis_test

import "testing"

// A pipe's head is checked against the stage's first parameter, as the
// direct call's first argument is, so `x |> f(a)` accepts what `f(x, a)`
// accepts (spec §8 *Pipes*).
func TestPipeHead_IsCheckedAsTheStagesFirstArgument(t *testing.T) {
	const decls = `fn number(i: Int) -> Int
    i
end

fn word(i: Int) -> String
    "w${i}"
end

fn show(fs: List<(Int) -> Display>) -> String
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(",")
end

fn show_with(fs: List<(Int) -> Display>, sep: String) -> String
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(sep)
end

fn apply(f: (Int) -> Int, x: Int) -> Int
    f(x)
end

fn total(xs: List<Int>) -> Int
    Iter.reduce(xs, 0, fn a, b -> a + b)
end

fn low(b: Byte) -> Int
    Byte.to_int(b)
end

`
	for name, body := range map[string]string{
		"direct": `fn f() -> String
    show([number, word])
end`,
		"pipe": `fn f() -> String
    [number, word] |> show()
end`,
		"pipe with more arguments": `fn f() -> String
    [number, word] |> show_with(";")
end`,
		"pipe with a named argument": `fn f() -> String
    [number, word] |> show_with(sep: ";")
end`,
		"then capture": `fn f() -> String
    [number, word] |> then &show(&1)
end`,
		"then capture at a later slot": `fn f() -> String
    ";" |> then &show_with([number, word], &1)
end`,
		"lambda head": `fn f() -> Int
    (fn x -> x + 1) |> apply(2)
end`,
		"capture head": `fn f() -> Int
    &(&1 * 3) |> apply(2)
end`,
		"empty list head": `fn f() -> Int
    [] |> total()
end`,
		"byte literal head": `fn f() -> Int
    7 |> low()
end`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body + "\n")
			expectNoErrorsT(t, errs)
		})
	}
}

// A bound value converts at a pipe's head as it does at the call's first
// argument (spec §5 *Covariant conversion*): a `List<(Int) -> Int>` is a
// `List<(Int) -> Display>`, since each function already returns a Display.
func TestPipeHead_ABoundValueConvertsAsTheArgumentDoes(t *testing.T) {
	const decls = `fn number(i: Int) -> Int
    i
end

fn show(fs: List<(Int) -> Display>) -> String
    fs |> Iter.map(fn f -> Display.to_string(f(1))) |> String.join(",")
end

`
	for name, body := range map[string]string{
		"direct": `fn f() -> String
    ints = [number]
    show(ints)
end
`,
		"pipe": `fn f() -> String
    ints = [number]
    ints |> show()
end
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
	const src = `interface Gets<T>
    fn got(value: self) -> T
end

struct Box
    s: String
end

impl Gets<String> for Box
    fn got(b: Box) -> String
        b.s
    end
end

impl Gets<Int> for Box
    fn got(b: Box) -> Int
        String.length(b.s)
    end
end

fn f(texts: List<Gets<String>>) -> String
    String.join(Iter.map(texts, &Gets.got(&1)), ",")
end
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
	const decls = `fn run<T>(f: (String) -> T, s: String) -> T
    f(s)
end

fn run_all<T>(fs: List<(String) -> T>, s: String) -> List<T>
    fs |> Iter.map(fn f -> f(s)) |> Iter.to_list()
end

fn run_maybe<T>(f: (String) -> Maybe<T>, s: String) -> Maybe<T>
    f(s)
end

fn run_pairs<K, T>(ps: List<(K, (String) -> T)>, s: String) -> List<(K, T)>
    ps |> Iter.map(fn p -> (p.first, p.second(s))) |> Iter.to_list()
end

fn parse(s: String, base: Int = 10) -> Maybe<Int>
    Some(String.length(s) * base)
end

fn parse_hex(s: String) -> Maybe<Int>
    Some(String.length(s) * 16)
end

fn size(s: String) -> Int
    String.length(s)
end

fn parse_in(s: String, base: Int) -> Maybe<Int>
    Some(String.length(s) * base)
end

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
				_, errs := checkSourceWithStdlib(decls + "fn f()\n    _ = " + expr + "\nend\n")
				if tc.ok {
					expectNoErrorsT(t, errs)
				} else if len(errs) == 0 {
					t.Fatalf("%s checks, but the extra parameter has no default", expr)
				}
			})
		}
	}
}
