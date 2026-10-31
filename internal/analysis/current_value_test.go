package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A bare `..` stands for the value already at a field or key of an update
// (current_value.go). Each case marks with ‸ where its one diagnostic is
// reported and gives the diagnostic's whole text, help lines included. A
// case fails when the source is admitted.

const currentValueDecls = `struct Address {
  street: String
  city: String
  zip: String = ""
}

struct Person {
  name: String
  address: Address
  nick: Maybe<Address>
  tags: List<String>
  recent: Vector<Int>
}

fn take(_p: Partial<Person>) -> Int {
  1
}

fn ada() -> Person {
  #Person{name: "Ada", address: #{street: "1 Main", city: "Bath"}, nick: None, tags: [], recent: #[]}
}

`

func TestCurrentValue_Diagnostics(t *testing.T) {
	const noValue = "a bare `..` has no value to update here\n" +
		"help: a bare `..` stands for the value already at a field or key of an update, as in `#{..user, address: #{.., city: \"NYC\"}}`; anywhere else, name the base: `#{..base, field: value}`"
	const empty = "an update with nothing after `..` changes nothing; leave this field or key out"
	cases := []struct{ name, body, want string }{
		{"the old implicit deep merge", `  _a = #{..ada(), address: ‸#{city: "NYC"}}`,
			"missing field 'street' of Address\n" +
				"help: a brace without `..` is a whole new Address; to change only the fields written, write `#{.., city: \"NYC\"}`"},
		{"the old implicit deep merge in Struct.update", `  _a = Struct.update(ada(), #{address: ‸#{city: "NYC"}})`,
			"missing field 'street' of Address\n" +
				"help: a brace without `..` is a whole new Address; to change only the fields written, write `#{.., city: \"NYC\"}`"},
		{"the old implicit deep merge at an anonymous-typed field", "  r = #{rec: #{a: 1, b: 2}}\n  _a = #{..r, rec: ‸#{a: 9}}",
			"field 'rec' of #{rec: #{a: Int, b: Int}}: expected #{a: Int, b: Int}, got #{a: Int}\n" +
				"help: a brace without `..` is a whole new #{a: Int, b: Int}; to change only the fields written, write `#{.., a: 9}`"},
		{"a plain literal", `  _a = #{‸.., name: "x"}`, noValue},
		{"a list element", `  _a = [#{‸.., name: "x"}]`, noValue},
		{"a map + operand", "  m = %{\"a\" => [1]}\n  _a = m + %{‸.., \"a\" => [2]}", noValue},
		{"a call argument", `  _a = Some(#{‸.., name: "x"})`, noValue},
		{"a plain list", `  _a = [1, ‸..]`, noValue},
		{"a plain vector", `  _a = #[‸.., 1]`, noValue},
		{"a plain set", `  _a = %[‸.., 1]`, noValue},
		{"a patch passed to another function's Partial<T>", `  _a = take(#{address: #{‸.., city: "x"}})`, noValue},
		{"inside a Maybe", `  _a = #{..ada(), nick: #{‸.., city: "x"}}`,
			"struct spread `..` requires a struct, got Maybe<Address>\n" +
				"help: a bare `..` cannot reach inside a Maybe, which may hold no value; write a whole value"},
		{"a missing key with a field that has no default", "  m = %{\"a\" => ada().address}\n  _a = %{..m, \"b\" => #{‸.., city: \"x\"}}",
			"a missing key leaves `..` no Address to update, and field 'street' has no default\n" +
				"help: when the key is missing the update builds a whole value from the fields written and the field defaults; write the other fields, or give them defaults"},
		{"a missing key two levels down", "  m = %{\"a\" => %{\"b\" => ada()}}\n  _a = %{..m, \"a\" => %{.., \"b\" => #{‸.., name: \"x\"}}}",
			"a missing key leaves `..` no Person to update, and fields 'address', 'nick', 'tags' and 'recent' have no default\n" +
				"help: when the key is missing the update builds a whole value from the fields written and the field defaults; write the other fields, or give them defaults"},
		{"a vector `..` at a list", `  _a = #{..ada(), tags: #[‸.., "x"]}`, "vector spread `..` requires a vector, got List<String>"},
		{"a list `..` at a vector", `  _a = #{..ada(), recent: [1, ‸..]}`, "list spread `..` requires a list, got Vector<Int>"},
		{"a vector element of the wrong type", `  _a = #{..ada(), recent: #[.., ‸"x"]}`, "vector element type mismatch: expected Int, got String"},
		{"an empty struct update", `  _a = #{..ada(), address: #{‸..}}`, empty},
		{"an empty vector update", `  _a = #{..ada(), recent: #[‸..]}`, empty},
		{"an empty list update", `  _a = #{..ada(), tags: [‸..]}`, empty},
		{"an empty map update", "  m = %{\"a\" => %{\"b\" => 1}}\n  _a = %{..m, \"a\" => %{‸..}}", empty},
		{"a set spread over an Int", `  _a = %[‸..1, "x"]`, "set spread `..` requires an Iter of the set's elements, got Int"},
		{"a named set spread with no elements", "  s = %[1]\n  _a = %[‸..s]", "a set spread with no elements is its base value; write `s`"},
		{"a set element of the wrong type", "  s = %[1]\n  _a = %[..s, ‸\"x\"]", "set spread element type mismatch: expected Int, got String"},
		{"a vector spread over a list of another element", `  _a = #[..#[1], ‸..ada().tags]`, "vector spread element type mismatch: expected Int, got String"},
		{"a last vector spread over an Int", "  n = 1\n  _a = #[2, ‸..n]", "vector spread `..` requires an Iter of the vector's elements, got Int"},
		{"a named vector spread with no elements", "  v = #[1]\n  _a = #[‸..v]", "a vector spread with no elements is its base value; write `v`"},
		{"a computed vector spread with no elements", `  _a = #[‸..ada().recent]`, "a vector spread with no elements is its base value; write the value itself"},
		{"a vector element after a spread of the wrong type", "  v = #[1]\n  _a = #[..v, ‸\"x\"]", "vector element type mismatch: expected Int, got String"},
		{"a vector element before a spread of the wrong type", "  v = #[1]\n  _a = #[‸\"x\", ..v]", "vector element type mismatch: expected Int, got String"},
		{"an empty vector spread against an annotation", "  _a: Vector<Int> = #[..#[], ‸\"x\"]", "vector element type mismatch: expected Int, got String"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := currentValueDecls + "fn main() {\n" + tc.body + "\n}\n"
			line, col := markerPos(t, src)
			src = strings.Replace(src, "‸", "", 1)
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end admits this")
			}
			for _, e := range errs {
				if e.Line == line && e.Col == col && diagText(e) == tc.want {
					if len(errs) != 1 {
						t.Fatalf("want one diagnostic, got %d: %s", len(errs), allText(errs))
					}
					return
				}
			}
			t.Fatalf("want at %d:%d\n  %s\ngot\n  %s", line, col, tc.want, allText(errs))
		})
	}
}

