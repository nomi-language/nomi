package stdwasm

// An engine runs a module's code: wazero (engine_wazero.go, every target
// but js) or the host's own WebAssembly engine through syscall/js
// (engine_js.go). Everything std::wasm decides is above it, in wasm.go: the
// types of imports, exports, arguments and results, the value encoding,
// the failures and traps and their kinds, the dead, dropped and expired
// states, the per-instance lock and run's convention. An engine compiles,
// instantiates, calls, and reads and writes memory, and answers a trap as
// the engine's own text, which wasm.go classifies.
//
// The engine of a build is the one of these files its GOOS compiles. Its
// compileEngine(data, sum, info) compiles data, whose SHA-256 is sum and
// which info describes (nil when std::wasm could not read the bytes, which
// the engine then only checks), or answers its reason the bytes are not a
// module.

// engineModule is a compiled module.
type engineModule interface {
	// instantiate makes an instance whose memory grows to limit bytes and
	// no further (memory.grow answers -1 inside the module past it; limit
	// is whole pages and at least the module's initial memory) and whose
	// function import i is answered by host(i, mem, stack): mem the
	// instance's memory (nil while the start function runs on an engine
	// that has none to give yet), the arguments in stack, the results
	// written over them, and false to stop the wasm, which then traps. Two
	// imports of one module and name are answered as the first. The
	// module's start function runs; `_initialize` does not.
	instantiate(limit uint64, host hostFunc) (engineInstance, error)
	// close frees the module's code.
	close()
}

// hostFunc answers a module's function import i over stack.
type hostFunc func(i int, mem engineMemory, stack []uint64) bool

// engineInstance is one instance of a module.
type engineInstance interface {
	// function is the export name, a function of typ.
	function(name string, typ funcType) engineFunc
	// call calls fn with its arguments in stack, its results written over
	// them, or answers the trap it stopped on. stack is as long as fn's
	// parameters or its results, whichever are more.
	call(fn engineFunc, stack []uint64) error
	// memory is the instance's first memory, or nil when it has none.
	memory() engineMemory
	// refused reports whether the last call asked its memory to grow past
	// the limit; an engine that cannot tell answers false.
	refused() bool
	// close frees the instance.
	close()
}

// engineFunc is an export as an engine calls it.
type engineFunc any

// engineMemory is an instance's linear memory, as wazero's api.Memory has
// it: a read answers a copy or a view the next call may change.
type engineMemory interface {
	Size() uint32
	Read(offset, count uint32) ([]byte, bool)
	Write(offset uint32, data []byte) bool
}
