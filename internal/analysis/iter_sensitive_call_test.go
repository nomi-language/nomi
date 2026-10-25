package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// iterSensitiveCallError is the error at a call of the iter-sensitive
// function name, which uses words (`break`, `continue`, or both); shape is
// the callback call the message suggests, or "" for none.
func iterSensitiveCallError(name, words, shape string) string {
	if shape == "" {
		return fmt.Sprintf("`%s` uses %s, so it cannot be called: pass it as an iteration callback itself", name, words)
	}
	return fmt.Sprintf("`%s` uses %s, so it cannot be called: pass it as an iteration callback itself, as in `%s`", name, words, shape)
}

// iterSensitiveUseError is the error at a value use of the iter-sensitive
// function name, which suggests shape.
func iterSensitiveUseError(name, words, shape string) string {
	return fmt.Sprintf("`%s` uses %s, so it can only be passed as an iteration callback itself, as in `%s`", name, words, shape)
}

// iterSensitiveDecideHint is the help at a call of name inside a callback.
func iterSensitiveDecideHint(name, words string) string {
	return fmt.Sprintf("to decide in this callback instead, return a `Maybe` or an enum from `%s` and write %s here", name, words)
}

const strayContinueError = "'continue' can only be used inside an iter-callback (e.g. a lambda passed to Iter.map/Iter.filter/Iter.loop/etc.)"

const keepHelpers = `fn keep(acc: Int, x: Int) -> Int {
    if x < 0 { continue }
    acc + x
}

fn skip(x: Int) -> Int {
    if x < 0 { continue }
    if x > 100 { break }
    x
}

fn emit(s: Int, x: Int) -> #(Int, Int) {
    if x < 0 { continue }
    #(x, s + x)
}

fn big(x: Int) -> Bool {
    if x < 0 { continue }
    x > 1
}

fn step(n: Int) -> Int {
    if n > 3 { break n }
    n + 1
}

fn add(acc: #(Int, List<Int>), x: Int) -> #(Int, List<Int>) {
    if x < 0 { continue }
    #(acc.0 + x, acc.1 + [x])
}

struct Acc {
    total: Int
    items: List<Int>
}

fn add_acc(acc: Acc, x: Int) -> Acc {
    if x < 0 { continue }
    #Acc{total: acc.total + x, items: acc.items + [x]}
}

fn apply(f: (Int) -> Int, x: Int) -> Int {
    f(x)
}
`

// iterSensitivityErrors is every error of src that is about where `break`,
// `continue` or an iter-sensitive function stands.
func iterSensitivityErrors(src string) []string {
	_, errs := checkSourceWithStdlib(src)
	var got []string
	for _, e := range errs {
		if strings.Contains(e.Message, "uses `") || strings.Contains(e.Message, "break") || strings.Contains(e.Message, "continue") {
			got = append(got, e.Message)
		}
	}
	return got
}

// The one way an iter-sensitive function may be used: as an iteration
// callback itself, positional, by name, piped, or after a seeded call's
// `from:`, for every iteration function, a fold's and a loop's with their
// starting value as `from:`, tuple and struct starting values included.
func TestIterSensitiveCall_AdmitsTheCallbackItself(t *testing.T) {
	src := keepHelpers + `
fn main() {
    _ = Iter.map([1, 2], skip) |> Iter.to_list()
    _ = [1, 2] |> Iter.map(skip) |> Iter.to_list()
    _ = Iter.filter([1, 2], big) |> Iter.to_list()
    _ = Iter.find([1, 2], f: big)
    _ = Iter.iterate(1, step) |> Iter.take(2) |> Iter.to_list()
    _ = Iter.reduce([1, 2], keep, from: 0)
    _ = [1, 2] |> Iter.reduce(keep, from: 0)
    _ = Iter.reduce([1, 2], f: keep, from: 0)
    _ = Iter.reduce([1, 2], from: 0, keep)
    _ = Iter.scan([1, 2], keep, from: 0) |> Iter.to_list()
    _ = Iter.map_reduce([1, 2], emit, from: 0)
    _ = Iter.loop(step, from: 0)
    _ = Iter.reduce([1, 2], add, from: #(0, []))
    _ = Iter.reduce([1, 2], add_acc, from: #Acc{total: 0, items: []})
    _ = Map.map_values(%{"a" => 1}, skip)
}
`
	if got := iterSensitivityErrors(src); len(got) != 0 {
		t.Errorf("the checker rejects an allowed form: %v\n%s", got, src)
	}
}

