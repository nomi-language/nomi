package irbuild

import (
	"strings"
	"testing"
)

// A parameter default that calls a sibling of its own impl block or
// interface by its bare name (spec §12: inside the block, sibling functions
// are called bare). The default is lowered where the call leaves it out, at
// the caller or in the declaring file's accessor, not in the block's body,
// so the bare call is lowered from the checker's record of it
// (bareImplCall), as the same call in a body is.

const bareDefaultImpl = `pub struct Sq
    s: Int
end

impl Sq
    pub fn side(q: Sq) -> Int
        q.s
    end

    pub fn area(q: Sq, extra: Int = side(q) * 2, tail: Int = 0) -> Int
        q.s * q.s + extra + tail
    end
end
`

const bareDefaultIface = `pub interface Shape
    fn side(value: self) -> Int

    open fn area(value: self, extra: Int = side(value) * 2, tail: Int = 0) -> Int
        side(value) * side(value) + extra + tail
    end
end
`

const bareDefaultIfaceSq = `pub struct Sq
    s: Int
end

impl Shape for Sq
    fn side(value: Sq) -> Int
        value.s
    end
end
`

const bareDefaultImplCalls = `fn main()
    io.print("direct ${Sq.area(Sq{s: 3})}")
    io.print("named ${Sq.area(Sq{s: 3}, tail: 1)}")
    io.print("piped ${Sq{s: 3} |> Sq.area()}")
    f = &Sq.area(&1)
    io.print("partial ${f(Sq{s: 3})}")
end
`

const bareDefaultImplWant = "direct 15\nnamed 16\npiped 15\npartial 15\n"

const bareDefaultIfaceCalls = `fn via<T>(x: T) -> Int where T: Shape
    T.area(x)
end

fn main()
    io.print("direct ${Sq.area(Sq{s: 3})}")
    io.print("qualified ${Shape.area(Sq{s: 3})}")
    io.print("named ${Shape.area(Sq{s: 3}, tail: 1)}")
    io.print("piped ${Sq{s: 3} |> Shape.area()}")
    f = &Shape.area(&1)
    io.print("partial ${f(Sq{s: 3})}")
    io.print("type parameter ${via(Sq{s: 3})}")
    s: Shape = Sq{s: 3}
    io.print("erased ${Shape.area(s)}")
end
`

const bareDefaultIfaceWant = "direct 15\nqualified 15\nnamed 16\npiped 15\npartial 15\n" +
	"type parameter 15\nerased 15\n"

func TestBareSiblingDefault_ImplBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+bareDefaultImpl+"\n"+bareDefaultImplCalls, bareDefaultImplWant)
	})
	t.Run("module-qualified from another file", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io
import sq

fn main()
    io.print(sq.Sq.area(sq.Sq{s: 3}))
    io.print(sq.Sq.area(sq.Sq{s: 3}, tail: 1))
end
`, "15\n16\n", map[string]string{"sq.nomi": bareDefaultImpl})
	})
}

func TestBareSiblingDefault_Interface(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Run("one file", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\n\n"+bareDefaultIface+"\n"+bareDefaultIfaceSq+"\n"+bareDefaultIfaceCalls, bareDefaultIfaceWant)
	})
	t.Run("module-qualified from another file", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io
import lib

fn main()
    io.print(lib.Shape.area(lib.Sq{s: 3}))
    io.print(lib.Shape.area(lib.Sq{s: 3}, tail: 1))
end
`, "15\n16\n", map[string]string{"lib.nomi": bareDefaultIface + "\n" + bareDefaultIfaceSq})
	})
	// The interface in one file and the implementing type in another: the
	// default is filled through shape.nomi's accessor, and the default
	// body is instantiated in sq.nomi.
	t.Run("interface and implementor in other files", func(t *testing.T) {
		verifyLambdaProgram(t, "import std::io\nimport shape.Shape\nimport sq.Sq\n\n"+bareDefaultIfaceCalls, bareDefaultIfaceWant,
			map[string]string{
				"shape.nomi": bareDefaultIface,
				"sq.nomi":    "import shape.Shape\n\n" + bareDefaultIfaceSq,
			})
	})
}

// The same defaults, filled in a test's body.
func TestBareSiblingDefault_InATest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	for _, tc := range []struct{ name, src string }{
		{"impl block", bareDefaultImpl + `
test "a default calls a sibling bare"
    assert Sq.area(Sq{s: 3}) == 15
    assert Sq.area(Sq{s: 3}, tail: 1) == 16
end
`},
		{"interface", bareDefaultIface + "\n" + bareDefaultIfaceSq + `
test "a default calls a sibling bare"
    assert Shape.area(Sq{s: 3}) == 15
    assert Sq.area(Sq{s: 3}, tail: 1) == 16
    s: Shape = Sq{s: 3}
    assert Shape.area(s) == 15
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := irTestBodyVM(t, tc.src, 1)
			if !strings.Contains(out, "1 passed, 0 failed") {
				t.Fatalf("want the case to pass; got:\n%s", out)
			}
		})
	}
}
