package analysis_test

import "testing"

// A variant declared inside an enum is not a type (spec §4 *Variants as
// Parameter Types*). Writing one where a type goes, bare or qualified, is
// refused with a hint that names the enum, and for a program's own enum how
// to declare the variant as a type of its own. An embedded variant's type is
// the embedded type, which the hint names.
func TestVariantAsTypeIsRefusedWithAHint(t *testing.T) {
	decls := `
struct Circle
  radius: Float
end

enum Shape
  embeds Circle
  Rectangle {width: Float, height: Float}
  Point
end
`
	inline := "`Rectangle` is a variant of `Shape`, not a type: take a `Shape`, or declare `Rectangle` on its own and write `embeds Rectangle` in `Shape`"
	for _, tc := range []struct{ typ, msg, help string }{
		{"Shape.Rectangle", `unknown type "Shape.Rectangle"`, inline},
		{"Rectangle", `unknown type "Rectangle"`, inline},
		{"Shape.Circle", `unknown type "Shape.Circle"`, "write `Circle`, the embedded variant's own type"},
		{"Some", `unknown type "Some"`, "`Some` is a variant of `Maybe`, not a type: take a `Maybe`"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "\nfn f(_: " + tc.typ + ") -> Int\n  1\nend\n")
			for _, e := range errs {
				if e.Message != tc.msg {
					continue
				}
				if len(e.Hints) != 1 || e.Hints[0] != tc.help {
					t.Fatalf("hints = %q, want [%q]", e.Hints, tc.help)
				}
				return
			}
			t.Fatalf("no %s error; the front end accepts the variant as a type: %v", tc.msg, errs)
		})
	}
}