// Every call is an error at the call, which names the keyword the function
// uses and suggests passing the function itself in a shape that fits it:
// the whole body of a callback lambda or a capture, which once forwarded
// the signal, a binding then the call, an `if` or `case` arm, a computed
// argument, a call as another call's argument, a lambda or capture that is
// not itself an iteration callback, a `defer`, a `concurrent` block, and a
// call outside every callback. A value is an error too.
func TestIterSensitiveCall_RejectsEveryCall(t *testing.T) {
	const both, cont = "`break` and `continue`", "`continue`"
	const mapSkip, reduceKeep = "Iter.map(xs, skip)", "Iter.reduce(xs, keep, from: <start>)"
	for _, c := range []struct {
		name, body, callee, words, shape string
		inCallback                       bool
	}{
		{"the whole body of a map lambda", "_ = Iter.map([1], fn x -> skip(x)) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"the whole body of a capture", "_ = Iter.map([1], &skip(&1)) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"the whole body of a find lambda", "_ = Iter.find([1], f: fn x -> big(x))", "big", cont, "Iter.find(xs, big)", true},
		{"the whole body of an iterate lambda", "_ = Iter.iterate(1, fn n -> step(n)) |> Iter.take(2) |> Iter.to_list()", "step", "`break`", "Iter.iterate(seed, step)", true},
		{"the whole body of a reduce lambda", "_ = Iter.reduce([1], fn acc = 0, x -> keep(acc, x))", "keep", cont, reduceKeep, true},
		{"the whole body of a scan lambda", "_ = Iter.scan([1], fn acc = 0, x -> keep(acc, x)) |> Iter.to_list()", "keep", cont, "Iter.scan(xs, keep, from: <start>)", true},
		{"the whole body of a map_reduce lambda", "_ = Iter.map_reduce([1], fn s = 0, x -> emit(s, x))", "emit", cont, "Iter.map_reduce(xs, emit, from: <start>)", true},
		{"the whole body of a loop lambda", "_ = Iter.loop(fn n = 0 -> step(n))", "step", "`break`", "Iter.loop(step, from: <start>)", true},
		{"a pattern parameter rebuilt", "_ = Iter.reduce([1], fn #(n, xs) = #(0, []), x -> add(#(n, xs), x))", "add", cont, "Iter.reduce(xs, add, from: <start>)", true},
		{"a binding then the call", "_ = Iter.reduce([1], fn acc = 0, x -> {\n        y = x * 10\n        keep(acc, y)\n    })", "keep", cont, reduceKeep, true},
		{"an if arm", "_ = Iter.reduce([1], fn acc = 0, x -> if x > 1 { keep(acc, x) } else { acc })", "keep", cont, reduceKeep, true},
		{"a case arm", "_ = Iter.map([1], fn x -> case x {\n        1 -> skip(x)\n        _ -> 0\n    }) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"a computed argument", "_ = Iter.reduce([1], fn acc = 0, x -> keep(acc, x * 2))", "keep", cont, reduceKeep, true},
		{"an argument of another call", "_ = Iter.map([1], fn x -> apply(fn y -> y, skip(x))) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"an operand", "_ = Iter.map([1], fn x -> skip(x) + 1) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"a lambda passed to another function", "_ = Iter.map([1], fn x -> apply(fn y -> skip(y), x)) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"a capture passed to another function", "_ = Iter.map([1], fn x -> apply(&skip(&1), x)) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"a capture with a value argument", "_ = Iter.map([1], &keep(1, &1)) |> Iter.to_list()", "keep", cont, reduceKeep, true},
		{"a defer", "_ = Iter.map([1], fn x -> {\n        defer io.print(skip(x))\n        x\n    }) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"a concurrent block", "_ = Iter.map([1], fn x -> {\n        concurrent {\n            _ = skip(x)\n        }\n        x\n    }) |> Iter.to_list()", "skip", both, mapSkip, true},
		{"outside every callback", "_ = skip(1)", "skip", both, mapSkip, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "import std/io\n\n" + keepHelpers + "\nfn main() {\n    " + c.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			want := iterSensitiveCallError(c.callee, c.words, c.shape)
			if c.inCallback {
				want += "\nhelp: " + iterSensitiveDecideHint(c.callee, c.words)
			}
			var got []string
			for _, e := range errs {
				if diagText(e) == want {
					return
				}
				got = append(got, diagText(e))
			}
			t.Errorf("the checker admits this or reports something else; want %q, got %q\n%s", want, got, src)
		})
	}
}

// The suggested callback call fits the helper: the iteration that holds the
// call when the helper fits its callback, by arity and, for a fold, a loop
// and a predicate, by its result; else `Iter.map` for one parameter and
// `Iter.reduce` with `from:` for two whose accumulator is its result; else
// none.
func TestIterSensitiveCall_SuggestsAShapeThatFits(t *testing.T) {
	for _, c := range []struct{ body, callee, words, shape string }{
		// A one-parameter helper in a fold's callback cannot be the fold's.
		{"_ = Iter.reduce([1], fn t = 0, x -> t + skip(x))", "skip", "`break` and `continue`", "Iter.map(xs, skip)"},
		// An Int-answering helper in a filter cannot be its predicate.
		{"_ = Iter.filter([1], fn x -> skip(x) > 0) |> Iter.to_list()", "skip", "`break` and `continue`", "Iter.map(xs, skip)"},
		{"_ = Iter.filter([1], fn x -> big(x)) |> Iter.to_list()", "big", "`continue`", "Iter.filter(xs, big)"},
		// A two-parameter helper in a map's callback is a fold's.
		{"_ = Iter.map([1], fn x -> keep(0, x)) |> Iter.to_list()", "keep", "`continue`", "Iter.reduce(xs, keep, from: <start>)"},
		// A loop's step answers its state.
		{"_ = Iter.loop(fn n = 0 -> { if n > 3 { break n }\n        step(n) })", "step", "`break`", "Iter.loop(step, from: <start>)"},
		// Three parameters fit no callback.
		{"_ = Iter.map([1], fn x -> three(x, 1, 2)) |> Iter.to_list()", "three", "`continue`", ""},
	} {
		src := "import std/io\n\n" + keepHelpers + threeHelper + "\nfn main() {\n    " + c.body + "\n}\n"
		want := iterSensitiveCallError(c.callee, c.words, c.shape)
		got := iterSensitivityErrors(src)
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("want %q, got %q\n%s", want, got, src)
		}
	}
}

