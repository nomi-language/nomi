package analysis_test

import (
	"strings"
	"testing"
)

// A prelude variant spelled bare is a value of its enum, so a `.` after it
// is a field access on that value and fails as one. The checker used to
// read `None` as a type qualifier, find no type named `None`, and return no
// type and no error, leaving the IR builder to decline the program. A `.`
// at a line end continues the expression onto the next line, which is how
// the fuzzer reached it: `None.` above `Task.await(p1)`.
func TestFieldAccessOnBareVariant_Rejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"nullary prelude variant", `fn f() {
  x = None.foo
  x
}`, "enum 'Maybe' has no field 'foo' (match on its variants instead)"},
		{"dot at a line end continues onto the next line", `fn f() {
  None.
    Task
}`, "enum 'Maybe' has no field 'Task' (match on its variants instead)"},
		{"variant constructor", `fn f() {
  x = Some.foo
  x
}`, "has no field 'foo'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(tc.src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts this; want an error containing %q", tc.want)
			}
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					return
				}
			}
			t.Fatalf("errors %v, want one containing %q", errs, tc.want)
		})
	}
}

// A field read off an enum value takes the type every variant that has the
// field gives it. Variants that disagree leave the read no one type, so it
// is rejected at the field; variants that agree read as that type.
func TestEnumFieldRead_VariantsMustAgreeOnItsType(t *testing.T) {
	const decl = `enum Shape {
    Rect {w: Int, label: String}
    Circle {w: Float, label: String}
    Dot
}

`
	errs := checkWithStdlib(decl + `fn f(s: Shape): String {
    s.label
}
`)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a field every variant types alike: %v", errs)
	}
	errs = checkWithStdlib(decl + `fn f(s: Shape): Int {
    s.w
}
`)
	want := "field 'w' is Int in Shape.Rect and Float in Shape.Circle, so reading it off a Shape has no one type"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("want an error containing %q, got %v", want, errs)
}
