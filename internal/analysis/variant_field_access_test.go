package analysis_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
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
		{"nullary prelude variant", `fn f()
  x = None.foo
  x
end`, "enum 'Option' has no field 'foo' (match on its variants instead)"},
		{"dot at a line end continues onto the next line", `fn f()
  None.
    Task
end`, "enum 'Option' has no field 'Task' (match on its variants instead)"},
		{"variant constructor", `fn f()
  x = Some.foo
  x
end`, "has no field 'foo'"},
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

// A field read off an enum value reads whichever variant the value is, so
// every variant must have the field, and at one type. Each form of the read
// (`s.w`, the accessor `.w`, a read through a typealias, a generic enum at
// an instance) is held to the same rule, which enumFieldAgreed decides.

// enumFieldDecls declares an enum whose variants hold `w` in each shape a
// field can take: a struct-shaped variant, an embedded struct and a
// positional struct payload.
const enumFieldDecls = `struct Box
    w: Float
end

struct Wrapped
    w: Float
    h: Float
end

enum Shape
    Rect {w: Float, h: Float}
    embeds Box
    Wrap Wrapped
end

typealias Figure Shape

fn apply(s: Shape, f: (Shape) -> Float) -> Float
    f(s)
end

enum Pair<T>
    One {x: T}
    Two {x: T, y: T}
end

`

func TestEnumFieldRead_EveryVariantHasTheField(t *testing.T) {
	for name, body := range map[string]string{
		"a field read":               "fn f(s: Shape) -> Float\n    s.w\nend\n",
		"an accessor":                "fn f(s: Shape) -> Float\n    apply(s, .w)\nend\n",
		"a read through a typealias": "fn f(s: Figure) -> Float\n    s.w\nend\n",
		"a generic enum's instance":  "fn f(p: Pair<Int>) -> Int\n    p.x\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			if errs := checkWithStdlib(enumFieldDecls + body); len(errs) != 0 {
				t.Fatalf("the front end rejects a field every variant has: %v", errs)
			}
		})
	}
}

func TestEnumFieldRead_SomeVariantLacksTheField(t *testing.T) {
	declLines := strings.Count(enumFieldDecls, "\n")
	const help = "match the value with `case` to read a field only some variants have"
	for _, tc := range []struct {
		name, body, want string
		col              int
		args             []string
	}{
		{"a field read", "fn f(s: Shape) -> Float\n    s.h\nend\n",
			"field 'h' is not in every variant of Shape: Box has none", 7, []string{"h", "Box"}},
		{"an accessor", "fn f(s: Shape) -> Float\n    apply(s, .h)\nend\n",
			"field 'h' is not in every variant of Shape: Box has none", 15, []string{"h", "Box"}},
		{"a read through a typealias", "fn f(s: Figure) -> Float\n    s.h\nend\n",
			"field 'h' is not in every variant of Shape: Box has none", 7, []string{"h", "Box"}},
		{"a generic enum's instance", "fn f(p: Pair<Int>) -> Int\n    p.y\nend\n",
			"field 'y' is not in every variant of Pair: One has none", 7, []string{"y", "One"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(enumFieldDecls + tc.body)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts a field some variant lacks; want %q", tc.want)
			}
			for _, e := range errs {
				if e.Message != tc.want {
					continue
				}
				if e.Code != analysis.EnumFieldNotEverywhereCode || !slices.Equal(e.Args, tc.args) {
					t.Errorf("code %q args %q, want %q %q", e.Code, e.Args, analysis.EnumFieldNotEverywhereCode, tc.args)
				}
				if e.Line != declLines+2 || e.Col != tc.col {
					t.Errorf("at %d:%d, want %d:%d, the field", e.Line, e.Col, declLines+2, tc.col)
				}
				if !slices.Contains(e.Hints, help) {
					t.Errorf("help %q, want %q", e.Hints, help)
				}
				return
			}
			t.Fatalf("errors %v, want %q", errs, tc.want)
		})
	}
}

// A variant lacks the field whatever its payload: a struct without the
// name, positional or struct-shaped, a scalar, an embedded distinct, or
// none. Past three such variants the message names three and counts the
// rest; every lacking variant is in the error's arguments.
func TestEnumFieldRead_ManyLackingVariantsAreCounted(t *testing.T) {
	errs := checkWithStdlib(`struct Circle
    radius: Float
end

type Marker Int

enum Shape
    Rect {height: Int}
    Wrap Circle
    Round {radius: Float}
    Num Int
    embeds Marker
    Point
end

fn f(s: Shape) -> Int
    s.height
end
`)
	want := "field 'height' is not in every variant of Shape: Wrap, Round, Num and 2 more have none"
	for _, e := range errs {
		if e.Message == want {
			if args := []string{"height", "Wrap", "Round", "Num", "Marker", "Point"}; !slices.Equal(e.Args, args) {
				t.Errorf("args %q, want %q", e.Args, args)
			}
			return
		}
	}
	t.Fatalf("errors %v, want %q", errs, want)
}

// Variants that disagree on the field's type leave the read no one type.
// That error is the one reported when some variant also lacks the field,
// since giving the field to the lacking variant leaves the disagreement.
func TestEnumFieldRead_VariantsMustAgreeOnItsType(t *testing.T) {
	errs := checkWithStdlib(`enum Shape
    Rect {w: Int, label: String}
    Circle {w: Float, label: String}
    Dot
end

fn f(s: Shape) -> Int
    s.w
end
`)
	want := "field 'w' is Int in Shape.Rect and Float in Shape.Circle, so reading it off a Shape has no one type"
	found := false
	for _, e := range errs {
		if e.Code == analysis.EnumFieldNotEverywhereCode {
			t.Errorf("a read with no one type also reports %q", e.Message)
		}
		found = found || e.Message == want
	}
	if !found {
		t.Fatalf("want an error %q, got %v", want, errs)
	}
}