// threeHelper takes three parameters, which no iteration's callback does.
const threeHelper = `
fn three(a: Int, b: Int, c: Int) -> Int {
    if a < 0 { continue }
    a + b + c
}
`

// A two-level chain: outer calls inner, which uses `break`. The call in
// outer is the error, and outer, which uses neither keyword, is an ordinary
// function.
func TestIterSensitiveCall_RejectsAChain(t *testing.T) {
	src := `fn inner(x: Int) -> Int {
    if x > 10 { break x } else { x + 1 }
}

fn outer(x: Int) -> Int {
    inner(x)
}

fn main() {
    _ = Iter.map([1, 2, 3], outer) |> Iter.to_list()
}
`
	got := iterSensitivityErrors(src)
	if len(got) != 1 || got[0] != iterSensitiveCallError("inner", "`break`", "Iter.map(xs, inner)") {
		t.Errorf("want the one error at outer's call of inner; got %v", got)
	}
}

// A `break` or `continue` written in a lambda that is not itself an
// iteration function's callback has nothing to catch it, even inside a
// callback or an iter-sensitive function.
func TestIterSensitiveCall_RejectsABreakInANestedLambda(t *testing.T) {
	for _, src := range []string{
		`fn apply(f: (Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    _ = Iter.map([1], fn x -> apply(fn y -> {
        if y == 2 { continue }
        y
    }, x)) |> Iter.to_list()
}
`,
		`fn apply(f: (Int) -> Int, x: Int) -> Int {
    f(x)
}

fn skip(x: Int) -> Int {
    apply(fn y -> {
        if y == 2 { continue }
        y
    }, x)
}

fn main() {
    _ = Iter.map([1], skip) |> Iter.to_list()
}
`,
	} {
		got := iterSensitivityErrors(src)
		found := false
		for _, g := range got {
			found = found || g == strayContinueError
		}
		if !found {
			t.Errorf("the checker admits this or reports something else; got %v\n%s", got, src)
		}
	}
}

