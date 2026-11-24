package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// writtenBoundUnit declares Unit2, which Feet implements and Bare does
// not. Neither has Display or Comparable.
const writtenBoundUnit = `import std::io

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

`

// An impl whose functions are written asks of its type arguments only what
// its where clause says (spec §12 *Implementations*). `impl Display for
// Measure<P> where P: Unit2` renders a Measure<Feet> though Feet has no
// Display, at every route: a qualified call, an interpolation, a bound, an
// interface value and the value inside an Option, a List and a tuple. The
// same holds for Comparable, through `<` and a List's compare, for a
// distinct type and an enum, and for std's own `impl Display for
// Range<T>`, which renders its endpoints with Debug. Before, each of these
// but the interface value and the tuple was refused with "no impl of
// `Display` for `Feet`".
func TestWrittenImplBound_IsAllTheImplAsksOfItsTypeArguments(t *testing.T) {
	for _, tc := range []struct {
		name, decls, body, want string
	}{
		{"Display", `struct Measure<P>
    amount: Int
    unit: P
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

fn shown<T>(x: T) -> String where T: Display
    Display.to_string(x)
end
`, `m = Measure{amount: 3, unit: Feet{n: 0}}
    io.print(Display.to_string(m))
    io.print("${m}")
    io.print(shown(m))
    d: Display = m
    io.print(Display.to_string(d))
    io.print(Display.to_string(Some(m)))
    io.print("${[m]}")
    io.print("${(m, 1)}")`,
			"3 ft\n3 ft\n3 ft\n3 ft\nSome(3 ft)\n[3 ft]\n(3 ft, 1)\n"},
		{"Comparable", `struct Measure<P>
    amount: Int
    unit: P
end

impl Equatable for Measure<P> where P: Unit2
    fn equal(a: Measure<P>, b: Measure<P>) -> Bool
        a.amount == b.amount
    end
end

impl Comparable for Measure<P> where P: Unit2
    fn compare(a: Measure<P>, b: Measure<P>) -> Ordering
        Comparable.compare(a.amount, b.amount)
    end
end

fn least<T>(a: T, b: T) -> T where T: Comparable
    if T.compare(a, b) == .Less then a else b
end
`, `a = Measure{amount: 3, unit: Feet{n: 0}}
    b = Measure{amount: 5, unit: Feet{n: 0}}
    io.inspect(Comparable.compare(a, b))
    io.inspect(a < b)
    io.inspect(least(b, a).amount)
    io.inspect(Comparable.compare([a], [b]))
    io.inspect(Comparable.compare((b, 1), (a, 1)))`,
			"Less\nTrue\n3\nLess\nGreater\n"},
		{"a distinct type", `type Tagged<P> P

impl Display for Tagged<P> where P: Unit2
    fn to_string(t: Tagged<P>) -> String
        Tagged(u) = t
        "tagged ${Unit2.symbol(u)}"
    end
end
`, `t = Tagged(Feet{n: 0})
    io.print("${t}")
    io.print(Display.to_string([Some(t)]))`,
			"tagged ft\n[Some(tagged ft)]\n"},
		{"an enum", `enum Reading<P>
    Seen {unit: P}
    Unseen
end

impl Display for Reading<P> where P: Unit2
    fn to_string(r: Reading<P>) -> String
        case r
            .Seen{unit:} -> Unit2.symbol(unit)
            .Unseen -> "none"
        end
    end
end
`, `r: Reading<Feet> = Reading.Seen{unit: Feet{n: 0}}
    io.print("${r}")
    io.print("${[r, Reading.Unseen]}")`,
			"ft\n[ft, none]\n"},
		{"std's Range", `derive Equatable for Bare

impl Comparable for Bare
    fn compare(a: Bare, b: Bare) -> Ordering
        Comparable.compare(a.n, b.n)
    end
end
`, `io.print("${Bare{n: 1}..Bare{n: 3}}")`,
			"Bare{n: 1}..Bare{n: 3}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runSourceOutput(t, writtenBoundUnit+tc.decls+"\nfn main()\n    "+tc.body+"\nend\n")
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// What a written impl's where clause asks is still asked: a Measure<Bare>
// is refused for its Bare, which has no Unit2, at a call and inside a List.
// A derive that writes no function and a type with no impl still ask
// Display of every type argument, since the type renders through its
// parts.
func TestWrittenImplBound_StillAsksItsWhereClause(t *testing.T) {
	measure := `struct Measure<P>
    amount: Int
    unit: P
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

struct Box<T>
    value: T
end
`
	for _, tc := range []struct {
		name, decls, body, want string
	}{
		{"the where clause unmet", measure, `io.print(Display.to_string(Measure{amount: 3, unit: Bare{n: 0}}))`,
			"no impl of `Unit2` for `Bare` (required by the call `Display.to_string`)"},
		{"the where clause unmet in a List", measure, `io.print("${[Measure{amount: 3, unit: Bare{n: 0}}]}")`,
			"no impl of `Unit2` for `Bare` (required to interpolate it)"},
		{"a derived Display's part", measure + "\nderive Display for Box<T>\n", `io.print("${Box{value: Feet{n: 0}}}")`,
			"no impl of `Display` for `Feet` (required to interpolate it)"},
		{"no impl's part", measure, `io.print(Display.to_string(Box{value: Feet{n: 0}}))`,
			"no impl of `Display` for `Feet` (required by the call `Display.to_string`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := writtenBoundUnit + tc.decls + "\nfn main()\n    " + tc.body + "\nend\n"
			_, err := vmhost.LoadSource("main.nomi", src)
			if err == nil {
				t.Fatalf("the front end admits the program; want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got:\n%v\nwant it to say %q", err, tc.want)
			}
		})
	}
}
