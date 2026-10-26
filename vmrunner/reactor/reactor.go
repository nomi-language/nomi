// Package reactor is the WASI reactor's call ABI: a wasip1 main package
// that imports it and is linked with -buildmode=c-shared exports
// `_initialize` and, from this package (//go:wasmexport),
//
//	nomi_alloc(len: i32) -> i32
//	nomi_call(name_ptr, name_len, args_ptr, args_len: i32) -> i64
//	nomi_free(ptr, len: i32)
//	nomi_load(ptr, len: i32) -> i64
//	nomi_main() -> i32
//
// over the program `nomi build` embedded in the module's memory, or one a
// runner hands nomi_load. cmd/nomi-vm-reactor is such a main package, and
// so is the runner `nomi build --target wasip1/wasm` generates for a
// project with Go bindings and an `export fn` (internal/ffirun), which
// passes its host table to Use. On any other target the package declares
// Use and nothing else.
package reactor

import "github.com/nomi-language/nomi/vmrunner"

// options are the host options the program boots with.
var options []vmrunner.Option

// Use adds host options to the ones the reactor boots its program with. A
// main package calls it from an init function: `_initialize` runs package
// initialization and nothing else, and the program boots on the first call
// after it.
func Use(opts ...vmrunner.Option) {
	options = append(options, opts...)
}
