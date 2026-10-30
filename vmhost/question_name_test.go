package vmhost_test

import "testing"

// A `?` right after a name propagates that name's value: `handler?(x)`
// propagates a Maybe of a function and calls its payload. `try` is an
// ordinary name, for a binding and a function alike.
func TestQuestion_AfterANameThenCallRuns(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn apply(handler: Maybe<(Int) -> Int>, x: Int) -> Maybe<Int> {
    Some(handler?(x))
}

fn try(x: Int) -> Int {
    x * 2
}

fn main() {
    io.print(apply(Some(fn n -> n + 1), 4))
    io.print(apply(None, 4))
    try = try(3)
    io.print(try)
}
`)
	want := "Some(5)\nNone\n6\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
