package analysis_test

import (
	"strings"
	"testing"
)

// A generic enum's list- and map-payload variants written in literal-attach
// form (`shapes.Bag.Items[1, 2]`, `Named{"a" => 4}`) infer the enum's type
// arguments from the elements, as the call form `Items([1, 2])` and the
// struct-variant literal do. The literal checked each element against the
// declared `T` and reported "shapes.Bag.Items element type: expected T, got
// Int" in every spelling.
const attachedGenericShapes = `pub enum Bag<T> {
    Items List<T>
    Named Map<String, T>
    Nothing
}

pub fn count(b: Bag<Int>): Int {
    case b {
        .Items(xs) -> Iter.count(xs)
        .Named(m) -> Iter.count(m)
        .Nothing -> 0
    }
}
`

func TestAttachedGenericVariant_InfersInEverySpelling(t *testing.T) {
	for _, tc := range []struct{ name, imports, body string }{
		{"a qualified list variant", "",
			"    b = shapes.Bag.Items[1, 2]\n    _ = shapes.count(b)\n"},
		{"a qualified map variant", "",
			"    b = shapes.Bag.Named{\"a\" => 4}\n    _ = shapes.count(b)\n"},
		{"a bare list variant", "import shapes.Bag.{Items}",
			"    b = Items[1, 2]\n    _ = shapes.count(b)\n"},
		{"a bare map variant", "import shapes.Bag.{Named}",
			"    b = Named{\"a\" => 4}\n    _ = shapes.count(b)\n"},
		{"aliased variants", "import shapes.Bag.{Items as I, Named as N}",
			"    _ = shapes.count(I[1])\n    _ = shapes.count(N{\"a\" => 2})\n"},
		{"the expected type of an annotation", "import shapes.Bag.{Items}",
			"    b: shapes.Bag<Int> = Items[1]\n    _ = shapes.count(b)\n"},
		{"the expected type of an argument", "import shapes.Bag.{Items, Named}",
			"    _ = shapes.count(Items[1])\n    _ = shapes.count(Named{\"a\" => 1})\n"},
		{"an empty literal under an annotation", "import shapes.Bag.{Items, Named}",
			"    b: shapes.Bag<Int> = Items[]\n    c: shapes.Bag<String> = shapes.Bag.Named{}\n    _ = shapes.count(b)\n    _ = c\n"},
		{"an empty literal as an argument", "import shapes.Bag.{Items}",
			"    _ = shapes.count(Items[])\n"},
		{"nested generics", "import shapes.Bag.{Items, Named}",
			"    b = Items[[1], [2, 3]]\n    c = Named{\"a\" => Items[\"x\"]}\n    _ = case b {\n        Items[xs, .._] -> Iter.count(xs)\n        _ -> 0\n    }\n    _ = c\n"},
		{"bare list and map patterns", "import shapes.Bag.{Items, Named}",
			"    b = Items[1, 2]\n    _ = case b {\n        Items[x, y] -> x + y\n        Named{\"k\" => v} -> v\n        _ -> 0\n    }\n"},
		{"aliased list and map patterns", "import shapes.Bag.{Items as I, Named as N}",
			"    b = N{\"k\" => \"v\"}\n    _ = case b {\n        I[s] -> s\n        N{\"k\" => v} -> v\n        _ -> \"\"\n    }\n"},
		{"qualified patterns bind the scrutinee's arguments", "",
			"    b = shapes.Bag.Items[1, 2]\n    _ = case b {\n        shapes.Bag.Items[x, y] -> x + y\n        shapes.Bag.Named{\"k\" => v} -> v\n        _ -> 0\n    }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import shapes\n" + tc.imports + "\n\nfn main() {\n" + tc.body + "}\n"
			_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": attachedGenericShapes})
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

// The forms that stay wrong still fail, each with its own message.
func TestAttachedGenericVariant_Rejections(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"mixed elements are checked against the first",
			"import shapes\nimport shapes.Bag.{Items}\n\nfn main() {\n    b = Items[1, \"a\"]\n    _ = b\n}\n",
			"Items element type: expected Int, got String"},
		{"an annotation seeds the arguments",
			"import shapes\n\nfn main() {\n    b: shapes.Bag<String> = shapes.Bag.Items[1]\n    _ = b\n}\n",
			"shapes.Bag.Items element type: expected String, got Int"},
		{"an annotation seeds a map value",
			"import shapes\nimport shapes.Bag.{Named}\n\nfn main() {\n    b: shapes.Bag<String> = Named{\"a\" => 1}\n    _ = b\n}\n",
			"Named value type: expected String, got Int"},
		{"an empty literal with nothing to solve it",
			"import shapes\nimport shapes.Bag.{Items}\n\nfn main() {\n    b = Items[]\n    _ = b\n}\n",
			"binding 'b' type is not locally determined: it has unsolved type parameter(s) — add an annotation (`b: T = …`) or a type argument (`f<T>(…)`)"},
		{"a bare same-file list variant in a pattern",
			"enum Bag<T> {\n    Items List<T>\n    Nothing\n}\n\nfn main() {\n    b = Bag.Items[1]\n    _ = case b {\n        Items[x] -> x\n        _ -> 0\n    }\n}\n",
			"bare variant 'Items' in pattern position; use '.Items' (or 'Bag.Items')"},
		{"a bare same-file map variant in a pattern",
			"enum Bag<T> {\n    Named Map<String, T>\n    Nothing\n}\n\nfn main() {\n    b = Bag.Named{\"k\" => 1}\n    _ = case b {\n        Named{\"k\" => v} -> v\n        _ -> 0\n    }\n}\n",
			"bare variant 'Named' in pattern position; use '.Named' (or 'Bag.Named')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkProjectWithFiles(t, tc.src, map[string]string{"shapes.nomi": attachedGenericShapes})
			for _, e := range errs {
				if e.Message == tc.want {
					return
				}
			}
			t.Fatalf("want %q, got %v", tc.want, errs)
		})
	}
}

// A selectively imported variant used only in literal-attach form, or only
// as a list or map pattern's prefix, is a use of its import.
func TestAttachedGenericVariant_LiteralIsAUseOfTheImport(t *testing.T) {
	for _, src := range []string{
		"import shapes\nimport shapes.Bag.{Items, Named}\n\nfn main() {\n    _ = shapes.count(Items[1])\n    _ = shapes.count(Named{\"a\" => 1})\n}\n",
		"import shapes\nimport shapes.Bag.{Items, Named}\n\nfn f(b: shapes.Bag<Int>): Int {\n    case b {\n        Items[x] -> x\n        Named{\"k\" => v} -> v\n        _ -> 0\n    }\n}\n\nfn main() {\n    _ = f(shapes.Bag.Nothing)\n}\n",
	} {
		_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": attachedGenericShapes})
		for _, e := range errs {
			if strings.Contains(e.Message, "is unused") {
				t.Fatalf("unexpected %q in\n%s", e.Message, src)
			}
		}
		if len(errs) > 0 {
			t.Fatalf("expected no errors, got %v\nsource:\n%s", errs, src)
		}
	}
}
