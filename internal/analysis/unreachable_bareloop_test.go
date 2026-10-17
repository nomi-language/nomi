package analysis_test

import "testing"

// A bare callee is std's `Iter.loop` when the reference recorded at its name
// resolves to it, whatever the local spelling: `loop` from a selective import
// and `forever` from an aliased one both never break.
func TestUnreachable_BareLoopResolvedBySymbol(t *testing.T) {
	errs := buildErrsWithStdlib(t, `import std/iter.Iter.{loop, loop as forever}

fn bare() {
  loop(fn -> {
    _ = 1
  })
  _ = 2
}

fn aliased() {
  _ = 0
  {
    forever(fn -> { _ = 1 })
  }
  _ = 3
}

fn guarded() {
  loop(fn n = 0 -> {
    if n > 3 { break }
    n + 1
  })
  _ = 4
}
`)
	got := unreachableErrors(errs)
	const loopHint = "remove it, or give the loop a `break` that ends it"
	want := []struct {
		line, col int
		msg       string
	}{
		{7, 3, "unreachable code after a loop that never breaks"},
		{15, 3, "unreachable code: the block above always loops forever"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unreachable-code errors, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		found := false
		for _, e := range got {
			if e.Line == w.line && e.Col == w.col && e.Message == w.msg && len(e.Hints) == 1 && e.Hints[0] == loopHint {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q at %d:%d in %v", w.msg, w.line, w.col, got)
		}
	}
}

// A local function named `loop` is not std's `Iter.loop`: a call to it is an
// ordinary call, and the statement after it runs.
func TestUnreachable_LocalFunctionNamedLoopIsACall(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn loop(f: () -> Unit) {
  f()
}

fn caller() {
  loop(fn -> {
    _ = 1
  })
  _ = 2
}
`)
	if got := unreachableErrors(errs); len(got) != 0 {
		t.Fatalf("unexpected unreachable-code errors: %v", got)
	}
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a local function named loop: %v", errs)
	}
}
