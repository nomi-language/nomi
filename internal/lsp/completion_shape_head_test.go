package lsp

import (
	"slices"
	"testing"
)

const shapeHeadDecls = `struct Point {
    x: Int
}

enum Shape {
    Circle {r: Float}
    Dot
}

type Pair #(Int, Int)
type Path Vector<Int>
type Ids List<Int>
type Tags Set<String>
type Kvs Map<String, Int>
type Meters Float

`

// A name right after a literal's `#` offers the types built on the indexed
// and product shapes, and after `%` those built on the hashed ones, however
// the request comes.
func TestCompletion_ShapeHeadOffersTheTypesOfItsShapes(t *testing.T) {
	tests := []struct {
		name, src, trigger string
		want, not          []string
	}{
		{"hash typed", "fn f() -> Int {\n    _p = #‸\n    0\n}\n", "#",
			[]string{"Point", "Shape", "Pair", "Path"}, []string{"Tags", "Kvs", "Ids", "Meters", "f"}},
		{"hash with a prefix", "fn f() -> Int {\n    _p = #P‸\n    0\n}\n", "",
			[]string{"Point", "Pair", "Path"}, []string{"Tags", "Kvs"}},
		{"percent typed", "fn f() -> Int {\n    _p = %‸\n    0\n}\n", "%",
			[]string{"Tags", "Kvs"}, []string{"Point", "Shape", "Pair", "Path", "Ids", "Meters", "f"}},
		{"percent as an argument", "fn g(t: Tags) -> Tags {\n    t\n}\n\nfn f() -> Tags {\n    g(%‸)\n}\n", "%",
			[]string{"Tags"}, []string{"Point"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := itemLabels(completeTriggered(t, shapeHeadDecls+tt.src, tt.trigger))
			for _, w := range tt.want {
				if !slices.Contains(labels, w) {
					t.Errorf("missing %s; got %v", w, labels)
				}
			}
			for _, n := range tt.not {
				if slices.Contains(labels, n) {
					t.Errorf("offered %s; got %v", n, labels)
				}
			}
		})
	}
}

// A `%` after an operand is the remainder operator, and its trigger opens
// nothing.
func TestCompletion_RemainderPercentOpensNothing(t *testing.T) {
	src := shapeHeadDecls + "fn f(a: Int) -> Int {\n    a %‸\n}\n"
	if items := completeTriggered(t, src, "%"); len(items) != 0 {
		t.Fatalf("offered %v after a remainder `%%`", itemLabels(items))
	}
}
