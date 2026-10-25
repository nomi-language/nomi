package frontend_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

func checkSource(t *testing.T, src string) error {
	t.Helper()
	_, err := frontend.New(frontend.Config{}).CheckSource("main", src, frontend.Mode{})
	return err
}

// An iter-sensitive function whose only callback use is in a test block is
// accepted: test bodies and assertion operands are call sites.
func TestIterCallback_ATestBodyIsACallSite(t *testing.T) {
	const src = `fn skip_odd(n: Int) -> Int {
  if n % 2 == 1 {
    continue
  }
  n
}

test "evens" {
  assert Iter.map([1, 2, 3, 4], skip_odd) |> Iter.to_list() == [2, 4]
}
`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("the front end rejects a callback use inside a test: %v", err)
	}
}

// A `break` written straight in a test body has no iteration to stop.
func TestIterCallback_AStrayBreakInATestBodyIsReported(t *testing.T) {
	const src = `test "stray" {
  if True {
    break
  }
  assert True
}
`
	err := checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), "break") {
		t.Fatalf("a stray break in a test body was admitted: %v", err)
	}
}

// `Iter.reduce` starts from `from:` or from its callback lambda's
// first-parameter default, exactly one. A callback without a default and no
// `from:` is rejected at the callback with both spellings as the help: a
// function value, a lambda with no default, a partial, a pipe stage, a named
// function, a slot left open, and `Iter.reduce` named as a value. `from:`
// with any callback, and a seeded lambda, are accepted.
func TestIterCallback_ReduceNeedsAStartingValue(t *testing.T) {
	const prelude = `fn add(a: Int, b: Int) -> Int {
  a + b
}

fn apply(g: (List<Int>, (Int, Int) -> Int) -> Int) -> Int {
  g([1], fn a = 0, b -> a + b)
}

fn main() {
  xs = [1, 2]
  `
	const msg = "`Iter.reduce` needs a starting value"
	for _, c := range []struct {
		src, at, help string
	}{
		{`_ = Iter.reduce(xs, Int.add)`, "11:23", "give it with `from:`: `Iter.reduce(xs, Int.add, from: <start>)`"},
		{`_ = Iter.reduce(xs, fn a, b -> a + b)`, "11:23", "give it with `from:`, `Iter.reduce(xs, fn a, b -> ..., from: <start>)`, or as the accumulator's default, `fn a = <start>, b -> ...`"},
		{`_ = Iter.reduce(xs, &add(&1, &2))`, "11:23", "give it with `from:`: `Iter.reduce(xs, f, from: <start>)`, or pass a lambda whose accumulator has a default: `fn acc = <start>, x -> ...`"},
		{`_ = xs |> Iter.reduce(Int.add)`, "11:25", "give it with `from:`: `Iter.reduce(xs, Int.add, from: <start>)`"},
		{`_ = Iter.reduce(xs, add)`, "11:23", "give it with `from:`: `Iter.reduce(xs, add, from: <start>)`"},
		{`g = &Iter.reduce(&1, add)
  _ = g(xs)`, "11:24", "give it with `from:`: `Iter.reduce(xs, add, from: <start>)`"},
		{`g = &Iter.reduce(xs, &1)
  _ = g(add)`, "11:24", "pass the callback here: `Iter.reduce(xs, f, from: <start>)`"},
		{"_ = apply(Iter.reduce)\n  _ = xs", "11:13", "call it with its starting value instead: `Iter.reduce(xs, f, from: <start>)`"},
	} {
		err := checkSource(t, prelude+c.src+"\n}\n")
		if err == nil {
			t.Errorf("%s: admitted", c.src)
			continue
		}
		got := err.Error()
		if !strings.Contains(got, c.at+": "+msg) || !strings.Contains(got, c.help) || strings.Count(got, msg) != 1 {
			t.Errorf("%s: want one %q at %s with help %q, got\n%s", c.src, msg, c.at, c.help, got)
		}
	}
	for _, good := range []string{
		`_ = Iter.reduce(xs, fn acc = 0, x -> acc + x)`,
		`_ = xs |> Iter.reduce(fn acc = 0, x -> add(acc, x))`,
		`_ = Iter.reduce(xs, fn acc = "", n -> acc + Int.to_string(n))`,
		`_ = Iter.reduce(xs, add, from: 0)`,
		`_ = Iter.reduce(xs, Int.add, from: 0)`,
		`_ = xs |> Iter.reduce(add, from: 0)`,
		`_ = Iter.reduce(xs, &add(&1, &2), from: 0)`,
		`_ = Iter.reduce(xs, fn acc, x -> acc + x, from: 0)`,
		`_ = Iter.reduce(xs, fn acc, n -> acc + Int.to_string(n), from: "")`,
		`_ = Iter.reduce(xs, f: add, from: 0)`,
		`_ = Iter.reduce(xs, from: 0, add)`,
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: rejected: %v", good, err)
		}
	}
}

