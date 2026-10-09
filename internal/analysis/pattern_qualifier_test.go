package analysis_test

import (
	"strings"
	"testing"
)

// A qualified variant pattern's qualifier must name the scrutinee's enum.
// The checker looked the variant up by its member name alone, so each
// rejected pattern below was accepted as the variant of that name, and the
// IR builder, which reads the qualifier, declined the `case` ("a `case` enum
// pattern outside retained bare/positional payload tests").
const patternQualifierDecls = `enum Color {
  Red
  Blue
}

enum Mood {
  Blue
  Calm
}

enum Shape {
  Dot(Int)
  Rect {w: Int}
}

struct Point {
  x: Int
}

typealias Label String

typealias Tint Color

`

func TestPatternQualifier_UnknownOrForeignIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a misspelled file-level enum", "  c = Color.Red\n  _ = case c {\n    C6lor.Blue -> 1\n    _ -> 0\n  }\n",
			"unknown type \"C6lor\"\nhelp: did you mean 'Color'?"},
		{"a misspelled block-local enum", "  enum Hue {\n    Warm\n    Cool\n  }\n  h = Hue.Warm\n  _ = case h {\n    Hu3.Cool -> 1\n    _ -> 0\n  }\n",
			"unknown type \"Hu3\"\nhelp: did you mean 'Hue'?"},
		{"a misspelled enum over a payload", "  s = Shape.Dot(1)\n  _ = case s {\n    Shap.Dot(n) -> n\n    _ -> 0\n  }\n",
			"unknown type \"Shap\"\nhelp: did you mean 'Shape'?"},
		{"a misspelled enum over a struct variant", "  s = Shape.Dot(1)\n  _ = case s {\n    Shpe.Rect{w} -> w\n    _ -> 0\n  }\n",
			"unknown type \"Shpe\"\nhelp: did you mean 'Shape'?"},
		// A lower-case qualifier reads as a module. The builder resolved
		// `ror.Rect` as this file's own `Rect` and recorded it at the
		// qualifier, which the check took for a resolved module.
		{"an unknown module over a struct variant", "  s = Shape.Dot(1)\n  _ = case s {\n    ror.Rect{w} -> w\n    _ -> 0\n  }\n",
			"unknown type \"ror\""},
		{"an unknown module over a payload", "  s = Shape.Dot(1)\n  _ = case s {\n    ror.Dot(n) -> n\n    _ -> 0\n  }\n",
			"unknown type \"ror\""},
		{"a misspelled prelude enum", "  m = Some(1)\n  _ = case m {\n    Mabye.Some(n) -> n\n    _ -> 0\n  }\n",
			"unknown type \"Mabye\"\nhelp: did you mean 'Maybe'?"},
		{"another enum with the variant", "  c = Color.Red\n  _ = case c {\n    Mood.Blue -> 1\n    _ -> 0\n  }\n",
			"pattern `Mood.Blue` is qualified by enum Mood, but the value matched is a Color\nhelp: write `Color.Blue`, or `.Blue`"},
		{"another enum without the variant", "  c = Color.Red\n  _ = case c {\n    Mood.Calm -> 1\n    _ -> 0\n  }\n",
			"pattern `Mood.Calm` is qualified by enum Mood, but the value matched is a Color"},
		{"a struct", "  c = Color.Red\n  _ = case c {\n    Point.Blue -> 1\n    _ -> 0\n  }\n",
			"pattern `Point.Blue` is qualified by struct Point, but the value matched is a Color"},
		// A built-in qualifier fell through the check, so `String .Cat(n)` (the
		// parser joins a qualifier and `.Variant` across spaces on one line, as
		// it joins `p .name` in an expression) matched `.Cat(n)`.
		{"a built-in type", "  c = Color.Red\n  _ = case c {\n    String.Blue -> 1\n    _ -> 0\n  }\n",
			"pattern `String.Blue` is qualified by type String, but the value matched is a Color\nhelp: write `Color.Blue`, or `.Blue`"},
		{"a built-in type spaced from the variant", "  s = Shape.Dot(1)\n  _ = case s {\n    Int  .Dot(n) -> n\n    _ -> 0\n  }\n",
			"pattern `Int.Dot` is qualified by type Int, but the value matched is a Shape\nhelp: write `Shape.Dot`, or `.Dot`"},
		{"a typealias of a non-enum", "  c = Color.Red\n  _ = case c {\n    Label.Blue -> 1\n    _ -> 0\n  }\n",
			"pattern `Label.Blue` is qualified by type Label, but the value matched is a Color"},
		{"a built-in type in an if pattern", "  s = Shape.Dot(1)\n  _ = if String.Dot(n) = s { n } else { 0 }\n",
			"pattern `String.Dot` is qualified by type String, but the value matched is a Shape"},
		{"a built-in type in a let-else", "  s = Shape.Dot(1)\n  String.Dot(n) = s else { return }\n  _ = n\n",
			"pattern `String.Dot` is qualified by type String, but the value matched is a Shape"},
		{"a built-in type nested in a payload", "  m = Some(Shape.Dot(1))\n  _ = case m {\n    Some(String.Dot(n)) -> n\n    _ -> 0\n  }\n",
			"pattern `String.Dot` is qualified by type String, but the value matched is a Shape"},
		{"another enum with the variant over a payload", "  s = Shape.Dot(1)\n  _ = case s {\n    Color.Dot(n) -> n\n    _ -> 0\n  }\n",
			"pattern `Color.Dot` is qualified by enum Color, but the value matched is a Shape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := patternQualifierDecls + "fn main() {\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			for _, e := range errs {
				if strings.Contains(diagText(e), tc.want) {
					return
				}
			}
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// The mirror: the enum's own name, the `.Variant` form, a block-local enum
// a prelude enum and a typealias of the enum stay accepted.
func TestPatternQualifier_TheEnumItselfIsAccepted(t *testing.T) {
	for _, body := range []string{
		"  c = Color.Red\n  _ = case c {\n    Color.Blue -> 1\n    .Red -> 0\n  }\n",
		"  s = Shape.Dot(1)\n  _ = case s {\n    Shape.Dot(n) -> n\n    Shape.Rect{w} -> w\n  }\n",
		"  enum Hue {\n    Warm\n    Cool\n  }\n  h = Hue.Warm\n  _ = case h {\n    Hue.Cool -> 1\n    Hue.Warm -> 0\n  }\n",
		"  m = Some(1)\n  _ = case m {\n    Maybe.Some(n) -> n\n    Maybe.None -> 0\n  }\n",
		"  c = Color.Red\n  _ = case c {\n    Tint.Blue -> 1\n    Tint.Red -> 0\n  }\n",
	} {
		src := patternQualifierDecls + "fn main() {\n" + body + "}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}

// Across files the qualifier may be a module-qualified name or an import
// alias, which the registry does not hold under that spelling.
func TestPatternQualifier_ImportedSpellings(t *testing.T) {
	shapes := "pub enum Shape {\n  Dot(Int)\n  Ring(Int)\n}\n"
	accepted := map[string]string{
		"shapes.nomi": shapes,
		"main.nomi": "import shapes\nimport shapes.{Shape as Sh}\n\nfn main() {\n  s = shapes.Shape.Dot(1)\n" +
			"  _ = case s {\n    shapes.Shape.Dot(n) -> n\n    Sh.Ring(n) -> n\n  }\n}\n",
	}
	if _, errs := opaqueProject(t, accepted); len(errs) != 0 {
		t.Fatalf("want accepted, got %v", errs)
	}
	rejected := map[string]string{
		"shapes.nomi": shapes,
		"main.nomi": "import shapes\n\nfn main() {\n  s = shapes.Shape.Dot(1)\n" +
			"  _ = case s {\n    shapes.Shap.Dot(n) -> n\n    _ -> 0\n  }\n}\n",
	}
	_, errs := opaqueProject(t, rejected)
	for _, e := range errs {
		if strings.Contains(e.Message, `unknown type "shapes.Shap"`) {
			return
		}
	}
	t.Fatalf("want an unknown-type error naming shapes.Shap, got %v", errs)
}
