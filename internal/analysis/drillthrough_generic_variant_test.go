package analysis_test

import (
	"strings"
	"testing"
)

// A generic enum's variant imported by name (`import shapes.Holder.{Of}`)
// infers its type arguments as the qualified spelling `shapes.Holder.Of{...}`
// does. The constructor's symbol returns the enum over its own parameters
// (`Holder<T>`), and the struct-literal check took those as arguments the
// site supplied, so `Of{value: 1}` reported "field 'value' type mismatch:
// expected T, got Int".
const drillGenericShapes = `pub enum Holder<T> {
    Of {value: T}
    Wrap T
    Empty
}

pub enum Pair<A, B> {
    Both {left: A, right: B}
}

pub fn holder_int(h: Holder<Int>): Int {
    case h {
        .Of{value} -> value
        .Wrap(v) -> v
        .Empty -> 0
    }
}
`

func TestDrillThroughGenericVariant_InfersAsTheQualifiedSpelling(t *testing.T) {
	for _, tc := range []struct{ name, imports, body string }{
		{"a struct variant", "import shapes.Holder.{Of}",
			"    h = Of{value: 1}\n    _ = shapes.holder_int(h)\n"},
		{"a positional variant", "import shapes.Holder.{Wrap}",
			"    h = Wrap(2)\n    _ = shapes.holder_int(h)\n"},
		{"two type parameters", "import shapes.Pair.{Both}",
			"    p: shapes.Pair<Int, String> = Both{left: 1, right: \"a\"}\n    q = Both{left: \"b\", right: 2}\n    _ = case q {\n        .Both{left, right} -> String.length(left) + right\n    }\n    _ = p\n"},
		{"an aliased struct variant", "import shapes.Holder.{Of as O}",
			"    h = O{value: 3}\n    _ = shapes.holder_int(h)\n"},
		{"a struct variant in a pattern", "import shapes.Holder.{Of}",
			"    h = Of{value: 4}\n    _ = case h {\n        Of{value} -> value + 1\n        _ -> 0\n    }\n"},
		{"an aliased struct variant in a pattern", "import shapes.Holder.{Of as O}",
			"    h = O{value: 4}\n    _ = case h {\n        O{value} -> value + 1\n        _ -> 0\n    }\n"},
		{"the expected type of an argument", "import shapes.Holder.{Of}",
			"    _ = shapes.holder_int(Of{value: 6})\n"},
		{"the expected type of an annotation", "import shapes.Holder.{Of}",
			"    h: shapes.Holder<Int> = Of{value: 7}\n    _ = shapes.holder_int(h)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import shapes\n" + tc.imports + "\n\nfn main() {\n" + tc.body + "}\n"
			_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": drillGenericShapes})
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

// The arguments an annotation supplies still win over the field: a value of
// the wrong type is reported against the instantiated field type, not `T`.
func TestDrillThroughGenericVariant_AnnotationSeedsTheArguments(t *testing.T) {
	src := "import shapes\nimport shapes.Holder.{Of}\n\nfn main() {\n    h: shapes.Holder<String> = Of{value: 1}\n    _ = h\n}\n"
	_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": drillGenericShapes})
	want := "field 'value' type mismatch: expected String, got Int"
	for _, e := range errs {
		if e.Message == want {
			return
		}
	}
	t.Fatalf("want %q, got %v", want, errs)
}

// A variant of this file's own enum written bare in a struct pattern stays
// rejected; only a drill-through import binds the bare name.
func TestDrillThroughGenericVariant_SameFileBareStructPatternIsRejected(t *testing.T) {
	src := "enum Holder<T> {\n    Of {value: T}\n}\n\nfn main() {\n    h = Holder.Of{value: 1}\n    _ = case h {\n        Of{value} -> value\n    }\n}\n"
	_, errs := checkProjectWithFiles(t, src, nil)
	want := "bare variant 'Of' in pattern position; use '.Of' (or 'Holder.Of')"
	for _, e := range errs {
		if e.Message == want {
			return
		}
	}
	t.Fatalf("want %q, got %v", want, errs)
}
