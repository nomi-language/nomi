// Command nomi-vm-reactor is nomi-vm linked as a WASI reactor: the wasip1
// module `nomi build --target wasip1/wasm` writes for a program with at
// least one `export fn`. It is built with `-buildmode=c-shared`, so it
// exports Go's `_initialize` instead of `_start`, and a runner calls the
// program's exported functions through a generic ABI over linear memory
// (reactor.go):
//
//	nomi_alloc(len: i32) -> i32
//	nomi_call(name_ptr, name_len, args_ptr, args_len: i32) -> i64
//	nomi_free(ptr, len: i32)
//	nomi_load(ptr, len: i32) -> i64
//	nomi_main() -> i32
//
// Go fixes a module's export names when it is linked, and this runner is
// shared by every program, so the program's own function names travel as
// data. It reads the program from its own linear memory, where `nomi build`
// wrote it, as nomi-vm does (vmrunner/image_wasip1.go), so it needs no file
// access, and runs it on the same VM.
//
// With nothing embedded (`nomi build-vm --reactor` copies it out as it
// is), its image area's length is zero, and a runner hands it one .nomic
// file through nomi_load instead.
//
// The build tag nomi_compiler links std/compiler's hosts (compiler.go), as
// it does for nomi-vm. On any target but wasip1 it is a stub that says what
// it is (other.go).
package main

import "github.com/nomi-language/nomi/vmrunner"

// options are the runner's host options: std/compiler's hosts when built
// with the nomi_compiler tag, nothing otherwise.
var options []vmrunner.Option
