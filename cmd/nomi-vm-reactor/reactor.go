//go:build wasip1

package main

// The reactor's call ABI. Go's `_initialize` runs the runtime and package
// initialization and returns; main is never called. The program boots on
// the first nomi_call or nomi_main rather than in `_initialize`: Go gives a
// c-shared module no hook of its own there except package init functions,
// whose order across this package's files (compiler.go adds an option) is
// the order the go command presents them in, and a boot that fails then
// has a caller to answer: nomi_call's `{"error": ...}`, nomi_main's stderr
// and status 1. Each later call reuses the booted program.
//
// Memory. Every buffer handed to the runner (one nomi_alloc made, or a
// result nomi_call wrote) is a Go []byte held in `buffers` under its
// address until nomi_free, so the collector keeps it, and Go's collector
// does not move objects, so the address stays valid. nomi_call copies the
// name and arguments out of the runner's buffers before it runs anything,
// so the runner may free or reuse them as soon as it returns.
//
// The module is single-threaded and a runner calls one export at a time.
// Goroutines a call starts (Nomi tasks) run while some export is running;
// between calls they wait.

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/nomi-language/nomi/vmrunner"
)

// main is never called in a c-shared module; a main package needs one.
func main() {}

var (
	booted   bool
	instance *vmrunner.Instance
	bootErr  error
	// buffers holds every buffer the runner may read or write, by address,
	// until it frees it.
	buffers = map[uintptr][]byte{}
)

// program boots the module's program once and answers it, or why it could
// not boot.
func program() (*vmrunner.Instance, error) {
	if !booted {
		booted = true
		image, err := vmrunner.OwnImage()
		if err != nil {
			bootErr = fmt.Errorf("this module's Nomi program cannot be read: %v", err)
		} else {
			instance, bootErr = vmrunner.Boot(image, os.Stdout, os.Stderr, os.Stdin, vmrunner.ProgramArgs(), options...)
		}
	}
	return instance, bootErr
}

// hold keeps buf for the runner until nomi_free and answers its address.
func hold(buf []byte) uint32 {
	if len(buf) == 0 {
		buf = make([]byte, 1)
	}
	p := uintptr(unsafe.Pointer(&buf[0]))
	buffers[p] = buf
	return uint32(p)
}

// nomiAlloc reserves size bytes for the runner to write into and answers
// their address, or 0 for a negative size.
//
//go:wasmexport nomi_alloc
func nomiAlloc(size int32) uint32 {
	if size < 0 {
		return 0
	}
	return hold(make([]byte, size))
}

// nomiFree releases a buffer nomiAlloc reserved or a result nomiCall wrote.
// An address it did not hand out is ignored.
//
//go:wasmexport nomi_free
func nomiFree(ptr unsafe.Pointer, size int32) {
	delete(buffers, uintptr(ptr))
}

// nomiCall calls the exported function the UTF-8 name names with the JSON
// array of arguments, and answers where its result is: the address in the
// high 32 bits and the length in the low 32 bits. The result is UTF-8 JSON,
// `{"ok": value}` or `{"error": "diagnostic"}`, held until nomi_free.
//
//go:wasmexport nomi_call
func nomiCall(namePtr unsafe.Pointer, nameLen int32, argsPtr unsafe.Pointer, argsLen int32) uint64 {
	var result []byte
	switch {
	case nameLen < 0 || argsLen < 0 || nameLen > 0 && namePtr == nil || argsLen > 0 && argsPtr == nil:
		result = vmrunner.ErrorResult(fmt.Errorf("nomi_call was handed a negative length or a null pointer"))
	default:
		name := string(unsafe.Slice((*byte)(namePtr), nameLen))
		args := append([]byte(nil), unsafe.Slice((*byte)(argsPtr), argsLen)...)
		in, err := program()
		if err != nil {
			result = vmrunner.ErrorResult(err)
		} else {
			result = in.CallResult(name, args)
		}
	}
	return uint64(hold(result))<<32 | uint64(uint32(len(result)))
}

// nomiMain runs the program's `fn main` over WASI stdin, stdout and
// stderr, and answers the exit status `nomi run` would: 0, or 1 after the
// failure is written to stderr. A program with no `fn main` answers 1 and
// says so.
//
//go:wasmexport nomi_main
func nomiMain() int32 {
	in, err := program()
	if err == nil {
		err = in.Main()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}
