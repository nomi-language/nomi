package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An ordering over values held as Comparable is a compile error (spec §12
// *Interface values*), where it was accepted and then refused as not
// supported. The program fails if it starts being admitted.
func TestInterfaceValueOrdering_IsACompileError(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import std::io

fn less(x: Comparable, y: Comparable) -> Bool
    x < y
end

fn main()
    io.print(less(1, 2))
end
`)
	if err == nil {
		t.Fatal("the front end admits an ordering over two Comparable values")
	}
	want := "`<` cannot compare `x`, a `Comparable`: its two operands must be one implementing type, and `Comparable` values may hold different ones"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q; got: %v", want, err)
	}
}

// The forms the error's hint names run: an ordering over a type parameter
// bounded by Comparable, at Int and at a derived struct, and `==` over two
// Equatable interface values, which compares the held values.
func TestInterfaceValueOrdering_TheGenericFormAndEqualityRun(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct P
    a: Int
end

derive Equatable for P

derive Comparable for P

fn less<T>(x: T, y: T) -> Bool where T: Comparable
    x < y
end

fn same(x: Equatable, y: Equatable) -> Bool
    x == y
end

fn main()
    io.print(less(1, 2))
    io.print(less(P{a: 3}, P{a: 2}))
    io.print(same(1, 1))
    io.print(same(1, "1"))
    io.print(same(P{a: 1}, P{a: 1}))
end
`)
	if want := "True\nFalse\nTrue\nFalse\nTrue\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
