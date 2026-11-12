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

interface Shape
    fn area(s: self) -> Int
end

interface Conv<U>
    fn conv(v: self) -> U
end

struct Labeled
    label: Display
end

struct Box<T>
    item: T
end

typealias Both Display and Hashable

fn take(x: Display) -> String
    "${x}"
end

fn total(shapes: List<Shape>) -> Int
    shapes |> Iter.map(fn s -> Shape.area(s)) |> Iter.sum()
end

fn key(h: Hashable) -> Int
    Hashable.hash(h)
end

fn as_json(j: ToJson) -> String
    Json.encode(ToJson.to_json(j))
end

fn use_int(c: Conv<Int>) -> Int
    Conv.conv(c)
end

fn param<T>(v: T) -> String where T: Display
    take(v)
end

fn binding<T>(v: T) -> String where T: Display
    d: Display = v
    "${d}"
end

fn element<T>(v: T, w: T) -> Int where T: Shape
    total([v, w])
end

fn returned<T>(v: T) -> Display where T: Display
    v
end

fn field<T>(v: T) -> Labeled where T: Display
    Labeled{label: v}
end

fn several<T>(v: T) -> String where T: Hashable and Display
    "${key(v)} ${take(v)}"
end

fn aliased<T>(v: T) -> String where T: Both
    "${key(v)} ${take(v)}"
end

fn encoded<T>(v: T) -> String where T: ToJson
    as_json(v)
end

fn converted<T>(v: T) -> Int where T: Conv<Int>
    use_int(v)
end

impl Display for Box<T> where T: Display
    fn to_string(b: Box<T>) -> String
        take(b.item)
    end
end

fn main()
    _ = param(3)
end
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
	const decls = `interface Conv<U>
    fn conv(v: self) -> U
end

struct Holder
    d: Display
end

fn take(x: Display) -> String
    "${x}"
end

fn use_int(c: Conv<Int>) -> Int
    Conv.conv(c)
end

`
	for _, tc := range []struct {
		name, fn, message, help string
	}{
		{
			"parameter, no bound",
			"fn f<T>(v: T) -> String\n    take(v)\nend\n",
			"argument 1: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Display`",
		},
		{
			"parameter, another bound",
			"fn f<T>(v: T) -> String where T: Hashable\n    take(v)\nend\n",
			"argument 1: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Hashable and Display`",
		},
		{
			"binding",
			"fn f<T>(v: T) -> String\n    d: Display = v\n    \"${d}\"\nend\n",
			"type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> String where T: Display`",
		},
		{
			"result",
			"fn f<T>(v: T) -> Display\n    v\nend\n",
			"return type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> Display where T: Display`",
		},
		{
			"list element",
			"fn f<T>(v: T) -> List<Display>\n    [v]\nend\n",
			"list element type mismatch: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> List<Display> where T: Display`",
		},
		{
			"struct field",
			"fn f<T>(v: T) -> Holder\n    Holder{d: v}\nend\n",
			"field 'd' of Holder: expected Display, got T",
			"require it in the signature: `fn f<T>(v: T) -> Holder where T: Display`",
		},
		{
			"generic interface at other arguments",
			"fn f<T>(v: T) -> Int where T: Conv<String>\n    use_int(v)\nend\n",
			"argument 1: expected Conv<Int>, got T",
			"require it in the signature: `fn f<T>(v: T) -> Int where T: Conv<String> and Conv<Int>`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.fn + "\nfn main() end\n"
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
