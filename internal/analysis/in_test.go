package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `x in c` is the call `Contains.contains(c, x)`: it checks wherever that
// call does, and a mismatch names the operand of the test.

// inErrors checks src and answers its errors as "message | hint | hint".
func inErrors(t *testing.T, src string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(src + "\n")
	var out []string
	for _, e := range errs {
		out = append(out, strings.Join(append([]string{e.Message}, e.Hints...), " | "))
	}
	return out
}

// Every std collection with a Contains impl answers `in`, at the element its
// impl takes, and so do a program's own impl and a type parameter bounded by
// Contains. The test is a Bool.
func TestIn_Accepted(t *testing.T) {
	for name, src := range map[string]string{
		"a list":             `fn f(x: Int) -> Bool { x in [1, 2] }`,
		"a vector":           `fn f(x: Int) -> Bool { x in #[1, 2] }`,
		"a set":              `fn f(x: Int) -> Bool { x in %[1, 2] }`,
		"a map's key":        `fn f(k: String) -> Bool { k in %{"a" => 1} }`,
		"a range":            `fn f(x: Int) -> Bool { x in 1..10 }`,
		"an inclusive range": `fn f(x: Int) -> Bool { x in 1..=10 }`,
		"bytes":              `fn f(b: Byte, bs: Bytes) -> Bool { b in bs }`,
		"a substring":        `fn f(s: String) -> Bool { "ab" in s }`,
		"a codepoint":        `fn f(s: String) -> Bool { 'a' in s }`,
		"under not":          `fn f(x: Int) -> Bool { not x in [1] and x > 0 }`,
		"a pipe's result":    `fn f(xs: List<Int>) -> Bool { 3 in xs |> Iter.to_set() }`,
		"in a capture":       `fn f(xs: List<Int>, seen: Set<Int>) -> Int { xs |> Iter.reject(&(&1 in seen)) |> Iter.count() }`,
		"a bound": `fn f<C, T>(c: C, x: T) -> Bool where C: Contains<T> {
    x in c
}`,
		"a program's impl": `struct Shelf {
    names: List<String>
}

impl Contains<String> for Shelf {
    fn contains(c: Shelf, x: String) -> Bool {
        x in c.names
    }
}

fn f(s: Shelf) -> Bool {
    "a" in s
}`,
		"a Bool where one is expected": `fn f(x: Int) -> Int {
    if x in [1, 2] { 1 } else { 2 }
}`,
	} {
		if got := inErrors(t, src); len(got) != 0 {
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(got, "\n"))
		}
	}
}

// A collection with no Contains impl, a value its impl does not take, and a
// String's three impls, none taking the value: each names the test.
func TestIn_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"no impl": {`struct Bag {
    items: List<Int>
}

fn f(b: Bag) -> Bool {
    1 in b
}`, "the collection `in` looks in: Bag does not implement Contains"},
		"an Int":            {`fn f(x: Int) -> Bool { x in 5 }`, "the collection `in` looks in: Int does not implement Contains"},
		"the wrong element": {`fn f(s: String) -> Bool { s in [1, 2] }`, "the value `in` looks for: expected Int, got String"},
		"a map's value":     {`fn f() -> Bool { 1 in %{"a" => 1} }`, "the value `in` looks for: expected String, got Int"},
		"a String's impls":  {`fn f(s: String) -> Bool { 1 in s }`, "`in` for `String` has impls at `String`, `Regex<C>` and `Codepoint`, and none takes `Int`"},
		"a bool's use":      {`fn f(x: Int) -> Int { x in [1] }`, "return type mismatch: expected Int, got Bool"},
	} {
		got := inErrors(t, tc.src)
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the front end accepts this; want %q, got:\n%s", name, tc.want, strings.Join(got, "\n"))
		}
	}
}

// Map's Contains tests keys, and the test's call is
// `Contains.contains(c, x)` over the test's own operands, which the checker
// typed.
func TestIn_TheCallItStandsFor(t *testing.T) {
	fa, errs := checkSourceWithStdlib("fn f(k: String) -> Bool {\n    k in %{\"a\" => 1}\n}\n")
	if len(errs) != 0 {
		t.Fatalf("the front end rejects this: %v", errs)
	}
	if len(fa.Memberships) != 1 {
		t.Fatalf("want one membership test, got %d", len(fa.Memberships))
	}
	for test, call := range fa.Memberships {
		if len(call.Args) != 2 || call.Args[0] != test.Right || call.Args[1] != test.Left {
			t.Fatalf("the call's arguments are not the test's collection and value: %#v", call.Args)
		}
		fn, ok := call.Func.(*ast.FieldAccess)
		if !ok || fn.Field.Name != "contains" {
			t.Fatalf("the callee is %#v, want Contains.contains", call.Func)
		}
		if got := analysis.ResolveTypeVar(fa.ExprTypes[test]); got != analysis.TypeBool {
			t.Errorf("the test's type is %v, want Bool", got)
		}
		if got := analysis.ResolveTypeVar(fa.ExprTypes[test.Right]).String(); got != "Map<String, Int>" {
			t.Errorf("the collection's type is %s", got)
		}
	}
}
