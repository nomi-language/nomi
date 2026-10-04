package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A brace literal with no type name and no spread builds the nominal struct
// its position expects (checkTargetTypedStructLit). These tables pin every
// position that carries an expected struct type, the messages a partial or
// wrong literal reports there, and the positions that do not guess.

const targetTypedDecls = `struct Address {
  street: String
  city: String
  zip: String = "00000"
}
struct Person {
  name: String
  address: Address
}
struct Box<T> {
  item: T
}
enum Place {
  Home(Address)
  Nowhere
}
fn show(a: Address): String { a.city }
fn unbox<T>(b: Box<T>): T { b.item }
fn ident<T>(x: T): T { x }
`

// targetStructNames lists the struct names TargetStructs recorded, so a test
// can say which literals were target-typed.
func targetStructNames(fa *analysis.FileAnalysis) []string {
	var out []string
	for _, st := range fa.TargetStructs {
		out = append(out, st.String())
	}
	return out
}

func TestTargetTypedStruct_Positions(t *testing.T) {
	cases := []struct{ name, src string }{
		{"annotated binding", `fn main() {
  a: Address = {street: "1 Main", city: "Bath"}
  _s = show(a)
}
`},
		{"once", `once home: Address = {street: "1 Main", city: "Bath"}
fn main() {
  _s = show(home)
}
`},
		{"struct field in a named literal", `fn main() {
  _p = Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}
}
`},
		{"nested, through an annotated binding", `fn main() {
  _p: Person = {name: "Ada", address: {street: "1 Main", city: "Bath"}}
}
`},
		{"function argument", `fn main() {
  _s = show({street: "1 Main", city: "Bath"})
}
`},
		{"variant constructor argument", `fn main() {
  _p = Place.Home({street: "1 Main", city: "Bath"})
}
`},
		{"prelude constructor under an annotation", `fn main() {
  _m: Maybe<Address> = Some({street: "1 Main", city: "Bath"})
}
`},
		{"return value", `fn home(): Address { {street: "1 Main", city: "Bath"} }
fn main() {
  _s = show(home())
}
`},
		{"explicit return", `fn home(n: Int): Address {
  if n > 0 {
    return {street: "1 Main", city: "Bath"}
  }
  {street: "2 Main", city: "Bath"}
}
fn main() {
  _s = show(home(1))
}
`},
		{"list element", `fn main() {
  _people: List<Person> = [{name: "Ada", address: {street: "1 Main", city: "Bath"}}]
}
`},
		{"set element", `fn main() {
  _s: Set<Address> = #{{street: "1 Main", city: "Bath"}}
}
`},
		{"map value", `fn main() {
  _m: Map<String, Address> = {"home" => {street: "1 Main", city: "Bath"}}
}
`},
		{"case arm", `fn pick(n: Int): Address {
  case n {
    0 -> {street: "a", city: "Zero"}
    _ -> {street: "b", city: "Many"}
  }
}
fn main() {
  _s = show(pick(0))
}
`},
		{"if branch", `fn pick(b: Bool): Address {
  if b { {street: "a", city: "Yes"} } else { {street: "b", city: "No"} }
}
fn main() {
  _s = show(pick(True))
}
`},
		{"generic struct with a known argument", `fn main() {
  b: Box<Int> = {item: 3}
  _n: Int = b.item
}
`},
		{"generic struct with a struct argument", `fn main() {
  b: Box<Address> = {item: {street: "1 Main", city: "Bath"}}
  _s = show(b.item)
}
`},
		{"generic callee infers the argument", `fn main() {
  _n: Int = unbox({item: 3})
}
`},
		{"a default fills in", `fn main() {
  a: Address = {street: "1 Main", city: "Bath"}
  _z: String = a.zip
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(targetTypedDecls + tc.src)
			if len(errs) != 0 {
				t.Fatalf("the front end rejects a target-typed literal: %v", errs)
			}
			if len(fa.TargetStructs) == 0 {
				t.Fatal("no brace literal was recorded as target-typed")
			}
		})
	}
}

func TestTargetTypedStruct_ErrorsNameTheNominalType(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"missing field at a binding", `  _a: Address = {city: "Bath"}`, "missing field 'street' of Address"},
		{"wrong field type at a binding", `  _a: Address = {street: "s", city: 3}`, "field 'city' of Address: expected String, got Int"},
		{"unknown field at a binding", `  _a: Address = {street: "s", city: "c", postcode: "z"}`, "Address has no field 'postcode'"},
		{"missing field at a struct field", `  _p = Person{name: "Ada", address: {city: "NYC"}}`, "missing field 'street' of Address"},
		{"missing field at an argument", `  _s = show({city: "NYC"})`, "missing field 'street' of Address"},
		{"missing field at a list element", `  _l: List<Address> = [{city: "NYC"}]`, "missing field 'street' of Address"},
		{"wrong type in a generic struct", `  _b: Box<Int> = {item: "s"}`, "field 'item' of Box: expected Int, got String"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(targetTypedDecls + "fn main() {\n" + tc.body + "\n}\n")
			if len(errs) == 0 {
				t.Fatalf("the front end accepts a wrong target-typed literal; want %q", tc.want)
			}
			expectExactlyOne(t, errs, tc.want)
		})
	}
}

func TestTargetTypedStruct_ReturnPositionMissingField(t *testing.T) {
	_, errs := checkSourceWithStdlib(targetTypedDecls + `fn home(): Address { {city: "York"} }
fn main() {
  _s = show(home())
}
`)
	expectExactlyOne(t, errs, "missing field 'street' of Address")
}

// Where nothing names one struct to build, the literal stays anonymous.
func TestTargetTypedStruct_DoesNotGuess(t *testing.T) {
	t.Run("no expected type", func(t *testing.T) {
		fa, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  r = {street: "s", city: "c"}
  _s: String = r.street
}
`)
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if names := targetStructNames(fa); len(names) != 0 {
			t.Fatalf("an unexpected literal was target-typed: %v", names)
		}
	})
	t.Run("a type parameter", func(t *testing.T) {
		fa, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  r = ident({street: "s", city: "c"})
  _s: String = r.street
}
`)
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if names := targetStructNames(fa); len(names) != 0 {
			t.Fatalf("a literal at a type parameter was target-typed: %v", names)
		}
	})
	t.Run("an anonymous struct type", func(t *testing.T) {
		fa, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  _r: {street: String, city: String} = {street: "s", city: "c"}
}
`)
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if names := targetStructNames(fa); len(names) != 0 {
			t.Fatalf("a literal at an anonymous struct type was target-typed: %v", names)
		}
	})
	t.Run("an interface", func(t *testing.T) {
		_, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  _d: Display = {street: "s", city: "c"}
}
`)
		expectExactlyOne(t, errs, "expected Display, got {street: String, city: String}")
	})
	t.Run("an enum", func(t *testing.T) {
		_, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  _m: Maybe<Address> = {street: "s", city: "c"}
}
`)
		if len(errs) == 0 {
			t.Fatal("a brace literal was accepted as a Maybe<Address>")
		}
		if !strings.Contains(errs[0].Message, "{street: String, city: String}") {
			t.Fatalf("want the anonymous struct type in the mismatch, got %v", errs)
		}
	})
}

