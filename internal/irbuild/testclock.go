package irbuild

// The `clock` clause on a `tests` group, lowered.
//
// # THE WHOLE MECHANISM IS ONE FIELD, AND THE REST OF THIS FILE IS WHY
//
// `clock Clock.Virtual` runs a group's cases inside a `testing/synctest`
// bubble, where the clock advances the moment every goroutine in the bubble is
// durably blocked. `clock Clock.System` runs them on the real clock, which is
// what a case with no clause already gets. So the lowering is one flag:
//
//	Virtual  ->  `ir.TestGroup.VirtualClock` is set (irtestgroup.go)
//	System   ->  nothing is set
//	absent   ->  nothing is set
//
// The VM runner supplies the bubble, `rt/vclock.RunCase`, with its four-layer
// discipline. It is a SUB-PACKAGE rather than part of `rt` because it imports
// `testing` and `testing/synctest` and `rt` is linked by every runner including
// a hello world; see the vclock package header.
//
// # THE CLAUSE EXPRESSION IS NOT LOWERED, AND THAT IS COMPLETE
//
// Nothing evaluates it. The clock has to be known BEFORE a case runs, while
// `boot` and `setup` run INSIDE it, so the front end reads the variant name
// syntactically and so does this. `ast.TestClockVariant` is the one
// implementation of that read, shared by both — not two kept in step by a test,
// because a disagreement about which clock a group runs under is a wrong
// ANSWER rather than a refusal, and every spelling it accepts
// (`.Virtual`, `Clock.Virtual`, `testing.Clock.Virtual`) has to mean the same
// thing to both readers.
//
// So `Clock.Virtual` is never lowered as a value, and `std/testing.Clock` has
// no `rt` representation and does not need one.
