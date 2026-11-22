package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// An `Iter.loop` whose callback holds no `break` never finishes, so a
// statement after it is unreachable: after the loop itself, after an `if` or
// `case` whose every branch runs one or leaves, and after a block holding
// one. The hint names the `break` that would end it.
func TestUnreachable_StatementAfterALoopThatNeverBreaks(t *testing.T) {
	errs := unreachableCheck(t, `
fn worker()
  Iter.loop(fn ->
    _ = 1
  end)
  _ = 2
end

fn returns_next_state()
  Iter.loop(0, fn n ->
    if n > 3 then return 0
    if n == 2 then continue
    n + 1
  end)
  _ = 3
end

fn either(flag: Bool)
  if flag then
    Iter.loop(fn ->
        _ = 1
    end)
  else
    Iter.loop(fn ->
        _ = 2
    end)
  end
  _ = 4
end

fn mixed(flag: Bool)
  case flag
    True -> Iter.loop(fn ->
        _ = 1
    end)
    False -> return
  end
  _ = 5
end

fn scoped()
  begin
    Iter.loop(fn ->
        _ = 1
    end)
  end
  _ = 6
end
`)
	got := unreachableErrors(errs)
	const loopHint = "remove it, or give the loop a `break` that ends it"
	want := []struct {
		line, col int
		msg, hint string
	}{
		{6, 3, "unreachable code after a loop that never breaks", loopHint},
		{15, 3, "unreachable code after a loop that never breaks", loopHint},
		{28, 3, "unreachable code: the `if` above loops forever in every branch", loopHint},
		{38, 3, "unreachable code: the `case` above exits in every arm", "remove it"},
		{47, 3, "unreachable code: the block above always loops forever", loopHint},
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
// so the outer loop still never breaks. A loop that never breaks is legal
// as a body's last statement, bound or not, since nothing follows it.
func TestUnreachable_ALoopThatCanStopIsClean(t *testing.T) {
	errs := unreachableCheck(t, `
fn guarded()
  Iter.loop(0, fn n ->
    if n > 3 then break
    n + 1
  end)
  _ = 1
end

fn in_an_arm() -> Int
  total = Iter.loop(0, fn n ->
    case n
      5 ->
        _ = n
        break n
      _ -> n + 1
    end
  end)
  total
end

fn server()
  Iter.loop(fn ->
      _ = 1
  end)
end

fn bound_last()
  _x = Iter.loop(fn ->
      _ = 1
  end)
end
`)
	if got := unreachableErrors(errs); len(got) != 0 {
		t.Fatalf("unexpected unreachable-code errors: %v", got)
	}
	for _, c := range []struct {
		src   string
		never bool
	}{
		{"Iter.loop(fn ->\n    _ = 1\nend)", true},
		{"Iter.loop(0, fn n ->\n    (if n > 1 then break)\n    n + 1\nend)", false},
		{"Iter.loop(fn ->\n    Iter.each([1], fn x ->\n        if x > 0 then break\n    end)\nend)", true},
		{"Iter.loop(fn ->\n    fn inner(x: Int) -> Int\n        (if x > 0 then break)\n        x\n    end\nend)", true},
		{"Iter.loop(fn ->\n    Iter.loop(fn ->\n        break\n    end)\nend)", true},
		{"Iter.loop(fn ->\n case 1\n  1 -> break\n  _ -> Unit\n end\nend)", false},
		{"Iter.map([1], fn x -> x)", false},
	} {
		fa, nodes, _ := checkSourceAsWrittenWithStdlib("fn f()\n" + c.src + "\nend\n")
		var fd *ast.FuncDef
		for _, n := range nodes {
			if d, ok := n.(*ast.FuncDef); ok && d.Name == "f" {
				fd = d
			}
		}
		if fd == nil || fd.Body == nil || len(fd.Body.Stmts) == 0 {
			t.Fatalf("%s: no body for f", c.src)
		}
		body := fd.Body.Stmts
		expr := body[len(body)-1]
		if es, ok := expr.(*ast.ExprStmt); ok {
			expr = es.Expr
		}
		call, _ := expr.(*ast.Call)
		if call == nil || fa.Calls[call] == nil {
			t.Fatalf("%s: the check recorded no call", c.src)
		}
		if got := fa.NeverReturns(call); got != c.never {
			t.Errorf("NeverReturns(%s) = %v, want %v", c.src, got, c.never)
		}
	}
}
