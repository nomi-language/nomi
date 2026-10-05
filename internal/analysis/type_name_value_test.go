package analysis_test

import (
	"strings"
	"testing"
)

// A type name is not a value and not a function (type_name_value.go). Each
// rejected form below passed the checker and was BLOCKED at run time with
// "a type name in value position".

const typeNameDecls = `struct Point {
  x: Int
}

type Id Int

type Label String

type Expired

enum Dir {
  North
  South
}

interface Named {
  fn name(n: self): String
}

`

func TestTypeName_CalledAsAFunctionIsRejected(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want string
	}{
		{`Int("4")`, "`Int` is a type, not a function\nhelp: to parse a String, call `String.to_int`, which returns a `Maybe<Int>`"},
		{`"4" |> Int()`, "`Int` is a type, not a function\nhelp: to parse a String, call `String.to_int`"},
		{`Int(2.5)`, "`Int` is a type, not a function\nhelp: to convert a Float, call `Float.to_int`"},
		{`String(4)`, "`String` is a type, not a function\nhelp: to turn a value into a String, interpolate it (`\"${x}\"`) or call `Display.to_string(x)`"},
		{`String(Id(4))`, "`String` is a type, not a function"},
		{`Float(3)`, "`Float` is a type, not a function\nhelp: to convert an Int, call `Int.to_float`"},
		{`Float("1.5")`, "`Float` is a type, not a function\nhelp: `Float(x)` only unwraps a distinct type that wraps Float"},
		{`Bool(1)`, "`Bool` is a type, not a function"},
		{`Dir(1)`, "`Dir` is a type, not a function\nhelp: build a value with one of its variants, as in `Dir.North`"},
		{`Maybe(3)`, "`Maybe` is a type, not a function"},
		{`Named(3)`, "`Named` is an interface, not a function"},
	} {
		src := typeNameDecls + "fn main() {\n  _ = " + tc.expr + "\n}\n"
		_, errs := checkSourceWithStdlib(src)
		found := false
		for _, e := range errs {
			if strings.Contains(diagText(e), tc.want) {
				found = true
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

func TestTypeName_AsAValueIsRejected(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want string
	}{
		{`x = Int`, "`Int` is a type, not a value"},
		{`x = Point`, "`Point` is a type, not a value\nhelp: build a value with `Point{...}`"},
		{`x = Dir`, "`Dir` is a type, not a value\nhelp: a value of `Dir` is one of its variants, as in `Dir.North`"},
		{`x = Named`, "`Named` is an interface, not a value"},
		{`x: List<String> = Iter.map([1], String) |> Iter.to_list()`, "`String` is a type, not a value"},
		{`x: List<Int> = Iter.map([Id(1)], Int) |> Iter.to_list()`, "`Int` is a type, not a value"},
		{`x = Iter.map([{x: 1}], Point) |> Iter.to_list()`, "`Point` is a type, not a value"},
		{`x = Iter.map([1], Named) |> Iter.to_list()`, "`Named` is an interface, not a value"},
		// A generic constructor with nothing to solve its type parameter
		// from is rejected as a generic function is.
		{`x = Some`, "cannot infer type parameter T of generic constructor 'Some' used as a value"},
		{`x = Iter.map([1], Ok) |> Iter.to_list()`, "cannot infer type parameter E of generic constructor 'Ok' used as a value"},
		{`x = Maybe.Some`, "cannot infer type parameter T of generic constructor 'Maybe.Some' used as a value"},
	} {
		src := typeNameDecls + "fn main() {\n  " + tc.stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		found := false
		for _, e := range errs {
			if strings.Contains(diagText(e), tc.want) {
				found = true
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

// The mirror: construction, unwrap, qualification, witnesses and the names
// that are values stay accepted.
func TestTypeName_ConstructionUnwrapAndValuesAreAccepted(t *testing.T) {
	for _, stmt := range []string{
		`x = Point({x: 1})`,
		`x = Point{x: 1}`,
		`x = Id(4)`,
		`x = 4 |> Id()`,
		`x = Int(Id(4))`,
		`x = Id(4) |> Int()`,
		`x = String(Label("a"))`,
		`x = Expired`,
		`x = Dir.North`,
		`x = Int.to_string(3)`,
		`x = True`,
		`x = Unit`,
		`x = Maybe.Some(3)`,
		`x = Context.value(Context.root(), Id)`,
		// A witness of a block-local type, which the type registry does
		// not hold.
		"type Token String\n  x = Context.value(Context.root(), Token)",
		// A distinct type and a positional variant named as a value are
		// their constructors (distinctCtorValue, generic_func_ref.go).
		`x: List<Id> = Iter.map([1], Id) |> Iter.to_list()`,
		`x = Iter.map([1], Id) |> Iter.to_list()`,
		"f = Id\n  x = f(3)",
		"f: (Int) -> Id = Id\n  x = f(3)",
		"type Token String\n  x = Token",
		`x: List<Maybe<Int>> = Iter.map([1], Some) |> Iter.to_list()`,
		`x = Iter.map([1], Some) |> Iter.to_list()`,
		`x = Iter.map([1], Maybe.Some) |> Iter.to_list()`,
		`x = [1] |> Iter.map(Some) |> Iter.to_list()`,
		`x: List<Result<Int, String>> = Iter.map([1], Ok) |> Iter.to_list()`,
		`x: List<Result<Int, String>> = Iter.map(["e"], Err) |> Iter.to_list()`,
		`x = Result.map_err(Err("e"), Label) |> Result.ok?()`,
		`x = Maybe.map(Some(1), Id)`,
		"f: (Int) -> Maybe<Int> = Some\n  x = f(3)",
		"f: (String) -> Result<Int, String> = Err\n  x = f(\"e\")",
	} {
		src := typeNameDecls + "fn main() {\n  " + stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}

// A positional variant over a tuple and a tuple-distinct are functions of
// the one tuple they take: the flat call `Pair(1, "x")` is a call form, not a
// second parameter list.
func TestConstructorValue_TupleTakesTheTuple(t *testing.T) {
	decls := "type Pair (Int, String)\n\nenum Shape {\n  Seg (Int, Int)\n}\n\n"
	for _, stmt := range []string{
		`x = Iter.map([(1, "a")], Pair) |> Iter.to_list()`,
		`x = Iter.map([(1, 2)], Shape.Seg) |> Iter.to_list()`,
		"f: ((Int, String)) -> Pair = Pair\n  x = f((1, \"a\"))",
	} {
		src := decls + "fn main() {\n  " + stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
	src := decls + "fn main() {\n  f = Pair\n  x = f(1, \"a\")\n  _ = x\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "expected 1 arguments, got 2")
}

// The names that stay errors keep their messages: a struct-shaped variant,
// and `.Variant`, which resolves only against an expected enum.
func TestConstructorValue_RejectedForms(t *testing.T) {
	decls := "enum Shape {\n  Circle Float\n  Rect {w: Int}\n}\n\n"
	for _, tc := range []struct {
		stmt string
		want string
	}{
		{`x = Iter.map([{w: 1}], Shape.Rect) |> Iter.to_list()`, "Shape.Rect is a struct-shaped variant and is not a function value"},
		{`x: (Float) -> Shape = .Circle`, ".Circle resolves only to enum variants, but the expected type at this position is (Float) -> Shape\nhelp: a constructor as a function value is written qualified, `Shape.Circle`"},
	} {
		src := decls + "fn main() {\n  " + tc.stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		found := false
		for _, e := range errs {
			if strings.Contains(diagText(e), tc.want) {
				found = true
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

// An opaque distinct's constructor is private outside its file whether it is
// called or named as a value; inside its file the value is accepted.
func TestConstructorValue_OpaqueDistinctOutsideItsFile(t *testing.T) {
	files := map[string]string{
		"secret.nomi": "pub opaque type Secret String\n\npub fn all(xs: List<String>): List<Secret> {\n  Iter.map(xs, Secret) |> Iter.to_list()\n}\n",
		"main.nomi":   "import secret\n\nfn main() {\n  xs = Iter.map([\"a\"], secret.Secret) |> Iter.to_list()\n  _ = xs\n  _ = secret.all([\"b\"])\n}\n",
	}
	_, errs := opaqueProject(t, files)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "constructor of opaque type 'Secret' is private to its defining module") {
		t.Fatalf("want one opaque-constructor error, got %v", errs)
	}
}