// Under a spread a bare brace at a struct-typed field is a patch of the base's
// value; outside one it builds the field's struct. The two rules must not
// reach each other's positions.
func TestTargetTypedStruct_SpreadPatchWins(t *testing.T) {
	fa, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  p = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}
  _moved = {..p, address: {city: "NYC"}}
}
`)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a nested patch under a spread: %v", errs)
	}
	for lit := range fa.TargetStructs {
		if len(lit.Fields) == 1 && lit.Fields[0].Name == "city" {
			t.Fatal("the patch `{city: \"NYC\"}` was recorded as a target-typed Address")
		}
	}
}

func TestTargetTypedStruct_SpreadValueAtAField(t *testing.T) {
	// A complete brace under a spread is still a patch (it names every
	// field), and a nested spread is a value of the field's type.
	_, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  p = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}
  _a = {..p, address: {street: "2 Main", city: "York"}}
  _b = {..p, address: {..p.address, city: "York"}}
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestTargetTypedStruct_RecordsTheNominalType(t *testing.T) {
	fa, errs := checkSourceWithStdlib(targetTypedDecls + `fn main() {
  _b: Box<Int> = {item: 3}
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var got []string
	for lit, st := range fa.TargetStructs {
		if lit.TypeName != nil {
			t.Fatalf("a named literal was recorded: %v", lit.TypeName)
		}
		got = append(got, st.String())
		if ty := fa.ExprTypes[ast.Node(lit)]; ty == nil || ty.String() != "Box<Int>" {
			t.Fatalf("the literal's type is %v, want Box<Int>", ty)
		}
	}
	if len(got) != 1 || got[0] != "Box<Int>" {
		t.Fatalf("TargetStructs = %v, want [Box<Int>]", got)
	}
}

func TestTargetTypedStruct_OpaqueFromOutsideIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn forge(): Counter {
  {value: -999, increments: 0}
}
`))
	want := "constructor of opaque type 'Counter' is private to its defining module"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("want %q, got %v", want, errs)
}

func TestTargetTypedStruct_TransparentFromOutsideIsBuilt(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import open.Open

fn make(): Open {
  {a: 1, b: 2}
}
`))
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a target-typed literal of an imported struct: %v", errs)
	}
}

func TestTargetTypedStruct_OpaqueInsideItsFileIsBuilt(t *testing.T) {
	_, errs := checkSourceWithStdlib(`opaque struct Counter {
  value: Int
}
fn new(): Counter { {value: 0} }
fn main() {
  _c = new()
}
`)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a target-typed opaque literal in its own file: %v", errs)
	}
}
