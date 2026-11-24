package irbuild

import (
	"fmt"
	"strings"
	"testing"
)

// `Hashable.hash` over a record hashes alike whatever order its fields are
// written in, directly and through a generic bound, and `Equatable.equal`
// over two tuples or two records is `==`, called directly and through a
// generic bound.
func TestIRStructuralHashAndEqual_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    Equatable.equal(a, b)
end

fn h<T>(a: T) -> Int where T: Hashable
    Hashable.hash(a)
end

fn main()
    io.inspect(Hashable.hash({name: "r", n: 1}) == Hashable.hash({n: 1, name: "r"}))
    io.inspect(Hashable.hash({name: "r", n: 1}) == Hashable.hash({n: 2, name: "r"}))
    io.inspect(Hashable.hash({name: "r", n: 1}) == h({n: 1, name: "r"}))
    io.inspect(h({name: "r", n: 1}) == Hashable.hash({name: "r", n: 1}))
    io.inspect(same((1, 2), (1, 2)))
    io.inspect(same((1, 2), (1, 3)))
    io.inspect(same({a: 1}, {a: 1}))
    io.inspect(Equatable.equal((1, "x"), (1, "x")))
    io.inspect(Equatable.equal({a: 1, b: (2, 3)}, {a: 1, b: (2, 4)}))
end
`
	irRunSource(t, src, "True\nFalse\nTrue\nTrue\nTrue\nFalse\nTrue\nTrue\nFalse\n")
}

// `Option.equal` is `==` over a payload whose own impl decides (a struct
// with a derived Equatable) and over two instances an embedded value tells
// apart.
func TestIROptionEqualAsTheOperator_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Inner
    a: Int
end

derive Equatable for Inner

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

derive Equatable for Circle
derive Equatable for Shape

fn main()
    c = Circle{r: 1}
    s: Shape = Shape.Dot
    io.print(Option.equal(Some(Inner{a: 1}), None))
    io.print(Option.equal(Some(Inner{a: 1}), Some(Inner{a: 1})))
    io.print(Option.equal(Some(c), Some(s)))
    io.print(Option.equal(Some(c), Some(Shape.Circle{r: 1})))
    r: Result<Inner, String> = Ok(Inner{a: 2})
    io.print(Result.equal(r, Ok(Inner{a: 2})))
end
`
	irRunSource(t, src, "False\nTrue\nFalse\nTrue\nTrue\n")
}