// Every seeded iteration function takes its starting value exactly once:
// `from:` beside a seeded lambda is two, reported at `from:` with the help
// to keep one, and a starting value written positionally is asked for by
// name.
func TestIterCallback_ASeededCallTakesOneStartingValue(t *testing.T) {
	const prelude = `fn add(a: Int, b: Int) -> Int {
  a + b
}

fn main() {
  xs = [1, 2]
  `
	for _, c := range []struct {
		src, at, msg, help string
	}{
		{`_ = Iter.reduce(xs, fn acc = 0, x -> acc + x, from: 1)`, "7:55", "`Iter.reduce` has two starting values: `from:` and the default of the callback's first parameter", "keep one: remove `from:`, or remove the default from `acc`"},
		{`_ = Iter.scan(xs, fn acc = 0, x -> acc + x, from: 1) |> Iter.to_list()`, "7:53", "`Iter.scan` has two starting values: `from:` and the default of the callback's first parameter", "keep one: remove `from:`, or remove the default from `acc`"},
		{`_ = Iter.map_reduce(xs, fn #(a, b) = #(0, 0), x -> #(x, #(a, b)), from: #(1, 1))`, "7:75", "`Iter.map_reduce` has two starting values: `from:` and the default of the callback's first parameter", "keep one: remove `from:`, or remove the default from `the first parameter`"},
		{"_ = xs\n  _ = Iter.loop(fn n = 0 -> { break n }, from: 1)", "8:48", "`Iter.loop` has two starting states: `from:` and the default of the callback's parameter", "keep one: remove `from:`, or remove the default from `n`"},
		{"_ = xs\n  _ = Iter.loop(fn -> { break }, from: 1)", "8:40", "a stateless `Iter.loop` takes no starting state", "remove `from:`, or give the callback a parameter for the state: `fn state -> ...`"},
		{`_ = Iter.reduce(xs, 0, add)`, "7:23", "`Iter.reduce` takes its starting value by name", "write it as `from:` after the callback: `Iter.reduce(xs, f, from: <start>)`"},
		{`_ = Iter.scan(xs, fn acc, x -> acc + x) |> Iter.to_list()`, "7:21", "`Iter.scan` needs a starting value", "give it with `from:`, `Iter.scan(xs, fn acc, x -> ..., from: <start>)`, or as the accumulator's default, `fn acc = <start>, x -> ...`"},
		{`_ = Iter.map_reduce(xs, add)`, "7:27", "`Iter.map_reduce` needs a starting value", "give it with `from:`: `Iter.map_reduce(xs, add, from: <start>)`"},
	} {
		err := checkSource(t, prelude+c.src+"\n}\n")
		if err == nil {
			t.Errorf("%s: admitted", c.src)
			continue
		}
		got := err.Error()
		if !strings.Contains(got, c.at+": "+c.msg) || !strings.Contains(got, c.help) || strings.Count(got, c.msg) != 1 {
			t.Errorf("%s: want one %q at %s with help %q, got\n%s", c.src, c.msg, c.at, c.help, got)
		}
	}
	for _, good := range []string{
		`_ = Iter.scan(xs, add, from: 0) |> Iter.to_list()`,
		`_ = Iter.map_reduce(xs, fn n, x -> #(x * n, n + 1), from: 0)`,
		`_ = Iter.map_reduce(xs, fn #(a, b), x -> #(x, #(a + x, b)), from: #(0, 0))`,
		"_ = xs\n  _ = Iter.loop(fn n -> { break n }, from: 1)",
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: rejected: %v", good, err)
		}
	}
}

