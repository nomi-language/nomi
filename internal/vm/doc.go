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
//   - No operand forcing. Materializing an impure left operand, so a later
//     operand's statements cannot run ahead of it, is an `ir.Copy` in the
//     graph, built by the producer. This package executes the Copy.
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
//   - A function's direct tail call to itself is a jump, `opLoop`
//     (loop.go): the compiler works out the moves that put the operands
//     into the parameters, per bank, ordered so that no move overwrites a
//     register a later one reads (a cycle, such as a swap, goes through one
//     scratch register), and drops each operand already in its parameter's
//     register. The jump runs those moves and starts the body again past
//     the constants loaded at entry, in the same activation: no window
//     work, no lookup of the callee, no depth. It burns the fuel a limit
//     watches and checks the stopping frame as a call does, so a spinning
//     loop stays stoppable and a stopped case shows the stack the tail
//     call would, and it sets the activation's ownership mask from the
//     call's moves as opTailO does, so an update round the loop is written
//     in place as before. A function that registers deferred calls (which
//     run at each transfer) or is a boot keeps opTail, whose own self call
//     (selfTail, exec.go) keeps the window too; so does a debug or
//     profiling machine, whose stops and samples are then per call as
//     written. A test body's tail call calls, through the opTail compiled
//     after each opLoop. NoLoopJumpEnv and WithoutLoopJumps turn the jumps
//     off on any machine. Machine.Redirect reaches a loop at its next call:
//     a jump stays in the activation that made it.
//   - A scalar or string constant whose register nothing else writes is
//     loaded once at entry (consthoist.go), and the self tail call restarts
//     past the loads.
//   - An Int or Float comparison only its block's branch reads compiles with
//     the branch into one `opBrCmpI` or `opBrCmpF` (branchCompare), and a
//     jump to a chain of jumps ending in a return is the return.
//
// TestDispatch_ATailRecursiveLoopIsItsWork holds mandelbrot's escape loop to
// twelve instructions an iteration, and TestLoop_* hold each loop compiled
// to a jump to the run of the same program with opTail.
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
//     so a call does no map lookup. A call of a function whose whole body is
//     one crossing over its parameters (`Bytes.index_of` over its private
//     `index_of_from`) compiles to that crossing (compiler.hostSite). An
//     adapter with a register form (every generated one of at most
//     hostadapt.MaxSlots operands) is called over the registers themselves
//     (hostslots.go): each operand is copied from its register into the
//     register stack's hostadapt.Slots, and each part of the answer from
//     there into the destination's register, so an Int that fits int64, a
//     Float, a Bool, a Byte, a String and a Bytes cross without a box, and a
//     tuple answered into a split destination builds no record. A
//     destination that keeps an Option, a Result or a tuple whole gets it
//     built from the parts (hostadapt.Regs.Answer). Any other
//     crossing's operands go in a scratch slice the activation record keeps
//     (frame.hostArgs) and clears after the call: a host reads its operands
//     during the call and keeps none of the slice (a host that keeps a value
//     keeps the value, not the slice). A debug or profiling machine runs
//     every crossing boxed, and a debug machine records its operands, so it
//     takes a fresh slice.
//   - A call answering an Option or a Result that every use takes apart
//     (variant tests, the payload read, `?`, `Option.with_default` and
//     `Result.with_default`) keeps it fused (optionfuse.go): the payload of
//     a Some or an Ok goes in a register of the payload's bank, or a tuple
//     payload's components each in one, the None or Err record in the
//     result's reference register and the tag in a word register, and the
//     tests and the read are `opTagIs` and `opProjM` over them, and a
//     default `opDefaultM`, the payload read with the default for a None
//     or an Err, so a Some or an Ok allocates nothing and neither does its
//     payload or a default. A crossing is `opHostM` and fuses when it can
//     answer in parts: every adapter whose register form carries an Option or
//     a Result, and Map.get, Vector.get, Vector.set and the Bytes reads by
//     fast paths of their own (optionfuse.go's fastPaths). Two of those are
//     reads of a value's storage that opHostM runs in the dispatch loop
//     itself (frame.readFused): Vector.get, and `Bytes.get` with the
//     fixed-width integer reads (`Bytes.uint8_at` and its siblings), the
//     bytes read from the string register by rt.BytesWordAt into the
//     payload's word, so a byte scan's read is one dispatch and no call. A
//     shape a read does not cover goes on to the crossing. A
//     call of a Nomi function fuses always: its return delivers the parts,
//     which a callee returning a Some or an Ok of a tuple it builds returns
//     with `opRetP` (tuplesplit.go), building neither. Vector.set's writes an owned vector in place
//     as opHost's in-place form does, so `Vector.set(v, i, x)!` on an owned
//     vector allocates nothing. The fast paths read the vector and index,
//     or the map and a scalar key, from their registers: an Int key of any
//     size, a Float or a String is hashed and compared where it sits
//     (rt.MapLookupInt, MapLookupString; a big Int's shadow is already a
//     value), so no lookup boxes its key. Any other operand runs the
//     intrinsic and takes its record apart.
//   - A call through a function value or a dispatched call on an interface
//     value that is no tail call is `opCallV` (valuecall.go): the callee's
//     bytecode is found at run time (a dispatch keeps its last selection
//     per receiver descriptor), and it is entered as a direct call is, its
//     operands moved from their registers and a closure's captures written
//     after them, so the call boxes nothing and makes no operand slice. A
//     function value whose body is Go, a receiver held in a box, a callee
//     whose parameter banks differ from the operands', and a debug or
//     profiling machine take the handler.
//   - A list pattern (`[h, ..t]`, `[a, b]`) tests the cell's cached length
//     with `opMatchL` and reads an element, or the cells from a position on,
//     with `opProjL`, walking from the front: an element goes straight into
//     the destination's register, an Int that fits a word unboxed. A tail
//     counted from the back (`[.., last]`) is cut by the handler.
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
// bytecode is the plain one, with no call compiled into its caller ("Calls
// compiled into their callers"), plus its hooks. On a debug or profiling
// machine a fused crossing goes through hookedCallHost, as opHost's does,
// instead of the fast path, so the debugger records it and the profiler
// samples at its return.
//
// TestOpCosts_* (opcosts_test.go), TestOptionFuse_*, TestOptionTuple_*,
// TestHostSlots_* and TestTupleSplit_* count each path's allocations.
//
// The construction handler (makeValue) runs a cons, a list or vector
// literal, a range, and a Map or Set literal. Only the last hashes, through
// key kernels that report a bad key by panicking (keys.go), so only it runs
// under `defer recoverKey`; a cons pays no deferred call
// (TestMakeValue_OnlyAKeyedLiteralDefers).
//
// # Calls compiled into their callers
//
// A direct call of a small function compiles the callee's body into the
// caller's code instead of opening an activation for it (inline.go). The
// callee's ir.Func is compiled by the compiler every function is compiled
// by (compileBody), into a code of its own, a view, whose registers are in
// the caller's window above the caller's own and whose parameters are the
// registers the caller's operands sit in. The call is `opInline`, which
// makes the view the activation's code and its callee the activation's
// function; the view's return makes the caller's code the activation's
// again, its answer computed into the call's destination (`opLeave`), or
// left as the stack's last return by the function's own return
// instruction, which leaves the loop, and delivered there as a call's
// return is (Machine.leaveViews, frame.deliver). The IR does not
// change: the reach walk, effects lists, body sums and every record read
// the call as it is.
//
// A view is the callee's activation without a frame. What an instruction
// reads off its activation (a temporary's register through code.locs, the
// function a handler names, a site's position and fault edge, the mask of
// parameters it owns) is the view's while it runs, so a handler runs over
// it as over the callee's own code, and an intrinsic it calls is handed
// the machine its stack keeps for the view's depth, as the callee's
// activation would be (Machine.boundAt). opInline and the view's return do what
// a call's entry and exit do: the call depth counts the view, the fuel a
// limit watches is burnt and a stopping frame checked at the entry, the
// moves an opCallO would make hand the view its owned parameters
// (frame.viewOwned keeps the caller's mask), and an error leaving the
// view leaves the loop and is carried to the call's site in the caller
// (Machine.leaveViews), where Machine.fault places it, takes the call's
// fault edge or leaves with it, as for an error leaving a callee's
// activation. A tail call in the callee's body replaces the view, as it
// would replace the callee's activation: one whose callee compiles in is
// `opSwitch`, which makes the callee's view (compiled to return where the
// view does, code.place) the activation's code at the view's depth, and
// any other is `opTailV`: the view leaves and its caller makes the call
// into the call's destination.
// So a trap, a `?`, an assertion's line, the call depth, a stopped case's
// stack and an update in place are what the call would have made them.
//
// What compiles in (planInlines, inlinable): a direct call that is not a
// tail call and that no crossing answers (hostSite), of a function of at
// most maxInlineInstrs instructions, each of inlinableInstr's kinds, that
// writes none of its parameters, returns no control, makes no tail call
// through a value or from the program into the stdlib (whose line a trap
// there is reported at, frame.stdEntry) and is no boot, from a function
// with no exit hooks
// (code.guard). A function is not compiled into its own body, directly or
// through the views around it, and views nest at most maxInlineDepth deep.
// An operand whose parameter the callee only takes apart (takenApart)
// fuses in the caller as if the callee's tests and reads were the
// caller's, so an Option a crossing answers and a small function matches is
// never built (optionfuse.go). A debug, profiling or entry-counting machine
// compiles no views, so its stops, samples and counts are per function as
// written; NoInlineEnv and WithoutInlining turn them off on any machine.
// A profile still names a view's function under its caller (profile.go).
// Machine.Redirect drops the code of every function a view of the
// redirected function was compiled into, so the REPL's next call compiles
// the replacement in. TestInline_* hold each of these to the run of the
// same program with no call compiled in.
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
//   - An `Iter.reduce` or `Iter.map_reduce` whose step is a lambda the
//     function builds or a function it names (ir.Ownership.Folds) calls
//     the step through invokeOwned with the accumulator's bits set when
//     the activation owns and hands over the seed, and sets them for each
//     answer the step's own ResultOwnership says is fresh under what it
//     was given, so a fold threads one vector or struct through every step
//     in place, from its second step when the seed was borrowed. Every
//     other callback enters with an empty mask. A fold's steps share one
//     register stack it holds for its run (iterCaller.holding), so the
//     machine view a crossing in a step is handed (boundAt) is made once,
//     and a push in a step costs what it costs in a tail call.
//   - `Vector.set`, `Vector.push`, `Map.put`, `Map.remove`, `Set.insert`
//     and `Set.remove` at a site whose receiver is owned and dies there
//     bind an in-place form beside the intrinsic (hostCall.inPlace), which
//     opHost runs when the activation owns the need. It calls rt's Owned
//     operations (rt/transient.go): Clojure's transients, an owner token
//     on each header, map root and node, so the operation writes the nodes
//     its value's lineage stamped and copies any other once. A set, or a
//     put over a key the map has, answers the operand's own box, so it
//     allocates nothing. Where the activation does not own the receiver
//     the operation copies it, and the copy is the activation's own
//     (ir.Callees.Builds; a remove of a missing key answers its receiver
//     and is the exception), so a borrowed collection is copied by its
//     first update and written in place from then on. `Vector.set` answers an Option; the vector a `!` or
//     a `Some` pattern reads out of it is owned as the Option is (ir package
//     doc, "Updates written in place"), so the next set writes it too. The lookups (`Vector.get`, `Map.size`, the
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
// declared `self` operand (dispatch.go). The instances of a generic struct,
// enum or distinct type share that name, so the descriptor a value is built
// with also carries its type's ir.ValType.InstanceKey, its name and type
// arguments (rt.TypeDesc.Inst, `main.Wrap<Int>`), and a dispatch, an erased
// Display or Debug rendering, and a Map key's or Set element's Equatable
// and Hashable select the body recorded under that key before the one
// recorded under the name (implOf). A value whose runtime type names no
// impl (a std container, a value widened into an `embeds` variant) is boxed
// where it becomes an interface value (rt/boxed.go): the box names the
// static type, dispatch selects by that name and opens the box for a body
// that takes the concrete type, and hashing and rendering otherwise see
// through it. A rendering by shape (rt.DebugText, rt.DisplayShapeText)
// asks its hook at each position of the value, and the hook (renderHook)
// follows the operand's static ValType down the same steps, so a part
// renders through the body the rendering names for its position's static
// type, and by its run-time type only where the position states no
// declared type: a value of an `embeds` enum renders through the enum's
// impl where its position is the enum's. A container, tuple or record that
// holds such a position enters std's Display or Debug boxed, and a declared
// type whose Display or Debug is its shape and holds one names a thunk at
// its type, so an interface value of either renders by its static type too
// (irbuild's irShapeHoldsBoxedEnum, heldDebugImpls, irRecordDisplayImpls). A std type that is no container and enters std's `Display` or
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
// # App fields and function values
//
// The app fields in force are the runtime frame's rt.AppFields, an
// immutable value every frame derived from it shares. A `with` line makes a
// new one for the rest of its block (boot.go's storeAppField, context.go's
// storeContext), and a RefScope and StoreScope pair puts back the one
// before. A function value the program writes (ir.FuncValue.KeepsFields,
// ir.Ref.KeepsFields) keeps the AppFields in force where it is made
// (rt.KeptFields). Every call of a function value with an IR body, from
// bytecode, from an iteration driver or from Go, runs the body on
// rt.EnterKeptFields' frame: the caller's runtime frame with the kept
// fields in force and their context floored by the caller's deadline. The
// caller's frame is not changed, so its fields are in force again however
// the body leaves. A tail call through a function value enters them on the
// frame it replaces (tail.go). When the kept fields are the caller's, as at
// every call in a program that replaces no field, the frame is the caller's
// own and the call costs one pointer comparison: opCallV calls the body in
// registers then (valuecall.go), and callValue makes every other call. A
// function value the builder makes to
// call the program's impl functions on a caller's behalf (an Iter view's
// `each_while`, a comparator) keeps nothing, and its calls run under the
// caller's fields as a dispatched call's do. A supervised task starts from
// its body's kept fields with their deadline dropped, and runs the body
// under them as they are (supervisedOn).
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
// per element. A Range's parts (`Range.start`, `Range.stop`,
// `Range.is_inclusive`) are pure hosts over its record (rangehosts.go),
// so a refinement rule that reads them runs when a literal is checked.
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
// # What a program reaches
//
// One walk (reach.go) answers what a program can run from its roots: main,
// test cases or exports, with their boots. It follows calls, every
// retained impl of a dispatched method and of a bound call's (an abstract
// body's call through a type parameter's bound, which the compiler compiles
// to a failure, since no machine runs one; it reaches the implementations
// every symbol of the function's name records, since a std interface of
// irbuild's fixed table records each file's under a symbol of its own:
// boundImpls), function values, once
// initializers and the bodies the machine's tables run. An effects walk
// may also take late roots (ReachedEffects' late, vmhost's public
// functions of a project): each one nothing reached, once the others are
// walked, and then every body outside std that a table or a dispatch
// selects for a value of a declared type that code the late roots reach
// makes (madeBodies). A table that
// selects a body by a value's type name (Display, Equatable, Hashable) is
// read through the type of the operand that renders, compares or hashes:
// the walk reaches the bodies of the declared types that type is or holds,
// through fields, variants, elements and type arguments (heldTypes), and
// every body only where the type does not bound what it holds (an
// interface value, an erased position, a layout not stated, an Iter
// rendered). A rendering's Debug bodies are the list the builder attached
// to it. A failed test's report renders a value through one function,
// rowText, which runs the hand-written Debug body of each declared type
// the value is or holds, found and called by renderHook as a Debug
// rendering's held bodies are (a box selects the body by its name and
// passes the value inside, and a host handle, a Regex, by its TypeName,
// which is its type's symbol name): an assertion's `values:` rows, a
// subtest's row,
// and the `returned:` block of a case a `?` or its group's boot ended
// early (returnedText, on a frame detached from the case's activation).
// So an assertion reaches the hand-written Debug bodies of the declared
// types its values are or hold, as a Display rendering does, and a `?` and
// a boot's result those of the types their `Err` payload holds
// (returnedHeld).
// On a machine Extend extended (the REPL's) a rendering also reaches the
// hand-written Debug body the machine holds for a type its list names no
// body for, which a later input wrote (debugImplHook); the walk does not
// list it.
// It queues a table's bodies, a method's impls included, in
// the order of their type names, so the chain it reports for a function
// is the same on every walk (TestReach_TheWalkIsTheSameEveryTime). A
// built image keeps only what it reaches (PruneImage), the
// pre-run check names a missing body within it (Machine.Unretained), and
// effects lists and target checks read the calls in it. An editor's hover
// also starts it from each function of the program those walks did not
// reach (EffectWalk.Walked, vmhost's editorWalks). An effect
// list names every crossing that is neither pure, held nor unlisted
// (effects.go, listedCrossing): a held crossing (`intrinsic.held`,
// `Binding.Held`) acts only through a value its operand already holds, a
// Reader, a channel end or a task, and the list names the effect where
// that value was made, as it does for a dispatch into a std implementation
// of a type only std code makes (a store from `storage.local`), whose
// effects are named at the call that makes the value (heldDispatch); the
// one unlisted crossing, `dbg`
// (`intrinsic.unlisted`), is reported with its call marked Unlisted and
// named by its key, and no list names it.
// TestReach_EveryEnteredFunctionIsReached holds it to the machine: every
// function a run of a corpus, tour, failure or std program or test case
// enters is in the walk from that run's roots.
//
// [Machine.ReachCase] is the walk from one test case that `nomi test` takes
// before running it (casereach.go): what it reaches that the machine cannot
// run, the effects its code can have in Effects' sense, the seeded hashes
// it can read (a case that reads one may answer otherwise in another
// process, so CaseReach.Pure counts it with the effects), and the code
// itself as parts, each reached function by key with its sum without positions
// (ir.BodySum) and the lines of its assertions whose failure the program
// reads as a value. vmhost's stored passes fingerprint a case by its parts.
// Since the walk is a superset of what a run enters, a part a run could
// enter is never missing. `nomi test --reaching` selects by the same walk:
// [FuncsAt] names the functions a source line is in and
// [CaseReach.Reaches] whether a case's walk reached one.
//
// # Effects
//
// [Machine.Effects] answers what stops a function being run where nothing
// may act on the world and the run must end: a refinement rule, a typed
// literal's handler, a checked literal's `check`, an editor's evaluation
// and a debugger's (evaluate.go, debugeval.go). An effect is a crossing
// into Go that its declaration does not mark pure. Purity is declared
// beside each crossing:
// an intrinsic's entry in the intrinsic table (`intrinsic.pure`, vm.go),
// and a stdlib adapter's row in internal/stdlibbindings (`Binding.Pure`),
// which `go generate ./internal/stdlibadapters` carries into the
// generated `stdlibadapters.Pure`. Every other crossing is an effect: the
// tables WithHosts adds (std/compiler's hosts, an embedder's tables)
// and what WithHostLookup answers. A held crossing and `dbg`, which an
// effect list does not name, are effects here. Cost is not purity: a pure crossing
// whose one call costs more as an operand's value grows, rather than its
// size, is also marked `Binding.Unbounded` (`stdlibadapters.Unbounded`;
// `strings.String.repeat`, the Decimal roundings). A call into Go is not
// interrupted and the evaluation's steps bound only the size of what it
// builds, so the same walk reports each call into one apart from the
// effects (`Effects.Unbounded`), and each of those callers refuses it with
// its own message; no intrinsic is unbounded. A pure crossing whose answer
// is a hash keyed by the run's seed (rt's hashkey.go) is marked seeded:
// `intrinsic.seeded` on `vm.hash_value`, and `Binding.Seeded`
// (`stdlibadapters.Seeded`) on `strings.String.hash` and the other `.hash`
// rows. The walk reports each call into one apart too (`Effects.Seeded`).
// A rule, a typed literal's handler and a `check` refuse it, since the
// compiler runs them once and their answer must hold in every run; an
// editor's and a debugger's evaluation run it. Building a Map or a Set
// reaches no seeded crossing: rt hashes a key in Go, and the walk reaches
// only a key body the program wrote (keyOperands), as it reaches any call.
// So a rule that keys a Map by a type whose hand-written Hashable reads a
// String's hash is refused, and must be: such a hash decides which keys
// share an entry where it disagrees with the type's `equal`, and a body
// that can fail on the value it computes fails under some seeds only.
// Reading an app field, or
// replacing one with `with`, is no effect: the field is an input the
// caller did not write. The walk reports each field read or replaced apart
// (`Effects.AppFields`, appFieldOf, which EffectWalk.Uses spreads back
// along the effect walk's edges for an editor's hover),
// since code run with no app (a rule, a literal, an editor's evaluation)
// has none to read; a debugger's evaluation reads the app of the frame it
// stopped in (rt.WithAppOf). A debug machine reads the
// pure mark to run a pure crossing again in a replay instead of recording
// it (debugtravel.go). TestPureCrossing_ReadsTheDeclarations,
// TestUnboundedCrossing_ReadsTheDeclarations and
// TestSeededCrossing_ReadsTheDeclarations hold the lookups to the
// declarations.
//
// An editor's marks read the same test over an effect walk's edges
// (EffectWalk.Acts and ActCalls, effects.go): a function can act when the
// edges lead from it to a body that makes an effect in this sense, a held
// crossing included, along every edge but a `once`'s initializer, since a
// read of a once is a value. A call in code outside std acts when it makes
// such a crossing, calls a function that can act, dispatches to an
// implementation that can (ActCall.Some names a call only some of whose
// implementations can), or is passed a function value whose body can.
//
// Effect is also the word a user reads: a function is pure, or it has
// effects, and an effects list names the effects a program has
// (effects.go), as an editor's hover and the stdlib reference say them.
// An intrinsic's `world`, like a std row's World, is a build fact, never
// shown as such: whether a crossing acts outside the machine, which
// decides what a target can run and never what a list names.
//
// # Waits and deadlock
//
// Each intrinsic that can wait (a channel's send and receive,
// `Channel.select`, the task waits, the end of a `concurrent` block, the
// read of a `once` another task forces) hands rt its call's position as
// the wait's rt.Site, and each spawn hands the spawn's, so rt's deadlock
// report names where every task waits and where it was spawned (rt package
// doc, "Deadlock"). Main and Call run as a main line of rt's task registry
// (rt.MainLine), so a deadlock among a run's tasks is the fault of the main
// line's wait, placed as any rt trap is; a test case is one through rt's
// test runner. The view a test case under a virtual clock runs on also
// notes each call into Go on the calling task's record (Machine.noteGo,
// rt.EnterGo), which is where a case that cannot be stopped waits
// (caselimit.go).
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
