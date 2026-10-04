package analysis

import (
	"strings"
	"testing"
)

// Construction of a distinct through its call form checks the argument
// against the wrapped type, and a zero-sized type has no call form. See
// distinct_construction.go. Each of these was accepted by the analyzer and
// failed or misbehaved at run time: `Id("x")` built an Id holding a String,
// and `Expired()` failed with `cannot call Expired`.

const distinctCtorDecls = `type Id Int

type Expired

type Pair (Int, String)

type Callback (String) -> String

type Tags List<String>

enum Shape {
  embeds Id
  embeds Expired
  Dot
}

`

func TestDistinctCtor_AcceptedForms(t *testing.T) {
	for _, stmt := range []string{
		`a: Id = Id(5)`,
		`a: Id = 5 |> Id()`,
		`a: Expired = Expired`,
		`a: Pair = Pair(1, "x")`,
		`a: Pair = Pair((1, "x"))`,
		`a: Callback = Callback(|s| s)`,
		`a: Tags = Tags(["a"])`,
		`a: Shape = Shape.Id(5)`,
		`a: Shape = 5 |> Shape.Id()`,
		`a: Shape = Shape.Expired`,
	} {
		src := distinctCtorDecls + "fn main() {\n  " + stmt + "\n  _ = a\n}\n"
		if _, errs := checkSource(src); len(errs) != 0 {
			t.Errorf("%s: want no errors, got %v", stmt, errs)
		}
	}
}

func TestDistinctCtor_RejectionsNameTheMistakeAndTheForm(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want string
	}{
		{`Id("x")`, "Id wraps Int, so Id(...) takes an Int; got String"},
		{`"x" |> Id()`, "Id wraps Int, so Id(...) takes an Int; got String"},
		{`Id(1, 2)`, "Id wraps Int, so Id(...) takes one argument, an Int; got 2"},
		{`Shape.Id("x")`, "Shape.Id builds an Id, which wraps Int, so Shape.Id(...) takes an Int; got String"},
		{`"x" |> Shape.Id()`, "Shape.Id builds an Id, which wraps Int, so Shape.Id(...) takes an Int; got String"},
		{`Expired()`, "Expired is a zero-sized type and takes no arguments; write Expired"},
		{`5 |> Expired()`, "Expired is a zero-sized type and takes no arguments; write Expired"},
		{`Shape.Expired()`, "Shape.Expired is a zero-sized variant and takes no arguments; write Shape.Expired"},
		{`Tags(5)`, "Tags wraps List<String>, so Tags(...) takes a List<String>; got Int"},
	} {
		src := distinctCtorDecls + "fn main() {\n  _ = " + tc.expr + "\n}\n"
		_, errs := checkSource(src)
		var got []string
		found := false
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

// A struct-shaped variant, inline or embedded, is not a function value: a
// struct has none, and `Shape.Rect` taken as one was typed `(Float, Float) ->
// Shape` and then failed when called, because its only call form takes a
// record. A positional variant and an embedded distinct stay first-class.
func TestStructVariantValue_IsRejected(t *testing.T) {
	const decls = `struct Circle {
  radius: Float
}

type Id Int

enum Shape {
  embeds Circle
  embeds Id
  Rect {w: Float, h: Float}
  Dot Float
}

fn apply(f: (Float) -> Shape): Shape {
  f(1.0)
}

fn apply_id(f: (Int) -> Shape): Shape {
  f(1)
}

fn build(f: ({w: Float, h: Float}) -> Shape): Shape {
  f({w: 1.0, h: 2.0})
}

`
	for _, tc := range []struct {
		stmt string
		want string
	}{
		{`f = Shape.Rect`, "Shape.Rect is a struct-shaped variant and is not a function value; build it with Shape.Rect{...} or Shape.Rect({...}), inside a lambda where a function is needed"},
		{`f = Shape.Circle`, "Shape.Circle is a struct-shaped variant and is not a function value"},
		{`f = build(Shape.Rect)`, "Shape.Rect is a struct-shaped variant and is not a function value"},
	} {
		src := decls + "fn main() {\n  " + tc.stmt + "\n  _ = f\n}\n"
		_, errs := checkSource(src)
		var got []string
		found := false
		for _, e := range errs {
			got = append(got, e.Message)
			if strings.Contains(e.Message, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want an error containing %q, got %v", tc.stmt, tc.want, got)
		}
	}
	for _, stmt := range []string{
		`s = apply(Shape.Dot)`,
		`s = Shape.Rect({w: 1.0, h: 2.0})`,
		`s = {w: 1.0, h: 2.0} |> Shape.Rect()`,
		`s = Shape.Rect{w: 1.0, h: 2.0}`,
		`s = Shape.Circle({radius: 1.0})`,
		`s = apply_id(Shape.Id)`,
	} {
		src := decls + "fn main() {\n  " + stmt + "\n  _ = s\n}\n"
		if _, errs := checkSource(src); len(errs) != 0 {
			t.Errorf("%s: want no errors, got %v", stmt, errs)
		}
	}
}
