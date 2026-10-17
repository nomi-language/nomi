package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// An `Iter.loop` whose callback holds no `break` never finishes, so a
// statement after it is unreachable: after the loop itself, after an `if` or
// `case` whose every branch runs one or leaves, and after a block holding
// one. The hint names the `break` that would end it.
func TestUnreachable_StatementAfterALoopThatNeverBreaks(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn worker() {
  Iter.loop(fn -> {
    _ = 1
  })
  _ = 2
}

fn returns_next_state() {
  Iter.loop(fn n = 0 -> {
    if n > 3 { return 0 }
    if n == 2 { continue }
    n + 1
  })
  _ = 3
}

fn either(flag: Bool) {
  if flag {
    Iter.loop(fn -> { _ = 1 })
  } else {
    Iter.loop(fn -> { _ = 2 })
  }
  _ = 4
}

fn mixed(flag: Bool) {
  case flag {
    True -> Iter.loop(fn -> { _ = 1 })
    False -> return
  }
  _ = 5
}

fn scoped() {
  {
    Iter.loop(fn -> { _ = 1 })
  }
  _ = 6
}
`)
	got := unreachableErrors(errs)
	const loopHint = "remove it, or give the loop a `break` that ends it"
	want := []struct {
		line, col int
		msg, hint string
	}{
		{6, 3, "unreachable code after a loop that never breaks", loopHint},
		{15, 3, "unreachable code after a loop that never breaks", loopHint},
		{24, 3, "unreachable code: the `if` above loops forever in every branch", loopHint},
		{32, 3, "unreachable code: the `case` above exits in every arm", "remove it"},
		{39, 3, "unreachable code: the block above always loops forever", loopHint},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unreachable-code errors, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		found := false
		for _, e := range got {
			if e.Line == w.line && e.Col == w.col && e.Message == w.msg &&
				len(e.Hints) == 1 && e.Hints[0] == w.hint {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q at %d:%d with hint %q in %v", w.msg, w.line, w.col, w.hint, got)
		}
	}
}

// A loop that can stop is not one that never breaks: a `break` anywhere in
// its own callback, however nested in its blocks and arms, ends it. A `break`
// in a lambda or a nested `fn` inside the callback belongs to that callback,
// so the outer loop still never breaks. A value position is not a statement:
// code after a binding of such a loop stays legal, as after a binding of a
// `case` whose arms all leave.
func TestUnreachable_ALoopThatCanStopIsClean(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn guarded() {
  Iter.loop(fn n = 0 -> {
    if n > 3 { break }
    n + 1
  })
  _ = 1
}

fn in_an_arm() -> Int {
  total = Iter.loop(fn n = 0 -> {
    case n {
      5 -> {
        _ = n
        break n
      }
      _ -> n + 1
    }
  })
  total
}

fn bound() {
  x = Iter.loop(fn -> { _ = 1 })
  _ = x
}
`)
	if got := unreachableErrors(errs); len(got) != 0 {
		t.Fatalf("unexpected unreachable-code errors: %v", got)
	}
	for _, c := range []struct {
		src   string
		never bool
	}{
		{"Iter.loop(fn -> { _ = 1 })", true},
		{"Iter.loop(fn n = 0 -> { if n > 1 { break } n + 1 })", false},
		{"Iter.loop(fn -> { Iter.each([1], fn x -> { if x > 0 { break } }) })", true},
		{"Iter.loop(fn -> { fn inner(x: Int) -> Int { if x > 0 { break } x } })", true},
		{"Iter.loop(fn -> { Iter.loop(fn -> { break }) })", true},
		{"Iter.loop(fn -> {\n case 1 {\n  1 -> break\n  _ -> Unit\n }\n})", false},
		{"Iter.map([1], fn x -> x)", false},
	} {
		nodes, err := parser.Parse(lexer.Lex("fn f() {\n" + c.src + "\n}\n"))
		if err != nil || len(nodes) != 1 {
			t.Fatalf("%s: %v", c.src, err)
		}
		body := nodes[0].(*ast.FuncDef).Body.Stmts
		expr := body[len(body)-1]
		if es, ok := expr.(*ast.ExprStmt); ok {
			expr = es.Expr
		}
		// No file analysis: only the qualified spelling is recognized.
		if got := (*analysis.FileAnalysis)(nil).LoopNeverBreaks(expr); got != c.never {
			t.Errorf("LoopNeverBreaks(%s) = %v, want %v", c.src, got, c.never)
		}
	}
}
