// Package vm runs an `ir.Module`: it compiles each `ir.Func` to bytecode on
// its first call and executes it.
//
// The bytecode is a flat `[]uint32` over three typed register banks: word
// (unboxed scalars), string, and reference. Every value is an rt value. A tail call replaces the caller's activation.
// `nomi run`, `nomi test`, the REPL, the tour and `nomi build`'s executables
// all run on this package.
//
// # What this package does not contain
//
// The linearization is the producer's (`internal/irbuild`), not this
// package's: operand forcing, temporary naming and block construction are
// read off `ir.Func` rather than rebuilt here.
//
// The absences are enforced rather than asserted:
// `TestVM_ReadsTheIRAndNothingElse` fails if this package's non-test sources
// import `nomi/ast`, `nomi/parser`, `nomi/analysis` or
// `nomi/internal/irbuild`.
//
//   - No AST. Nothing here sees a `*ast.Binary` or an `*ast.If`.
//   - No operand forcing. `gen.operand`/`gen.hold`'s rule (materialize an
//     impure left operand so a later operand's statements cannot be hoisted
//     ahead of it) is an `ir.Copy` in the graph, built by the producer. This
//     package executes the Copy.
//   - No temporary naming. Every operand is an `ir.Temp` the producer
//     allocated out of `ir.Func`'s namespace; the bytecode compiler gives
//     each one a register in the bank its stored `ir.ValType` names.
//   - No block construction. Control flow is `Block.Term()` plus
//     `Block.Fault()`; the bytecode compiler lays the producer's blocks out
//     in order and turns each edge into a code offset (bytecode.go).
//
// What it does contain is the consumer's own answers: which rt
// representation realizes a constant, which Go function performs a checked
// `+`, how a frame is laid out, and which Go function a marked crossing
// lands on.
//
// # The value representation is rt's
//
// Scalars and strings live unboxed in typed registers; every other value is
// an rt value (`*rt.Record`, rt's collections, rt's handles), and equality,
// hashing and rendering are rt's kernels. values.go lists the representation
// of each kind. A Go caller hands `Run` rt values and reads one back, and a
// crossing into Go calls a generated adapter over rt values (hostcall.go).
// TestVM_ReadsTheIRAndNothingElse pins which packages this package's
// non-test sources may reach.
//
// # Big Ints in the word bank
//
// An Int stays in the word bank at any size (rt package doc, "Int: exact at
// any size"). The collector does not scan the word bank, so a pointer stored
// there as bits would be freed under it. The word bank therefore has a
// shadow (regStack.b in exec.go), a slice of `any` with one entry per word.
// A big Int occupies the word `rt.BigWord` (2^63, the bits of
// `math.MinInt64`) and its `*rt.BigInt` sits in the shadow at the same
// index. A word of `rt.BigWord` with a nil shadow is the int64
// `math.MinInt64`.
//
// A reader looks beside a word only when the word is `rt.BigWord`, so the
// shadow beside any other word may be stale. Every path that can write the
// word `rt.BigWord` writes its shadow too: constant loads, moves, call
// arguments, returns, tail calls, field reads and the slow path of each Int
// operation. A closing window clears its shadows only once a big value has
// been written on that stack (regStack.bigs), so a program that stays within
// int64 pays one predictable branch per return.
//
// The fast path of addition adds two words when neither is `rt.BigWord` and
// the result fits int64 and is not `rt.BigWord`; anything else goes to a
// slow path ((*frame).slowInt) kept out of the loop. Multiplication detects a
// product past int64 with `bits.Mul64` rather than a division. Equality
// compares words and looks at the shadows only when both are `rt.BigWord`,
// since in canonical form a big value never equals an int64. Division and
// remainder fault on a zero divisor and on nothing else.
//
// Alternatives not taken:
//
//   - Int in the reference bank, as `any`: Go allocates when it boxes an
//     int64 of 256 or more into an interface, so most arithmetic results
//     would allocate.
//   - A 16-byte register of a word and a pointer: it doubles the word bank,
//     makes every Float and Bool register a pointer the collector scans, and
//     adds a store to every write.
//   - A tagged word holding an index into a table of big values: entries
//     would need a collector of their own to be freed.
//   - A separate `BigInt` type beside a 64-bit `Int`: it keeps the fast
//     path, but every API has to choose between two integer types, and
//     `Int` arithmetic still traps where it runs out of bits.
//
// bigint_test.go moves big values through every path that carries a word.
//
// # Loops written as tail calls
//
// A loop in Nomi is a tail-recursive function, so the dispatch loop's cost
// per iteration is the instructions of the body plus the tail call. Three
// things keep that to the body's work:
//
//   - A function's tail call to itself (selfTail, exec.go) keeps its window
//     and moves the operands into the parameters; it neither clears nor
//     resizes the window and looks nothing up. A function with pending
//     deferred calls, a boot, a test body and a debug machine take the
//     general transfer.
//   - A scalar or string constant whose register nothing else writes is
//     loaded once at entry (consthoist.go), and the self tail call restarts
//     past the loads.
//   - An Int or Float comparison only its block's branch reads compiles with
//     the branch into one `opBrCmpI` or `opBrCmpF` (branchCompare), and a
//     jump to a chain of jumps ending in a return is the return.
//
// TestDispatch_ATailRecursiveLoopIsItsWork holds mandelbrot's escape loop to
// twelve instructions an iteration.
//
// # Constructions and crossings off the boxed path
//
// An instruction the bytecode compiler has no typed opcode for runs its
// handler over operands boxed out of their registers (`opIR`,
// Machine.irInstr). The kinds that programs run in their inner loops have
// typed opcodes instead, each resolving at compile time what the handler
// looked up per run:
//
//   - A struct, record, tuple or variant construction is `opMake` over the
//     descriptor its plan named (records.go), writing each operand into its
//     slot from its register.
//   - A struct update `{..b, f: v}` is `opUpdate`: per new value, the
//     field's name, the operand and the slot type the operand's stored type
//     gives it. Its cache holds the base descriptor's fields, resolved at
//     compile time when the base's stored type names a descriptor and on
//     the first run otherwise, so a run is one pointer compare, one clone
//     and a write per field, a scalar straight from its register. An update
//     whose base the activation owns and nothing reads again is
//     `opUpdateIn`, which skips the clone (below, "Updates written in
//     place"). An update the cache cannot
//     resolve (a field the base's layout holds as a scalar the operand does
//     not fill) runs the handler, which rebuilds the record over a layout
//     that fits.
//   - A crossing into Go is `opHost`, bound at compile time to the
//     intrinsic of its name, else the adapter that answers it (hostcall.go),
//     so a call does no map lookup. Its operands go in a scratch slice the
//     activation record keeps (frame.hostArgs) and clears after the call:
//     a host reads its operands during the call and keeps none of the slice
//     (a host that keeps a value keeps the value, not the slice). A debug
//     machine records a crossing's operands, so it takes a fresh slice.
//   - A crossing answering a Maybe or a Result that every use takes apart
//     (variant tests, the payload read, `?`) is `opHostM` (maybefuse.go):
//     the payload of a Some or an Ok, or the None or Err record, goes in the
//     result's reference register and the tag in a word register, and the
//     tests and the read are `opTagIs` and `opProjM` over them, so a Some
//     or an Ok allocates nothing. A crossing fuses when it can answer in
//     parts: every generated adapter whose binding answers a Maybe or a
//     Result with a payload of its own has a parts form
//     (hostadapt.PartsFunc), and Map.get and Vector.get have fast paths of
//     their own. The parts form converts the operands as the adapter does,
//     so a String or Bytes operand still allocates its box. The fast path reads
//     the vector and index, or the map and a scalar key, from their
//     registers: an Int key of any size, a Float or a String is hashed and
//     compared where it sits (rt.MapLookupInt, MapLookupString; a big Int's
//     shadow is already a value), so no lookup boxes its key. Any other
//     operand runs the intrinsic and takes its record apart.
//   - A tuple component read `t.0` is `opProjS`, the slot read straight into
//     the destination's register. A tuple the function builds and only reads
//     components of (`case (a, b)`, `(x, y) = (b, a)`) is split
//     (tuplesplit.go): its construction moves each operand into a register
//     of its own and each read moves one out, so it allocates nothing. A
//     split tuple crosses a call in parts: a function returning one it
//     built (`(a / b, a % b)`, each branch's tuple through the result slot)
//     returns with `opRetP`, which copies the components to the register
//     stack's return scratch, and a caller taking the answer apart
//     (`(q, r) = divmod(a, b)`) splits it too and moves them into its own
//     registers. Any other reader of such a return (a caller keeping the
//     tuple whole, a call from Go, the debugger, an activation whose
//     deferred calls run after the return) gets the record built
//     (regStack.ret), so a callee need not know its caller.
//
// A value kept in registers instead of a record is rebuilt when something
// reads it boxed: a handler, a stopped frame's variables (frame.readPacked,
// from code.packed). A debug machine keeps the same registers, so its
// bytecode is the plain one plus its hooks. On a debug or profiling
// machine a fused crossing goes through hookedCallHost, as opHost's does,
// instead of the fast path, so the debugger records it and the profiler
// samples at its return.
//
// TestOpCosts_* (opcosts_test.go), TestMaybeFuse_* and TestTupleSplit_*
// count each path's allocations.
//
// The construction handler (makeValue) runs a cons, a list or vector
// literal, a range, and a Map or Set literal. Only the last hashes, through
// key kernels that report a bad key by panicking (keys.go), so only it runs
// under `defer recoverKey`; a cons pays no deferred call
// (TestMakeValue_OnlyAKeyedLiteralDefers).
//
// # Updates written in place
//
// Every Nomi update makes a new value, and a program cannot tell a new
// value from an old one written over when nothing reads the old one again.
// ir.Owns proves that statically (ir package doc, "Updates written in
// place"), per alias class, as a need: the parameters whose arguments the
// caller must have handed over, or handed over deep (the values their
// fields hold theirs too), for the class to be the activation's own, or
// never. A field read of a value owned deep owns what it reads when
// nothing reads that field through the value again, so `{..s, v:
// Vector.set(s.v, i, x)}` sets the vector in place. The compiler asks it
// once per function (inplace.go):
//
//   - An update whose base is owned and dies there is `opUpdateIn`, opUpdate
//     without the clone when the activation owns the need, so a literal
//     updated n times allocates once.
//   - A direct call that hands an argument on (a move of an owned class)
//     is `opCallO` or `opTailO`: opCall and opTail with the index of its
//     moves. Each activation carries the mask of parameters its caller
//     handed it (frame.owned: bit j owned, bit ir.DeepBit+j owned deep),
//     and the call sets the callee's bits for each move whose needs the
//     caller's own mask meets, a self tail call included. A move whose
//     callee tests neither of the argument's bits is dropped (pruneMoves),
//     so a call that hands on nothing its callee uses stays opCall.
//     Every other way into a function (a function value, interface
//     dispatch, a call from Go or a host, a handler's call, a tail call
//     with deferred calls pending) leaves the mask empty, so a parameter
//     is borrowed unless a caller proved it its own, and one body serves
//     both. A callee's
//     ResultOwnership (memoized in ownTable, each component of mutually
//     recursive functions iterated to a fixpoint) tells the caller its
//     result is fresh, so `s = step(s)` keeps s owned round a loop.
//   - An `Iter.reduce` or `Iter.map_reduce` whose seed the activation
//     owns and hands over (ir.Ownership.Folds) calls its lambda through
//     invokeOwned with the accumulator's bits set, and sets them again for
//     each answer the lambda's own ResultOwnership says is fresh under
//     what it was given, so a fold threads one vector or struct through
//     every step in place. Every other callback enters with an empty mask.
//   - `Vector.set`, `Vector.push`, `Map.put`, `Map.remove`, `Set.insert`
//     and `Set.remove` at a site whose receiver is owned and dies there
//     bind an in-place form beside the intrinsic (hostCall.inPlace), which
//     opHost runs when the activation owns the need. It calls rt's Owned
//     operations (rt/transient.go): Clojure's transients, an owner token
//     on each header, map root and node, so the operation writes the nodes
//     its value's lineage stamped and copies any other once. A set, or a
//     put over a key the map has, answers the operand's own box, so it
//     allocates nothing. The lookups (`Vector.get`, `Map.size`, the
//     others in borrowingHosts) keep nothing of their operands, so they do
//     not end a collection's ownership. A profiling machine's crossings go
//     through hookedCallHost, which takes the in-place form as opHost does.
//
// A debug machine copies, on both sides of every call: it compiles no
// opUpdateIn, opCallO or in-place crossing, so no activation's mask is
// set and no callee writes into a caller's value. A stopped frame shows a
// variable from its register after its last use, and must show the value
// the variable was bound to. A profiling machine reads no values and
// writes updates and collection operations in place. NOMI_NO_INPLACE (vm.NoInPlaceEnv) and
// Machine.WithCopiedUpdates make every update and collection operation
// copy. TestExpectation_InPlaceUpdatesRunAsCopying (internal/expectation)
// runs every corpus program, stdlib module and benchmark both ways and
// requires the same transcripts, the run harness's second run of a
// generated program copies (vmhost's FuzzLoweredProgramsRun), TestInPlace_*
// count the allocations, and vmhost's TestInPlaceCensus logs how much the
// analysis finds, folds and field reads included.
//
// # Interface dispatch and boxes
//
// A dispatched call selects an impl body by the runtime type name of its
// declared `self` operand (dispatch.go). A value whose runtime type names no
// impl (a std container, a value widened into an `embeds` variant) is boxed
// where it becomes an interface value (rt/boxed.go): the box names the
// static type, dispatch selects by that name and opens the box for a body
// that takes the concrete type, and hashing and rendering otherwise see
// through it. A std type that is no container and enters std's `Display` or
// `Debug` without its own erased rendering (`d: Display = 'c'`, a
// `Duration`, a `Date`, a `Byte` as `Debug`) is boxed too: the `Display`
// box names a thunk whose body is the direct call `Display.to_string(v)`,
// and `Debug` renders the value inside through std's Debug body
// (irbuild's irStdNamedBoxPlan).
//
// `==` on two interface values, a Set element and a Map key compare by
// run-time type. Values of two types (two box names) are unequal. Values of
// one type compare through its hand-written `Equatable` and `Hashable` when
// the module records them, and structurally otherwise (keys.go), so
// `Equatable.equal` and `Hashable.hash` on interface values agree with `==`
// and hashing.
//
// # Iteration
//
// An `ir.Iter` builds or drives an rt.Seq (iter.go). The drivers run in Go
// and call the program's callbacks through an iterCaller, which counts
// each callback's depth from the instruction's frame, so building a stage
// costs no view of the Machine.
//
// A `Range<Int>` view (ir.IterOverIntRange) walks in Go with std's Int
// `compare`, `next` and `steps_between` built in (rt.IntRangeSeq), and a
// `Range<Codepoint>` view (ir.IterOverCodepointRange), std's only other
// Discrete scalar, walks with Codepoint's built in, stepping over the
// surrogate block (rt.CodepointRangeSeq): each element costs its Codepoint
// record and no call. A Range of any other element calls its linked impls
// per element.
//
// `Iter.zip` pulls its right side one element per left element. When the
// right side is the view of a List, a Vector or a `Range<Int>` (its Src),
// zip steps that source directly (directCursor, lend.go;
// rt.SeqZipCursor); any other right side runs on a coroutine (iter.Pull)
// with a handoff per element. The cursor is made when the zip runs, so a
// view costs nothing more to build.
//
// Lent tuples (lend.go). `with_index`, `zip` and a Map's entries build a
// tuple per element. A consumer that is done with its element when its
// callback returns, and whose callback reads that element only through
// tuple projections (`fn sum, (j, uj) -> ...`), takes the stage's Reusing
// form, which rewrites one tuple per run.
//
// # The fault edge
//
// The fault edge is part of the representation (`internal/ir/fault.go`):
// when an instruction faults, the VM reads `Block.Fault()`. An edge is a
// transfer to that block; no edge unwinds to the caller, which is the
// default.
//
// # Debug machines
//
// A machine opened with [Machine.WithDebugger] compiles a statement hook (`opStmt`) at each
// statement mark the builder recorded (statements, arms, loop iterations,
// pipe stages) and a `pc` hook (`opPC`) before each instruction that may
// call back into Nomi. A machine without one compiles neither, so a plain
// run pays nothing (TestDebug_AMachineWithoutADebuggerCompilesNoHooks).
// debug.go holds the stops and the logical call stack, debugtravel.go step
// back by recording and replay, and debugeval.go evaluation in a stopped
// frame on a view of the machine. internal/dap drives them for `nomi dap`.
// TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes
// (internal/expectation) holds a debug machine's runs to a plain one's.
//
// # Profiling machines
//
// A machine opened WithProfiler (`nomi run --profile`, NOMI_PROFILE)
// samples the program's logical call stack every millisecond (profile.go).
// It needs no hooks compiled in: its fuel watches nothing and takes the
// pending samples at the calls and backward branches every fuel already
// burns at; an activation's return and a host call's return take theirs
// too, and a callback started from Go links its stack to the frame that
// started it. A machine that neither debugs nor profiles tests one
// field (Machine.hooked) where a debug machine's paths branch off, so a
// plain run pays nothing for either.
package vm
