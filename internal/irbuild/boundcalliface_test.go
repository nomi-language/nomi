package irbuild

import "testing"

// A bounded `T.to_string(x)` runs at a type with no owner to spell the call
// by (a tuple, in a module function and in a nested one) as the bound
// interface's call, and at another file's type whose impl this file writes
// (`impl Display for lib.Dog`), which interpolation and another file's
// generic reach too.
func TestBoundCallIface_TupleAndAnImplBesideTheCaller(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    lib
}

impl Display for lib.Dog {
    fn to_string(d: lib.Dog): String {
        "dog " + d.name
    }
}

fn show<T>(x: T): String where T: Display {
    T.to_string(x)
}

fn main() {
    fn txt<T>(x: T): String where T: Display {
        T.to_string(x)
    }
    d = lib.Dog{name: "d"}
    io.print(show((1, "a")))
    io.print(txt((2, "b")))
    io.print(show(d))
    io.print(Display.to_string(d))
    io.print("${d}")
    io.print(lib.render(d))
}
`, "(1, a)\n(2, b)\ndog d\ndog d\ndog d\n<dog d>\n", map[string]string{"lib.nomi": `pub struct Dog {
    name: String
}

pub fn render<T>(x: T): String where T: Display {
    "<${x}>"
}
`})
}
