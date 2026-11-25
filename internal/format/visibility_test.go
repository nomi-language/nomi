package format

import "testing"

// The formatter prints `private` where `pub` goes, on every declaration
// that takes a modifier, and keeps it through a second pass.
func TestFormat_PrivateOnEveryDeclaration(t *testing.T) {
	roundTrip(t, `private fn helper(n: Int) -> Int
    n + 1
end

private struct Point
    x: Int
end

private opaque struct Token
    raw: String
end

private enum Kind
    A
    B
end

private type Meters Int

private opaque type Secret Int

private subtype Small Int when fn n -> n < 10

private interface Shape
    fn area(s: self) -> Int
end

private typealias Count Int

private once limit: Int = 3

private host fn now() -> Int

private host type Handle

private phrase greeting `+"`Hello`"+`

impl Point
    private fn origin() -> Point
        Point{x: 0}
    end

    private once zero: Int = 0
end
`)
}

// Extra spaces after `private` collapse to one, as after `pub`.
func TestFormat_PrivateSpacing(t *testing.T) {
	got, err := Format("private   fn f() -> Int\n    1\nend\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "private fn f() -> Int\n    1\nend\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
