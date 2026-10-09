package vmhost_test

import "testing"

// A generic interface default a type inherits runs called through the
// type, as it does through the interface: `Box.second(b, "x")` beside
// `Pairs.second(b, 2.5)`, in the declaring file and from another one. The
// checker typed the type-qualified call from the interface's signature but
// recorded no reference at `second`, so the call's instantiation had nowhere
// to go and the IR builder could not solve the default's `U`.
func TestTypeQualifiedInheritedGenericDefault_Runs(t *testing.T) {
	t.Run("same file", func(t *testing.T) {
		got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

struct Box {
    value: Int
}

interface Pairs {
    fn pair<U>(b: self, u: U): (self, U)

    fn second<U>(_b: self, u: U): U {
        u
    }
}

impl Pairs for Box {
    fn pair<U>(b: Box, u: U): (Box, U) {
        (b, u)
    }
}

fn main() {
    b = Box { value: 1 }
    io.inspect(Pairs.second(b, 2.5))
    io.inspect(Box.second(b, "x"))
    io.inspect(Box.second(b, [1, 2]))
    f = Box.second(b, _)
    io.inspect(f(True))
}
`})
		if want := "2.5\n\"x\"\n[1, 2]\nTrue\n"; got != want {
			t.Fatalf("output %q, want %q", got, want)
		}
	})
	t.Run("another file", func(t *testing.T) {
		main := "import {\n    std/io\n    leaf\n    leaf.{Box, Pairs}\n}\n\n" +
			"fn main() {\n    b = Box { value: 1 }\n    io.inspect(Pairs.second(b, 0))\n" +
			"    io.inspect(Box.second(b, \"y\"))\n    io.inspect(leaf.Box.second(b, 3))\n}\n"
		got := runSiblingProgram(t, map[string]string{"leaf.nomi": siblingGenericLeaf, "main.nomi": main})
		if want := "0\n\"y\"\n3\n"; got != want {
			t.Fatalf("output %q, want %q", got, want)
		}
	})
}
