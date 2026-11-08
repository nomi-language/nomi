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

// A fold's starting value is its `from` parameter, an ordinary argument:
// positional before the callback, or named. A call without it is the
// ordinary arity error, and a default on the callback lambda's accumulator
// is a lambda default like any other, which the fold never reads.
func TestIterCallback_AFoldTakesItsStartingValueAsAnArgument(t *testing.T) {
	const prelude = `fn add(a: Int, b: Int) -> Int {
  a + b
}

fn step(n: Int) -> Int {
  if n > 3 { break n }
  n + 1
}

fn main() {
  xs = [1, 2]
  _ = xs
  _ = Iter.loop(0, step)
  `
	for _, c := range []struct{ src, msg string }{
		{`_ = Iter.reduce(xs, add)`, "expected 3 arguments, got 2"},
		{`_ = Iter.reduce(xs, fn a, b -> a + b)`, "expected 3 arguments, got 2"},
		{`_ = Iter.reduce(xs, &(&1 + &2))`, "expected 3 arguments, got 2"},
		{`_ = xs |> Iter.reduce(add)`, "missing argument for parameter 'from'"},
		{`_ = Iter.scan(xs, add) |> Iter.to_list()`, "expected 3 arguments, got 2"},
		{`_ = Iter.map_reduce(xs, fn n, x -> (n + 1, x))`, "expected 3 arguments, got 2"},
		{`_ = Iter.loop(step)`, "expected 2 arguments, got 1"},
		{`_ = Iter.loop(fn n: Int -> n + 1)`, "expected 2 arguments, got 1"},
		{`_ = Iter.loop(1, fn -> { break })`, "a stateless `Iter.loop` takes no starting state"},
	} {
		err := checkSource(t, prelude+c.src+"\n}\n")
		if err == nil {
			t.Errorf("%s: admitted", c.src)
			continue
		}
		if got := err.Error(); !strings.Contains(got, c.msg) {
			t.Errorf("%s: want %q, got\n%s", c.src, c.msg, got)
		}
	}
	for _, good := range []string{
		`_ = Iter.reduce(xs, 0, fn acc, x -> acc + x)`,
		`_ = xs |> Iter.reduce(0, fn acc, x -> add(acc, x))`,
		`_ = Iter.reduce(xs, 0, add)`,
		`_ = Iter.reduce(xs, 0, &add(&1, &2))`,
		`_ = Iter.reduce(xs, add, from: 0)`,
		`_ = Iter.reduce(xs, f: add, from: 0)`,
		`_ = Iter.reduce(xs, 0, fn acc = 5, x -> acc + x)`,
		`_ = Iter.scan(xs, 0, add) |> Iter.to_list()`,
		`_ = Iter.map_reduce(xs, (0, 0), fn (a, b), x -> ((a + x, b), x))`,
		`_ = Iter.loop(0, step)`,
		`_ = Iter.loop(step, from: 0)`,
		"_ = Iter.loop(0, fn n -> {\n    if n > 3 { break n }\n    n + 1\n  })",
		"Iter.loop(fn -> { break })",
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: the front end rejects it: %v", good, err)
		}
	}
}
