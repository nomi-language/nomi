package lsp

import (
	"strings"
	"testing"
)

// The declarations every shape-hover case reads.
const shapeHoverDecls = `import std/io

/// A point on the plane.
struct Point {
  x: Int
  y: Int
}

/// A shape.
enum Shape {
  /// A round one.
  Circle { radius: Float }
  Square(Float)
  Dot
}

/// Tags of a post.
type Tags Set<String>

/// A walk.
type Path Vector<Int>

/// Settings by name.
type Kvs Map<String, Int>

/// Two of a kind.
type Pair #(Int, Int)

`

type shapeHoverCase struct {
	name string
	body string
	// want are substrings the hover holds; an empty want asks for no hover.
	want []string
	not  []string
}

func runShapeHoverCases(t *testing.T, decls string, cases []shapeHoverCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hoverText(t, "shape", decls+tc.body)
			if len(tc.want) == 0 && got != "" {
				t.Fatalf("hover = %q, want none", got)
			}
			if len(tc.want) > 0 && got == "" {
				t.Fatalf("no hover, want %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("hover = %q, want it to hold %q", got, w)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("hover = %q, want it not to hold %q", got, n)
				}
			}
		})
	}
}

const pointCard = "struct Point {\n    x: Int\n    y: Int\n}"

// A struct literal's head, its name and its brackets answer with the
// struct and its doc.
func TestHover_StructLiteralHeadAndClose(t *testing.T) {
	use := "\n  io.inspect(p)\n}\n"
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"the #", "fn main() {\n  p = ▮#Point{x: 1, y: 2}" + use, []string{pointCard, "A point on the plane."}, nil},
		{"the name", "fn main() {\n  p = #Po▮int{x: 1, y: 2}" + use, []string{pointCard, "A point on the plane."}, nil},
		{"the opening brace", "fn main() {\n  p = #Point▮{x: 1, y: 2}" + use, []string{pointCard, "A point on the plane."}, nil},
		{"the closing brace", "fn main() {\n  p = #Point{x: 1, y: 2▮}" + use, []string{pointCard, "A point on the plane."}, nil},
		{"the closing brace on its own line", "fn main() {\n  p = #Point{\n    x: 1,\n    y: 2,\n  ▮}" + use, []string{pointCard, "A point on the plane."}, nil},
		{"a file-qualified name", "import std/startup\n\nfn main() {\n  p = ▮#startup.Startup{env: %{}, args: []}" + use, []string{"struct Startup"}, nil},
		{"a field is unchanged", "fn main() {\n  p = #Point{▮x: 1, y: 2}" + use, []string{"x: Int"}, []string{"struct Point"}},
		{"a value is unchanged", "fn main() {\n  p = #Point{x: 1, y: ▮2}" + use, nil, nil},
		{"a comma shows nothing", "fn main() {\n  p = #Point{x: 1▮, y: 2}" + use, nil, nil},
	})
}

// `#{` and its closing brace answer with the struct the expected type made
// it, or with the anonymous struct's type.
func TestHover_UnnamedStructLiteral(t *testing.T) {
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"the # of a target-typed literal", "fn main() {\n  p: Point = ▮#{x: 1, y: 2}\n  io.inspect(p)\n}\n", []string{pointCard, "*from the expected type*", "A point on the plane."}, nil},
		{"its brace", "fn main() {\n  p: Point = #▮{x: 1, y: 2}\n  io.inspect(p)\n}\n", []string{pointCard, "*from the expected type*"}, nil},
		{"its closing brace", "fn main() {\n  p: Point = #{x: 1, y: 2▮}\n  io.inspect(p)\n}\n", []string{pointCard, "*from the expected type*"}, nil},
		{"an anonymous struct", "fn main() {\n  p = ▮#{x: 1, y: \"a\"}\n  io.inspect(p.x)\n}\n", []string{"```nomi\nanonymous struct #{x: Int, y: String}\n```"}, []string{"struct Point"}},
		{"a binding of an anonymous struct keeps its form", "fn main() {\n  ▮p = #{x: 1, y: \"a\"}\n  io.inspect(p.x)\n}\n", []string{"p: #{x: Int, y: String}"}, []string{"anonymous"}},
	})
}

