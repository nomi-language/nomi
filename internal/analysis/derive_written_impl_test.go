package analysis_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// deriveWrittenImplDecls is writtenImplRouteDecls with Op, which implements
// Unit2 and holds a function, so it has no equality of its own.
const deriveWrittenImplDecls = writtenImplRouteDecls + `struct Op
    run: (Int) -> Int
end

impl Unit2 for Op
    fn symbol(_unit: Op) -> String
        "op"
    end
end

`

// derivesOverWrittenImpls are the derives a written impl of Measure
// answers for at a part: Equatable, Hashable and Display, which write no
// function, and Comparable, whose body compares each part.
var derivesOverWrittenImpls = []string{"Equatable", "Hashable", "Comparable", "Display"}

// A derive over a part whose type has a hand-written impl asks only that
// impl's where clause of the part's type arguments, as every other route
// to the impl does: a Measure<Feet> and a Measure<Op> meet it, though Feet
// has no Comparable or Display and Op holds a function, and a
// Measure<Bare> is the one error that Bare has no Unit2, at the field,
// naming the derive. The same holds where the part's type holds a Measure,
// in a List, an Option or a tuple. std's Option has no Comparable, so a
// derived Comparable over one is left out.
func TestDeriveOverAWrittenImpl_AsksItsWhereClause(t *testing.T) {
	for _, iface := range derivesOverWrittenImpls {
		for _, field := range []string{"Measure<%s>", "List<Measure<%s>>", "Option<Measure<%s>>", "(Measure<%s>, Int)"} {
			if iface == "Comparable" && strings.HasPrefix(field, "Option") {
				continue
			}
			t.Run(iface+" "+fmt.Sprintf(field, "U"), func(t *testing.T) {
				at := func(unit string) []string {
					src := deriveWrittenImplDecls + fmt.Sprintf("struct Holder\n    m: %s\nend\n\nderive %s for Holder\n", fmt.Sprintf(field, unit), iface)
					return equalityBoundErrors(src)
				}
				for _, unit := range []string{"Feet", "Op"} {
					if errs := at(unit); len(errs) != 0 {
						t.Errorf("at %s: want no error, got %q", unit, errs)
					}
				}
				want := fmt.Sprintf("no impl of `Unit2` for `Bare` (required via `derive %s for Holder`, field `m`) | `Measure<P>`'s `%s` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block", iface, iface)
				if errs := at("Bare"); !slices.Equal(errs, []string{want}) {
					t.Errorf("at Bare: got %q\nwant [%q]", errs, want)
				}
			})
		}
	}
}

// A generic derive's parts are checked over its own type parameter, which
// carries the bound the derive implies and its where clause. A part whose
// written impl asks more of the parameter than the derive does is an error
// at the part that says to write the bound on the derive; written there,
// the derive is accepted, and a use at Bare is the one error that Bare has
// no Unit2.
func TestDeriveOverAWrittenImpl_AGenericDeriveNeedsTheBound(t *testing.T) {
	for _, iface := range derivesOverWrittenImpls {
		t.Run(iface, func(t *testing.T) {
			decl := "struct Hold<T>\n    m: Measure<T>\nend\n\n"
			errs := equalityBoundErrors(deriveWrittenImplDecls + decl + "derive " + iface + " for Hold\n")
			want := fmt.Sprintf("`derive %[1]s for Hold`: field `m` holds values of `T`, and the derive does not require Unit2 of `T` | require it on the derive: `derive %[1]s for Hold<T> where T: %[1]s and Unit2`", iface)
			if !slices.Equal(errs, []string{want}) {
				t.Errorf("without the bound: got %q\nwant [%q]", errs, want)
			}
			bounded := deriveWrittenImplDecls + decl + "derive " + iface + " for Hold<T> where T: Unit2\n"
			if errs := equalityBoundErrors(bounded); len(errs) != 0 {
				t.Errorf("with the bound: want no error, got %q", errs)
			}
		})
	}
	use := deriveWrittenImplDecls + "struct Hold<T>\n    m: Measure<T>\nend\n\nderive Equatable for Hold<T> where T: Unit2\n\n" +
		"fn main()\n    a = Hold{m: Measure{amount: 1, unit: %s{n: 0}}}\n    _ = a == a\nend\n"
	if errs := equalityBoundErrors(fmt.Sprintf(use, "Feet")); len(errs) != 0 {
		t.Errorf("Hold<Feet> == Hold<Feet>: want no error, got %q", errs)
	}
	want := "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Hold<T>`'s `Equatable` impl requires `T: Unit2` | write an `impl Unit2 for Bare` block"
	if errs := equalityBoundErrors(fmt.Sprintf(use, "Bare")); !slices.Equal(errs, []string{want}) {
		t.Errorf("Hold<Bare> == Hold<Bare>: got %q\nwant [%q]", errs, want)
	}
}

// A part whose type itself derives the interface answers through that
// derive: a derive over a part of a non-generic type that derives is the
// part's derive's to check, and one over a generic type that derives asks
// that derive's bounds, where clause included, at the part's type
// arguments.
func TestDeriveOverAWrittenImpl_APartThatDerives(t *testing.T) {
	inner := "struct Inner<T>\n    m: Measure<T>\nend\n\nderive Equatable for Inner<T> where T: Unit2\n\n"
	at := func(unit string) []string {
		return equalityBoundErrors(deriveWrittenImplDecls + inner + "struct Holder\n    i: Inner<" + unit + ">\nend\n\nderive Equatable for Holder\n")
	}
	if errs := at("Feet"); len(errs) != 0 {
		t.Errorf("at Feet: want no error, got %q", errs)
	}
	want := "no impl of `Unit2` for `Bare` (required via `derive Equatable for Holder`, field `i`) | `Inner<T>`'s `Equatable` impl requires `T: Unit2` | write an `impl Unit2 for Bare` block"
	if errs := at("Bare"); !slices.Equal(errs, []string{want}) {
		t.Errorf("at Bare: got %q\nwant [%q]", errs, want)
	}
	plain := deriveWrittenImplDecls + "struct Inner\n    m: Measure<Bare>\nend\n\nderive Equatable for Inner\n\nstruct Holder\n    i: Inner\nend\n\nderive Equatable for Holder\n"
	errs := equalityBoundErrors(plain)
	want = "no impl of `Unit2` for `Bare` (required via `derive Equatable for Inner`, field `m`) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"
	if !slices.Equal(errs, []string{want}) {
		t.Errorf("a part that derives over Measure<Bare>: got %q\nwant the one error [%q]", errs, want)
	}
	if strings.Contains(strings.Join(errs, "\n"), "for Holder") {
		t.Errorf("Holder's derive reports Inner's part: %q", errs)
	}
}
