package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A positional construction has no parameter names: a positional variant's
// payload (`Some(1)`, `Shape.Sq(2)`), a distinct type's wrapped value
// (`Id(5)`), and a struct's record call form (`Point(#{x: 1})`). The checker
// knew no names for a variant constructor and skipped the name, so
// `Some(l: 1)` checked as `Some(1)` against an expected type and the IR
// builder declined the call ("a type name in value position: Some"); with
// no expected type the binding was "not locally determined". A named
// argument to a tuple distinct was admitted the same way. Each is now
// "no parameter named" at the name.

const namedCtorDecls = `
enum Shape {
  Sq Int
  Pair #(Int, Int)
  Rect {w: Int, h: Int}
  embeds Id
}

enum Box<T> {
  Full T
  Empty
}

type Id Int

type Two #(Int, String)

struct Point {
  x: Int
}
`

const (
	variantNameHint  = "a variant's payload is positional and has no name; pass it by position"
	distinctNameHint = "a distinct type's constructor takes the value it wraps by position; pass it without a name"
)

func TestNamedArgToConstructor_IsAnError(t *testing.T) {
	for _, tc := range []struct{ name, src, at, hint string }{
		{"Some with an expected type", `fn f() -> Maybe<Int> {
  Some(l: 1)
}`, "l: 1", variantNameHint},
		{"Some without an expected type", `fn f() {
  x = Some(l: 1)
  _ = x
}`, "l: 1", variantNameHint},
		{"Ok", `fn f() -> Result<Int, String> {
  Ok(v: 1)
}`, "v: 1", variantNameHint},
		{"Err", `fn f() -> Result<Int, String> {
  Err(e: "x")
}`, `e: "x"`, variantNameHint},
		{"a user variant", `fn f() -> Shape {
  Shape.Sq(n: 1)
}`, "n: 1", variantNameHint},
		{"a dot variant", `fn f() -> Shape {
  .Sq(n: 1)
}`, "n: 1", variantNameHint},
		{"a tuple-payload variant, flat", `fn f() -> Shape {
  Shape.Pair(a: 1, 2)
}`, "a: 1", variantNameHint},
		{"a tuple-payload variant, whole", `fn f() -> Shape {
  Shape.Pair(a: #(1, 2))
}`, "a: #(1, 2)", variantNameHint},
		{"a generic variant", `fn f() -> Box<Int> {
  Box.Full(v: 1)
}`, "v: 1", variantNameHint},
		{"a named placeholder", `fn f() -> (Int) -> Maybe<Int> {
  Some(l: _)
}`, "l: _", variantNameHint},
		{"a pipe", `fn f() -> Maybe<Int> {
  1 |> Some(l: _)
}`, "l: _", variantNameHint},
		{"the fuzzer's Assertable impl", `import std/assertions.{Assertable, AssertionDetails}
struct Same {
  a: Int
  b: Int
}
impl Assertable for Same {
  fn failure(s: Same) -> Maybe<AssertionDetails> {
    if s.a == s.b {
      None
    } else {
      Some(l: #AssertionDetails{
        reason: "values were not equal",
      })
    }
  }
}`, "l: #AssertionDetails", variantNameHint},
		{"a distinct", `fn f() -> Id {
  Id(n: 5)
}`, "n: 5", distinctNameHint},
		{"a tuple distinct", `fn f() -> Two {
  Two(t: #(1, "x"))
}`, `t: #(1, "x")`, distinctNameHint},
		{"an embedded distinct", `fn f() -> Shape {
  Shape.Id(n: 3)
}`, "n: 3", distinctNameHint},
		{"a struct's call form", `fn f() -> Point {
  Point(p: #{x: 1})
}`, "p: #{x: 1}", "a struct's call form takes one record with the fields in it; write Point(#{...}) or #Point{...}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := namedCtorDecls + tc.src + "\nfn main() {\n}\n"
			if strings.HasPrefix(tc.src, "import ") {
				imp, rest, _ := strings.Cut(tc.src, "\n")
				src = imp + "\n" + namedCtorDecls + rest + "\nfn main() {\n}\n"
			}
			_, errs := checkSourceWithStdlib(src)
			expectNamedArgError(t, src, errs, tc.at, tc.hint)
		})
	}
}

// A struct-shaped variant keeps its own message: it has no positional form,
// named or not.
func TestNamedArgToConstructor_StructVariantNamesItsForms(t *testing.T) {
	src := namedCtorDecls + `fn f() -> Shape {
  Shape.Rect(w: 1, h: 2)
}
fn main() {
}
`
	_, errs := checkSourceWithStdlib(src)
	line, col := lineColOf(t, src, "Rect(w")
	want := "Shape.Rect is a struct-shaped variant and has no positional form; construct it with #Shape.Rect{...} or Shape.Rect(#{...})"
	for _, e := range errs {
		if e.Message == want && e.Line == line && e.Col == col {
			return
		}
	}
	t.Fatalf("want %q at %d:%d, got:\n%s", want, line, col, errorList(errs))
}

