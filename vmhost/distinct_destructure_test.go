package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A distinct is unwrapped by destructuring, in every position a pattern
// takes: a binding, a `case` arm, an `if` pattern, a binding with `else`, a
// function parameter, a lambda parameter, and nested inside another pattern.
// Each runs to the inner value.
func TestDistinctDestructuring_EveryFormRuns(t *testing.T) {
	got := runSourceOutput(t, `import std/io

type Balance Int
type Ratio Float
type Name String
type Pair (Int, String)

fn cents(Balance(n)): Int {
    n
}

fn first(maybe: Maybe<Balance>): Int {
    Some(Balance(n)) = maybe else {
        return -1
    }
    n
}

fn main() {
    balance = Balance(42)
    Balance(n) = balance
    io.inspect(n)
    io.inspect(case Ratio(1.5) {
        Ratio(x) -> x
    })
    io.inspect(if Some(Name(s)) = Some(Name("ada")) { s } else { "" })
    io.inspect(first(Some(Balance(7))))
    io.inspect(first(None))
    io.inspect(cents(Balance(3)))
    double = |Balance(m)| Balance(m * 2)
    io.inspect(double(balance))
    Pair(a, b) = Pair(1, "x")
    io.inspect((a, b))
}
`)
	want := "42\n1.5\n\"ada\"\n7\n-1\n3\nBalance(84)\n(1, \"x\")\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The inner type's name called on a distinct is rejected, prefix and piped,
// with help naming the destructuring.
func TestDistinctDestructuring_TheInnerTypesNameDoesNotUnwrap(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{"Int(balance)", "to get the Int inside, destructure the Balance: `Balance(n) = balance`"},
		{"balance |> Int()", "to get the Int inside, destructure the Balance: `Balance(n) = balance`"},
	} {
		src := "type Balance Int\n\nfn main() {\n    balance = Balance(42)\n    _ = " + tc.expr + "\n}\n"
		_, err := vmhost.LoadSource("main.nomi", src)
		if err == nil {
			t.Fatalf("%s: the front end accepts it", tc.expr)
		}
		if msg := err.Error(); !strings.Contains(msg, "`Int` is a type, not a function") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: got %s", tc.expr, msg)
		}
	}
}
