package analysis_test

import (
	"slices"
	"testing"
)

// A struct literal whose head names a type that is not a struct is an error
// at the head. Each of these used to pass the checker with no diagnostic,
// and `Context{}` was then reported as not supported yet when it ran.
func TestNonStructLiteral_IsRejected(t *testing.T) {
	cases := []struct{ name, src, want, hint string }{
		{"host type", "fn main() {\n    c = Context{}\n    _ = c\n}\n",
			"`Context` is a host type, not a struct, so a struct literal cannot build it",
			"`Context` has no fields to fill in; its values come from its own functions or literals"},
		{"enum", "enum Shape {\n    Dot\n}\n\nfn main() {\n    s = Shape{}\n    _ = s\n}\n",
			"`Shape` is an enum, not a struct, so a struct literal cannot build it",
			"build one of its variants, as `Shape.Variant{...}` or `Shape.Variant(...)`"},
		{"distinct type", "type Meters Int\n\nfn main() {\n    m = Meters{}\n    _ = m\n}\n",
			"`Meters` is a distinct type, not a struct, so a struct literal cannot build it",
			"build one with `Meters(...)`"},
		{"interface", "interface I {\n    fn f(x: self) -> Int\n}\n\nfn main() {\n    _a = I{v: 1}\n}\n",
			"`I` is an interface, not a struct, so a struct literal cannot build it", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
				if e.Message == tc.want {
					if tc.hint != "" && !slices.Contains(e.Hints, tc.hint) {
						t.Errorf("hints %q; want %q", e.Hints, tc.hint)
					}
					return
				}
			}
			t.Errorf("the checker admits this source or reports something else; want %q, got %v\n%s",
				tc.want, got, tc.src)
		})
	}
}
