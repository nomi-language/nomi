package analysis_test

import "testing"

// The declarations the anonymous-struct JSON tests share: a type with no
// JSON impl, one with both derived, and a bound and an interface-typed
// parameter for each interface.
const anonJsonDecls = `import std::json.{FromJson, Json, ToJson}

struct X
  n: Int
end

struct U
  name: String
end

derive ToJson for U
derive FromJson for U

fn enc<T>(v: T) -> Json where T: ToJson
  ToJson.to_json(v)
end

fn dec<T>(j: Json) -> Result<T, Json.ShapeError> where T: FromJson
  T.from_json(j)
end

fn put(v: ToJson) -> Json
  ToJson.to_json(v)
end
`

// An anonymous struct is ToJson and FromJson when each field is (spec §12
// *Tuples, anonymous structs and functions*): at a bound, as a ToJson
// value, through std's functions that take one, nested in another
// anonymous struct or a std container, and as a named struct's field
// under its derive.
func TestAnonStructJsonAdmitsWhatItsFieldsImplement(t *testing.T) {
	for _, stmt := range []string{
		`_ = enc({a: 1, b: "x"})`,
		`_ = enc({u: U{name: "a"}, tags: ["t"], at: Some(2)})`,
		`_ = enc({p: {x: 1, y: 2}})`,
		`_ = enc([{x: 1}])`,
		`_ = enc({"k" => {x: 1}})`,
		`_ = put({error: "no"})`,
		`_ = Json.encode_as({error: "no", n: Some(1)})`,
		`r: Result<{title: String, order: Maybe<Int>}, Json.ShapeError> = dec(Json.Null)
  _ = r`,
		`_ = Json.decode_as<{title: String, order: Maybe<Int>}>("{}")`,
		`_ = Json.decode_as<List<{p: {x: Int}, u: U}>>("[]")`,
		`_ = FromJson.from_json<{a: Int}>(Json.Null)`,
	} {
		t.Run(stmt, func(t *testing.T) {
			src := anonJsonDecls + "\nfn run() -> Unit\n  " + stmt + "\nend\n"
			fa := buildWithStdlibForDerive(src)
			if len(fa.TypeErrors) != 0 {
				t.Fatalf("the checker rejects %s:\n%s", stmt, errorsText(fa.TypeErrors))
			}
		})
	}
}

// A named struct whose field is an anonymous struct derives ToJson and
// FromJson through it.
func TestAnonStructJsonFieldOfADerivedStruct(t *testing.T) {
	src := anonJsonDecls + `
struct W
  at: {x: Int, y: Int}
  items: List<{id: Int}>
end

derive ToJson for W
derive FromJson for W

fn run() -> Unit
  _ = Json.encode_as(W{at: {x: 1, y: 2}, items: []})
  _ = Json.decode_as<W>("{}")
end
`
	fa := buildWithStdlibForDerive(src)
	if len(fa.TypeErrors) != 0 {
		t.Fatalf("the checker rejects a derive over an anonymous struct field:\n%s", errorsText(fa.TypeErrors))
	}
}

// An anonymous struct with a field that is not ToJson, or not FromJson,
// is refused where it enters, and the help names the field's type.
func TestAnonStructJsonRejectsAFieldWithoutTheImpl(t *testing.T) {
	cases := []struct {
		name, stmt, want string
	}{
		{"a ToJson parameter", `_ = put({at: X{n: 1}})`,
			"argument 1: expected ToJson, got {at: X}\n" +
				"help: `{at: X}` is ToJson when each field is, and `X` is not: add `derive ToJson` to `X` or write an `impl ToJson for X` block with `fn to_json(value: X) -> Json`"},
		{"std's ToJson parameter", `_ = Json.encode_as({a: 1, at: X{n: 1}})`,
			"argument 1: expected ToJson, got {a: Int, at: X}\n" +
				"help: `{a: Int, at: X}` is ToJson when each field is, and `X` is not"},
		{"a ToJson bound", `_ = enc({at: X{n: 1}})`,
			"{at: X} does not implement ToJson (required by `where T: ToJson`)\n" +
				"help: `{at: X}` is ToJson when each field is, and `X` is not"},
		{"a nested anonymous struct", `_ = enc({p: {at: X{n: 1}}})`,
			"{p: {at: X}} does not implement ToJson (required by `where T: ToJson`)\n" +
				"help: `{p: {at: X}}` is ToJson when each field is, and `{at: X}` is not: `{at: X}` is ToJson when each field is, and `X` is not"},
		{"a FromJson bound", `r: Result<{at: X}, Json.ShapeError> = dec(Json.Null)
  _ = r`,
			"{at: X} does not implement FromJson (required by `where T: FromJson`)\n" +
				"help: `{at: X}` is FromJson when each field is, and `X` is not: add `derive FromJson` to `X` or write an `impl FromJson for X` block with `fn from_json(json: Json) -> Result<X, Json.ShapeError>`"},
		{"std's FromJson bound", `_ = Json.decode_as<{at: X}>("{}")`,
			"{at: X} does not implement FromJson (required by `where T: FromJson`)\n" +
				"help: `{at: X}` is FromJson when each field is, and `X` is not"},
		// Tuples are not JSON: only an anonymous struct has names to key
		// an object by.
		{"a tuple", `_ = put((1, 2))`,
			"argument 1: expected ToJson, got (Int, Int)\n" +
				"help: no impl can be written for a tuple, which implements only Equatable, Hashable, Comparable and Display, each when every element does; declare a struct of your own that implements `ToJson`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := anonJsonDecls + "\nfn run() -> Unit\n  " + tc.stmt + "\nend\n"
			fa := buildWithStdlibForDerive(src)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.stmt, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
}

// A derive over a named struct holds each field of an anonymous struct
// field to the interface, and names the field.
func TestAnonStructJsonDeriveRejectsAFieldWithoutTheImpl(t *testing.T) {
	src := anonJsonDecls + `
struct W
  at: {x: X}
end

derive ToJson for W
`
	fa := buildWithStdlibForDerive(src)
	const want = "no impl of `ToJson` for `X`"
	if !hasErrSubstring(fa.TypeErrors, want) {
		t.Fatalf("the derive admits an anonymous struct field holding X, or rejects it with another message; want %q, got:\n%s",
			want, errorsText(fa.TypeErrors))
	}
}
