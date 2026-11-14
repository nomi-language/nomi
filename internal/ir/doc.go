// Package ir is the representation between the checked Nomi AST, as
// `internal/irbuild` builds it, and its consumer, the VM (`internal/vm`), which
// compiles each Func to bytecode.
//
// Three decisions are load-bearing in ir.go and the three files beside it:
//
//  1. A checked `Int` operation records that overflow is a fault at this
//     point. It does not record how the fault is delivered: delivery is a
//     consumer property, and a `trap bool` on the node would make every
//     producer know which consumer it was serving. See arith.go.
//
//  2. Position is mandatory and per node. Most of the builder's lowering
//     sites inherit the ambient `gen.nomiLine` cursor, and a cursor has to be
//     re-established by hand after every child lowering, and one missed
//     restore misplaces every node after it (position_witness_test.go).
//     A per-node position needs no remembering. Nothing in
//     this package inherits a position: every constructor takes one as its
//     first parameter, every field holding one is unexported, and
//     Block.Append and Block.SetTerm reject a node whose position is invalid.
//
//  3. An assertion's rendered source text is a field on the node:
//     `Assert.Text`, `Record.Text` and `Try.Text`. `?` carries one because
//     whoever reports a non-local exit prints where it left from. The
//     renderer is one function (`format.RenderNode`); which node to render is
//     the producer's choice, and it is recorded here. See assert.go and
//     try.go.
//
// Shape: linear with explicit temporaries, basic blocks and terminators, not a
// tree. The builder has already linearized the AST by hand in three mechanisms
// whose only job is turning a tree into named temporaries (`gen.slot` /
// `gen.fixSlot`, `expr.pure`, `gen.operand` / `gen.hold`), so this is the shape
// it already has rather than a new one. Blocks and terminators are here for one
// reason, and it is not speculative generality: Go's `&&` and `||`
// short-circuit only over expressions, and a Nomi right operand can need
// statements, so short-circuit `and` / `or` cannot be an expression tree. See
// logic.go.
//
// Not SSA: a temporary may be assigned by more than one instruction, because
// that is what the two arms of a branch writing one destination requires and
// what `gen.slot` / `gen.fixSlot` already is. Copy exists for exactly that.
//
// Scope: sixteen of the builder's 22 operation classes are modelled here:
// const, ref, arith, logic, make, proj, match, destructure, assert, try,
// closure, iter, call, branch, jump and render, plus the type and
// declaration table (table.go), which is not an operation class but the thing
// an operation's operands and callee are named in.
//
// Five classes are partly routed, and each says which part. `iter` covers
// every operation over a sequence; `Iter.loop`, the three widened callback
// frames and the sort family are refused by name. `call` covers the call in
// all three of its callee forms and leaves the partial application, the
// operand holds, four non-call lowerings inside routed owners, and the
// dictionary dispatch; see call.go and internal/irbuild/ircall.go. `jump`
// covers the jump sites whose target block exists and leaves those whose
// target belongs to a construct nothing lowers; see
// internal/irbuild/irjump.go. `tail` and `interp`: below.
//
// Five classes contribute no instruction, for two reasons.
// `logic` and `branch` are pure control flow whose content is the
// graph's shape (`logic` is a Branch, a block and a Copy; `branch` is a chain
// of Branches and Jumps over one join), so `logic` has a builder instead,
// BeginShortCircuit, and internal/irbuild builds a branch's blocks directly.
// `bind`, `tail` and `interp` contribute none because their operations
// exist under another class's name: a Copy and a Bind for `bind`, a
// Copy and a Jump over a Region for `tail`, and a Const plus a Render for
// `interp`. See logic.go, and internal/irbuild/irbind.go, irtail.go
// and irinterp.go.
//
// `tail` is also taken as a fact: the builder's tail driver is `closure`,
// `branch`, `bind` and `jump` with no call in it, while tail position is a
// front-end fact the consumer reads, so `ir.Call.Tail()` records the fact.
// What the class carries beyond that fact is a cycle, a whole-module
// strongly connected component, which the VM's tail transfer never asks for,
// so it stays in the builder.
//
// Three classes have no node, deliberately rather than by omission: `box`,
// `effect` and `conc`. `box`'s three owners are three unrelated operations:
// a Go pointer for a self-reaching type, a `gen.coerce` arm with no position,
// and a type retag that emits nothing; a consumer that needs to skip a
// representation change reads `Table`'s own `TypeForm`. `effect` is a scope
// stack with two blockers: nothing in this package writes a declaration, and
// the general scope-region form (a region whose exit block holds the
// restores) is owed by the retained population rather than by a missing
// type. `conc`'s blocker is narrower: fault.go puts the exceptional edge on
// the block, but no terminator resumes an unwind, so a handler can run and
// cannot hand the fault back.
// See internal/irbuild/irbox.go, ireffect.go and irconc.go.
//
// [Symbol] (ir.go) is what the table replaces where a signature is needed;
// it remains the right shape wherever a name plus an identity is all an
// operand has, which includes a pattern's own bindings (destructure.go).
//
// # Updates written in place
//
// A Nomi update makes a new value, so a consumer may write an update into
// its base, or a collection operation into its operand, only when nothing
// can read the old value again. [Owns] answers that from the graph and
// what the function's direct callees return, as [TailTransfers] answers
// which tail calls transfer. Per alias class (the temporaries Copy, Bind
// and a parameter's RefLocal join) it answers never, or a need: a mask of
// the parameters whose arguments the caller must have handed over, and
// of those it must have handed over deep (bit [DeepBit]+j), for the class
// to be owned. A class is owned when every write is a literal, an update,
// a fresh collection, a parameter, an owned callee result, an owned
// collection operation, a granted field read or a fold that hands its
// accumulator on, and every read is a move within the class, a field
// read, a borrowing lookup, a Return, or a read after which no member is
// live (a move: whoever receives it is its one holder). It is owned DEEP
// when the values its fields hold are its own too; a field read of a
// class owned deep owns the value it reads when nothing reads that field
// through the class again (fields.go). From that it answers the updates
// and collection operations that may write in place, the arguments each
// direct call hands on, the folds (`Iter.reduce`, `Iter.map_reduce`) that
// may hand their callback the accumulator ([Fold]), and the function's
// own [ResultOwnership], which its callers' analyses and its folds read
// through [Callees]. [InPlaceUpdates] is the part that needs no caller and
// no callee. unique.go's and fields.go's file comments have the rules. The
// VM is the consumer
// (internal/vm package doc, "Updates written in place"). The fact is not
// stored, so it cannot go stale and no image carries it.
//
// # Objects
//
// A symbol another module can name carries a linkage name (Symbol.Link),
// which internal/irbuild builds from the declaration (internal/irbuild
// package doc, "Linkage names"). The image codec writes it, so a module
// encoded alone ([EncodeObject]) is an object: a [Linker] resolves every
// linkage name to one symbol across the objects it decodes, linking a name
// two objects define once when the bodies encode identically, and hands
// the VM ordinary modules. [CheckLinkage] reports a symbol two modules name
// with no linkage name, and two symbols sharing one with different code.
// [Parts] encodes each function and cell of a module on its own, so two
// lowerings of one module compare part by part. link.go's file comment
// has the rules. TestLink_* hold the linker's rules, and
// internal/expectation's TestObjectsLinkToTheSameProgram runs every
// recorded program linked through objects.
package ir
