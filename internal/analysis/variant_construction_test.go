package analysis

import (
	"strings"
	"testing"
)

// Construction through a struct-shaped or embedded variant: `Enum.V` takes
// the construction forms of V's shape. See variant_construction.go.

const variantCtorDecls = `struct Circle {
  radius: Float = 3.14
}

type Id Int

type Expired

enum Shape {
  embeds Circle
  embeds Id
  embeds Expired
  Rectangle {width: Float = 2.5, height: Float}
  Dot Float
}

fn record(): {height: Float} {
  {height: 7.0}
}

fn round(): {radius: Float} {
  {radius: 4.0}
}

`

func TestVariantCtor_AcceptedForms(t *testing.T) {
	for _, expr := range []string{
		// Embedded struct: the struct's brace form and record call form.
		`Shape.Circle{radius: 1.0}`,
		`Shape.Circle({radius: 2.0})`,
		`Shape.Circle({})`,
		`Shape.Circle(round())`,
		`{radius: 2.5} |> Shape.Circle()`,
		`round() |> Shape.Circle()`,
		// Inline struct variant: the same two forms, defaults filled.
		`Shape.Rectangle{height: 1.0}`,
		`Shape.Rectangle({width: 4.0, height: 5.0})`,
		`Shape.Rectangle({height: 5.0})`,
		`Shape.Rectangle(record())`,
		`{width: 1.0, height: 3.0} |> Shape.Rectangle()`,
		`record() |> Shape.Rectangle()`,
		`.Rectangle({height: 1.0})`,
		// Embedded distinct: the distinct's call form, from its inner value.
		`Shape.Id(5)`,
		`5 |> Shape.Id()`,
		// Embedded marker and positional variant are unchanged.
		`Shape.Expired`,
		`Shape.Dot(1.0)`,
	} {
		src := variantCtorDecls + "fn demo(): Shape {\n  " + expr + "\n}\n"
		_, errs := checkSource(src)
		if len(errs) != 0 {
			t.Errorf("%s: want no errors, got %v", expr, errs)
		}
	}
}

func TestVariantCtor_RejectedFormsNameTheWorkingOnes(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want string
	}{
		{`Shape.Circle(Circle{radius: 1.0})`,
			"Shape.Circle constructs a Circle with Shape.Circle{...} or Shape.Circle({...}); this argument is already a Circle — use it where a Shape is expected and it widens"},
		{`Circle{radius: 1.0} |> Shape.Circle()`,
			"this argument is already a Circle — use it where a Shape is expected and it widens"},
		{`Shape.Id(Id(5))`,
			"Shape.Id constructs an Id with Shape.Id(<Int>); this argument is already an Id — use it where a Shape is expected and it widens"},
		{`Id(5) |> Shape.Id()`,
			"this argument is already an Id"},
		{`Shape.Expired()`,
			"Shape.Expired is a zero-sized variant and takes no arguments; write Shape.Expired"},
		{`Shape.Rectangle(4.0, 5.0)`,
			"Shape.Rectangle is a struct-shaped variant and has no positional form; construct it with Shape.Rectangle{...} or Shape.Rectangle({...})"},
		{`Shape.Rectangle(4.0)`,
			"Shape.Rectangle expects an anonymous struct literal {...} matching its fields, got Float"},
		{`Shape.Circle(5)`,
			"Shape.Circle expects an anonymous struct literal {...} matching its fields, got Int"},
		{`Shape.Rectangle({})`,
			"missing field 'height' of Shape.Rectangle"},
		{`Shape.Rectangle({height: "x"})`,
			"field 'height' type mismatch: expected Float, got String"},
		{`Shape.Circle({radius: "x"})`,
			"field 'radius' type mismatch: expected Float, got String"},
		{`Shape.Id("x")`,
			"Shape.Id builds an Id, which wraps Int, so Shape.Id(...) takes an Int; got String"},
		{`"x" |> Shape.Id()`,
			"Shape.Id builds an Id, which wraps Int, so Shape.Id(...) takes an Int; got String"},
		{`{height: "x"} |> Shape.Rectangle()`,
			"field 'height' of Shape.Rectangle: expected Float, got String"},
	} {
		src := variantCtorDecls + "fn demo(): Shape {\n  " + tc.expr + "\n}\n"
		_, errs := checkSource(src)
		found := false
		var got []string
		for _, e := range errs {
			got = append(got, e.Message)
			if strings.Contains(e.Message, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want an error containing %q, got %v", tc.expr, tc.want, got)
		}
	}
}

// In a generic enum, constructing an embedded or struct-shaped variant
// leaves the enum's parameters open, as an ordinary variant's constructor
// does: each form below fits a `Shape<Int>` parameter. They were typed
// `Shape<T>` and rejected ("argument 1: expected Shape<Int>, got Shape<T>"),
// and the piped record form typed its record as the Circle and called it
// already built.
func TestVariantCtor_GenericEnumConstructionSolvesItsParameter(t *testing.T) {
	decls := `type UserId Int

struct Circle {
  r: Int
}

enum Shape<T> {
  embeds UserId
  embeds Circle
  Val(T)
  Rect {w: Int}
}

fn first(s: Shape<Int>): Shape<Int> {
  s
}

`
	for _, expr := range []string{
		`Shape.UserId(3)`,
		`3 |> Shape.UserId()`,
		`Shape.Circle({r: 1})`,
		`{r: 1} |> Shape.Circle()`,
		`Shape.Rect({w: 1})`,
		`{w: 1} |> Shape.Rect()`,
		`Shape.Val(1)`,
	} {
		src := decls + "fn demo(): Shape<Int> {\n  first(" + expr + ")\n}\n"
		_, errs := checkSource(src)
		if len(errs) != 0 {
			t.Errorf("%s: want no errors, got %v", expr, errs)
		}
	}
	// The open parameter is solved once: a construction that fits a
	// Shape<Int> does not also fit a Shape<String>.
	src := decls + "fn demo(): Shape<String> {\n  s = Shape.UserId(3)\n  _ = first(s)\n  s\n}\n"
	_, errs := checkSource(src)
	var got []string
	for _, e := range errs {
		got = append(got, e.Message)
	}
	if !strings.Contains(strings.Join(got, "\n"), "Shape<String>") {
		t.Errorf("want a mismatch naming Shape<String>, got %v", got)
	}
}
