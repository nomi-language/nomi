package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/parser"
)

const positionalDecls = `struct Student {
  name: String
  score: Int
}

struct Opt {
  a: Int
  b: Int = 2
}

enum Shape {
  Rect {w: Float, h: Float}
}

fn apply<A, B>(x: A, f: (A) -> B) -> B {
  f(x)
}

`

// A named struct literal's values with no field names are one error at the
// literal. When they fit the fields in order, it writes the named form and
// carries the fix that names them; otherwise it counts them and names the
// fields. It never suggests building the struct by position.
func TestPositionalStructLit_Errors(t *testing.T) {
	for _, tc := range []struct{ body, want, hint, fixed string }{
		{"  s = apply((\"a\", 1), &Student{&1.first, &1.second})",
			"a struct literal names its fields; write `Student{name: &1.first, score: &1.second}`", "",
			"  s = apply((\"a\", 1), &Student{name: &1.first, score: &1.second})"},
		{"  s = Opt{1}", "a struct literal names its fields; write `Opt{a: 1}`", "", "  s = Opt{a: 1}"},
		{"  s = Shape.Rect{1.0, 2.0}", "a struct literal names its fields; write `Shape.Rect{w: 1.0, h: 2.0}`", "", "  s = Shape.Rect{w: 1.0, h: 2.0}"},
		{"  s = Opt{1, 2, 3}", "a struct literal names its fields; `Opt` has 2 fields, and this literal gives 3 values without names",
			"its fields are `a` and `b` (with a default); write each value as `field: value`", ""},
		{"  s = Student{\"x\"}", "a struct literal names its fields; `Student` has 2 fields, and this literal gives 1 value without names",
			"its fields are `name` and `score`", ""},
		{"  s = Student{name: \"x\", 4}", "a struct literal names its fields; `Student` has 2 fields, and this literal gives 2 values, 1 without a name",
			"its fields are `name` and `score`", ""},
		{"  s = Nope{1}", "a struct literal names its fields; write each value as `field: value` in `Nope{...}`", "", ""},
	} {
		src := positionalDecls + "fn main() {\n" + tc.body + "\n  _ = s\n}\n"
		_, errs := checkSource(src)
		var found *TypeError
		for i := range errs {
			if errs[i].Message == tc.want {
				found = &errs[i]
			}
		}
		if found == nil {
			t.Errorf("%s: no error %q in %v", tc.body, tc.want, errs)
			continue
		}
		if tc.hint != "" && !strings.Contains(strings.Join(found.Hints, "\n"), tc.hint) {
			t.Errorf("%s: hints %q, want %q", tc.body, found.Hints, tc.hint)
		}
		if strings.Contains(found.Message+strings.Join(found.Hints, ""), "position") {
			t.Errorf("%s: the error suggests building by position: %q", tc.body, found.Message)
		}
		if tc.fixed == "" {
			if found.Fix != nil {
				t.Errorf("%s: a fix %+v where the values do not fit the fields", tc.body, found.Fix)
			}
			continue
		}
		if found.Fix == nil {
			t.Errorf("%s: no fix", tc.body)
			continue
		}
		if got := parser.ApplyEdits(src, found.Fix.Edits); !strings.Contains(got, tc.fixed+"\n") {
			t.Errorf("%s: fixed source\n%s\nwant the line %q", tc.body, got, tc.fixed)
		} else if _, errs := checkSource(got); len(withoutUnusedBindingErrors(errs)) != 0 {
			t.Errorf("%s: the fixed source has errors: %v", tc.body, errs)
		}
	}
}
