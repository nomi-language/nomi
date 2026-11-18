package irbuild

// How `ir.Jump` and `ir.Return` stand for `return`, `break` and `continue`.
//
// A Nomi `return` is `ir.Return`, whose value is a temporary and whose
// position is the `return`'s own. A return with no value is
// `ir.NewReturnUnit`.
//
// In an inline `Iter.loop`, `break` and `continue` are one operation and
// differ only in which of the loop's blocks the Jump names: the exit or the
// head. Nothing on the node says which, because the graph already does.
//
// A signalling callback's return carries its `Iter` control outcome on the
// node: one `ir.Return` whose `Ctl()` names the outcome (`ir.NewReturnCtl`).
// A `break v` in a loop is a Copy and then a Jump: a jump that carried a
// value would be a second meaning for one node.
//
// The node records the operation and not the mechanism. `break`, `continue`
// and `return` are "leave for a target named somewhere else", which is what
// `ir.Jump` records.
