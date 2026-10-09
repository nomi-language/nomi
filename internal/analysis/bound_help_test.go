package analysis_test

import (
	"strings"
	"testing"
)

// An unmet `where` bound's help says what the program can do. `derive` is
// offered only for a derivable interface on a type the program declares; an
// impl block is spelled with the interface's own functions; and where the
// program declares neither the type nor the interface, so no impl may be
// written, it says so, then names the few types that implement a narrow
// interface or, for a broad one, says to wrap the type.
func TestBoundErrorHelp(t *testing.T) {
	const decls = `import std/matcher.Matcher

struct Point {
    x: Int
}

interface Shape {
    fn area(s: self): Float
    fn scale(s: self, by: Float): self
    fn name(s: self): String {
        "shape"
    }
}

fn has<M>(text: String, m: M): Bool where M: Matcher {
    Matcher.contained_in?(m, text)
}

fn total<S>(s: S): Float where S: Shape {
    Shape.area(s)
}

fn key<H>(h: H): Int where H: Hashable {
    Hashable.hash(h)
}

`
	for _, tc := range []struct {
		name, call, message, help string
	}{
		{
			"std interface, std type",
			`has("abc", 3)`,
			"Int does not implement Matcher (required by `where M: Matcher`)",
			"only std can implement `Matcher` for `Int`; `Regex` and `String` implement it",
		},
		{
			"broad std interface, std type",
			`key(Unit)`,
			"Unit does not implement Hashable (required by `where H: Hashable`)",
			"only std can implement `Hashable` for `Unit`; wrap it in a type of your own that implements `Hashable`",
		},
		{
			"own interface",
			`total(Point{x: 1})`,
			"Point does not implement Shape (required by `where S: Shape`)",
			"write an `impl Shape for Point` block with `fn area(s: Point): Float` and `fn scale(s: Point, by: Float): Point`",
		},
		{
			"own interface, std type",
			`total(4)`,
			"Int does not implement Shape (required by `where S: Shape`)",
			"write an `impl Shape for Int` block with `fn area(s: Int): Float` and `fn scale(s: Int, by: Float): Int`",
		},
		{
			"derivable interface, own type",
			`key(Point{x: 1})`,
			"Point does not implement Hashable (required by `where H: Hashable`)",
			"add `derive Hashable` to `Point` or write an `impl Hashable for Point` block with `fn hash(value: Point): Int`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main() {\n    _ = " + tc.call + "\n}\n"
			line := strings.Count(decls, "\n") + 2
			errs := checkWithStdlib(src)
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if e.Line != line || e.Col != 9 {
					t.Errorf("at %d:%d, want %d:9", e.Line, e.Col, line)
				}
				if len(e.Hints) != 1 || e.Hints[0] != tc.help {
					t.Fatalf("hints %q, want [%q]", e.Hints, tc.help)
				}
				return
			}
			t.Fatalf("no %q error; got %v", tc.message, errs)
		})
	}
}
