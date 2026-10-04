package vmhost_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

// `nomi check` reports a body the compiler accepts and cannot lower as an
// error at its source, worded for the author, instead of leaving it to
// `nomi run` to refuse as BLOCKED. The fixture must keep type-checking: if
// the front end started rejecting it, the error below would be a type error
// and this test would say nothing about lowering.
func TestCheck_ReportsWhatTheCompilerCannotLower(t *testing.T) {
	path := writeProgram(t, `import std/io

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn is_big(n: Int): Bool {
  if n > 100 {
    break True
  }
  n > 2
}

fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}

fn first_big(xs: List<Int>): Maybe<Int> {
  Iter.find(xs, is_big)
}

fn unused(xs: List<Int>): Maybe<Int> {
  Iter.find(xs, is_big)
}

fn main() {
  io.print(evens([1, 2]))
  io.print(first_big([1, 5]))
}
`)
	err := vmhost.Check(path)
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("Check answered %v; want diagnostics", err)
	}
	type want struct {
		line, col int
		message   string
		hint      string
	}
	wants := []want{
		{18, 20, "this call to `skip_odd` is not supported yet, so `fn evens` cannot run",
			"`skip_odd` uses break or continue, so pass it as the callback itself"},
		{22, 17, "using `is_big` here is not supported yet, so `fn first_big` cannot run",
			"so far only Iter.map, Iter.filter, Iter.take_while, Iter.each, Iter.iterate and Iter.reduce"},
	}
	if len(ds) != len(wants) {
		t.Fatalf("got %d diagnostics, want %d (an unreached body is not reported):\n%v", len(ds), len(wants), err)
	}
	for i, w := range wants {
		d := ds[i]
		if d.Path != path || d.Line != w.line || d.Col != w.col || d.Message != w.message {
			t.Errorf("diagnostic %d = %s:%d:%d %q; want line %d col %d %q", i, d.Path, d.Line, d.Col, d.Message, w.line, w.col, w.message)
		}
		if len(d.Hints) != 1 || !strings.Contains(d.Hints[0], w.hint) {
			t.Errorf("diagnostic %d hints %q; want one containing %q", i, d.Hints, w.hint)
		}
	}
	for _, internal := range []string{"*ast.", "builder", "not retained", "iter-sensitive", "ident bound"} {
		if strings.Contains(err.Error(), internal) {
			t.Errorf("the report names compiler internals (%q):\n%v", internal, err)
		}
	}
}

// A body the run never reaches is not reported, and a program whose every
// reached body lowers checks clean.
func TestCheck_ALoweredProgramChecksClean(t *testing.T) {
	path := writeProgram(t, `import std/io

fn add(a: Int, b: Int): Int {
  a + b
}

fn main() {
  io.print(Iter.reduce([1, 2, 3], add))
}
`)
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("Check rejected a program that runs: %v", err)
	}
}

// When `main` itself cannot be lowered, the report is at the expression it
// stopped at.
func TestCheck_ReportsADeclinedMainAtTheExpression(t *testing.T) {
	path := writeProgram(t, `import std/io

fn parse(s: String, strict: Bool = False): Int {
  if strict { 0 } else { String.length(s) }
}

fn main() {
  g = parse
  io.print(g("ab"))
}
`)
	err := vmhost.Check(path)
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("Check answered %v; want one diagnostic", err)
	}
	if d := ds[0]; d.Line != 8 || d.Col != 7 || d.Message != "using `parse` here is not supported yet, so `fn main` cannot run" {
		t.Errorf("got %d:%d %q", d.Line, d.Col, d.Message)
	}
}
