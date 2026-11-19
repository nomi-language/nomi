package irbuild

import (
	"sort"
	"strings"
	"testing"
)

// checkSubstCensus fails when an instance the programs
// TestIRGolden_RecordsMatchTheVM runs is entered with a type parameter
// that has no checker type beside its kind (substFrame, substCensus),
// naming each. Every instance is entered with the checker's types for its
// type parameters, read where the instance is asked for: a call's type
// arguments, an impl block's receiver, a boxed value's coercion, a view's
// element, a field's or a part's type within its whole (the irbuild
// package doc, "Instantiation and dispatch"). The census counts each
// parameter of each instance of a program's own lowering, and of the std
// instances that lowering asks for, and never std's own lowerings, so it
// counts the same with an empty artifact cache and a warm one.
//
// Outside this population, `nomi test std` and `nomi test tests` under
// NOMI_SUBST_VERIFY=1 enter a few instances with no checker type. Two have
// none to read, since no expression or record has their type:
//
//   - the generic signature a bounded function's declaration resolves with
//     each type parameter as its bound's existential (dict.go), and the
//     generic type instances and impls that signature asks for
//     (`Box<Grow>` in expansive_impl_member_test.nomi). An existential
//     stands for every instance, and the checker types the body once,
//     over the parameter;
//   - a renderer's Debug for a Range an `Iter<T>` may view at run time
//     (nestedDebugImpls' Iter arm), and the instances its body reaches
//     (`Range.to_string<Int>`): the renderer names every source the
//     position may hold, and the program may never make that Range.
//
// Two have a record the builder does not reach yet:
//
//   - a constructor's payload converted to an interface where the checker
//     records the conversion at the constructor (`m: Maybe<Show> =
//     Some(Vector["v"])`, analysis.Conversion.Boxes), whose box the
//     payload's lowering builds without it, and what that box's entries
//     reach. The payload's conversion (convertTo, recordedCoercion) reads
//     the payload's own records, not its constructor's;
//   - a named argument's stand-in (namedPlacedCall), an identifier at the
//     written argument's position, whose node type finds the written
//     argument's coercion only when the argument is itself an identifier,
//     so a std value it boxes has no type (`ToJson.to_json_field` at Json
//     in std/http's cases).
func checkSubstCensus(t *testing.T, missing map[substEntry][]string) {
	t.Helper()
	var lines []string
	for e := substEntry(0); e < substEntries; e++ {
		if names := missing[e]; len(names) > 0 {
			sorted := append([]string(nil), names...)
			sort.Strings(sorted)
			lines = append(lines, e.String()+": "+strings.Join(sorted, ", "))
		}
	}
	if len(lines) > 0 {
		t.Errorf("instances are entered with type parameters that have no checker type: read each instance's arguments from the checker's record where it is asked for:\n  %s",
			strings.Join(lines, "\n  "))
	}
}
