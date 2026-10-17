package irbuild

import "testing"

// A typed literal whose handler answers a function: the literal's value is
// that function, which a call then runs. The handler's result had to be a
// retained plain value, so a function result declined.
func TestLiteralFuncResult_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import {
    std/io
    std/literals.{Fragment, Literal}
}

type Adder

impl Literal for Adder {
    fn from_fragments(fragments: List<Fragment<Int>>) -> (Int) -> Int {
        n = fragments |> Iter.map(fn f -> case f {
            Fragment.Static(s) -> String.length(s)
            Fragment.Dynamic(d) -> d
        }) |> Iter.reduce(fn acc = 0, x -> acc + x)
        fn x -> x + n
    }
}

fn main() {
    f = Adder"add"
    io.print("${f(1)}")
    g = Adder"a${10}b"
    io.print("${g(1)}")
    h = Adder`+"`raw`"+`
    io.print("${h(0)}")
}
`, "4\n13\n3\n")
}
