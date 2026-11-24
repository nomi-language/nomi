package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// writtenRouteMeasure declares Measure<P>, whose written Equatable,
// Hashable, Comparable, Display and Debug each ask only `P: Unit2`. Feet
// implements Unit2; Bare implements nothing.
const writtenRouteMeasure = `import std::io

interface Unit2
    fn symbol(unit: self) -> String
end

struct Feet
    n: Int
end

impl Unit2 for Feet
    fn symbol(_unit: Feet) -> String
        "ft"
    end
end

struct Bare
    n: Int
end

struct Measure<P>
    amount: Int
    unit: P
end

impl Equatable for Measure<P> where P: Unit2
    fn equal(a: Measure<P>, b: Measure<P>) -> Bool
        a.amount == b.amount
    end
end

impl Hashable for Measure<P> where P: Unit2
    fn hash(m: Measure<P>) -> Int
        m.amount
    end
end

impl Comparable for Measure<P> where P: Unit2
    fn compare(a: Measure<P>, b: Measure<P>) -> Ordering
        Comparable.compare(a.amount, b.amount)
    end
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

impl Debug for Measure<P> where P: Unit2
    fn inspect(m: Measure<P>) -> String
        "Measure(${m.amount})"
    end
end

fn shown(x: Display) -> String
    Display.to_string(x)
end
`

// Where the where clause is met, every route runs through the written
// bodies: `==` and `in` alone and nested, a key, an ordering, an
// interpolation, `dbg` of the value and of a List of it, each rendered by
// the written Debug, and an interface-typed parameter.
func TestWrittenImplRoute_RunsThroughTheWrittenBodies(t *testing.T) {
	body := `a = Measure{amount: 3, unit: Feet{n: 1}}
    b = Measure{amount: 3, unit: Feet{n: 2}}
    io.inspect(a == b)
    io.inspect([a] != [b])
    io.inspect((a, 1) == (b, 1))
    io.inspect(Some(a) in [Some(b)])
    io.inspect(Map.get({a => "three"}, b))
    io.inspect(Set.size(Set[a, b]))
    io.inspect(a < b)
    io.print("${[a]}")
    _ = dbg a
    _ = dbg [a]
    io.print(shown(a))`
	want := "True\nFalse\nTrue\nTrue\nSome(\"three\")\n1\nFalse\n[3 ft]\n" +
		"dbg line 71: a = Measure(3)\ndbg line 72: [a] = [Measure(3)]\n3 ft\n"
	got := runSourceOutput(t, writtenRouteMeasure+"\nfn main()\n    "+body+"\nend\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Where the where clause is unmet, each route is one error naming the unmet
// bound and none reaches the IR builder: `a == a` was admitted and stopped
// the builder with "calleeTypeArgs: the builder lowers only checked
// programs", and `[a] == [a]`, a key, `dbg` and a tuple compared with `==`
// were admitted.
func TestWrittenImplRoute_AnUnmetWhereClauseIsOneError(t *testing.T) {
	const want = "no impl of `Unit2` for `Bare`"
	for _, expr := range []string{
		"a == a",
		"a != a",
		"[a] == [a]",
		"Some(a) == Some(a)",
		"(a, 1) == (a, 1)",
		"a in [a]",
		"a < a",
		"Hashable.hash(a)",
		"{a => 1}",
		"Set[a]",
		`"${a}"`,
		"dbg a",
		"dbg [a]",
		"shown(a)",
	} {
		t.Run(expr, func(t *testing.T) {
			src := writtenRouteMeasure + "\nfn main()\n    a = Measure{amount: 1, unit: Bare{n: 0}}\n    _ = " + expr + "\n    io.print(\"x\")\nend\n"
			_, err := vmhost.LoadSource("main.nomi", src)
			if err == nil {
				t.Fatalf("the front end admits the program; want %q", want)
			}
			msg := err.Error()
			if strings.Contains(msg, "internal compiler error") || !strings.Contains(msg, want) {
				t.Fatalf("got:\n%s\nwant one error saying %q", msg, want)
			}
			if n := strings.Count(msg, "no impl of"); n != 1 {
				t.Fatalf("got %d errors, want one:\n%s", n, msg)
			}
		})
	}
}
