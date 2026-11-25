// Package stdwasm is std/wasm/host: WebAssembly modules compiled and run
// by an engine. std/wasm is Nomi, lowered with each program, over it;
// internal/hostgen/stdtable's alsoImplements names this package as
// std/wasm/host's.
//
// # Engines
//
// The engine is behind one small interface (engine.go): compile bytes to a
// module, instantiate it with a memory limit and its imports, call an
// export, and read, write and size the instance's memory. Everything
// std::wasm decides is above it, once, in wasm.go: the types of imports,
// exports, arguments and results, which it reads from the module's bytes
// itself (binary.go), the value encoding, the failures, the traps and their
// kinds, the dead, dropped and expired states, the per-instance lock, the
// stopping answers and run's convention.
//
// Every build but js runs wazero (github.com/tetratelabs/wazero, pure Go;
// engine_wazero.go). A js build, the browser's and the tour's, runs the
// host's own engine through syscall/js (engine_js.go), and links no
// wazero. wasip1 has neither: no runner for it has std::wasm
// (internal/stdlibbindings/families).
//
// The engines differ in one way a program sees: on the host's engine a
// trap after the module asked its memory to grow past the limit is the
// trap it is (an `unreachable`, say), where wazero, whose allocator sees
// the refusal, names it memory_limit. The growth itself fails inside the
// module on both. A module's compile error is the engine's message, and
// the host's engine may compile a module of a newer proposal that wazero
// refuses.
//
// The tests run on the engine of their build: natively with `go test`, and
// on the host's under Node with scripts/test-js.sh, which every case passes
// but for that one difference (refusedGrowthTrap).
//
// # Modules
//
// Compile checks and compiles a module's bytes, once a process for the
// same bytes (by their SHA-256). On wazero each module gets a runtime of
// its own, whose compilation cache is the module's own directory under the
// artifact cache, artifacts/wasm/<sha256>/ (cache.go), so a later process
// reads the machine code back instead of compiling again, and the artifact
// cache's one pruning rule keeps it: using a module touches its directory,
// and a process prunes the directories nothing has used for two weeks once
// a day. With no artifact cache (NOMI_ARTIFACT_CACHE=off, or a target with
// no file system) a module compiles in each process. On amd64 and arm64
// wazero compiles to machine code; on other architectures it interprets,
// 20 to 50 times slower. The host's engine compiles to machine code
// itself and keeps nothing on disk.
//
// # Instances
//
// Instantiate makes an instance of a module whose function imports are the
// Nomi functions it was given (serve). Nothing else is given to a module:
// no WASI, and no import of a memory, a table or a global, which answers
// missing_import. Each import's declared types are checked against the
// module's at instantiation, and a module whose initial memory is over the
// limit is memory_limit before the engine makes it.
//
// On wazero one host module per import module name, instantiated once a
// module, serves the imports: its functions find the instance whose call
// reached them in the call's context. An instance's memory is allocated
// through experimental.WithMemoryAllocator, which grows it to the limit
// and refuses past it, so `memory.grow` answers -1 inside the module. Its
// buffer is one a closed instance left in spare when one there is large
// enough, and goes back there when it closes, up to 64 MiB held in all:
// a `run` makes and drops an instance a call, and megabytes allocated
// for each kept the collector busy for more than half the call's time.
// Every byte is cleared before the memory holds it, the bytes a growth
// adds included, so an instance sees nothing an earlier one wrote
// (TestRun_AReusedMemoryHoldsNothingAnEarlierRunLeft). Each instance is
// still a new one: its globals, tables and data segments are the
// module's own, set up by wazero. On
// the host's engine every value crosses JavaScript as i32s, through
// trampoline modules that split an i64 or an f64 into halves and keep a
// float's bits; the import trampoline also stops the wasm when an import
// failed. A module is compiled with each memory's maximum lowered to the
// limit and its first memory exported, which JavaScript reaches only
// through an export. An import's Go function runs while JavaScript waits in
// the call into the module, so it must not wait for anything that needs
// JavaScript's event loop (in Node, a write to the process's output); a
// channel between tasks or a sleep is fine. engine_js.go has the detail.
//
// One operation runs on an instance at a time (instance.mu), a call
// holding the lock for its length, so a task waits while another task's
// call runs, and an import must not call the instance it serves. A call
// that traps leaves the instance dead and closed; an import whose Nomi
// function fails with a Nomi trap stops the wasm, and once the instance is
// dead the call goes on unwinding with the same panic, so the trap is the
// trap of the call that reached the import. A trap is classified by the
// engine's text for it (engineTraps, one table for wazero and each host
// engine), an `other` trap carrying wazero's text, so a trap reads the
// same on every engine; TestCall_TrapsLeaveTheInstanceDead provokes each
// kind. An instance looks each export up in the engine once and keeps it
// (instance.exports).
//
// Nothing interrupts a call: the module runs until it returns or traps, as
// a Go call does (the Unbounded rows). A call is one call into the host,
// so while its task is stopping (cancelled, past its deadline, or its
// context ended by a test case's limit) an import's Nomi function runs to
// its end on rt.Unstoppable's frame, as a deferred call runs, and the task
// stops at its next safe point once the call returns. An import may declare
// the values it answers while its task is stopping (instance.stopping);
// serve answers them without running Nomi code, so a module that polls an
// import to learn whether to stop, as SQLite's progress handler does, ends
// its call early and stays usable.
//
// # The boundary
//
// A value crosses as two Int64s, its kind (0 i32, 1 i64, 2 f32, 3 f64) and
// its bits, an i32 sign-extended and a float as its IEEE pattern. A NaN
// keeps its bits here, on both engines; std/wasm reads every NaN as
// Float.nan(), so the bits of a NaN an architecture makes never reach a
// program. A failure crosses as a list of strings, its kind first
// ("Failures" in wasm.go), which std/wasm reads into its Error.
//
// Run is `wasm.run` and RunOn `Instance.run`, by one convention
// (instance.convention): `alloc(size) -> ptr`, the input written there,
// the named function called with (ptr, len), and the (ptr << 32 | len) it
// returns read back. Run does it in a fresh instance with no imports and
// the limit it is given, dropped after; RunOn in a live instance, so a
// request and its answer cross in one call. Run makes a new instance
// rather than resetting a kept one: wazero can neither shrink a memory
// nor set a global the module does not export, restore a table, or undo
// a `data.drop`, and a codec's call grows its memory, so no reset is both
// sound and of use to the calls that cost most. What a new instance costs
// is mostly its memory, which spare keeps cheap.
//
// The modules the tests run are assembled by fixtures_test.go from the
// text in testdata/<name>.wat, with no WebAssembly toolchain, by the
// encoders binary.go also assembles the trampolines with.
package stdwasm
