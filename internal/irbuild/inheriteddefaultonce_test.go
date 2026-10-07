package irbuild

import "testing"

// An interface default written in another file reads that file's `once`,
// in its body and in a parameter default, even where the implementing file
// binds the same name to its own `once`: the default is monomorphized here
// and its names are the declaring file's. A private `once` is readable, since
// the code naming it was written beside it.
func TestInheritedDefault_ReadsTheDeclaringFilesOnce(t *testing.T) {
	lib := `once tag = "sibling"

once mark = "!"

pub interface Powered {
    fn label(w: self): String

    fn tagged(w: self, suffix: String = tag): String {
        Powered.label(w) + ":" + suffix + mark
    }
}
`
	verifyLambdaProgram(t, `import {
    std/io
    lib.Powered
}

once tag = "entry"

struct Widget {
    name: String
}

impl Powered for Widget {
    fn label(w: Widget): String {
        w.name
    }
}

fn main() {
    io.print(Powered.tagged(Widget{name: "w"}))
    io.print(Powered.tagged(Widget{name: "v"}, "given"))
    ps: List<Powered> = [Widget{name: "a"}]
    io.inspect(Iter.map(ps, |p| Powered.tagged(p)) |> Iter.to_list())
    io.print(tag)
}
`, "w:sibling!\nv:given!\n[\"a:sibling!\"]\nentry\n", map[string]string{"lib.nomi": lib})
}
