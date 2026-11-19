//go:build wasip1

package reactor

// The reactor's call ABI. Go's `_initialize` runs the runtime and package
// initialization and returns; the main package's main is never called.
//
// A reactor has at most one program for its life. One `nomi build`
// embedded in its memory boots on the first nomi_call or nomi_main rather
// than in `_initialize`: Go gives a c-shared module no hook of its own there
// except package init functions, which is where the main package calls
// Use, and a boot that fails then has a caller to answer: nomi_call's
// `{"error": ...}`, nomi_main's stderr and status 1. A reactor with none
// (its image area's length is zero, as `nomi build-vm --reactor` writes
// it) takes one through nomi_load or nomi_load_with, which boots it at once
// and answers the boot's failure. An empty load boots the embedded one the
// same way, with nomi_load_with's options when it is the first call. Each
// later call reuses the booted program. A second program is refused rather
// than swapped in: a booted program leaves state in the module (its `once`
// values, tasks it started, the Go heap it grew), and the runner gets a
// clean one by instantiating the compiled module again.
//
// A call or main its timeout_ms stopped leaves the program loaded: the stop
// unwinds the call and the tasks it started (vmrunner.Instance), and the
// next call runs on the same booted program. A load its timeout_ms stopped
// leaves no program, as any failed load does.
//
// Memory. Every buffer handed to the runner (one nomi_alloc made, or a
// result nomi_call or nomi_load wrote) is a Go []byte held in `buffers`
// under its address until nomi_free, so the collector keeps it, and Go's
// collector does not move objects, so the address stays valid. The exports
// copy what they read out of the runner's buffers before they run
// anything, so the runner may free or reuse them as soon as they return.
//
// The module is single-threaded and a runner calls one export at a time.
// Goroutines a call starts (Nomi tasks) run while some export is running;
// between calls they wait. Go does not preempt a computing goroutine here,
// so a timeout is read from the clock as the program runs (internal/vm's
// within.go), not left to a timer.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/nomi-language/nomi/vmrunner"
)

var (
	// instance is the booted program, and bootErr why it could not boot;
	// once either is set it answers every later call.
	instance *vmrunner.Instance
	bootErr  error
	// loaded is set when a load booted the program.
	loaded bool
	// timeout is nomi_load_with's "timeout_ms", each later call's bound
	// unless it gives its own; 0 for none.
	timeout time.Duration
	// buffers holds every buffer the runner may read or write, by address,
	// until it frees it.
	buffers = map[uintptr][]byte{}
)

// errNoProgram is a call to a reactor that has no program yet.
var errNoProgram = errors.New("this reactor has no program yet: write a compiled program (.nomic) into memory " +
	"from nomi_alloc and pass it to nomi_load first")

// errNoBuiltIn is an empty load, which asks for the program built into the
// module, to a reactor that has none.
var errNoBuiltIn = errors.New("this reactor has no program built in: pass it a compiled program (.nomic)")

// program answers the booted program, booting the one `nomi build`
// embedded on the first call, with opts, or why there is none.
func program(opts ...vmrunner.Option) (*vmrunner.Instance, error) {
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
		instance, bootErr = vmrunner.BootImage(image, os.Stdout, os.Stderr, os.Stdin, vmrunner.ProgramArgs(), append(options[:len(options):len(options)], opts...)...)
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

// read is a copy of the size bytes at ptr, or ok false for a negative size
// or a null pointer with bytes behind it.
func read(ptr unsafe.Pointer, size int32) (data []byte, ok bool) {
	if size < 0 || size > 0 && ptr == nil {
		return nil, false
	}
	return append([]byte(nil), unsafe.Slice((*byte)(ptr), size)...), true
}

// bound is ctx ending after d, or the background for no bound.
func bound(d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), d)
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

// nomiFree releases a buffer nomiAlloc reserved or a result an export
// wrote. An address it did not hand out is ignored.
//
//go:wasmexport nomi_free
func nomiFree(ptr unsafe.Pointer, size int32) {
	delete(buffers, uintptr(ptr))
}

// nomiCall calls the exported function the UTF-8 name names with the JSON
// array of arguments, and answers where its result is: the address in the
// high 32 bits and the length in the low 32 bits. The result is UTF-8 JSON,
// `{"ok": value}` or `{"error": "diagnostic"}`, held until nomi_free. The
// call is bounded by nomi_load_with's timeout_ms, when it gave one.
//
//go:wasmexport nomi_call
func nomiCall(namePtr unsafe.Pointer, nameLen int32, argsPtr unsafe.Pointer, argsLen int32) uint64 {
	return nomiCallWith(namePtr, nameLen, argsPtr, argsLen, nil, 0)
}

// nomiCallWith is nomiCall with options, a JSON object: "timeout_ms"
// bounds this call in place of nomi_load_with's.
//
//go:wasmexport nomi_call_with
func nomiCallWith(namePtr unsafe.Pointer, nameLen int32, argsPtr unsafe.Pointer, argsLen int32, optsPtr unsafe.Pointer, optsLen int32) uint64 {
	nameData, okName := read(namePtr, nameLen)
	args, okArgs := read(argsPtr, argsLen)
	optsData, okOpts := read(optsPtr, optsLen)
	if !okName || !okArgs || !okOpts {
		return packed(vmrunner.ErrorResult(errors.New("nomi_call was handed a negative length or a null pointer")))
	}
	opts, err := parseOptions("nomi_call_with", optsData, keyTimeout)
	if err != nil {
		return packed(vmrunner.ErrorResult(err))
	}
	in, err := program()
	if err != nil {
		return packed(vmrunner.ErrorResult(err))
	}
	name := string(nameData)
	d := timeout
	if opts.timeout > 0 {
		d = opts.timeout
	}
	ctx, cancel := bound(d)
	defer cancel()
	result, err := in.Call(ctx, name, args)
	if errors.Is(err, context.DeadlineExceeded) {
		err = stopped(fmt.Sprintf("the call to `%s`", name), d)
	}
	return packed(vmrunner.Result(result, err))
}

