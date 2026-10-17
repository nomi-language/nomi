package vmhost_test

import "testing"

// Positional construction still checks and runs once a named argument to a
// positional constructor is an error (internal/analysis
// named_arg_constructor_test.go), and an interface-qualified call takes the
// interface method's parameter names, evaluated in Nomi's order: positionals
// first, then named arguments as written.
func TestNamedArgConstructor_PositionalFormsAndInterfaceNamesRun(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Shape {
    Sq Int
    Pair #(Int, Int)
    embeds Id
}

derive Debug for Shape

type Id Int

derive Debug for Id

type Two #(Int, String)

derive Debug for Two

struct P {
    x: Int
    y: Int
}

fn say(n: Int) -> Int {
    io.print("say ${n}")
    n
}

fn mk() -> P {
    io.print("mk")
    P{x: 1, y: 1}
}

fn show<T>(x: T) -> String where T: Display {
    T.to_string(value: x)
}

fn main() {
    a: Maybe<Int> = Some(1)
    b: Result<Int, String> = Ok(2)
    c: Shape = .Sq(3)
    d = Shape.Pair(4, 5)
    e = Shape.Id(6)
    f = Some(_)
    g: Maybe<Int> = 7 |> Some()
    h = Two(8, "x")
    io.print(Debug.inspect(#(a, b, c, d, e, f(9), g, h)))
    p = Struct.update(updates: {x: say(10)}, original: mk())
    io.print("${p.x} ${p.y}")
    q = Struct.update(updates: {y: say(11)}, mk())
    io.print("${q.x} ${q.y}")
    r = Struct.update(mk(), updates: {y: say(12)})
    io.print("${r.x} ${r.y}")
    io.print(Display.to_string(value: 13))
    io.print(show(14))
}
`)
	want := `#(Some(1), Ok(2), Sq(3), Pair(4, 5), Id(6), Some(9), Some(7), Two(8, "x"))
say 10
mk
10 1
mk
say 11
1 11
mk
say 12
1 12
13
14
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
