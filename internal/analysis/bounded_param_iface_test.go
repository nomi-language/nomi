package analysis_test

import (
	"strings"
	"testing"
)

// A value of a type parameter bounded by an interface enters any position
// typed by that interface: a parameter, an annotated binding, a list
// element, a result and a struct field, for a std interface and a program's
// own, under one bound, several, a bound alias, an impl block's bound and a
// generic interface's bound at its type arguments.
func TestBoundedTypeParamEntersInterfacePositions(t *testing.T) {
	src := `import std::json.{Json, ToJson}

interface Shape {
    fn area(s: self) -> Int
}

interface Conv<U> {
    fn conv(v: self) -> U
}

struct Labeled {
    label: Display
}

struct Box<T> {
    item: T
}

typealias Both Display and Hashable

fn take(x: Display) -> String {
    "${x}"
}

fn total(shapes: List<Shape>) -> Int {
    shapes |> Iter.map(fn s -> Shape.area(s)) |> Iter.sum()
}

fn key(h: Hashable) -> Int {
    Hashable.hash(h)
}

fn as_json(j: ToJson) -> String {
    Json.encode(ToJson.to_json(j))
}

fn use_int(c: Conv<Int>) -> Int {
    Conv.conv(c)
}

fn param<T>(v: T) -> String where T: Display {
    take(v)
}

fn binding<T>(v: T) -> String where T: Display {
    d: Display = v
    "${d}"
}

fn element<T>(v: T, w: T) -> Int where T: Shape {
    total([v, w])
}

fn returned<T>(v: T) -> Display where T: Display {
    v
}

fn field<T>(v: T) -> Labeled where T: Display {
    Labeled{label: v}
}

fn several<T>(v: T) -> String where T: Hashable and Display {
    "${key(v)} ${take(v)}"
}

fn aliased<T>(v: T) -> String where T: Both {
    "${key(v)} ${take(v)}"
}

fn encoded<T>(v: T) -> String where T: ToJson {
    as_json(v)
}

fn converted<T>(v: T) -> Int where T: Conv<Int> {
    use_int(v)
}

impl Display for Box<T> where T: Display {
    fn to_string(b: Box<T>) -> String {
        take(b.item)
    }
}

fn main() {
    _ = param(3)
}
`
	if errs := checkWithStdlib(src); len(errs) != 0 {
		t.Fatalf("the checker rejects a bounded type parameter at an interface position: %v", errs)
	}
}

// A type parameter without the interface among its bounds stays out of
// that interface's positions, and the help shows the declaration with the
// bound added. A generic interface's bound at other type arguments is not
// that interface.
func TestUnboundedTypeParamRefusedAtInterfacePositions(t *testing.T) {
	const decls = `interface Conv<U> {
    fn conv(v: self) -> U
}

struct Holder {
    d: Display
}

fn take(x: Display) -> String {
    "${x}"
}

fn use_int(c: Conv<Int>) -> Int {
    Conv.conv(c)
}

`
	for _, tc := range []struct {
		name, fn, message, help string
	}{
		{
			"parameter, no bound",
			"fn f<T>(v: T) -> String {\n    take(v)\n}\n",
			"argument 1: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Display`",
		},
		{
			"parameter, another bound",
			"fn f<T>(v: T) -> String where T: Hashable {\n    take(v)\n}\n",
			"argument 1: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Hashable and Display`",
		},
		{
			"binding",
			"fn f<T>(v: T) -> String {\n    d: Display = v\n    \"${d}\"\n}\n",
			"type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Display`",
		},
		{
			"result",
			"fn f<T>(v: T) -> Display {\n    v\n}\n",
			"return type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> Display where T: Display`",
		},
		{
			"list element",
			"fn f<T>(v: T) -> List<Display> {\n    [v]\n}\n",
			"list element type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> List<Display> where T: Display`",
		},
		{
			"struct field",
			"fn f<T>(v: T) -> Holder {\n    Holder{d: v}\n}\n",
			"field 'd' of Holder: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> Holder where T: Display`",
		},
		{
			"generic interface at other arguments",
			"fn f<T>(v: T) -> Int where T: Conv<String> {\n    use_int(v)\n}\n",
			"argument 1: expected Conv<Int>, got T",
			"require it in the signature: `fn f<T>(v: T) -> Int where T: Conv<String> and Conv<Int>`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.fn + "\nfn main() {\n}\n"
			errs := checkWithStdlib(src)
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if want := strings.Count(decls, "\n") + 2; e.Line != want {
					t.Errorf("at line %d, want %d", e.Line, want)
				}
				if len(e.Hints) == 0 || e.Hints[0] != tc.help {
					t.Fatalf("hints %q, want %q first", e.Hints, tc.help)
				}
				return
			}
			t.Fatalf("no %q error; got %v", tc.message, errs)
		})
	}
}
