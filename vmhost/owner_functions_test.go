package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// ownerFunctionsSrc declares owner functions, the functions of an inherent
// block on a generic type or an interface whose signature names none of the
// block's type parameters, as std's `impl Iter<T>` declares `Iter.from`.
const ownerFunctionsSrc = `import std/io

pub interface Src<T> {
    fn get(s: self): T
}

impl Src<T> {
    pub fn seven(): Int { 7 }
    pub fn twice<U>(x: U): (U, U) { (x, x) }
}

pub interface Shape {
    fn area(s: self): Int
}

impl Shape {
    pub fn unit(): Int { 1 }
}

struct Box<T> {
    v: T
}

impl Box<T> {
    fn get(b: Box<T>): T { b.v }
    fn seven(): Int { 7 }
    fn twice<U>(x: U): (U, U) { (x, x) }
    fn fourteen(): Int { Box.seven() * 2 }
    fn bare_fourteen(): Int { seven() + seven() }
    fn shown<U>(x: U): String where U: Display { "<${x}>" }
    fn mk<T>(x: T): Box<T> { Box{v: x} }
    fn plus_seven(b: Box<T>): Int { seven() + Iter.count([b]) }
}

enum Tree<T> {
    Leaf
    Node(T)
}

impl Tree<T> {
    fn depth_limit(): Int { 3 }
    fn pair<U>(x: U): (U, U) { (x, x) }
}
`

// Every form of a call to an owner function runs: type-qualified on a
// generic interface, a plain interface, a generic struct and a generic enum,
// through a pipe, as a function value, from another owner function by
// qualified and bare name, from a function built per instance, with the
// function's own bounded type parameter, and with its own `T` shadowing the
// block's.
func TestOwnerFunctions_RunInEveryForm(t *testing.T) {
	got := runOutput(t, ownerFunctionsSrc+`
fn main() {
    io.inspect(Src.seven())
    io.inspect(Src.twice(1))
    io.inspect(Shape.unit())
    io.inspect(Box.seven())
    io.inspect(Box.twice(1))
    io.inspect("a" |> Box.twice())
    f = Box.seven
    io.inspect(f())
    g: (Int) -> (Int, Int) = Box.twice
    io.inspect(g(3))
    io.inspect(Box.fourteen())
    io.inspect(Box.bare_fourteen())
    io.inspect(Box.shown(5))
    io.inspect(Box.get(Box.mk(9)))
    io.inspect(Box.plus_seven(Box{v: "x"}))
    io.inspect(Tree.depth_limit())
    io.inspect(Tree.pair(True))
}
`)
	want := `7
(1, 1)
1
7
(1, 1)
("a", "a")
7
(3, 3)
14
14
"<5>"
9
8
3
(True, True)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The same calls from test bodies.
func TestOwnerFunctions_RunInTests(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "owner_test.nomi"
	p, err := vmhost.LoadSource(name, strings.TrimPrefix(ownerFunctionsSrc, "import std/io\n\n")+`
test "src" {
    assert Src.seven() == 7
    assert Src.twice(1) == (1, 1)
}

test "box" {
    assert Box.seven() == 7
    assert 2 |> Box.twice() == (2, 2)
    assert Box.fourteen() == 14
    assert Box.shown(5) == "<5>"
}

test "tree" {
    assert Tree.depth_limit() == 3
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	if out := buf.String(); strings.Contains(out, "BLOCKED") || !strings.Contains(out, "3 passed, 0 failed") {
		t.Fatalf("want three passing cases:\n%s", out)
	}
}
