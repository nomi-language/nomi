package analysis_test

import "testing"

// A statement that always exits without being a keyword: an `if` with an
// `else` whose every branch exits, a `case` whose every arm exits, and a block
// that exits. The first statement after it is reported, with a hint.
func TestUnreachable_StatementAfterAStatementThatAlwaysExits(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn choose(flag: Bool) {
  if flag {
    _ = 1
    return
  } else {
    return
  }
  _ = 2
}

fn chain(n: Int): Int {
  if n > 0 {
    return 1
  } else if n < 0 {
    return -1
  } else {
    return 0
  }
  n
}

fn arms(m: Maybe<Int>): Int {
  case m {
    Some(n) -> return n
    None -> {
      return 0
    }
  }
  1
}

fn scoped(): Int {
  {
    return 1
  }
  2
}

fn looped(): Int {
  Iter.loop(|n = 0| {
    if n > 3 {
      break n
    } else {
      continue
    }
    _ = n
    n + 1
  })
}
`)
	got := unreachableErrors(errs)
	want := []struct {
		line, col int
		msg       string
	}{
		{9, 3, "unreachable code: the `if` above returns in every branch"},
		{20, 3, "unreachable code: the `if` above returns in every branch"},
		{30, 3, "unreachable code: the `case` above returns in every arm"},
		{37, 3, "unreachable code: the block above always returns"},
		{47, 5, "unreachable code: the `if` above exits in every branch"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unreachable-code errors, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		found := false
		for _, e := range got {
			if e.Line == w.line && e.Col == w.col && e.Message == w.msg &&
				len(e.Hints) == 1 && e.Hints[0] == "remove it" {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q at %d:%d with hint `remove it` in %v", w.msg, w.line, w.col, got)
		}
	}
}

// Only a statement every path of which exits makes code after it unreachable:
// an `if` without an `else`, an `if` or `case` with one branch that falls
// through, and a `todo`, which is a Unit statement so that code after a stub
// stays legal while it is written.
func TestUnreachable_APathThatFallsThroughIsClean(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn f(n: Int): Int {
  if n > 0 {
    return 1
  }
  if n < 0 {
    return -1
  } else {
    _ = n
  }
  case n {
    0 -> return 0
    _ -> {}
  }
  todo "finish"
  n
}
`)
	if got := unreachableErrors(errs); len(got) != 0 {
		t.Fatalf("unexpected unreachable-code errors: %v", got)
	}
}