// A `continue` in a fold's callback lambda written before `from:` is the
// fold's: the lambda is the callback wherever the named argument stands.
func TestIterSensitiveCall_AdmitsASignalInACallbackBeforeFrom(t *testing.T) {
	src := `fn main() {
    _ = Iter.reduce([1, 2], fn acc, x -> {
        if x > 1 { continue }
        acc + x
    }, from: 0)
    _ = Iter.reduce([1, 2], from: 0, fn acc, x -> {
        if x > 1 { break acc }
        acc + x
    })
    _ = Iter.loop(fn n -> {
        if n > 1 { break n }
        n + 1
    }, from: 0)
}
`
	if got := iterSensitivityErrors(src); len(got) != 0 {
		t.Errorf("the checker rejects a callback's own signal: %v", got)
	}
}

// A `break` or `continue` directly in a `concurrent` block cannot leave it,
// in a callback or in an iter-sensitive function's own body.
func TestIterSensitiveCall_RejectsASignalInAConcurrentBlock(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`fn main() {
    _ = Iter.map([1, 2], fn x -> {
        concurrent {
            if x > 1 { continue }
        }
        x
    }) |> Iter.to_list()
}
`, "'continue' cannot leave a `concurrent` block: the block runs as an activation of its own, so no iteration around it sees the signal"},
		{`fn stop(x: Int) -> Int {
    concurrent {
        if x > 1 { break }
    }
    x
}

fn main() {
    _ = Iter.map([1, 2], stop) |> Iter.to_list()
}
`, "'break' cannot leave a `concurrent` block: the block runs as an activation of its own, so no iteration around it sees the signal"},
	} {
		got := iterSensitivityErrors(c.src)
		found := false
		for _, g := range got {
			found = found || g == c.want
		}
		if !found {
			t.Errorf("the checker admits this or reports something else; want %q, got %v\n%s", c.want, got, c.src)
		}
	}
}

// A generic iter-sensitive function passed as a callback is a use the
// checker sees, through the reference each generic call records, and a call
// of one is rejected as any other's is.
func TestIterSensitiveCall_AdmitsAGenericFunction(t *testing.T) {
	src := `fn head_or_skip<T>(xs: List<T>) -> T {
    case List.head(xs) {
        Some(x) -> x
        None -> continue
    }
}

fn main() {
    _ = Iter.map([[1], []], head_or_skip) |> Iter.to_list()
    _ = Iter.map([["a"], []], head_or_skip) |> Iter.to_list()
}
`
	if got := iterSensitivityErrors(src); len(got) != 0 {
		t.Errorf("the checker rejects a generic iter-sensitive callback: %v", got)
	}
	bad := strings.Replace(src, "Iter.map([[\"a\"], []], head_or_skip)", "Iter.map([[\"a\"], []], fn xs -> head_or_skip(xs))", 1)
	want := iterSensitiveCallError("head_or_skip", "`continue`", "Iter.map(xs, head_or_skip)")
	got := iterSensitivityErrors(bad)
	if len(got) != 1 || got[0] != want {
		t.Errorf("want %q for a generic call in a lambda; got %v", want, got)
	}
}
