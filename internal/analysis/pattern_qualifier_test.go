package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
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
		{"a misspelled enum over a struct variant", "  s = Shape.Dot(1)\n  _ = case s {\n    #Shpe.Rect{w} -> w\n    _ -> 0\n  }\n",
			"unknown type \"Shpe\"\nhelp: did you mean 'Shape'?"},
		// A lower-case qualifier reads as a module. The builder resolved
		// `ror.Rect` as this file's own `Rect` and recorded it at the
		// qualifier, which the check took for a resolved module.
		{"an unknown module over a struct variant", "  s = Shape.Dot(1)\n  _ = case s {\n    #ror.Rect{w} -> w\n    _ -> 0\n  }\n",
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
		"  s = Shape.Dot(1)\n  _ = case s {\n    Shape.Dot(n) -> n\n    #Shape.Rect{w} -> w\n  }\n",
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

// A qualifier the type registry does not hold was accepted whenever the
// builder had recorded some symbol at it, and the builder records whatever
// name is in scope under that spelling: a variant of any enum, a function, a
// binding, a module. The variant was then looked up by the member name
// alone, so `Of.Square(n)` over a Shape matched `.Square(n)` and the IR
// builder declined the `case`.
const variantQualifierDecls = `enum Shape {
  Ring {r: Int}
  Square(Int)
}

enum Holder<T> {
  Of {value: T}
}

fn helper(n: Int) -> Int {
  n
}

`

func TestPatternQualifier_ANameThatIsNotATypeIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, body, at, want string
	}{
		{"a variant of another enum", "  s = Shape.Square(1)\n  _ = case s {\n    #.Ring{r} -> r\n    Of.Square(n) -> n\n  }\n",
			"Of.Square", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a variant of another enum spaced from the variant", "  s = Shape.Square(1)\n  _ = case s {\n    #.Ring{r} -> r\n    Of .Square(n) -> n\n  }\n",
			"Of .Square", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a variant of the same enum", "  s = Shape.Square(1)\n  _ = case s {\n    #Square.Ring{r} -> r\n    _ -> 0\n  }\n",
			"Square.Ring", "`Square` is a variant of Shape, not a type\nhelp: write `Shape.Ring`, or `.Ring`"},
		{"the variant itself", "  s = Shape.Square(1)\n  _ = case s {\n    Square.Square(n) -> n\n    _ -> 0\n  }\n",
			"Square.Square", "`Square` is a variant of Shape, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a variant of the generic enum matched", "  h = #Holder.Of{value: 1}\n  _ = case h {\n    #Of.Of{value} -> value\n  }\n",
			"Of.Of", "`Of` is a variant of Holder, not a type\nhelp: write `Holder.Of`, or `.Of`"},
		{"a prelude variant", "  m = Some(1)\n  _ = case m {\n    Some.Some(n) -> n\n    _ -> 0\n  }\n",
			"Some.Some", "`Some` is a variant of Maybe, not a type\nhelp: write `Maybe.Some`, or `.Some`"},
		{"another prelude variant", "  m = Some(1)\n  _ = case m {\n    None.Some(n) -> n\n    _ -> 0\n  }\n",
			"None.Some", "`None` is a variant of Maybe, not a type\nhelp: write `Maybe.Some`, or `.Some`"},
		{"a prelude variant over a user enum", "  s = Shape.Square(1)\n  _ = case s {\n    Some.Square(n) -> n\n    _ -> 0\n  }\n",
			"Some.Square", "`Some` is a variant of Maybe, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a function", "  s = Shape.Square(1)\n  _ = case s {\n    helper.Square(n) -> n\n    _ -> 0\n  }\n",
			"helper.Square", "`helper` is a function, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a binding", "  x = 3\n  s = Shape.Square(1)\n  _ = case s {\n    x.Square(n) -> n\n    _ -> 0\n  }\n",
			"x.Square", "unknown type \"x\""},
		{"a variant in an if pattern", "  s = Shape.Square(1)\n  _ = if Of.Square(n) = s { n } else { 0 }\n",
			"Of.Square", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a variant in a let-else", "  s = Shape.Square(1)\n  Of.Square(n) = s else { return }\n  _ = n\n",
			"Of.Square", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		{"a variant nested in a payload", "  m = Some(Shape.Square(1))\n  _ = case m {\n    Some(Of.Square(n)) -> n\n    _ -> 0\n  }\n",
			"Of.Square", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Square`, or `.Square`"},
		// Construction already rejected these; they stay rejected.
		{"a variant qualifying a construction", "  s: Shape = Of.Square(4)\n  _ = s\n",
			"Of.Square", "variant 'Of' must be qualified through its enum; use 'Holder.Of'"},
		{"a prelude variant qualifying a construction", "  m: Maybe<Int> = Some.Some(4)\n  _ = m\n",
			"Some(4)", "(T) -> Maybe<T> has no field 'Some'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := variantQualifierDecls + "fn main() {\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			expectErrorAt(t, src, errs, tc.at, tc.want)
		})
	}
}

// expectErrorAt fails unless errs holds want, hints included, at the first
// occurrence of marker in src.
func expectErrorAt(t *testing.T, src string, errs []analysis.TypeError, marker, want string) {
	t.Helper()
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("marker %q not in source", marker)
	}
	before := src[:i]
	line, col := strings.Count(before, "\n")+1, i-strings.LastIndex(before, "\n")
	for _, e := range errs {
		if diagText(e) == want {
			if e.Line != line || e.Col != col {
				t.Fatalf("%q reported at %d:%d, want %d:%d", want, e.Line, e.Col, line, col)
			}
			return
		}
	}
	if len(errs) == 0 {
		t.Fatalf("the checker admits this source; want %q at %d:%d", want, line, col)
	}
	t.Fatalf("want %q at %d:%d, got %v", want, line, col, errs)
}

// The mirror: the enum's own name, a generic enum without type arguments,
// the `.Variant` form and a prelude enum's name stay accepted.
func TestPatternQualifier_TheEnumNameOverAVariantNameIsAccepted(t *testing.T) {
	for _, body := range []string{
		"  s = Shape.Square(1)\n  _ = case s {\n    #.Ring{r} -> r\n    Shape.Square(n) -> n\n  }\n",
		"  s = Shape.Square(1)\n  _ = case s {\n    #Shape.Ring{r} -> r\n    .Square(n) -> n\n  }\n",
		"  h = #Holder.Of{value: 1}\n  _ = case h {\n    #Holder.Of{value} -> value\n  }\n",
		"  m = Some(1)\n  _ = case m {\n    Maybe.Some(n) -> n\n    Maybe.None -> 0\n  }\n",
	} {
		src := variantQualifierDecls + "fn main() {\n" + body + "}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}

// Across files: a module is not a type, and neither is a variant brought in
// by a drill-through import. The module-qualified enum and an import alias
// of it stay accepted (TestPatternQualifier_ImportedSpellings).
func TestPatternQualifier_ImportedNamesThatAreNotTypesAreRejected(t *testing.T) {
	shapes := "pub enum Shape {\n  Dot(Int)\n  Ring(Int)\n}\n\npub enum Holder<T> {\n  Of {value: T}\n}\n"
	for _, tc := range []struct {
		arm, want string
	}{
		{"shapes.Dot(n) -> n", "`shapes` is a module, not a type\nhelp: write `Shape.Dot`, or `.Dot`"},
		{"Of.Dot(n) -> n", "`Of` is a variant of Holder, not a type\nhelp: write `Shape.Dot`, or `.Dot`"},
		{"Ring.Dot(n) -> n", "`Ring` is a variant of Shape, not a type\nhelp: write `Shape.Dot`, or `.Dot`"},
	} {
		t.Run(tc.arm, func(t *testing.T) {
			main := "import shapes\nimport shapes.Holder.{Of}\nimport shapes.Shape.{Ring}\n\nfn main() {\n" +
				"  s = shapes.Shape.Dot(1)\n" +
				"  _ = case s {\n    " + tc.arm + "\n    _ -> 0\n  }\n}\n"
			_, errs := opaqueProject(t, map[string]string{"shapes.nomi": shapes, "main.nomi": main})
			expectErrorAt(t, main, errs, tc.arm, tc.want)
		})
	}
}
