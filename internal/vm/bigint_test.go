package vm_test

// An Int is exact at any size (docs/implementation-notes.md, "Int: exact at
// any size"). The VM keeps a value that fits int64 in a word register and a
// larger one beside its word, in the word bank's shadow. These programs move
// big values through every path that carries a word: constants, moves,
// arithmetic on each side of the boundary, comparison and equality, calls
// and tail calls, returns, struct fields, enum payloads, distinct types,
// collections, patterns and the 64-bit wrapping operators.

import (
	"strings"
	"testing"
)

const bigIntProgram = `import std/io

struct Account {
  id: Int
  balance: Int
}

enum Amount {
  Exact Int
  Unknown
}

type Cents Int

fn factorial(n: Int) -> Int {
  if n <= 1 { 1 } else { n * factorial(n - 1) }
}

fn sum_to(n: Int, acc: Int) -> Int {
  if n == 0 { acc } else { sum_to(n - 1, acc + n * 1_000_000_000_000_000_000) }
}

fn through(a: Int, b: Int, c: Int) -> Int {
  if a == 0 { b - c } else { through(a - 1, c, b) }
}

fn pick(n: Int) -> String {
  case n {
    18_446_744_073_709_551_616 -> "2^64"
    -9_223_372_036_854_775_808 -> "-2^63"
    _ -> "other"
  }
}

fn main() {
  top = 9_223_372_036_854_775_807
  past = top + 1
  io.print(past)
  io.print(past - 1 == top)
  io.print(-past)
  io.print(-(-past))
  io.print(past * past)
  io.print(past / 3)
  io.print(past % 1_000_000_007)
  io.print(-past / 7)
  io.print(-past % 7)
  io.print(past > top and top < past and -past <= -top)
  io.print(factorial(25))
  io.print(sum_to(100, 0))
  io.print(through(3, past, 1))
  io.print(through(4, past, 1))
  account = #Account{id: 1, balance: past * 4}
  io.print(account.balance - past)
  io.print(account == #Account{id: 1, balance: 36_893_488_147_419_103_232})
  io.print(Amount.Exact(past) == Amount.Exact(top + 1))
  io.print(Cents(past) == Cents(top + 1))
  io.print([past, 1, -past])
  io.print(%{past => "big"} |> Map.get(top + 1))
  io.print(pick(Int.pow(2, 64)))
  io.print(pick(-past))
  io.print(pick(past))
  io.print(Int.wrapping_add(past, past))
  io.print(Int.wrapping_mul(past + 3, 2))
  io.print(Hashable.hash(past) == Hashable.hash(top + 1))
  io.print(past - past + 5 == 5)
}
`

const bigIntWant = `9223372036854775808
True
-9223372036854775808
9223372036854775808
85070591730234615865843651857942052864
3074457345618258602
291172004
-1317624576693539401
-1
True
15511210043330985984000000
5050000000000000000000
-9223372036854775807
9223372036854775807
27670116110564327424
True
True
True
[9223372036854775808, 1, -9223372036854775808]
Some(big)
2^64
-2^63
other
0
6
True
True
`

func TestBigInt_ValuesPastInt64RunThroughEveryWordPath(t *testing.T) {
	out, err := runDepth(t, bigIntProgram)
	if err != nil {
		t.Fatalf("the program failed: %v\noutput:\n%s", err, out)
	}
	if out != bigIntWant {
		t.Fatalf("output:\n%s\nwant:\n%s", out, bigIntWant)
	}
}

// TestBigInt_AnIndexPastInt64IsPastAnyCollection: an index, a count or a
// bound past int64's range reads as past the collection, so `get(...)!`
// finds nothing there.
func TestBigInt_AnIndexPastInt64IsPastAnyCollection(t *testing.T) {
	out, err := runDepth(t, `import std/io

fn main() {
  big = Int.pow(10, 30)
  xs = [1, 2, 3]
  io.print(Iter.get(xs, big))
  io.print(Iter.take(xs, big) |> Iter.to_list())
  io.print(Iter.drop(xs, big) |> Iter.to_list())
  io.print(String.slice("hello", 1, big))
  io.print(Bytes.get(String.to_bytes("ab"), -big))
  io.print(Vector.get(#[1, 2], big))
  io.print(Vector.get(#[1, 2], big)!)
}
`)
	want := "None\n[1, 2, 3]\n[]\nello\nNone\nNone\n"
	if out != want || err == nil || err.Error() != "line 12: `Vector.get(#[1, 2], big)` found nothing" {
		t.Fatalf("output %q, error %v; want %q and the index fault", out, err, want)
	}
}

// TestBigInt_AZeroDivisorFaultsWhateverTheDividend: the one Int fault is a
// zero divisor, reported at its operator, whatever the dividend's size.
func TestBigInt_AZeroDivisorFaultsWhateverTheDividend(t *testing.T) {
	_, err := runDepth(t, `import std/io

fn main() {
  zero = 0
  io.print(Int.pow(10, 30) / zero)
}
`)
	if err == nil || !strings.Contains(err.Error(), "line 5: division by zero") {
		t.Fatalf("got %v, want the zero divisor at line 5", err)
	}
}
