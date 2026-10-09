package vmhost_test

import "testing"

// A dot-leading variant or a brace literal compared against a typed operand
// takes that operand's type and runs: in a binding, an annotated Bool
// binding, an if condition, a case guard, reversed, under `and`, and with an
// ordering operator.
func TestRun_ComparisonOperandTakesTheOtherOperandsType(t *testing.T) {
	out := runOutput(t, `import std/io

enum Kind {
    Deposit
    Withdrawal
}

derive Comparable for Kind

struct Point {
    x: Int
    y: Int
}

struct Txn {
    kind: Kind
    amount: Int
}

fn describe(r: Result<Txn, String>): String {
    case r {
        Ok(t) when t.kind == .Deposit -> "in ${t.amount}"
        Ok(t) when .Withdrawal == t.kind -> "out ${t.amount}"
        _ -> "none"
    }
}

fn main() {
    k = Kind.Withdrawal
    a = k == .Deposit
    b: Bool = k != .Deposit
    io.print("${a} ${b}")
    if .Withdrawal == k and k > .Deposit {
        io.print("withdrawal after deposit")
    }
    p = Point{x: 1, y: 2}
    io.print("${p == {x: 1, y: 2}} ${{x: 2, y: 1} == p}")
    io.print(describe(Ok(Txn{kind: .Deposit, amount: 5})))
    io.print(describe(Ok(Txn{kind: .Withdrawal, amount: 3})))
    io.print(describe(Err("x")))
}
`)
	want := "False True\nwithdrawal after deposit\nTrue False\nin 5\nout 3\nnone\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}
