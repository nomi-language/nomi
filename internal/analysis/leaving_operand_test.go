package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const leavingOperandDecls = `fn close(n: Int)
    _ = n
end

fn ident(n: Int) -> Int
    n
end

`

// A body's last statement that always evaluates an operand every path of
// which returns leaves in every path, so the body needs no result after
// it: a call's argument, a binding's value, a tuple's item, a `defer`
// call's argument, a branch whose block ends in one, and a callback whose
// last statement breaks in an operand.
func TestLeavingOperand_ABodyEndingInOneReturnsInEveryPath(t *testing.T) {
	for _, tc := range []struct{ name, fn string }{
		{"a call's argument", "fn f(c: Bool) -> Int\n    close(if c then return 1 else return 2)\nend\n"},
		{"a binding's value", "fn f(c: Bool) -> Int\n    _x = ident(if c then return 1 else return 2)\nend\n"},
		{"a tuple's item", "fn f(c: Bool) -> Int\n    _t = (ident(7), if c then return 1 else return 2)\nend\n"},
		{"a defer's argument", "fn f(c: Bool) -> Int\n    defer close(if c then return 1 else return 2)\nend\n"},
		{"a defer's argument, a case", "fn f(c: Bool) -> Int\n    defer close(case c\n        True -> return 1\n        False -> return 2\n    end)\nend\n"},
		{"an operand of an operand", "fn f(c: Bool) -> Int\n    defer close(ident(8) + if c then return 5 else return 6)\nend\n"},
		{"a branch ending in one", "fn f(c: Bool) -> Int\n    if c then\n        close(if c then return 3 else return 30)\n    else\n        return 4\n    end\nend\n"},
		{"a callback's break", "fn f(xs: List<Int>) -> Int\n    Iter.reduce(xs, 0, fn acc, x ->\n        close(if x > 2 then break acc else break acc + x)\n    end)\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(leavingOperandDecls + tc.fn)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// An operand that leaves only in some paths, or one the statement does not
// always evaluate, leaves the body without a result.
func TestLeavingOperand_OneThatMayNotLeaveIsNoResult(t *testing.T) {
	for _, tc := range []struct{ name, fn, at string }{
		{"one arm goes on", "fn f(c: Bool) -> Int\n    defer close(if c then 1 else return 2)\nend\n",
			"defer close(if c then 1 else return 2)"},
		{"the right side of `and`", "fn f(c: Bool) -> Int\n    _b = c and (if c then return 1 else return 2)\nend\n",
			"_b = c and (if c then return 1 else return 2)"},
		{"a lambda's body", "fn f(c: Bool) -> Int\n    _g = fn -> if c then return 1 else return 2\nend\n",
			"_g = fn -> if c then return 1 else return 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := leavingOperandDecls + tc.fn
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, "return type mismatch: expected Int, got Unit", tc.at)
		})
	}
}

// A statement after one that always evaluates an operand that leaves can
// never run, and is reported as one after a `return` is: the rule asks the
// question a block's type asks (leaves). Each case is one error, at the
// dead statement, with its exact message and help.
func TestLeavingOperand_AStatementAfterOneIsUnreachable(t *testing.T) {
	const returns = "unreachable code: the statement above always returns"
	const loopsForever = "unreachable code: the statement above always loops forever"
	const loopHint = "remove it, or give the loop a `break` that ends it"
	for _, tc := range []struct{ name, fn, msg, hint, at string }{
		{"a defer's argument", "fn f(c: Bool) -> Int\n    defer close(if c then return 1 else return 2)\n    3\nend\n",
			returns, "remove it", "3\nend"},
		{"a call's argument", "fn f(c: Bool) -> Int\n    close(if c then return 1 else return 2)\n    3\nend\n",
			returns, "remove it", "3\nend"},
		{"a binding's value", "fn f(c: Bool) -> Int\n    y = case c\n        True -> return 1\n        False -> return 2\n    end\n    y\nend\n",
			returns, "remove it", "y\nend"},
		{"an operand of an operand", "fn f(c: Bool) -> Int\n    _x = ident(8) + ident(if c then return 5 else return 6)\n    close(1)\n    3\nend\n",
			returns, "remove it", "close(1)"},
		{"a callback's break or continue", "fn f(xs: List<Int>) -> Int\n    Iter.reduce(xs, 0, fn acc, x ->\n        close(if x > 2 then break acc else continue)\n        acc + x\n    end)\nend\n",
			"unreachable code: the statement above always exits", "remove it", "acc + x"},
		{"a branch ending in one", "fn f(c: Bool) -> Int\n    if c then\n        close(if c then return 3 else return 30)\n    else\n        return 4\n    end\n    5\nend\n",
			"unreachable code: the `if` above returns in every branch", "remove it", "5\nend"},
		{"a binding of a loop that never breaks", "fn f()\n    x = Iter.loop(0, fn n -> n + 1)\n    close(x)\nend\n",
			loopsForever, loopHint, "close(x)"},
		{"a call that takes a loop that never breaks", "fn f()\n    close(Iter.loop(0, fn n -> n + 1))\n    close(1)\nend\n",
			loopsForever, loopHint, "close(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := leavingOperandDecls + tc.fn
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, tc.msg)
			}
			i := strings.LastIndex(src, tc.at)
			line, col := strings.Count(src[:i], "\n")+1, i-strings.LastIndex(src[:i], "\n")
			e := errs[0]
			if e.Message != tc.msg || e.Line != line || e.Col != col {
				t.Fatalf("%q at %d:%d, want %q at %d:%d", e.Message, e.Line, e.Col, tc.msg, line, col)
			}
			if e.Code != analysis.UnreachableCodeCode || len(e.Hints) != 1 || e.Hints[0] != tc.hint {
				t.Fatalf("code %q, hints %q; want %q and %q", e.Code, e.Hints, analysis.UnreachableCodeCode, tc.hint)
			}
		})
	}
}

// An operand that leaves only in some paths, one the statement does not
// always evaluate and an `unreachable` that is an operand leave the next
// statement reachable. A statement that holds a loop that never breaks is
// legal as a body's last statement, where nothing follows it.
func TestLeavingOperand_AStatementAfterOneThatMayNotLeaveIsReachable(t *testing.T) {
	for _, tc := range []struct{ name, fn string }{
		{"one arm goes on", "fn f(c: Bool) -> Int\n    close(if c then 1 else return 2)\n    3\nend\n"},
		{"the right side of `and`", "fn f(c: Bool) -> Int\n    _b = c and (if c then return 1 else return 2)\n    3\nend\n"},
		{"a lambda's body", "fn f(c: Bool) -> Int\n    _g = fn -> if c then return 1 else return 2\n    3\nend\n"},
		{"a binding of a loop that never breaks, last", "fn f()\n    _x = Iter.loop(0, fn n -> n + 1)\nend\n"},
		{"a call that takes a loop that never breaks, last", "fn f()\n    close(Iter.loop(0, fn n -> n + 1))\nend\n"},
		{"a binding of `unreachable`", "fn f() -> Int\n    x: Int = unreachable \"never\"\n    x\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(leavingOperandDecls + tc.fn)
			expectNoStdlibErrors(t, errs)
		})
	}
}
