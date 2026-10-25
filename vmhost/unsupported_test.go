package vmhost_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/vmhost"
)

// `nomi check` reports a body the compiler accepts and cannot lower as an
// error at its source, worded for the author, instead of leaving it to
// `nomi run` to refuse as BLOCKED, with advice for the declines whose cause
// the author can act on. Each case blocks on a planted decline carrying the
// builder reason the advice is keyed by. The fixture must keep
// type-checking: if the front end started rejecting it, the error below
// would be a type error and this test would say nothing about lowering.
func TestCheck_ReportsWhatTheCompilerCannotLower(t *testing.T) {
	path := writeProgram(t, `import std/io

fn planted_decline(n: Int) -> Int {
  n
}

fn evens(xs: List<Int>) -> Int {
  planted_decline(Iter.count(xs))
}

fn unused() -> Int {
  planted_decline(0)
}

fn main() {
  io.print(evens([1, 2]))
}
`)
	cases := []struct {
		reason string
		hint   string
	}{
		{"an iter-sensitive function passed where its break or continue cannot be caught: is_big",
			"runs as a callback only when the iteration function is named with its owner, as in `Iter.find(xs, is_big)`"},
	}
	for _, c := range cases {
		restore := irbuild.PlantDeclineForTest("planted_decline", c.reason)
		err := vmhost.Check(path)
		restore()
		var ds frontend.Diagnostics
		if !errors.As(err, &ds) {
			t.Fatalf("Check answered %v; want diagnostics", err)
		}
		if len(ds) != 1 {
			t.Fatalf("got %d diagnostics, want 1 (an unreached body is not reported):\n%v", len(ds), err)
		}
		d := ds[0]
		const message = "this call to `planted_decline` is not supported yet, so `fn evens` cannot run"
		if d.Path != path || d.Line != 8 || d.Col != 3 || d.Message != message {
			t.Errorf("diagnostic = %s:%d:%d %q; want line 8 col 3 %q", d.Path, d.Line, d.Col, d.Message, message)
		}
		if len(d.Hints) != 2 || !strings.Contains(d.Hints[0], c.hint) || d.Hints[1] != gapHint {
			t.Errorf("hints %q; want one containing %q, then the gap hint", d.Hints, c.hint)
		}
		for _, internal := range []string{"*ast.", "builder", "not retained", "iter-sensitive", "ident bound"} {
			if strings.Contains(err.Error(), internal) {
				t.Errorf("the report names compiler internals (%q):\n%v", internal, err)
			}
		}
	}
}

// A body the run never reaches is not reported, and a program whose every
// reached body lowers checks clean.
func TestCheck_ALoweredProgramChecksClean(t *testing.T) {
	path := writeProgram(t, `import std/io

fn add(a: Int, b: Int) -> Int {
  a + b
}

fn main() {
  io.print(Iter.reduce([1, 2, 3], fn acc = 0, n -> add(acc, n)))
}
`)
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("Check rejected a program that runs: %v", err)
	}
}

// When `main` itself cannot be lowered, the report is at the expression it
// stopped at: under plantDecline, the call to `planted_decline`.
func TestCheck_ReportsADeclinedMainAtTheExpression(t *testing.T) {
	plantDecline(t)
	path := writeProgram(t, `import std/io

fn planted_decline(a: Int) -> Int {
    a
}

fn main() {
    u = planted_decline(1)
    io.inspect(u)
}
`)
	err := vmhost.Check(path)
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("Check answered %v; want one diagnostic", err)
	}
	if d := ds[0]; d.Line != 8 || d.Col != 9 || d.Message != "this call to `planted_decline` is not supported yet, so `fn main` cannot run" {
		t.Errorf("got %d:%d %q", d.Line, d.Col, d.Message)
	}
}

// gapHint is the last hint of every unsupported diagnostic.
const gapHint = "this is a gap in Nomi, not a mistake in your code; please report it at https://github.com/nomi-language/nomi/issues"