// The forms the rules admit, so a rule that refused too much fails here.
func TestCurrentValue_Admitted(t *testing.T) {
	for name, body := range map[string]string{
		"a struct field":                      `  _a = #{..ada(), address: #{.., city: "NYC"}}`,
		"Struct.update":                       `  _a = Struct.update(ada(), #{address: #{.., city: "NYC"}})`,
		"Struct.update in a pipeline":         `  _a = ada() |> Struct.update(#{address: #{.., city: "NYC"}})`,
		"Struct.update with named fields":     `  _a = Struct.update(updates: #{address: #{.., city: "NYC"}}, original: ada())`,
		"list, vector":                        `  _a = #{..ada(), tags: ["x", ..], recent: #[.., 1]}`,
		"a missing key of a defaulted struct": "  m = %{\"a\" => ada().address}\n  _a = %{..m, \"b\" => #{.., street: \"s\", city: \"x\"}}",
		"three deep":                          "  m = %{\"a\" => %{\"b\" => ada().address}}\n  _a = %{..m, \"a\" => %{.., \"b\" => #{.., street: \"s\", city: \"x\"}}}",
		"a whole value":                       `  _a = #{..ada(), address: #{street: "s", city: "c"}}`,
		"a named vector spread first":         "  v = #[1]\n  _a = #[..v, 2]",
		"a named vector spread last":          "  v = #[1]\n  _a = #[2, ..v]",
		"a computed vector spread":            `  _a = #[..ada().recent, 2]`,
		"an empty vector spread":              "  _a = #[..#[], 1]\n  _b: Vector<Maybe<Int>> = #[..#[], None]",
		"a vector spread of embedded values":  "  v: Vector<Maybe<Int>> = #[None]\n  _a = #[..v, Some(1), None]",
	} {
		t.Run(name, func(t *testing.T) {
			src := currentValueDecls + "fn main() {\n" + body + "\n}\n"
			if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
				t.Fatalf("the front end rejects this: %s", allText(errs))
			}
		})
	}
}

// A computed patch is checked by type, and its fields are whole values: a
// nested partial is not an Address.
func TestCurrentValue_ComputedPatchHoldsWholeValues(t *testing.T) {
	src := currentValueDecls + `fn main() {
  patch = #{address: #{city: "NYC"}}
  _a = Struct.update(ada(), patch)
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "expected Partial<Person>, got #{address: #{city: String}}") {
		t.Fatalf("want the Partial<Person> mismatch, got %s", allText(errs))
	}
}

func markerPos(t *testing.T, src string) (int, int) {
	t.Helper()
	at := strings.Index(src, "‸")
	if at < 0 {
		t.Fatal("no ‸ marker")
	}
	line := strings.Count(src[:at], "\n") + 1
	col := at - strings.LastIndex(src[:at], "\n")
	return line, col
}

func allText(errs []analysis.TypeError) string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = diagText(e)
	}
	return strings.Join(out, "\n  ")
}
