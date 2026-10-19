package analysis_test

import (
	"strings"
	"testing"
)

// A variant over a tuple has one payload, and `Two.Both(1, 2)` is a call
// form of that one tuple (spec §8). The flat form of a generic variant
// infers its type arguments from the elements, as the tupled
// `Two.Both(#(1, 2))` does. It was checked against the declaration's own
// `A` and `B`, and then a second time as a one-argument call, so
// `t: Two<Int, Int> = Two.Both(1, 2)` gave three errors, two at column 1.
const flatVariantShapes = `pub enum Two<A, B> {
    Both #(A, B)
}

pub enum Three<A, B, C> {
    All #(A, B, C)
}

pub enum Tagged<T> {
    Tag #(T, String)
}

pub enum Seg {
    Pair #(Int, String)
}

pub fn first(t: Two<Int, String>) -> Int {
    case t {
        .Both(n, _) -> n
    }
}
`

func TestFlatVariantCall_InfersAsTheTupledForm(t *testing.T) {
	for _, tc := range []struct{ name, imports, body string }{
		{"no expected type", "",
			"    t = shapes.Two.Both(1, \"x\")\n    _ = shapes.first(t)\n"},
		{"an annotation", "",
			"    t: shapes.Two<Int, Int> = shapes.Two.Both(1, 2)\n    _ = t\n"},
		{"a dot variant under an annotation", "",
			"    t: shapes.Two<Int, Int> = .Both(1, 2)\n    _ = t\n"},
		{"the expected type of an argument", "",
			"    _ = shapes.first(.Both(1, \"x\"))\n"},
		{"an imported variant", "import shapes.Two.{Both}",
			"    t = Both(1, \"x\")\n    _ = shapes.first(t)\n"},
		{"an aliased variant", "import shapes.Two.{Both as B2}",
			"    t = B2(1, \"x\")\n    _ = shapes.first(t)\n"},
		{"three elements", "",
			"    t = shapes.Three.All(1, \"y\", True)\n    _ = case t {\n        .All(n, s, b) -> if b { n + String.length(s) } else { 0 }\n    }\n"},
		{"a type parameter beside a concrete type", "",
			"    t = shapes.Tagged.Tag(7, \"seven\")\n    _ = case t {\n        .Tag(n, s) -> n + String.length(s)\n    }\n"},
		{"nested generics", "",
			"    t = shapes.Two.Both(shapes.Two.Both(1, \"x\"), Maybe.Some(\"z\"))\n    _ = case t {\n        .Both(inner, _) -> shapes.first(inner)\n    }\n"},
		{"a pipe", "",
			"    t = 9 |> shapes.Two.Both(\"nine\")\n    _ = shapes.first(t)\n"},
		{"a pipe into a placeholder", "",
			"    t = \"e\" |> then &shapes.Two.Both(5, &1)\n    _ = shapes.first(t)\n"},
		{"a partial", "",
			"    f = &shapes.Two.Both(&1, \"c\")\n    _ = shapes.first(f(3))\n"},
		{"a partial under an annotation", "",
			"    f: (String) -> shapes.Two<Int, String> = &shapes.Two.Both(4, &1)\n    _ = shapes.first(f(\"d\"))\n"},
		{"a non-generic partial", "",
			"    f = &shapes.Seg.Pair(&1, \"c\")\n    _ = f(3)\n"},
		{"a non-generic pipe", "",
			"    _ = 2 |> shapes.Seg.Pair(\"b\")\n"},
		{"a non-generic dot variant", "",
			"    s: shapes.Seg = .Pair(1, \"a\")\n    _ = s\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import shapes\n" + tc.imports + "\n\nfn main() {\n" + tc.body + "}\n"
			_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": flatVariantShapes})
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				t.Fatalf("expected no errors, got:\n  %s\nsource:\n%s", strings.Join(msgs, "\n  "), src)
			}
		})
	}
}

// A wrong element is one error, at the element, worded against the type the
// annotation gives that element.
func TestFlatVariantCall_ReportsTheWrongElement(t *testing.T) {
	for _, tc := range []struct {
		name, line, msg string
		col             int
	}{
		{"qualified", "    t: shapes.Two<Int, Int> = shapes.Two.Both(1, \"x\")", "argument 2: expected Int, got String", 50},
		{"a dot variant", "    t: shapes.Two<Int, Int> = .Both(\"x\", 2)", "argument 1: expected Int, got String", 37},
		{"non-generic", "    t = shapes.Seg.Pair(1, 2)", "argument 2: expected String, got Int", 28},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import shapes\n\nfn main() {\n" + tc.line + "\n    _ = t\n}\n"
			_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": flatVariantShapes})
			if len(errs) != 1 || errs[0].Message != tc.msg || errs[0].Line != 4 || errs[0].Col != tc.col {
				t.Fatalf("want one error %q at 4:%d, got %v", tc.msg, tc.col, errs)
			}
		})
	}
}