const namedCtorShapes = `pub enum Shape {
  Sq Int
  Dot
}
`

// A variant imported by name, or under an alias, is the same constructor.
func TestNamedArgToConstructor_ImportedVariant(t *testing.T) {
	for _, tc := range []struct{ name, imp, call string }{
		{"selective import", "import shapes.Shape.{Sq}", "Sq(n: 1)"},
		{"aliased import", "import shapes.Shape.{Sq as Square}", "Square(n: 1)"},
		{"file-qualified", "import shapes", "shapes.Shape.Sq(n: 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.imp + "\nimport shapes.Shape\n\nfn f() -> Shape {\n  " + tc.call + "\n}\n\nfn main() {\n}\n"
			if tc.imp == "import shapes" {
				src = tc.imp + "\n\nfn f() -> shapes.Shape {\n  " + tc.call + "\n}\n\nfn main() {\n}\n"
			}
			_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": namedCtorShapes})
			expectNamedArgError(t, src, errs, "n: 1", variantNameHint)
		})
	}
}

// The positional forms still check, with no error and a typed result.
func TestNamedArgToConstructor_PositionalStillChecks(t *testing.T) {
	_, errs := checkSourceWithStdlib(namedCtorDecls + `fn main() {
  a: Maybe<Int> = Some(1)
  b: Result<Int, String> = Ok(1)
  c: Shape = .Sq(1)
  d: Shape = Shape.Pair(1, 2)
  e: Box<Int> = Box.Full(1)
  f = Some(_)
  g: Maybe<Int> = 1 |> Some()
  h: Id = Id(5)
  i: Two = #Two(1, "x")
  j: Shape = Shape.Id(3)
  k: Point = Point(#{x: 1})
  _ = #(a, b, c, d, e, f(1), g, h, i, j, k)
}
`)
	expectNoStdlibErrors(t, errs)
}

func expectNamedArgError(t *testing.T, src string, errs []analysis.TypeError, at, hint string) {
	t.Helper()
	line, col := lineColOf(t, src, at)
	want := "no parameter named '" + at[:strings.Index(at, ":")] + "'"
	var found *analysis.TypeError
	for i, e := range errs {
		if e.Message == want && e.Line == line && e.Col == col {
			found = &errs[i]
		}
	}
	if found == nil {
		t.Fatalf("the front end admits a named argument to a positional constructor; want %q at %d:%d, got:\n%s",
			want, line, col, errorList(errs))
	}
	if !containsString(found.Hints, hint) {
		t.Errorf("hints %q; want %q", found.Hints, hint)
	}
	for _, e := range errs {
		if e.Line == line && e.Message != want {
			t.Errorf("a second error on the rejected call's line: %s", e.Error())
		}
	}
}

// An interface-qualified call names the interface method's parameters. The
// checker knew no names for such a callee and skipped any name:
// `Struct.update(p, patch: #{x: 2})` and `Display.to_string(v: 1)` passed it
// and the IR builder declined them.
func TestNamedArgToInterfaceFunction_ChecksTheName(t *testing.T) {
	for _, tc := range []struct{ name, src, at string }{
		{"Struct.update", `fn f(p: Point) -> Point {
  Struct.update(p, patch: #{x: 2})
}`, "patch: #{x: 2}"},
		{"Display.to_string", `fn f() -> String {
  Display.to_string(v: 1)
}`, "v: 1"},
		{"a bounded type parameter", `fn f<T>(x: T) -> String where T: Display {
  T.to_string(v: x)
}`, "v: x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := namedCtorDecls + tc.src + "\nfn main() {\n}\n"
			_, errs := checkSourceWithStdlib(src)
			line, col := lineColOf(t, src, tc.at)
			want := "no parameter named '" + tc.at[:strings.Index(tc.at, ":")] + "'"
			for _, e := range errs {
				if e.Message == want && e.Line == line && e.Col == col {
					return
				}
			}
			t.Fatalf("the front end admits a name the interface function does not declare; want %q at %d:%d, got:\n%s",
				want, line, col, errorList(errs))
		})
	}
	_, errs := checkSourceWithStdlib(namedCtorDecls + `fn show<T>(x: T) -> String where T: Display {
  T.to_string(value: x)
}
fn main() {
  p = #Point{x: 1}
  a: Point = Struct.update(p, updates: #{x: 2})
  b: Point = Struct.update(updates: #{x: 3}, original: p)
  c: String = Display.to_string(value: 4)
  _ = #(a, b, c, show(5))
}
`)
	expectNoStdlibErrors(t, errs)
}
