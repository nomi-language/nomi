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
package vm
