package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runOutput loads src as a program and runs it, failing the test on a load
// error, a BLOCKED function or a fault.
func runOutput(t *testing.T, src string) string {
	t.Helper()
	p, err := vmhost.Load(writeProgram(t, src))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
	}
	return out.String()
}

// The comparison surface of the std containers runs on the VM at every
// element type that has an order: the direct `List.compare` /
// `Vector.compare`, the ordering operators, and `Comparable.compare`, over
// scalars, Bools, a declared type, and Lists and Vectors nested in each
// other. Equality and hashing are held for Set as well.
func TestRun_ContainerComparisonSurface(t *testing.T) {
	out := runOutput(t, `import std/io

struct P {
  a: Int
}

derive Equatable for P

derive Comparable for P

fn main() {
  io.inspect(List.compare([[1]], [[0]]))
  io.inspect([[1], [2]] < [[1], [3]])
  io.inspect(List.compare([True], [False]))
  io.inspect([False] < [True])
  io.inspect(List.compare([[#P{a: 1}]], [[#P{a: 2}]]))
  io.inspect(List.compare([#[1]], [#[2]]))
  io.inspect(Vector.compare(#[#P{a: 1}], #[#P{a: 2}]))
  io.inspect(Vector.compare(#[[1]], #[[1], [0]]))
  io.inspect(Comparable.compare(#[1], #[2]))
  io.inspect(Comparable.compare(True, False))
  io.inspect(Bool.compare(False, True))
  io.inspect(Hashable.hash(True))
  io.inspect(List.hash([[1]]))
  io.inspect(Set.equal?(%[1, 2], %[2, 1]))
  io.inspect(Hashable.hash(%[1, 2]) == Hashable.hash(%{1 => True, 2 => True}))
  io.inspect(Iter.sort([True, False, True]))
}
`)
	want := strings.Join([]string{
		"Greater", "True", "Greater", "True", "Less", "Less", "Less", "Less", "Less",
		"Greater", "Less", "31", "1055", "True", "True", "[False, True, True]",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("output\n%s\nwant\n%s", out, want)
	}
}

// A tuple is Comparable when its elements are, lexicographically, on every
// route that orders one: `<` on two, `Comparable.compare`, a List of tuples
// compared directly or by operator, a sort of them and a sort by a tuple
// key, with tuples nested in tuples and a declared type as an element.
func TestRun_TuplesCompareLexicographically(t *testing.T) {
	out := runOutput(t, `import std/io

struct P {
  a: Int
}

derive Comparable for P

fn main() {
  io.inspect(#(1, "b") < #(2, "a"))
  io.inspect(#(1, "b") >= #(1, "c"))
  io.inspect(Comparable.compare(#(1, #(2, 3)), #(1, #(2, 4))))
  io.inspect(Comparable.compare(#(#P{a: 2}, 0), #(#P{a: 1}, 9)))
  io.inspect(List.compare([#(1, 2)], [#(1, 3)]))
  io.inspect([#(1, 2)] < [#(1, 3)])
  io.inspect(Iter.sort([#(2, "a"), #(1, "b"), #(1, "a")]))
  io.inspect(Iter.sort_by(["bb", "a", "ab"], fn s -> #(String.length(s), s)))
  io.inspect(Iter.max([#(1, 2.5), #(1, 3.5), #(0, 9.0)]))
}
`)
	want := strings.Join([]string{
		"True", "False", "Less", "Greater", "Less", "True",
		`[#(1, "a"), #(1, "b"), #(2, "a")]`, `["a", "ab", "bb"]`, "Some(#(1, 3.5))",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("output\n%s\nwant\n%s", out, want)
	}
}

// An anonymous struct has no order, and neither does a tuple with an
// element that has none, so every route that demands one is a compile error
// rather than a program the VM cannot run.
func TestLoad_OrderingOverAnAnonymousStructIsACompileError(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		{"Iter.sort([#{a: 2}, #{a: 1}])", "types cannot implement `Comparable`"},
		{"#{a: 2} < #{a: 1}", "types cannot implement `Comparable`"},
		{"[#(1, Unit)] < [#(1, Unit)]", "no impl of `Comparable` for `Unit`"},
		{"Iter.sort([#(1, Unit)])", "is Comparable when each element is, and `Unit` is not"},
	} {
		_, err := vmhost.Load(writeProgram(t, "import std/io\n\nfn main() {\n  io.inspect("+c.expr+")\n}\n"))
		if err == nil {
			t.Fatalf("%s: the front end now ACCEPTS this", c.expr)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: rejected for another reason: %v", c.expr, err)
		}
	}
}
