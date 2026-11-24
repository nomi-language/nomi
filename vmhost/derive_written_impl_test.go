package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// deriveWrittenImplMeasure is writtenRouteMeasure with Op, which implements
// Unit2 and holds a function.
const deriveWrittenImplMeasure = writtenRouteMeasure + `
struct Op
    run: (Int) -> Int
end

impl Unit2 for Op
    fn symbol(_unit: Op) -> String
        "op"
    end
end
`

// A derive over a field whose type has a hand-written impl runs through
// that impl, whatever its type argument holds. Two Holders whose units
// differ compare, hash and order alike, since Measure's written bodies
// read only the amount, and a derived Display renders the field with
// Measure's own Display: at Feet, which has no Comparable or Display of its
// own, and at Op, which holds a function and has no equality of its own.
func TestDeriveOverAWrittenImpl_RunsThroughTheWrittenBody(t *testing.T) {
	for _, unit := range []struct{ name, a, b, shown string }{
		{"Feet", "Feet{n: 1}", "Feet{n: 2}", "ft"},
		{"Op", "Op{run: fn x -> x}", "Op{run: fn x -> x + 1}", "op"},
	} {
		t.Run(unit.name, func(t *testing.T) {
			src := deriveWrittenImplMeasure + `
struct Holder
    m: Measure<` + unit.name + `>
end

derive Equatable for Holder
derive Hashable for Holder
derive Comparable for Holder
derive Display for Holder

fn main()
    a = Holder{m: Measure{amount: 3, unit: ` + unit.a + `}}
    b = Holder{m: Measure{amount: 3, unit: ` + unit.b + `}}
    io.inspect(a == b)
    io.inspect(Set.size(Set[a, b]))
    io.inspect(Comparable.compare(a, b))
    io.print("${a}")
end
`
			want := "True\n1\nEqual\nHolder{m: 3 " + unit.shown + "}\n"
			if got := runSourceOutput(t, src); got != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// Where the written impl's where clause is unmet, each derive over the field
// is the one error naming the unmet bound, and none reaches the IR builder:
// a derived Equatable or Hashable over a Measure<Bare> was admitted and ran
// the written body with P = Bare.
func TestDeriveOverAWrittenImpl_AnUnmetWhereClauseIsOneError(t *testing.T) {
	for _, iface := range []string{"Equatable", "Hashable", "Comparable", "Display"} {
		t.Run(iface, func(t *testing.T) {
			src := deriveWrittenImplMeasure + "\nstruct Holder\n    m: Measure<Bare>\nend\n\nderive " + iface + " for Holder\n\nfn main()\n    io.print(\"x\")\nend\n"
			_, err := vmhost.LoadSource("main.nomi", src)
			if err == nil {
				t.Fatal("the front end admits the program; want Bare's missing Unit2")
			}
			msg := err.Error()
			want := "no impl of `Unit2` for `Bare` (required via `derive " + iface + " for Holder`, field `m`)"
			if strings.Contains(msg, "internal compiler error") || !strings.Contains(msg, want) {
				t.Fatalf("got:\n%s\nwant one error saying %q", msg, want)
			}
			if n := strings.Count(msg, "no impl of"); n != 1 {
				t.Fatalf("got %d errors, want one:\n%s", n, msg)
			}
		})
	}
}

// A generic derive whose field's written impl asks a bound of its type
// parameter runs at a type that meets it once the derive writes the bound,
// and a use at one that does not is the one error naming it.
func TestDeriveOverAWrittenImpl_AGenericDeriveAtBoth(t *testing.T) {
	decls := deriveWrittenImplMeasure + `
struct Hold<T>
    m: Measure<T>
end

derive Equatable for Hold<T> where T: Unit2
`
	got := runSourceOutput(t, decls+"\nfn main()\n    a = Hold{m: Measure{amount: 3, unit: Feet{n: 1}}}\n    b = Hold{m: Measure{amount: 3, unit: Feet{n: 2}}}\n    io.inspect(a == b)\nend\n")
	if got != "True\n" {
		t.Fatalf("Hold<Feet>: got %q, want %q", got, "True\n")
	}
	_, err := vmhost.LoadSource("main.nomi", decls+"\nfn main()\n    a = Hold{m: Measure{amount: 3, unit: Bare{n: 1}}}\n    io.inspect(a == a)\nend\n")
	if err == nil {
		t.Fatal("Hold<Bare> == Hold<Bare> is admitted; want Bare's missing Unit2")
	}
	msg := err.Error()
	if strings.Contains(msg, "internal compiler error") || !strings.Contains(msg, "no impl of `Unit2` for `Bare`") || strings.Count(msg, "no impl of") != 1 {
		t.Fatalf("Hold<Bare>: got:\n%s\nwant the one error that Bare has no Unit2", msg)
	}
}
