package vmhost_test

import "testing"

// A pipe fills a named `_` with the piped value: a plain function, one with
// defaults the `_` skips, an owner function, an interface-qualified call, a
// generic function whose type argument comes from the piped value, and a
// `try` stage. A trailing positional argument after the named `_` fills the
// first parameter left, as in a direct call. The piped value is evaluated
// before the stage's other arguments, whatever their order in the call, and
// once however many `_`s it fills.
func TestPipe_FillsANamedHole(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

fn tag(a: Int, w: String): String {
    "${a}:${w}"
}

fn spaced(a: Int = 1, b: Int = 2, w: String): String {
    "${a}/${b}/${w}"
}

fn copies<T>(n: Int, item: T): List<T> {
    if n > 1 { [item, item] } else { [item] }
}

fn halve(by: Int, n: Int): Result<Int, String> {
    if n % by == 0 { Ok(n / by) } else { Err("odd ${n}") }
}

fn add(a: Int, b: Int): Int {
    a + b
}

fn noted(n: Int): Int {
    io.print("made ${n}")
    n
}

fn word(s: String): String {
    io.print("word ${s}")
    s
}

fn run(n: Int): Result<Int, String> {
    half = n |> try halve(by: 2, n: _)
    Ok(half + 1)
}

fn main() {
    io.print("x" |> tag(1, w: _))
    io.print("y" |> tag(w: _, a: 2))
    io.print("z" |> spaced(w: _))
    io.print("q" |> spaced(5, w: _))
    io.print(4 |> spaced(b: _, w: "r", 7))
    io.print("3" |> String.pad_start(width: 4, s: _))
    io.inspect("s" |> copies(n: 2, item: _))
    io.print(7 |> Display.to_string(value: _))
    io.inspect(run(8))
    io.inspect(run(7))
    io.print(word("first") |> tag(noted(9), w: _))
    io.print(word("again") |> tag(w: _, a: noted(8)))
    io.print(noted(4) |> add(_, b: _))
}
`})
	want := "1:x\n2:y\n1/2/z\n5/2/q\n7/4/r\n   3\n[\"s\", \"s\"]\n7\nOk(5)\nErr(\"odd 7\")\n" +
		"word first\nmade 9\n9:first\nword again\nmade 8\n8:again\nmade 4\n8\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
