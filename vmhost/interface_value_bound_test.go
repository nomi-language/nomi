package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A generic call bounded by Comparable at the interface type Comparable is
// a compile error (spec §12 *Interface values*), where it was accepted and
// then refused as not supported. The program fails if it starts being
// admitted.
func TestInterfaceValueBound_ComparableIsACompileError(t *testing.T) {
	_, err := vmhost.LoadSource("main.nomi", `import std::io

fn lt<T>(a: T, b: T) -> Bool where T: Comparable
    a < b
end

fn both(x: Comparable, y: Comparable) -> Bool
    lt(x, y)
end

fn main()
    io.print(both(1, 2))
end
`)
	if err == nil {
		t.Fatal("the front end admits lt(x, y) over two Comparable values")
	}
	want := "`Comparable` does not meet the bound `Comparable`, since `Comparable.compare` cannot be called on a `Comparable` value: its 2 `self` arguments must be one implementing type, and `Comparable` values may hold different ones"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q; got: %v", want, err)
	}
}

// The bounds an interface type meets run through dispatch: Equatable and
// Hashable on the held values' own types, Display on its one `self`.
func TestInterfaceValueBound_EquatableDisplayAndHashableRun(t *testing.T) {
	got := runSourceOutput(t, `import std::io

fn eq<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn show<T>(a: T) -> String where T: Display
    Display.to_string(a)
end

fn count<T>(xs: List<T>) -> Int where T: Hashable
    Set.size(Iter.to_set(xs))
end

fn main()
    a: Equatable = 1
    b: Equatable = "1"
    io.print(eq(a, b))
    io.print(eq(a, a))
    d: Display = 42
    io.print(show(d))
    h: List<Hashable> = [1, "1", 1]
    io.print(count(h))
end
`)
	if want := "False\nTrue\n42\n2\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
