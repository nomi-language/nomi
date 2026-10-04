package irbuild

// The ordering of a scalar whose std `Comparable` impl does not lower.
//
// # One member, and its absence is a DEAD END rather than pending work
//
// `Bool`. Two independent routes are closed, which is why this arm exists rather
// than a row in an existing table:
//
//   - std/bool.nomi's ordering is `derive Comparable for Bool`, and the
//     synthesized body does not lower. The `True`/`False` singletons are not
//     the cause: they have `stdHostSpecs` rows, and `bool.Bool.equal?`,
//     `bool.Bool.inspect` and `bool.Bool.to_string` all lower. The identical
//     shape lowers for a USER declaration (`type Lo; type Hi; enum Rung {
//     embeds Lo; embeds Hi }` with `derive Comparable` on all three), so the
//     obstacle is in the stdlib path rather than in the construct. See
//     opaque_test.go's `unlowered stdlib function` row and its control.
//   - a `stdlibBindings` row cannot reach it: `stdCandidateFor`'s binding arm is
//     `case fd == nil`, i.e. a `host fn` whose only route is an rt symbol, and a
//     derive-synthesized declaration carries a FuncDef.
//
// So `stdCompareAt(kindBool, …)` answers nil and always will while both doors are
// shut. rt/scalarorder.go carries the ordering itself and the transcript that
// fixes it.
//
// # String is deliberately NOT here, and that is the discriminator
//
// `stdCompareAt(kindString, …)` RESOLVES: std/strings.nomi declares
// `impl Comparable for String` with a body inside the subset, so String's
// ordering comes from std through the ordinary index and this file must not
// shadow it. Two scalars arrive at the same gate with different answers, which is
// the whole reason the table below is a table rather than a `default:`.
//
// # ASKED LAST, so std wins the moment std can answer
//
// compareResolves' order is local impl, sibling impl, std index, then this. If
// `bool.Bool.compare` ever lowers, this arm silently stops being reached, which
// is the correct direction (std is the source of truth for its own semantics)
// and leaves a stand-in that has outlived its reason.
// TestScalarOrder_BoolStillHasNoStdRoute states that precondition as an
// assertion, so the test fails when it changes.

// scalarOrdered is the scalar kinds whose `Comparable.compare` std cannot serve
// and the VM answers itself.
//
// A map rather than a switch so the POPULATION is one readable expression: rule
// (2) is that a table-driven guard is only as wide as its table, and a table you
// can see the whole of is the version of that a reader can audit.
var scalarOrdered = map[tag]bool{
	tagBool: true,
}

// scalarCompares reports whether k is a scalar whose std impl does not lower
// and whose order the VM answers.
//
// Declines for every other kind, so a scalar with no entry keeps whatever refusal
// its consumer already named — `comparison outside Int and Float` for the
// operator, `Iter.sort over an unorderable element` for the sort. Minting a
// refusal here would replace three informative names with one.
func scalarCompares(k kind) bool {
	// A NAMED kind can share a tag with nothing here, and the guard is a
	// fence rather than a filter: every entry above is a bare scalar, so a
	// def-carrying kind reaching this map would mean the tag enum had grown
	// a second meaning.
	return k.def == nil && scalarOrdered[k.tag]
}
