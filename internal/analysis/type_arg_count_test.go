package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// typeArgCountDecls declares one type of each kind with no type parameters,
// and two generic ones, for TestTypeArgCount_*.
const typeArgCountDecls = `import std::duration.Duration

struct S
    x: Int
end

enum E
    A
end

type D Int

refine Small Int when fn n -> n < 10

typealias Al Int

typealias IntBox Box<Int>

typealias Pairs<T> List<(T, T)>

interface I
    fn name(v: self) -> String
end

struct Box<T>
    inner: T
end

struct Two<A, B>
    a: A
    b: B
end

struct Pair<A, B = String>
    a: A
    b: B
end

fn id<T>(t: T) -> T
    t
end

fn keep(_d: Duration) -> Int
    0
end

`

// positionOf is the 1-based line and column at which the first occurrence
// of at begins in src.
func positionOf(t *testing.T, src, at string) (int, int) {
	t.Helper()
	i := strings.Index(src, at)
	if i < 0 {
		t.Fatalf("%q is not in the source", at)
	}
	line := strings.Count(src[:i], "\n") + 1
	col := i - strings.LastIndex(src[:i], "\n")
	return line, col
}

// TestTypeArgCount_RejectedAtTheUse holds the one arity rule: a use of a
// type writes as many type arguments as the type takes, whatever kind of
// type it is and wherever the type is written. Each case's error is the
// one checkArity builds, with its code, at the type's name.
func TestTypeArgCount_RejectedAtTheUse(t *testing.T) {
	cases := []struct {
		name, src, at, want string
	}{
		// Each kind of type with no type parameters.
		{"struct, parameter", "fn f(_s: S<Int>) -> Int\n    0\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"enum, parameter", "fn f(_e: E<Int>) -> Int\n    0\nend\n", "E<Int>", "`E` expects 0 type arguments, got 1"},
		{"distinct type, return type", "fn f() -> D<Int>\n    D(1)\nend\n", "D<Int>", "`D` expects 0 type arguments, got 1"},
		{"refinement, as", "fn f() -> Int\n    1 as Small<Int>\nend\n", "Small<Int>", "`Small` expects 0 type arguments, got 1"},
		{"typealias, field", "struct H\n    a: Al<Int>\nend\n", "Al<Int>", "`Al` expects 0 type arguments, got 1"},
		{"typealias of an instance, parameter", "fn f(_b: IntBox<Int>) -> Int\n    0\nend\n", "IntBox<Int>", "`IntBox` expects 0 type arguments, got 1"},
		{"interface, value type", "fn f(_i: I<Int>) -> Int\n    0\nend\n", "I<Int>", "`I` expects 0 type arguments, got 1"},
		{"interface, where bound", "fn f<T>(_t: T) -> Int where T: I<Int>\n    0\nend\n", "I<Int>", "`I` expects 0 type arguments, got 1"},
		{"interface, impl header", "impl I<Int> for S\n    fn name(_v: S) -> String\n        \"s\"\n    end\nend\n", "I<Int>", "`I` expects 0 type arguments, got 1"},
		{"primitive Int, binding annotation", "fn f() -> Int\n    x: Int<Int> = 1\n    x\nend\n", "Int<Int>", "`Int` expects 0 type arguments, got 1"},
		{"primitive String, parameter", "fn f(_s: String<Int>) -> Int\n    0\nend\n", "String<Int>", "`String` expects 0 type arguments, got 1"},
		{"std type, parameter", "fn f(_d: Duration<Int>) -> Int\n    0\nend\n", "Duration<Int>", "`Duration` expects 0 type arguments, got 1"},
		{"type parameter, parameter", "fn f<T>(_t: T<Int>) -> Int\n    0\nend\n", "T<Int>", "`T` expects 0 type arguments, got 1"},

		// Each position a type is written in.
		{"type argument of another type", "fn f(_s: List<S<Int>>) -> Int\n    0\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"enum variant payload", "enum W\n    V S<Int>\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"distinct type's inner type", "type W S<Int>\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"typealias target", "typealias W S<Int>\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"a distinct type over itself", "type W W<Int>\n", "W<Int>", "`W` expects 0 type arguments, got 1"},
		{"inherent impl receiver", "impl S<Int>\n    fn one() -> Int\n        1\n    end\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"interface impl receiver", "impl Display for S<Int>\n    fn to_string(v: S) -> String\n        \"${v.x}\"\n    end\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"turbofish", "fn f() -> Int\n    s = id<S<Int>>(S{x: 1})\n    s.x\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"a parameter written as its type alone", "fn f(S<Int>) -> Int\n    0\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"a destructured parameter's type", "fn f(S{x:}: S<Int>) -> Int\n    x\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"lambda parameter", "fn f() -> Int\n    g = fn s: S<Int> -> s.x\n    g(S{x: 1})\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},
		{"interface function signature", "interface J\n    fn j(v: self, s: S<Int>) -> Int\nend\n", "S<Int>", "`S` expects 0 type arguments, got 1"},

		// A generic type given too few or too many.
		{"generic, too many", "fn f(_b: Box<Int, Int>) -> Int\n    0\nend\n", "Box<Int, Int>", "`Box` expects 1 type argument, got 2"},
		{"generic, too few written", "fn f(_t: Two<Int>) -> Int\n    0\nend\n", "Two<Int>", "`Two` expects 2 type arguments, got 1"},
		{"generic, too few in an as", "fn f() -> Int\n    t = Two{a: 1, b: 2} as Two<Int>\n    t.a\nend\n", "Two<Int>", "`Two` expects 2 type arguments, got 1"},
		{"generic with a default, too many", "fn f(_p: Pair<Int, Int, Int>) -> Int\n    0\nend\n", "Pair<Int, Int, Int>", "`Pair` expects 1 to 2 type arguments, got 3"},
		{"generic typealias, too many", "fn f(_p: Pairs<Int, Int>) -> Int\n    0\nend\n", "Pairs<Int, Int>", "`Pairs` expects 1 type argument, got 2"},
		{"List, too many", "fn f(_l: List<Int, Int>) -> Int\n    0\nend\n", "List<Int, Int>", "`List` expects 1 type argument, got 2"},
		{"Map, too few", "fn f(_m: Map<Int>) -> Int\n    0\nend\n", "Map<Int>", "`Map` expects 2 type arguments, got 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := typeArgCountDecls + tc.src
			line, col := positionOf(t, src[len(typeArgCountDecls):], tc.at)
			line += strings.Count(typeArgCountDecls, "\n")
			errs := diagnosticsFor(t, src)
			var got []string
			found := false
			for _, e := range errs {
				got = append(got, fmt.Sprintf("%d:%d %s [%s]", e.Line, e.Col, e.Message, e.Code))
				if e.Message == tc.want && e.Line == line && e.Col == col && e.Code == analysis.TypeArgCountCode {
					found = true
				}
			}
			if !found {
				t.Errorf("want %q [%s] at %d:%d; got:\n  %s", tc.want, analysis.TypeArgCountCode, line, col, strings.Join(got, "\n  "))
			}
		})
	}
}

// TestTypeArgCount_LegalUsesStayLegal is the other half: uses whose count
// the type takes, or whose omitted arguments a default or inference fills,
// report no arity error.
func TestTypeArgCount_LegalUsesStayLegal(t *testing.T) {
	cases := []struct{ name, src string }{
		{"a type with none, written bare", "fn f(s: S, e: E, d: D, i: I) -> Int\n    _ = (s, e, d, i)\n    0\nend\n"},
		{"a typealias of an instance, written bare", "fn f(b: IntBox) -> Int\n    b.inner\nend\n"},
		{"a generic typealias with its argument", "fn f(p: Pairs<Int>) -> Int\n    List.length(p)\nend\n"},
		{"a default fills the rest", "fn f(p: Pair<Int>) -> Int\n    p.a\nend\n"},
		{"every argument written", "fn f(p: Pair<Int, Int>, t: Two<Int, String>, b: Box<S>) -> Int\n    p.a + t.a + b.inner.x\nend\n"},
		{"a struct literal infers its arguments", "fn f() -> Int\n    b = Box{inner: 1}\n    b.inner\nend\n"},
		{"a turbofish with the arguments the type takes", "fn f() -> Int\n    b = id<Box<Int>>(Box{inner: 1})\n    b.inner\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, e := range diagnosticsFor(t, typeArgCountDecls+tc.src) {
				if e.Code == analysis.TypeArgCountCode || strings.Contains(e.Message, "type argument") {
					t.Errorf("a legal use reported an arity error: %d:%d %s", e.Line, e.Col, e.Message)
				}
			}
		})
	}
}
