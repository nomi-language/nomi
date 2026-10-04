package irbuild

// The INFERRED channel's two walkers, cross-checked against each other and
// against the ANNOTATED channel's.
//
// # The gap this file closes, and what it cost to leave open
//
// `TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses` drives `typeOf` and
// `typeRefusal` over one corpus of annotations and requires their answers to be
// complements. There was no equivalent for `project` and `projectRefusal`, and
// the asymmetry was not cosmetic: the annotation pair is cross-checked and the
// INFERENCE pair is not, so a projection arm that refuses a representable type
// had nothing to fail against.
//
// A disagreement between the two produces a FABRICATED refusal. Two shapes it
// takes:
//
//   - a resolver that refuses every non-local origin makes a SIBLING FILE's
//     type reachable by annotation and unreachable by inference, and
//     `nominalRefusal` then synthesizes `unlowered type | ...` for a perfectly
//     representable type;
//   - a `project` InterfaceType arm that answers only for a STDLIB interface
//     makes a USER interface reachable by annotation (`typeOf` returns
//     `existential(d)`) and unreachable by inference, and the fall-through
//     names `unrepresentable inferred type | Speaker`. The test below catches
//     this one.
//
// # Two properties, because they fail differently
//
// COMPLEMENTARITY (this file's first test) catches a refusal with no reason:
// `project` says kindInvalid and `projectRefusal` names nothing, so the gap
// vanishes from the tally entirely and the sweep undercounts. `nominalRefusal`
// and the new InterfaceType arm both have a `return "", "", false` branch that
// does exactly this on purpose for a representable type, and each is correct
// only for as long as `project` agrees.
//
// AGREEMENT BETWEEN CHANNELS (this file's second test) catches the other
// direction, which complementarity structurally cannot: `projectRefusal` opens
// with `if g.project(t) != kindInvalid { return … false }`, so a reason for a
// type `project` accepts is unreachable while that guard stands, and a reason
// for a type the OTHER CHANNEL accepts is not. Both fabricated refusals above
// were of the second shape. One test would have caught neither on its own.
//
// # The population is the ANALYZER's, not a list in this file
//
// Both tests read `fa.Definitions` — the checker's own record of every symbol
// it solved a type for — rather than enumerating `analysis.Type` constructors.
// A test that derived its population from `project`'s own switch would pass by
// construction for every arm that exists and say nothing about the one that is
// missing, which is exactly the failure mode both bugs above had. Adding a
// naming form to `project` without adding it here therefore fails without
// anybody remembering to update a list — and a witness that stops covering an
// arm shows up as the vacuity check below, not as a silent pass.