// A struct-variant literal's head answers with the variant and its enum.
func TestHover_VariantStructLiteral(t *testing.T) {
	use := "\n  io.inspect(s)\n}\n"
	circle := "Shape.Circle { radius: Float }"
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"the # of the qualified form", "fn main() {\n  s = ▮#Shape.Circle{radius: 1.0}" + use, []string{circle, "A round one."}, []string{"from the expected type"}},
		{"the enum name", "fn main() {\n  s = #Sh▮ape.Circle{radius: 1.0}" + use, []string{circle}, nil},
		{"the brace", "fn main() {\n  s = #Shape.Circle▮{radius: 1.0}" + use, []string{circle}, nil},
		{"the # of the dot form", "fn main() {\n  s: Shape = ▮#.Circle{radius: 1.0}" + use, []string{circle, "*`Shape` from the expected type*", "A round one."}, nil},
		{"the . of the dot form", "fn main() {\n  s: Shape = #▮.Circle{radius: 1.0}" + use, []string{circle, "*`Shape` from the expected type*"}, nil},
		{"the name of the dot form", "fn main() {\n  s: Shape = #.Cir▮cle{radius: 1.0}" + use, []string{circle, "*`Shape` from the expected type*"}, nil},
		{"the closing brace of the dot form", "fn main() {\n  s: Shape = #.Circle{radius: 1.0▮}" + use, []string{circle, "*`Shape` from the expected type*"}, nil},
	})
}

// The unnamed collection and tuple literals answer with their full type on
// the head and on the closing bracket; an element answers as it did.
func TestHover_CollectionLiteral(t *testing.T) {
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"the % of %[", "fn main() {\n  s = ▮%[\"a\"]\n  io.inspect(s)\n}\n", []string{"```nomi\nSet<String>\n```"}, nil},
		{"the [ of %[", "fn main() {\n  s = %▮[\"a\"]\n  io.inspect(s)\n}\n", []string{"```nomi\nSet<String>\n```"}, nil},
		{"the ] of %[", "fn main() {\n  s = %[\"a\"▮]\n  io.inspect(s)\n}\n", []string{"```nomi\nSet<String>\n```"}, nil},
		{"#[", "fn main() {\n  v = #▮[1, 2]\n  io.inspect(v)\n}\n", []string{"```nomi\nVector<Int>\n```"}, nil},
		{"%{", "fn main() {\n  m = ▮%{\"a\" => 1}\n  io.inspect(m)\n}\n", []string{"```nomi\nMap<String, Int>\n```"}, nil},
		{"#(", "fn main() {\n  p = ▮#(1, \"a\")\n  io.inspect(p)\n}\n", []string{"```nomi\ntuple #(Int, String)\n```"}, nil},
		{"the ) of #(", "fn main() {\n  p = #(1, \"a\"▮)\n  io.inspect(p)\n}\n", []string{"```nomi\ntuple #(Int, String)\n```"}, nil},
		{"a binding of a tuple keeps its form", "fn main() {\n  ▮p = #(1, \"a\")\n  io.inspect(p)\n}\n", []string{"p: #(Int, String)"}, []string{"tuple"}},
		{"a multi-line map's closing brace", "fn main() {\n  m = %{\n    \"a\" => 1,\n    \"b\" => 2,\n  ▮}\n  io.inspect(m)\n}\n", []string{"```nomi\nMap<String, Int>\n```"}, nil},
		{"a list", "fn main() {\n  xs = ▮[1, 2]\n  io.inspect(xs)\n}\n", []string{"```nomi\nList<Int>\n```"}, nil},
		{"an element is unchanged", "fn main() {\n  m = %{\"a\" => ▮1}\n  io.inspect(m)\n}\n", nil, nil},
		{"=> shows nothing", "fn main() {\n  m = %{\"a\" =▮> 1}\n  io.inspect(m)\n}\n", nil, nil},
	})
}

// A named collection or tuple literal answers with the type it names, the
// shape that type is built on, and its doc.
func TestHover_NamedCollectionLiteral(t *testing.T) {
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"%Tags[", "fn main() {\n  t = ▮%Tags[\"a\"]\n  io.inspect(t)\n}\n", []string{"```nomi\nTags\n```", "a `Set<String>`", "Tags of a post."}, nil},
		{"the name of %Tags[", "fn main() {\n  t = %Ta▮gs[\"a\"]\n  io.inspect(t)\n}\n", []string{"```nomi\nTags\n```", "a `Set<String>`"}, nil},
		{"the ] of %Tags[", "fn main() {\n  t = %Tags[\"a\"▮]\n  io.inspect(t)\n}\n", []string{"```nomi\nTags\n```", "a `Set<String>`"}, nil},
		{"#Path[", "fn main() {\n  p = #Path▮[1, 2]\n  io.inspect(p)\n}\n", []string{"```nomi\nPath\n```", "a `Vector<Int>`", "A walk."}, nil},
		{"%Kvs{", "fn main() {\n  k = ▮%Kvs{\"a\" => 1}\n  io.inspect(k)\n}\n", []string{"```nomi\nKvs\n```", "a `Map<String, Int>`", "Settings by name."}, nil},
		{"#Pair(", "fn main() {\n  p = ▮#Pair(1, 2)\n  io.inspect(p)\n}\n", []string{"```nomi\nPair\n```", "a `#(Int, Int)`", "Two of a kind."}, nil},
	})
}