// `Iter.scan` and `Iter.map_reduce` take their seed as `Iter.reduce` does,
// and report the same error under their own names: a function value, a
// lambda with no default, and the function named as a value. A seeded
// lambda is accepted, and the min/max help stays `reduce`'s.
func TestIterCallback_ScanAndMapReduceNeedAStartingValue(t *testing.T) {
	const prelude = `fn max(a: Int, b: Int) -> Int {
  if a > b { a } else { b }
}

fn apply(g: (List<Int>, (Int, Int) -> Int) -> Iter<Int>) -> Int {
  Iter.count(g([1], fn a = 0, b -> a + b))
}

fn main() {
  xs = [1, 2]
  `
	for _, c := range []struct {
		src, msg, at, help string
	}{
		{`_ = Iter.scan(xs, max) |> Iter.to_list()`, "`Iter.scan` needs a starting value", "11:21",
			"give it with `from:`: `Iter.scan(xs, max, from: <start>)`"},
		{`_ = xs |> Iter.scan(fn a, b -> a + b) |> Iter.to_list()`, "`Iter.scan` needs a starting value", "11:23",
			"give it with `from:`, `Iter.scan(xs, fn a, b -> ..., from: <start>)`, or as the accumulator's default, `fn a = <start>, b -> ...`"},
		{"_ = apply(Iter.scan)\n  _ = xs", "`Iter.scan` needs a starting value", "11:13",
			"call it with its starting value instead: `Iter.scan(xs, f, from: <start>)`"},
		{`_ = Iter.map_reduce(xs, fn n, x -> #(x, n + 1))`, "`Iter.map_reduce` needs a starting value", "11:27",
			"give it with `from:`, `Iter.map_reduce(xs, fn n, x -> ..., from: <start>)`, or as the accumulator's default, `fn n = <start>, x -> ...`"},
	} {
		err := checkSource(t, prelude+c.src+"\n}\n")
		if err == nil {
			t.Errorf("%s: admitted", c.src)
			continue
		}
		got := err.Error()
		if !strings.Contains(got, c.at+": "+c.msg) || !strings.Contains(got, c.help) || strings.Count(got, c.msg) != 1 {
			t.Errorf("%s: want one %q at %s with help %q, got\n%s", c.src, c.msg, c.at, c.help, got)
		}
		if strings.Contains(got, "Iter.max") {
			t.Errorf("%s: the Iter.max help is reduce's, got\n%s", c.src, got)
		}
	}
	for _, good := range []string{
		`_ = Iter.scan(xs, fn acc = 0, x -> max(acc, x)) |> Iter.to_list()`,
		`_ = xs |> Iter.map_reduce(fn n = 0, x -> #(x * n, n + 1))`,
		`_ = Iter.scan(xs, max, from: 0) |> Iter.to_list()`,
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: rejected: %v", good, err)
		}
	}
}

// `Iter.loop` starts from `from:` or from its callback lambda's parameter
// default, or with no state from a lambda with no parameter. A lambda whose
// parameter has no default and a function value, with no `from:`, are
// rejected at the callback with both spellings as the help; a seeded
// lambda, a stateless one, and a lambda or a named step with `from:` are
// accepted.
func TestIterCallback_LoopNeedsAStartingState(t *testing.T) {
	const prelude = `fn step(n: Int) -> Int {
  if n > 3 { break n }
  n + 1
}

fn main() {
  `
	const msg = "`Iter.loop` needs a starting state"
	for _, c := range []struct {
		src, at, help string
	}{
		{`_ = Iter.loop(step)`, "7:17", "give it with `from:`: `Iter.loop(step, from: <start>)`"},
		{"_ = Iter.loop(fn n: Int -> {\n    if n > 3 { break n }\n    n + 1\n  })", "7:17", "give it with `from:`, `Iter.loop(fn n -> ..., from: <start>)`, or as the parameter's default, `fn n = <start> -> ...`; or take no parameter for a loop with no state: `fn -> ...`"},
	} {
		err := checkSource(t, prelude+c.src+"\n}\n")
		if err == nil {
			t.Errorf("%s: admitted", c.src)
			continue
		}
		got := err.Error()
		if !strings.Contains(got, c.at+": "+msg) || !strings.Contains(got, c.help) || strings.Count(got, msg) != 1 {
			t.Errorf("%s: want one %q at %s with help %q, got\n%s", c.src, msg, c.at, c.help, got)
		}
	}
	for _, good := range []string{
		`_ = Iter.loop(step, from: 0)`,
		"_ = Iter.loop(fn n = 0 -> {\n    if n > 3 { break n }\n    n + 1\n  })\n  _ = Iter.loop(step, from: 1)",
		"Iter.loop(fn -> { break })\n  _ = Iter.loop(f: step, from: 2)",
		"_ = Iter.loop(fn n: Int -> {\n    if n > 3 { break n }\n    n + 1\n  }, from: 0)\n  _ = Iter.loop(step, from: 0)",
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: rejected: %v", good, err)
		}
	}
}

// A function named `min` or `max` passed to `Iter.reduce` also points at
// `Iter.min` / `Iter.max`.
func TestIterCallback_ReduceWithMaxPointsAtIterMax(t *testing.T) {
	const src = `fn max(a: Int, b: Int) -> Int {
  if a > b { a } else { b }
}

fn main() {
  _ = Iter.reduce([1, 2], max)
}
`
	err := checkSource(t, src)
	const want = "for the greatest element, `Iter.max(xs)` answers a `Maybe`, with `None` for an empty source"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want the Iter.max help, got %v", err)
	}
}
