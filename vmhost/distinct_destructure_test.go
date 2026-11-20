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
	got := runSourceOutput(t, `import std::io

type Balance Int
type Ratio Float
type Name String
type Pair (Int, String)

fn cents(Balance(n)) -> Int
    n
end

fn first(maybe: Maybe<Balance>) -> Int
    Some(Balance(n)) = maybe else
        return -1
    end
    n
end

fn main()
    balance = Balance(42)
    Balance(n) = balance
    io.inspect(n)
    io.inspect(case Ratio(1.5)
        Ratio(x) -> x
    end)
    io.inspect(if Some(Name(s)) = Some(Name("ada")) then s else "")
    io.inspect(first(Some(Balance(7))))
    io.inspect(first(None))
    io.inspect(cents(Balance(3)))
    double = fn Balance(m) -> Balance(m * 2)
    io.inspect(double(balance))
    Pair(a, b) = Pair(1, "x")
    io.inspect((a, b))
end
`)
	want := "42\n1.5\n\"ada\"\n7\n-1\n3\nBalance(84)\n(1, \"x\")\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A destructuring statement whose head names a type or a variant is a
// pattern binding, lowered as the pattern is in a `case` arm: a subtype, a
// distinct over a subtype's base, `_`, a generic distinct, a distinct of a
// list, and a one-variant enum's positional, struct and embedded variants.
func TestDistinctDestructuring_StatementsRunAsPatterns(t *testing.T) {
	got := runSourceOutput(t, `import std::io

subtype Port Int when fn p -> p >= 1

type Meters Int

derive Comparable for Meters

subtype Short Meters when fn m -> m < Meters(100)

type Items List<Int>

type Wrap<T> T

enum Box
    Only(Int)
end

enum Rec
    Only{a: Int, b: String}
end

enum Emb
    embeds Meters
end

fn main()
    p: Port = 80
    Port(n) = p
    io.inspect(n)
    Some(s) = Short.try_from(Meters(5)) else return
    Short(m) = s
    Meters(mi) = m
    io.inspect(mi)
    Meters(_) = m
    Wrap(w) = Wrap(3)
    io.inspect(w)
    Items(xs) = Items([1, 2])
    io.inspect(xs)
    Box.Only(b) = Box.Only(4)
    io.inspect(b)
    Rec.Only(r) = Rec.Only{a: 1, b: "z"}
    io.inspect((r.a, r.b))
    e: Emb = Meters(9)
    Emb.Meters(ei) = e
    io.inspect(ei)
end
`)
	want := "80\n5\n3\n[1, 2]\n4\n(1, \"z\")\n9\n"
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
		src := "type Balance Int\n\nfn main()\n    balance = Balance(42)\n    _ = " + tc.expr + "\nend\n"
		_, err := vmhost.LoadSource("main.nomi", src)
		if err == nil {
			t.Fatalf("%s: the front end accepts it", tc.expr)
		}
		if msg := err.Error(); !strings.Contains(msg, "`Int` is a type, not a function") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: got %s", tc.expr, msg)
		}
	}
}