// A pattern's head and closing bracket say what the pattern matches and
// what its names are; `..rest` answers as rest does, and a bare `..`
// shows nothing.
func TestHover_ShapePatterns(t *testing.T) {
	set := "fn f(s: Set<String>) -> Int {\n  case s {\n    %s -> 1\n    _ -> 0\n  }\n}\n"
	at := func(form, pattern string) string { return strings.Replace(form, "%s", pattern, 1) }
	setWant := "set pattern: matches a `Set<String>` containing `\"admin\"`; `rest` is `Set<String>`"
	vec := "fn f(v: Vector<Int>) -> Int {\n  case v {\n    %s -> 1\n    _ -> 0\n  }\n}\n"
	vecWant := "vector pattern: matches a `Vector<Int>` of at least 1 element; `first` is `Int`, `rest` is `Vector<Int>`"
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"the % of a set pattern", at(set, "▮%[\"admin\", ..rest]"), []string{setWant}, nil},
		{"the [ of a set pattern", at(set, "%▮[\"admin\", ..rest]"), []string{setWant}, nil},
		{"the ] of a set pattern", at(set, "%[\"admin\", ..rest▮]"), []string{setWant}, nil},
		{"the .. of ..rest", at(set, "%[\"admin\", ▮..rest]"), []string{"```nomi\nrest: Set<String>\n```"}, []string{"items"}},
		{"rest itself", at(set, "%[\"admin\", ..▮rest]"), []string{"```nomi\nrest: Set<String>\n```"}, []string{"items"}},
		{"a bare .. shows nothing", at(set, "%[\"admin\", ▮..]"), nil, nil},
		{"an element is unchanged", at(set, "%[▮\"admin\", ..rest]"), nil, []string{"set pattern"}},
		{"a set pattern without a spread", at(set, "▮%[\"a\", \"b\"]"), []string{"set pattern: matches a `Set<String>` holding exactly `\"a\"`, `\"b\"`"}, nil},
		{"the # of a vector pattern", at(vec, "▮#[first, ..rest]"), []string{vecWant}, nil},
		{"the ] of a vector pattern", at(vec, "#[first, ..rest▮]"), []string{vecWant}, nil},
		{"the .. of a vector rest", at(vec, "#[first, ▮..rest]"), []string{"```nomi\nrest: Vector<Int>\n```"}, nil},
		{"an exact vector pattern", at(vec, "▮#[a, b]"), []string{"vector pattern: matches a `Vector<Int>` of exactly 2 elements; `a` is `Int`, `b` is `Int`"}, nil},
		{"a map pattern", "fn f(m: Map<String, Int>) -> Int {\n  case m {\n    ▮%{\"a\" => v} -> v\n    _ -> 0\n  }\n}\n", []string{"map pattern: matches a `Map<String, Int>` with the key `\"a\"`; `v` is `Int`"}, nil},
		{"a struct pattern", "fn f(p: Point) -> Int {\n  case p {\n    ▮#Point{x, y} -> x + y\n  }\n}\n", []string{"struct pattern: matches a `Point`; `x` is `Int`, `y` is `Int`"}, nil},
		{"a struct pattern's closing brace on its own line", "fn f(p: Point) -> Int {\n  case p {\n    #Point{\n      x,\n      y,\n    ▮} -> x + y\n  }\n}\n", []string{"struct pattern: matches a `Point`"}, nil},
		{"a variant struct pattern", "fn f(s: Shape) -> Float {\n  case s {\n    ▮#.Circle{radius} -> radius\n    _ -> 0.0\n  }\n}\n", []string{"struct pattern: matches `Shape.Circle`; `radius` is `Float`"}, nil},
		{"a tuple pattern", "fn f(p: #(Int, String)) -> Int {\n  case p {\n    ▮#(n, _) -> n\n  }\n}\n", []string{"tuple pattern: matches a `#(Int, String)`; `n` is `Int`"}, nil},
		{"a named set pattern", "fn f(t: Tags) -> Int {\n  case t {\n    ▮%Tags[\"a\", ..] -> 1\n    _ -> 0\n  }\n}\n", []string{"set pattern: matches a `Tags` containing `\"a\"`"}, nil},
	})
}
