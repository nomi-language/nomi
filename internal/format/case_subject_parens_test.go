package format

import "testing"

// A case's subject is parsed with struct literals allowed, so a type name,
// a dot variant or a path of type names straight before the `{` opening
// the arms would start a literal: `case Shape {` reads `Shape { ... }`.
// Parenthesized there, it keeps its parentheses, also at the end of a
// longer subject. A path whose receiver had them gets them around the
// whole path.
func TestFormat_CaseSubjectEndingInATypeNameKeepsParens(t *testing.T) {
	arms := " {\n        _ -> 0\n    }\n}\n"
	for _, c := range []struct{ src, want string }{
		// The fuzz find.
		{"(Tos_call)", "(Tos_call)"},
		{"((Shape))", "(Shape)"},
		{"(.Neg)", "(.Neg)"},
		{"(Shape.Circle)", "(Shape.Circle)"},
		{"(shapes.Circle)", "(shapes.Circle)"},
		{"(shapes.Shape.Circle)", "(shapes.Shape.Circle)"},
		{"(Shape).Circle", "(Shape.Circle)"},
		{"(shapes).Circle", "(shapes.Circle)"},
		{"(shapes.Shape).Circle", "(shapes.Shape.Circle)"},
		{"1 + (Shape)", "1 + (Shape)"},
		{"-(Shape)", "-(Shape)"},
		{"try (Shape)", "try (Shape)"},
		{"x |> (Shape)", "x |> (Shape)"},
		// Elsewhere the parentheses go: a `{` does not join these.
		{"(x)", "x"},
		{"(f(x))", "f(x)"},
		{"(shapes.circle)", "shapes.circle"},
		{"(shapes.circle).Radius", "shapes.circle.Radius"},
		{"(Shape.circle).Radius", "Shape.circle.Radius"},
		{"(f(x)).Radius", "f(x).Radius"},
		{"(Shape) + 1", "Shape + 1"},
	} {
		formatsKeepingMeaning(t,
			"fn main() {\n    a = case "+c.src+arms,
			"fn main() {\n    a = case "+c.want+arms)
	}
}

// An `if` condition is parsed with struct literals off, so a type name
// before its `{` loses its parentheses.
func TestFormat_IfConditionTypeNameLosesParens(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn main() {\n    a = if (Flag) { 1 } else { 2 }\n}\n",
		"fn main() {\n    a = if Flag { 1 } else { 2 }\n}\n")
}
