package vmhost_test

import "testing"

// A partial application's open parameters cannot shadow a name its callee
// expression starts from. `maybe.Maybe.map`'s first parameter is named
// `maybe`, and the partial's lambda took the callee's parameter names, so
// inside it `maybe` was the parameter and `maybe.Maybe` a field read on it.
// The partial is still called by the callee's names.
func TestPartialApplication_ParameterNamedLikeTheQualifier(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import {
    std/io
    std/maybe
    std/lists
}

fn noted(n: Int): Int {
    io.print("made ${n}")
    n
}

fn tag(_label: String, n: Int): Int {
    n
}

fn main() {
    m = maybe.Maybe.map(_, |n: Int| n + noted(1))
    io.inspect(m(Some(4)))
    io.inspect(m(maybe: Some(1)))
    w = maybe.Maybe.with_default(_, noted(0))
    io.inspect(w(None))
    io.inspect(w(Some(5)))
    h = lists.List.head(_)
    io.inspect(h([7]))
    t = tag(_, noted(3))
    io.inspect(t(_label: "z"))
}
`})
	want := "made 1\nSome(5)\nmade 1\nSome(2)\nmade 0\n0\n5\nSome(7)\nmade 3\n3\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
