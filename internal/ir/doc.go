// Package ir is the representation of a lowered Nomi program.
// `internal/irbuild` builds it from the checked AST, one [Func] per body, and
// `internal/vm` compiles each Func to register bytecode on its first call and
// runs it. Nothing else executes a program, so a rule here is a rule between
// that producer and that consumer.
//
// A [Module] (module.go) is one lowered compilation unit: its functions, its
// module-scoped storage ([Cell]) and, for a file that declares tests, its
// test cases. A Func is a [Region], a list of basic blocks whose first is the
// entry, plus a name, its parameters and a namespace of temporaries.
//
// # Blocks and terminators
//
// A function is linear code in basic blocks, not a tree. A [Block] is a run
// of instructions and one terminator, and the terminators are [Jump],
// [Branch] and [Return]. The node set is sealed, so a switch over it can be
// exhaustive.
//
// Control flow is explicit because a Nomi operand can need instructions of
// its own. The right operand of `and` or `or` runs only when the left does
// not decide, so each is a Branch, a block for the right operand and a
// [Copy] of either operand into one destination. [BeginShortCircuit] builds
// that shape (logic.go). The VM lays the blocks out in the producer's order
// and turns each edge into a code offset.
//
// # Temporaries
//
// Every operand is a [Temp]. A literal is an instruction that writes one, so
// an instruction reads temporaries and writes at most one. Func.Def answers
// the instruction that first writes a temporary.
//
// The graph is not SSA: a temporary may be written by more than one
// instruction, because the arms of a branch write one destination, with a
// Copy or into storage a [Slot] declares. Lint requires instead that every
// path from the entry to a read passes through a write.
//
// Every temporary a function names carries a [ValType] (valtype.go): what
// the value is in Nomi's terms. The producer states it (Func.SetType), or
// the instruction fixes it when it is appended. The VM keeps scalars and
// strings unboxed in typed register banks, and the stored type is what tells
// its bytecode compiler which bank a temporary's register is in. Lint
// requires the type and checks it against what writes and reads the
// temporary (typelint.go).
//
// # Positions
//
// Position is mandatory and per node. Nothing in this package inherits a
// position: every node constructor takes a [Pos] as its first parameter,
// Pos's fields are unexported, and Block.Append and Block.SetTerm reject a
// node whose position is invalid. A block and a Region carry their own
// positions too. A Module has none, since it is a set of files.
//
// A Pos is an extent: a file, a start, and the end of the construct's nodes.
// [At] builds a point, [Spanning] a construct that reaches past its start
// line, and [AtSynthesized] the position of derived code, which names the
// source it was derived from. The start is what a diagnostic names.
// Pos.Covers is the query a breakpoint on an interior line resolves through.
// Pos.Stdlib marks a position in a standard library module, where the VM
// reports a trap at the program's own call instead.
//
// # Faults
//
// An operation that can fault records that a fault is possible at its
// position. It does not record how the fault is delivered: rt carries the
// error and the VM delivers it. [InstrFaults] answers the ways an
// instruction can fault. Int arithmetic is exact at any size, so only a zero
// divisor faults it (arith.go).
//
// Where a fault goes is on the block, not in a terminator: Block.Fault names
// the block control transfers to when an instruction in the block faults. A
// block with no fault edge unwinds to the caller (fault.go).
//
// # Source text
//
// An assertion's report prints the source it was written as, and so does the
// report of a `?` that leaves a function. That text is stored on the node:
// Assert.Text, Record.Text and Try.Text. The producer renders it with
// `format.RenderNode` and chooses which AST node to render; a consumer
// prints the field (assert.go, try.go).
//
// # Calls and tail calls
//
// A [Call] names its callee and a flat vector of operands. Named arguments,
// defaults, evaluation order and overload selection are settled before the
// instruction exists (call.go).
//
// The front end decides which calls are in tail position and Call.Tail
// records it. [TailTransfers] answers, from the graph, which of those calls
// the VM may run by replacing the caller's activation: the ones whose result
// reaches a plain Return unchanged, with no fault edge on the path (tail.go).
//
// # Names and types
//
// A [Symbol] (ir.go) is a declaration's name plus a pointer identity. Two
// declarations with one printed name are two symbols. That is enough for
// anything an instruction only has to name: a callee, a local, a `once`
// cell, a pattern's binding (destructure.go). The [Table] (table.go) interns
// symbols and types against the producer's own identities, one table per
// lowering. A [Decl] there adds a signature, which Table.SelectOverload
// needs to choose between two declarations. A function of an impl block
// carries its block as an [ImplFunc] (Table.ImplSymbol), and its name is
// the ImplFunc's, `Display.to_string for Loud`; a reader that needs the
// type or the function's own name reads the ImplFunc and never parses the
// name.
//
// # Lint
//
// [Lint] checks one function, and [LintModule] one module and every function
// in it, for what only the whole graph can get wrong. A constructor panics
// on what one call can get wrong. The rules are the LintRule constants in
// lint.go: every block is terminated; every read of a temporary is reached
// by a write; every position is valid and its end is not before its start; a
// module records each declaration once; slot storage is declared before it
// is used; a fault edge names a block of the function; nothing follows a
// NoMatch; every RefLocal names a parameter or a binding of the function;
// operands have the shape their position accepts (shape.go); every RunDefer
// names a registered Defer; every temporary is typed and its type agrees
// with the graph; and the debugger's variable table and statement marks
// (debuginfo.go) name temporaries and instructions the function has.
//
// A violation is a producer bug, so Lint returns all of them and
// internal/irbuild panics on the error. It lints what each module gained as
// it lowers ([LintModuleAdded]) and each finished module whole before the VM
// reads it. The tests named `CatchesItsPlant` or `CatchesItsPlants` build a
// graph that breaks a rule and require Lint to report it.
//
// # Images
//
// [EncodeImage] writes an [Image], a linked program's modules and what a
// runner needs to start them, to bytes, and [DecodeImage] reads it back
// (encode.go, decode.go). `nomi build` appends an image to a runner binary,
// and the runner hands the decoded modules to the VM in place of a lowering.
// The image holds this graph and not bytecode, because bytecode names state
// of one process and is cheap to compile.
//
// The codec writes every field of every node and keeps pointer identity: a
// Symbol, a Func, a ValType, a Type and a Table are each written once and
// referred to by index, so cycles decode to cycles. The order of writing is
// fixed by the modules' own slices, so one program encodes to the same bytes
// each time. [FormatVersion] changes with any change to what is written.
// TestImage_RoundTripKeepsEveryFieldAndIdentity and
// TestImage_EncodingIsDeterministic hold both.
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
// read, a variant test, a borrowing lookup, a Return, or a read after
// which no member is live (a move: whoever receives it is its one holder).
// It is owned deep when the values its fields hold are its own too; a
// field read of a class owned deep (a Some's or an Ok's payload among
// them) owns the value it reads when nothing reads that field through the
// class again (fields.go). A collection operation that answers a Maybe
// (`Vector.set`) answers it owned deep, so `Vector.set(v, i, x)!` hands
// the vector on owned. From that it answers the updates
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
