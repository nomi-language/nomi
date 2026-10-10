package vmhost_test

import "testing"

// The flat call of a tuple-payload variant (`Two.Both(1, 2)`, a call form of
// its one tuple) runs in every spelling the checker admits: generic and not,
// qualified, dot-leading, imported and aliased, piped, partially applied,
// with three elements, a type parameter beside a concrete type, and nested.
// Each builds the value the tupled form builds.
func TestFlatVariantCall_Runs(t *testing.T) {
	out := runSiblingProgram(t, map[string]string{
		"two.nomi": `pub enum Two<A, B> {
    Both (A, B)
}

pub enum Three<A, B, C> {
    All (A, B, C)
}

pub enum Tagged<T> {
    Tag (T, String)
}

pub enum Seg {
    Pair (Int, String)
}
`,
		"main.nomi": `import std/io
import two.Two
import two.Two.{Both}
import two.Two.{Both as B2}
import two.Three
import two.Tagged
import two.Seg

fn pick(): Two<Int, Int> {
    .Both(3, 4)
}

fn main() {
    a: Two<Int, Int> = Two.Both(1, 2)
    io.inspect(a)
    io.inspect(a == Two.Both((1, 2)))
    io.inspect(Two.Both(1, "x"))
    c: Two<Int, Int> = .Both(5, 6)
    io.inspect(c)
    io.inspect(pick())
    io.inspect(Both(7, "y"))
    io.inspect(B2("z", 8))
    io.inspect(Three.All(1, "y", True))
    io.inspect(Tagged.Tag(7, "seven"))
    io.inspect(Two.Both(Two.Both(1, 2), Maybe.Some("z")))
    io.inspect(9 |> Two.Both("nine"))
    io.inspect("e" |> Two.Both(5, _))
    f = Two.Both(_, "c")
    io.inspect(f(3))
    g: (String) -> Two<Int, String> = Two.Both(4, _)
    io.inspect(g("d"))
    s: Seg = .Pair(1, "a")
    io.inspect(s)
    io.inspect(2 |> Seg.Pair("b"))
    h = Seg.Pair(_, "c")
    io.inspect(h(3))
}
`,
	})
	want := `Both(1, 2)
True
Both(1, "x")
Both(5, 6)
Both(3, 4)
Both(7, "y")
Both("z", 8)
All(1, "y", True)
Tag(7, "seven")
Both(Both(1, 2), Some("z"))
Both(9, "nine")
Both(5, "e")
Both(3, "c")
Both(4, "d")
Pair(1, "a")
Pair(2, "b")
Pair(3, "c")
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}
