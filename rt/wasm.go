package rt

// The WebAssembly handles std/wasm/host carries, as slots rt carries and
// never opens: a compiled module, an instance, and an instance's memory
// for the length of one import call. internal/stdwasm builds them over
// an engine; rt names none.

// WasmModule is std/wasm/host's `Compiled`: a compiled WebAssembly module,
// which every instance of it shares and nothing changes.
type WasmModule struct{ module any }

// NewWasmModule wraps module as a value.
func NewWasmModule(module any) WasmModule { return WasmModule{module: module} }

// Module is the compiled module the value wraps.
func (w WasmModule) Module() any { return w.module }

// WasmInstance is std/wasm/host's `Live`: one instance of a module, with
// its own memory and globals, which calls into it change.
type WasmInstance struct{ instance any }

// NewWasmInstance wraps instance as a value.
func NewWasmInstance(instance any) WasmInstance { return WasmInstance{instance: instance} }

// Instance is the instance the value wraps.
func (w WasmInstance) Instance() any { return w.instance }

// WasmMemory is std/wasm/host's `Memory`: the memory of the instance whose
// call reached an import, for the length of that import's call.
type WasmMemory struct{ memory any }

// NewWasmMemory wraps memory as a value.
func NewWasmMemory(memory any) WasmMemory { return WasmMemory{memory: memory} }

// Memory is the memory the value wraps.
func (w WasmMemory) Memory() any { return w.memory }
