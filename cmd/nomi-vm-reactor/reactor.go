//go:build wasip1

package main

// The reactor's call ABI. Go's `_initialize` runs the runtime and package
// initialization and returns; main is never called.
//
// A reactor has at most one program for its life. One `nomi build`
// embedded in its memory boots on the first nomi_call or nomi_main rather
// than in `_initialize`: Go gives a c-shared module no hook of its own there
// except package init functions, whose order across this package's files
// (compiler.go adds an option) is the order the go command presents them
// in, and a boot that fails then has a caller to answer: nomi_call's
// `{"error": ...}`, nomi_main's stderr and status 1. A reactor with none
// (its image area's length is zero, as `nomi build-vm --reactor` writes
// it) takes one through nomi_load, which boots it at once and answers the
// boot's failure. An empty nomi_load boots the embedded one the same way.
// Each later call reuses the booted program. A second program is refused rather
// than swapped in: a booted program leaves state in the module (its `once`
// values, tasks it started, the Go heap it grew), and the runner gets a
// clean one by instantiating the compiled module again.
//
// Memory. Every buffer handed to the runner (one nomi_alloc made, or a
// result nomi_call or nomi_load wrote) is a Go []byte held in `buffers`
// under its address until nomi_free, so the collector keeps it, and Go's
// collector does not move objects, so the address stays valid. nomi_call
// and nomi_load copy what they read out of the runner's buffers before they
// run anything, so the runner may free or reuse them as soon as they return.
//
// The module is single-threaded and a runner calls one export at a time.
// Goroutines a call starts (Nomi tasks) run while some export is running;
// between calls they wait.

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/nomi-language/nomi/vmrunner"
)

// main is never called in a c-shared module; a main package needs one.
func main() {}

var (
	// instance is the booted program, and bootErr why it could not boot;
	// once either is set it answers every later call.
	instance *vmrunner.Instance
	bootErr  error
	// loaded is set when nomi_load booted the program.
	loaded bool
	// buffers holds every buffer the runner may read or write, by address,
	// until it frees it.
	buffers = map[uintptr][]byte{}
)

// errNoProgram is a call to a reactor that has no program yet.
var errNoProgram = errors.New("this reactor has no program yet: write a compiled program (.nomic) into memory " +
	"from nomi_alloc and pass it to nomi_load first")

// errNoBuiltIn is an empty nomi_load, which asks for the program built into
// the module, to a reactor that has none.
var errNoBuiltIn = errors.New("this reactor has no program built in: pass it a compiled program (.nomic)")

// program answers the booted program, booting the one `nomi build`
// embedded on the first call, or why there is none.
func program() (*vmrunner.Instance, error) {
	if instance != nil || bootErr != nil {
		return instance, bootErr
	}
	image, err := vmrunner.OwnImage()
	if errors.Is(err, vmrunner.ErrNoImage) {
		return nil, errNoProgram
	}
	if err != nil {
		bootErr = fmt.Errorf("this module's Nomi program cannot be read: %v", err)
	} else {
		instance, bootErr = vmrunner.Boot(image, os.Stdout, os.Stderr, os.Stdin, vmrunner.ProgramArgs(), options...)
	}
	return instance, bootErr
}

// packed is a result held for the runner, as nomi_call and nomi_load
// answer it: the address in the high 32 bits and the length in the low 32.
func packed(result []byte) uint64 {
	return uint64(hold(result))<<32 | uint64(uint32(len(result)))
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

// nomiFree releases a buffer nomiAlloc reserved or a result nomiCall or
// nomiLoad wrote.
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
	return packed(result)
}

// nomiLoad boots the compiled program the runner wrote at ptr, the whole
// of a .nomic file as `nomi build --no-vm` writes it, and answers where its
// result is, as nomiCall does: `{"ok": [<signature>, ...]}` listing the
// program's exported functions, or `{"error": "diagnostic"}`. A reactor
// that has a program, embedded or loaded, refuses another. A load that
// fails leaves the reactor with no program, so another nomi_load may
// follow.
//
// With size 0 it loads nothing: it boots the program `nomi build` embedded,
// as the first nomi_call would, and answers the same result, so a runner
// learns the exported functions, or why the program cannot boot, before it
// calls one. A reactor with nothing embedded answers errNoBuiltIn.
//
//go:wasmexport nomi_load
func nomiLoad(ptr unsafe.Pointer, size int32) uint64 {
	var result []byte
	_, ownErr := vmrunner.OwnImage()
	embedded := !errors.Is(ownErr, vmrunner.ErrNoImage)
	switch {
	case size < 0 || size > 0 && ptr == nil:
		result = vmrunner.ErrorResult(fmt.Errorf("nomi_load was handed a negative length or a null pointer"))
	case loaded:
		result = vmrunner.ErrorResult(errors.New("this reactor already loaded a program, and a reactor runs one " +
			"program for its life. To run another, instantiate the module again"))
	case size == 0 && embedded:
		if in, err := program(); err != nil {
			result = vmrunner.ErrorResult(err)
		} else {
			result = in.ExportsResult()
		}
	case size == 0:
		result = vmrunner.ErrorResult(errNoBuiltIn)
	case embedded:
		result = vmrunner.ErrorResult(errors.New("this reactor carries the program `nomi build` embedded in it, " +
			"so it loads no other. Load programs into a reactor `nomi build-vm --reactor` writes, which carries none"))
	default:
		data := append([]byte(nil), unsafe.Slice((*byte)(ptr), size)...)
		in, err := vmrunner.LoadProgram(data, os.Stdout, os.Stderr, os.Stdin, vmrunner.ProgramArgs(), options...)
		if err != nil {
			result = vmrunner.ErrorResult(err)
		} else {
			instance, loaded = in, true
			result = in.ExportsResult()
		}
	}
	return packed(result)
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
