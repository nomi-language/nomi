package irbuild

// The tail class beyond `ir.Call.Tail()`.
//
// `Call.Tail()` is a fact about ONE call site, read from
// `analysis.MarkTailCalls`. tail.go also computes the strongly connected
// component each tail callee belongs to, a whole-MODULE graph property found
// by Tarjan over every lowered function. The VM needs no such thing: it
// replaces the caller's activation at each tail call, one call at a time
// (internal/vm/tail.go), and never asks which functions form a cycle.
//
// The hop is not a call. Nothing here builds an `ir.Call`. `ir.Call.Tail()`
// records that a call site is in tail position, and a driver-loop edge would
// record where control goes when it is; the two never meet in one node.

import "github.com/nomi-language/nomi/internal/ir"

// irTailObserved is a test-only hook, nil in production, for a driver-loop
// edge as it is built. The builder builds no driver loops, so nothing calls
// it; irMuteObservers saves and clears it with the other observation hooks.
//
// It is a hook rather than a field on the gen, so the gen carries no
// node-keyed side table for it.
var irTailObserved func(p *tailPlan, j *ir.Jump)
