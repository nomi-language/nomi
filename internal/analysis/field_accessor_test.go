package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// fieldAccessorDecls declares what the field accessor tests read from. The
// checker tests run without the prelude (checkSource), so the generic
// callers are declared here: `apply` takes the value first, so its type
// parameter is pinned before the accessor is checked, and `pick` puts a
// defaulted parameter between the value and the function, as Iter.sort_by
// does.
const fieldAccessorDecls = `struct Address {
  city: String
}

struct User {
  name: String
  age: Int
  address: Address
  greet: (String) -> String
}

struct Box<T> {
  value: T
}

enum Color {
  Red
  Blue
}

fn apply<A, B>(x: A, f: (A) -> B): B {
  f(x)
}

fn apply_first<A, B>(f: (A) -> B, x: A): B {
  f(x)
}

fn pick<T, K>(x: T, flip: Int = 0, key: (T) -> K): K {
  key(x)
}

fn user(): User {
  User{name: "Ann", age: 3, address: Address{city: "Bath"}, greet: |s| s}
}
`

func TestFieldAccessor_Accepted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"argument whose parameter an earlier argument pinned", `fn f(): String {
  apply(user(), .name)
}`},
		{"pipe stage argument, pinned by the piped value", `fn f(): Int {
  user() |> apply(.age)
}`},
		{"trailing argument routed past a defaulted parameter", `fn f(): Int {
  pick(user(), .age)
}`},
		{"annotated binding", `fn f(): Int {
  get: (User) -> Int = .age
  get(user())
}`},
		{"return position", `fn f(): (User) -> String {
  .name
}`},
		{"generic struct through the expected type", `fn f(b: Box<Address>): Address {
  apply(b, .value)
}`},
		{"chain", `fn f(): String {
  apply(user(), .address.city)
}`},
		{"chain through a generic struct", `fn f(b: Box<Address>): String {
  apply(b, .value.city)
}`},
		{"field holding a function is returned, not called", `fn f(): String {
  g = apply(user(), .greet)
  g("hi")
}`},
		{"tuple index", `fn f(p: (Int, String)): String {
  apply(p, .1)
}`},
		{"anonymous struct", `fn f(p: {x: Int, y: Int}): Int {
  apply(p, .y)
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(fieldAccessorDecls + tc.body)
			expectNoErrors(t, errs)
		})
	}
}

// The accessor's type is (S) -> F, recorded with the type it reads from for
// the IR builder.
func TestFieldAccessor_TypeAndRecordedParam(t *testing.T) {
	fa, errs := checkSource(fieldAccessorDecls + `fn f(): String {
  apply(user(), .address.city)
}`)
	expectNoErrors(t, errs)
	var acc *ast.FieldAccessor
	for n := range fa.FieldAccessors {
		acc = n
	}
	if acc == nil || len(fa.FieldAccessors) != 1 {
		t.Fatalf("FieldAccessors = %v, want the one accessor", fa.FieldAccessors)
	}
	if got := fa.FieldAccessors[acc].String(); got != "User" {
		t.Errorf("recorded parameter type = %s, want User", got)
	}
	if got := fa.ExprTypes[acc]; got == nil || ResolveTypeVar(got).String() != "(User) -> String" {
		t.Errorf("accessor type = %v, want (User) -> String", got)
	}
}

func TestFieldAccessor_Rejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no expected type", `fn f() {
  a = .name
  a
}`, "`.name` reads a field only where a function is expected; write `x.name` to read it from a value"},
		{"a non-function expected type", `fn f(): String {
  .name
}`, "`.name` reads a field only where a function is expected, and String is expected here; write `x.name` to read it from a value"},
		{"pipe stage", `fn f(): String {
  user() |> .name
}`, "`.name` is not a pipe stage; read the field from the value instead, as in `x.name`"},
		{"parameter type not known yet", `fn f(): String {
  apply_first(.name, user())
}`, "`.name` needs the record type it reads from; annotate the binding, e.g. `f: (User) -> String = .name`"},
		{"unknown field, with a suggestion", `fn f(): String {
  apply(user(), .nmae)
}`, "struct 'User' has no field 'nmae'\nhelp: did you mean 'name'?"},
		{"unknown field in a chain", `fn f(): String {
  apply(user(), .address.town)
}`, "struct 'Address' has no field 'town'"},
		{"not a struct", `fn f(): Int {
  apply(3, .name)
}`, "`.name` reads a field of a struct, a record or a tuple, and Int is none of those"},
		{"an enum", `fn f(): Int {
  apply(Color.Red, .name)
}`, "`.name` reads a field of a struct, a record or a tuple, and Color is none of those"},
		{"a function of two arguments", `fn f() {
  g: (User, User) -> String = .name
  g
}`, "`.name` is a function of one argument, and the function expected here takes 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(fieldAccessorDecls + tc.body)
			for _, e := range errs {
				if diagText(e) == tc.want {
					return
				}
			}
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			t.Fatalf("want the error %q, got:\n  %s", tc.want, strings.Join(msgs, "\n  "))
		})
	}
}
