package main

import (
	"strings"
	"testing"
)

// TestRelinkAfterABodyEditInAnInterfaceDefaultsFile: shape.nomi declares an
// interface whose defaults, one generic, call its private helpers;
// shapes.nomi implements it for Tri, and main.nomi calls the defaults on
// Tri, so the generic default's instance at Tri is requested through
// main.nomi. Each edit of a body in shape.nomi, made one after another,
// relinks the kept program from its file records, and so does undoing
// them. An edit of a default's body used to miss with "shapes.nomi names
// shape.Shape.pick<…>, which no module defines", and an edit after a
// relinked one with "shape's declarations moved, and a std instance holds
// positions in it", and lower the whole program again.
func TestRelinkAfterABodyEditInAnInterfaceDefaultsFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	shape := `pub interface Shape
    fn area(s: self) -> Int

    fn describe(s: self) -> String
        label(area(s))
    end

    fn pick<T>(s: self, a: T, b: T) -> T
        if big(area(s)) then a else b
    end
end

fn label(n: Int) -> String
    "area ${n}"
end

fn big(n: Int) -> Bool
    n > 10
end
`
	p := cacheProject{name: "iface-default", entry: "main.nomi", files: map[string]string{
		"nomi.toml":  "[module]\nname = \"ifacedefault\"\nentry_points = [\"main\"]\n",
		"shape.nomi": shape,
		"shapes.nomi": `import shape.Shape

pub struct Tri
    base: Int
end

impl Shape for Tri
    fn area(t: Tri) -> Int
        t.base * 2
    end
end
`,
		"main.nomi": `import
    std::io
    shape.Shape
    shapes.Tri
end

fn main()
    t = Tri{base: 9}
    io.print(Tri.describe(t))
    io.inspect(Shape.pick(t, 1, 2))
    io.print(Tri.pick(Tri{base: 2}, "big", "small"))
end
`,
	}}
	edits := []cacheEdit{
		{what: "a helper's body", rel: "shape.nomi", text: strings.Replace(shape, `"area ${n}"`, `"size ${n}"`, 1)},
		{what: "a helper's other body", rel: "shape.nomi", text: strings.Replace(shape, "n > 10", "n > 20", 1)},
		{what: "a default's body", rel: "shape.nomi", text: strings.Replace(shape, "label(area(s))", "label(area(s) + 1)", 1)},
		{what: "a generic default's body", rel: "shape.nomi", text: strings.Replace(shape, "then a else b", "then b else a", 1)},
		{what: "the original back", rel: "shape.nomi", text: shape},
	}
	_, _, byEdit := runCacheEditsBy(t, p, edits)
	for _, e := range edits {
		if byEdit[e.what] == 0 {
			t.Errorf("no command relinked the program after %s in shape.nomi: %q", e.what, cacheWhy[e.what])
		}
	}
}
