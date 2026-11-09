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

// A collection whose Contains impl holds only where its element meets a
// bound (`impl Contains for List<T> where T: Equatable`) and an element
// that misses it is one error: the element's missing impl, at the
// collection, naming the `in`. Not also the collection's missing Contains,
// which it has.
func TestIn_AnElementMissingTheImplsBoundIsOneError(t *testing.T) {
	const p = "struct P {\n    x: Int\n}\n\n"
	// An element type with equality meets List's and Vector's bound
	// (TestIn_AListOrVectorOfAStructWithNoImplsIsAccepted); one that holds
	// a function has none.
	const op = "struct P {\n    f: (Int) -> Int\n}\n\n"
	help := "`P` holds a function, an Iter or a handle to running state, which has no equality; write an `impl Equatable for P` block that leaves it out"
	equatable := "no impl of `Equatable` for `P` (required via membership test `in`) | " + help
	for name, tc := range map[string]struct {
		src  string
		want string
		line int
		col  int
	}{
		"a list":          {op + "fn f(p: P) -> Bool {\n    p in [p]\n}", equatable, 6, 10},
		"a vector":        {op + "fn f(p: P) -> Bool {\n    p in #[p]\n}", equatable, 6, 10},
		"a tuple element": {op + "fn f(p: P) -> Bool {\n    (1, p) in [(1, p)]\n}", equatable, 6, 15},
		"the call it stands for": {op + "fn f(p: P) -> Bool {\n    Contains.contains([p], p)\n}",
			"no impl of `Equatable` for `P` (required via interface-typed param/field) | " + help, 6, 23},
		"a program's impl with a bound": {p + `struct Bag<T> {
    items: List<T>
}

impl Contains for Bag<T> where T: Comparable {
    fn contains(c: Bag<T>, x: T) -> Bool {
        Iter.any_match(c.items, &(Comparable.compare(&1, x) == .Equal))
    }
}

fn f(b: Bag<P>, p: P) -> Bool {
    p in b
}`, "no impl of `Comparable` for `P` (required via membership test `in`) | add `derive Comparable` to `P` or write an `impl Comparable for P` block", 16, 10},
	} {
		fa, errs := checkSourceWithStdlib(tc.src + "\n")
		errs = append(errs, analysis.FinalizeCoherence(fa)...)
		var got []string
		for _, e := range errs {
			got = append(got, strings.Join(append([]string{e.Message}, e.Hints...), " | "))
		}
		if len(got) == 0 {
			t.Errorf("%s: the front end accepts this; want %q", name, tc.want)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: want the one error %q, got:\n%s", name, tc.want, strings.Join(got, "\n"))
			continue
		}
		if errs[0].Line != tc.line || errs[0].Col != tc.col {
			t.Errorf("%s: the error is at %d:%d, want %d:%d", name, errs[0].Line, errs[0].Col, tc.line, tc.col)
		}
	}
}

// A Set and a Map find a member by `==` and hash it structurally, so an
// element or key with no Equatable or Hashable impl of its own is looked
// for (spec §5 *Hashing and keys*).
func TestIn_ASetOrMapOfAStructWithNoImplsIsAccepted(t *testing.T) {
	const p = "struct P {\n    x: Int\n}\n\n"
	for name, src := range map[string]string{
		"a set":       p + "fn f(p: P) -> Bool {\n    p in %[p]\n}",
		"a map's key": p + "fn f(p: P) -> Bool {\n    p in %{p => 1}\n}",
	} {
		if got := inErrors(t, src); len(got) != 0 {
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(got, "\n"))
		}
	}
}

// A List or Vector finds a member by `==` too: their `Contains` impls bound
// the element by Equatable, which a struct with equality and no impl of its
// own meets (spec §5 *Equality bounds*).
func TestIn_AListOrVectorOfAStructWithNoImplsIsAccepted(t *testing.T) {
	const p = "struct P {\n    x: Int\n}\n\n"
	for name, src := range map[string]string{
		"a list":          p + "fn f(p: P) -> Bool {\n    p in [p]\n}",
		"a vector":        p + "fn f(p: P) -> Bool {\n    p in #[p]\n}",
		"a tuple element": p + "fn f(p: P) -> Bool {\n    (1, p) in [(1, p)]\n}",
		"the call":        p + "fn f(p: P) -> Bool {\n    Contains.contains([p], p)\n}",
		"List.contains":   p + "fn f(p: P) -> Bool {\n    List.contains([p], p)\n}",
	} {
		if got := inErrors(t, src); len(got) != 0 {
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(got, "\n"))
		}
	}
}
