package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

func unreachableErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.UnreachableCodeCode {
			out = append(out, e)
		}
	}
	return out
}

// Each keyword that ends a block reports the first statement after it, at
// that statement's first token, once per block.
func TestUnreachable_StatementAfterReturnBreakContinue(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn early(n: Int): Int {
  return n
  _ = n + 1
  n
}

fn looped(): Int {
  Iter.loop(|n = 0| {
    if n > 3 {
      break n
      _ = n
    }
    if n == 1 {
      continue
      _ = n
    }
    n + 1
  })
}
`)
	got := unreachableErrors(errs)
	want := []struct {
		line, col int
		msg       string
	}{
		{4, 3, "unreachable code after return"},
		{12, 7, "unreachable code after break"},
		{16, 7, "unreachable code after continue"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unreachable-code errors, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		found := false
		for _, e := range got {
			if e.Line == w.line && e.Col == w.col && e.Message == w.msg {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q at %d:%d in %v", w.msg, w.line, w.col, got)
		}
	}
}

// A return inside a branch leaves the code after the branch reachable, and a
// return as the last statement leaves nothing behind it.
func TestUnreachable_ConditionalAndTrailingReturnAreClean(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn f(n: Int): Int {
  if n > 0 {
    return n
  }
  m = n + 1
  return m
}
`)
	if got := unreachableErrors(errs); len(got) != 0 {
		t.Fatalf("unexpected unreachable-code errors: %v", got)
	}
}
