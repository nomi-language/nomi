package irbuild

// Go spellings of `ir.Jump` and `ir.Return`.
//
// A Nomi `return` is `ir.Return`, whose value is a temporary and whose
// position is the `return`'s own. `emitReturn` reads `HasVal()`, and the Unit
// spelling is the Go consumer's: `rt.Unit{}` is a Go zero value, not a Nomi
// one.
//
// THE DIRECTION OF THE EDGE IS READ OFF THE GRAPH. `break` and `continue`
// are one operation in the IR and differ only in which of the boundary's two
// blocks the Jump names, so `irCtlJumpGo` derives the keyword instead of each
// site deciding it. That is `irLogicTests`' discipline at a third class, and
// here it is the back edge against the forward one: nothing on the node says
// which, because the graph already does.
//
// A signalling callback's return carries its `Iter` control outcome as a
// second Go result (`return acc, false`, `return v, rt.CtlEmitStop`), because
// Go has no other way to leave a function literal with a decision attached.
// In the IR it is one `ir.Return` whose `Ctl()` names the outcome;
// `irCtlSuffix` spells it. A `break v` is a Copy and then a Jump: a jump that
// carried a value would be a second meaning for one node.
//
// THE NODE RECORDS THE OPERATION AND NOT THE MECHANISM. `break`,
// `continue` and `return` are "leave for a target named somewhere else",
// which is what `ir.Jump` records; how a consumer delivers the jump is not
// on the node.
