package irbuild

// The try class. irassert.go sits beside it.
//
// `ir.Try.Text` is the `try` as `format.RenderNode` renders it. The two
// spellings of one `try` render differently: the pipe spelling renders the
// whole `*ast.Binary` (`-1 |> try parse()`) and the prefix spelling the
// `*ast.TryOp` (`try parse(-1)`), so `tryOp` takes the node to render
// separately from the node carrying the position. The producer fills the
// field with the text.
//
// A `try` decomposes into three operations, and only one is its own node:
//
//	refute      is the operand the unwind variant?   ir.MatchVariant
//	exit        leave, carrying it                   ir.Try
//	navigate    read the unwrap variant's payload    ir.Proj
//
// A pattern is a sequence, not a node, and that holds here for a construct
// that is not a pattern: irTryCond goes through `ir.NewMatchVariant` and
// takes the condition off it with irVariantTest. A `case` arm puts the true
// answer on an opened block and a `try` puts it on a one-line guard: one
// node, two deliveries.
//
// The boundary is not on the node. `try` unwinds to the enclosing
// activation (a lambda is one, so a `try` inside a lambda leaves the lambda
// and not the enclosing `fn`), and no consumer reads a field for it: the VM
// leaves the innermost activation, so its boundary is the run-time call
// stack.
//
// What the boundary decides is the shape of the exit, and that stays here:
// three guards, classified from the checker's hover symbol rather than from
// this builder's own two flags. Delivery is the consumer's and the
// classification is the front end's.
//
// The class has no predicate. A `try` cannot fault: a `try` on a value that
// is neither `Result` nor `Maybe` is a front-end error, and the builder's arm
// for it is a named refusal rather than a trap. `ir.Proj.Faults` exists
// because a projection can trap at run time; nothing here can.