// nomiLoad boots the compiled program the runner wrote at ptr, the whole
// of a .nomic file as `nomi build --no-vm` writes it, and answers where its
// result is, as nomiCall does: `{"ok": [<signature>, ...]}` listing the
// program's exported functions, or `{"error": "diagnostic"}`. A reactor
// that has a program, embedded or loaded, refuses another. A load that
// fails leaves the reactor with no program, so another load may follow.
//
// With size 0 it loads nothing: it boots the program `nomi build` embedded,
// as the first nomi_call would, and answers the same result, so a runner
// learns the exported functions, or why the program cannot boot, before it
// calls one. A reactor with nothing embedded answers errNoBuiltIn.
//
//go:wasmexport nomi_load
func nomiLoad(ptr unsafe.Pointer, size int32) uint64 {
	return nomiLoadWith(ptr, size, nil, 0)
}

// nomiLoadWith is nomiLoad with options, a JSON object: "std" holds the
// program to a list of std names and "host" its host functions to a list
// of keys (vmrunner.WithCapabilitiesFrom), and "timeout_ms"
// bounds the boot and is every later call's bound. Options for an embedded
// program that an earlier call already booted are refused.
//
//go:wasmexport nomi_load_with
func nomiLoadWith(ptr unsafe.Pointer, size int32, optsPtr unsafe.Pointer, optsLen int32) uint64 {
	return packed(load(ptr, size, optsPtr, optsLen))
}

func load(ptr unsafe.Pointer, size int32, optsPtr unsafe.Pointer, optsLen int32) []byte {
	optsData, okOpts := read(optsPtr, optsLen)
	if size < 0 || size > 0 && ptr == nil || !okOpts {
		return vmrunner.ErrorResult(errors.New("nomi_load was handed a negative length or a null pointer"))
	}
	opts, err := parseOptions("nomi_load_with", optsData, keyStd, keyHost, keyTimeout)
	if err != nil {
		return vmrunner.ErrorResult(err)
	}
	_, ownErr := vmrunner.OwnImage()
	embedded := !errors.Is(ownErr, vmrunner.ErrNoImage)
	switch {
	case loaded:
		return vmrunner.ErrorResult(errors.New("this reactor already loaded a program, and a reactor runs one " +
			"program for its life. To run another, instantiate the module again"))
	case size == 0 && !embedded:
		return vmrunner.ErrorResult(errNoBuiltIn)
	case size > 0 && embedded:
		return vmrunner.ErrorResult(errors.New("this reactor carries the program `nomi build` embedded in it, " +
			"so it loads no other. Load programs into a reactor `nomi build-vm --reactor` writes, which carries none"))
	case size == 0 && (instance != nil || bootErr != nil) && (opts.std != nil || opts.host != nil || opts.timeout > 0):
		return vmrunner.ErrorResult(errors.New("this reactor's program was booted by an earlier call, " +
			"so nomi_load_with's options cannot hold it: pass them in the first call, an empty nomi_load_with"))
	}
	var with []vmrunner.Option
	if opts.std != nil || opts.host != nil {
		with = append(with, vmrunner.WithCapabilitiesFrom(stdVia, opts.std, hostVia, opts.host))
	}
	ctx, cancel := bound(opts.timeout)
	defer cancel()
	if opts.timeout > 0 {
		with = append(with, vmrunner.WithStop(ctx))
	}
	var in *vmrunner.Instance
	if size == 0 {
		// The embedded program, which an empty load may ask for again.
		in, err = program(with...)
		if errors.Is(err, context.DeadlineExceeded) {
			err = stopped("the boot", opts.timeout)
			bootErr = err
		}
	} else {
		data, _ := read(ptr, size)
		in, err = vmrunner.LoadProgram(data, os.Stdout, os.Stderr, os.Stdin, vmrunner.ProgramArgs(), append(options[:len(options):len(options)], with...)...)
		if errors.Is(err, context.DeadlineExceeded) {
			err = stopped("the load", opts.timeout)
		}
		if err == nil {
			instance, loaded = in, true
		}
	}
	if err != nil {
		return vmrunner.ErrorResult(err)
	}
	if opts.timeout > 0 {
		timeout = opts.timeout
	}
	return in.ExportsResult()
}

// nomiMain runs the program's `fn main` over WASI stdin, stdout and
// stderr, and answers the exit status `nomi run` would: 0, an Exit's code,
// or 1, after the failure is written to stderr. A program with no `fn main`
// answers 1 and says so. nomi_load_with's timeout_ms bounds it.
//
//go:wasmexport nomi_main
func nomiMain() int32 {
	in, err := program()
	if err == nil {
		ctx, cancel := bound(timeout)
		err = in.Main(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			err = stopped("main", timeout)
		}
	}
	if err != nil {
		if text := err.Error(); text != "" {
			fmt.Fprintf(os.Stderr, "%s\n", text)
		}
		return int32(vmrunner.ExitCode(err))
	}
	return 0
}