// A call of Equatable's equal, Hashable's hash or Comparable's compare
// qualified by the type (`Option.equal(a, b)`, `Decimal.compare(a, b)`)
// answers what the operator or the interface-qualified call answers, over
// every prelude container and std type with an impl, a derived struct,
// enum and distinct type, operands of two instances an embedded value
// tells apart, and a bare `None`. `Decimal.divide(a, b)` is Divide's
// function, not Decimal's own four-parameter one. Each line prints True.
// A tuple has no name to qualify a call by, so it has no row.
func TestIRTypeQualifiedOperatorCalls_AgreeWithTheOperator(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const shapes = `struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

derive Equatable for Circle
derive Equatable for Shape
`
	const derived = `derive Equatable for %[1]s
derive Hashable for %[1]s
derive Comparable for %[1]s
`
	rows := []struct {
		name, decls, setup string
		lines              []string
	}{
		{"Option", "", "", []string{
			"Option.equal(Some(1), Some(2)) == (Some(1) == Some(2))",
			"Option.equal(Some(1), Some(1)) == (Some(1) == Some(1))",
			"Option.equal(Some(1), None) == (Some(1) == None)",
			"Option.equal(None, Some(1)) == (None == Some(1))",
			"Option.hash(Some(1)) == Hashable.hash(Some(1))",
		}},
		{"Result", "", "ok: Result<Int, String> = Ok(1)\nerr: Result<Int, String> = Err(\"x\")", []string{
			`Result.equal(ok, err) == (ok == err)`,
			`Result.equal(ok, Ok(1)) == (ok == Ok(1))`,
			`Result.hash(ok) == Hashable.hash(ok)`,
		}},
		{"List", "", "", []string{
			"List.equal([1, 2], [1, 3]) == ([1, 2] == [1, 3])",
			"List.hash([1, 2]) == Hashable.hash([1, 2])",
			"List.compare([1, 2], [1, 3]) == Comparable.compare([1, 2], [1, 3])",
			"(List.compare([1, 2], [1, 3]) == Ordering.Less) == ([1, 2] < [1, 3])",
		}},
		{"Vector", "", "", []string{
			"Vector.equal(Vector[1, 2], Vector[1, 3]) == (Vector[1, 2] == Vector[1, 3])",
			"Vector.hash(Vector[1, 2]) == Hashable.hash(Vector[1, 2])",
			"Vector.compare(Vector[1, 2], Vector[1, 3]) == Comparable.compare(Vector[1, 2], Vector[1, 3])",
			"(Vector.compare(Vector[2], Vector[1, 3]) == Ordering.Greater) == (Vector[2] > Vector[1, 3])",
		}},
		{"Set", "", "", []string{
			"Set.equal(Set[1, 2], Set[2, 1]) == (Set[1, 2] == Set[2, 1])",
			"Set.equal(Set[1, 2], Set[3]) == (Set[1, 2] == Set[3])",
			"Set.hash(Set[1, 2]) == Hashable.hash(Set[1, 2])",
		}},
		{"Map", "", "", []string{
			`Map.equal({"a" => 1}, {"b" => 1}) == ({"a" => 1} == {"b" => 1})`,
			`Map.hash({"a" => 1}) == Hashable.hash({"a" => 1})`,
		}},
		{"Bytes", "", "", []string{
			`Bytes.equal(String.to_bytes("ab"), String.to_bytes("ac")) == (String.to_bytes("ab") == String.to_bytes("ac"))`,
			`Bytes.hash(String.to_bytes("ab")) == Hashable.hash(String.to_bytes("ab"))`,
			`Bytes.compare(String.to_bytes("ab"), String.to_bytes("ac")) == Comparable.compare(String.to_bytes("ab"), String.to_bytes("ac"))`,
		}},
		{"Decimal", "", "", []string{
			"Decimal.equal(1.5d, 1.50d) == (1.5d == 1.50d)",
			"Decimal.hash(1.5d) == Hashable.hash(1.50d)",
			"Decimal.compare(1.5d, 2.25d) == Comparable.compare(1.5d, 2.25d)",
			"(Decimal.compare(2.25d, 1.5d) == Ordering.Greater) == (2.25d > 1.5d)",
			"Decimal.divide(6d, 4d) == 6d / 4d",
		}},
		{"String", "", "", []string{
			`String.equal("a", "b") == ("a" == "b")`,
			`String.hash("a") == Hashable.hash("a")`,
			`String.compare("a", "b") == Comparable.compare("a", "b")`,
		}},
		{"derived struct", "struct P\n    x: Int\nend\n\n" + fmt.Sprintf(derived, "P"), "", []string{
			"P.equal(P{x: 1}, P{x: 2}) == (P{x: 1} == P{x: 2})",
			"P.hash(P{x: 1}) == Hashable.hash(P{x: 1})",
			"P.compare(P{x: 1}, P{x: 2}) == Comparable.compare(P{x: 1}, P{x: 2})",
		}},
		{"derived enum", "enum E\n    A(Int)\n    B\nend\n\n" + fmt.Sprintf(derived, "E"), "", []string{
			"E.equal(E.A(1), E.B) == (E.A(1) == E.B)",
			"E.hash(E.A(1)) == Hashable.hash(E.A(1))",
			"E.compare(E.A(1), E.B) == Comparable.compare(E.A(1), E.B)",
		}},
		{"distinct", "type Id Int\n\n" + fmt.Sprintf(derived, "Id"), "", []string{
			"Id.equal(Id(1), Id(2)) == (Id(1) == Id(2))",
			"Id.hash(Id(1)) == Hashable.hash(Id(1))",
			"Id.compare(Id(1), Id(2)) == Comparable.compare(Id(1), Id(2))",
		}},
		{"embedded value", shapes, "c = Circle{r: 1}\ns: Shape = Shape.Dot", []string{
			"Option.equal(Some(c), Some(s)) == (Some(c) == Some(s))",
			"List.equal([c], [s]) == ([c] == [s])",
			"Vector.equal(Vector[c], Vector[s]) == (Vector[c] == Vector[s])",
			"Shape.equal(c, s) == (c == s)",
			"Shape.equal(s, c) == (s == c)",
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString("import std::io\n\n")
			if row.decls != "" {
				b.WriteString(row.decls + "\n")
			}
			b.WriteString("fn main()\n")
			if row.setup != "" {
				b.WriteString("    " + strings.ReplaceAll(row.setup, "\n", "\n    ") + "\n")
			}
			for _, l := range row.lines {
				b.WriteString("    io.print(" + l + ")\n")
			}
			b.WriteString("end\n")
			irRunSource(t, b.String(), strings.Repeat("True\n", len(row.lines)))
		})
	}
}
