// Package cpuprofile writes a Go CPU profile of a process that runs a Nomi
// program, for work on the VM.
//
// When NOMI_CPUPROFILE names a file, Start begins a runtime/pprof CPU
// profile into it, and the stop function it returns finishes the profile.
// `nomi run` and `nomi-vm` (so every built executable) call Start before the
// program runs and stop before the process exits with the program's status.
// `go tool pprof <binary> <file>` then reads the profile; the VM is Go, so
// its functions (internal/vm's dispatch loop, rt's collections) are the
// frames. A Nomi function is not a frame: the profile shows where the VM
// spends its time, not which Nomi function it was running.
package cpuprofile

import (
	"fmt"
	"os"
	"runtime/pprof"
)

// Start begins the profile NOMI_CPUPROFILE asks for and returns the function
// that ends it. With the variable unset it does nothing. A profile that
// cannot start is reported on stderr and the program runs unprofiled.
func Start() (stop func()) {
	path := os.Getenv("NOMI_CPUPROFILE")
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nomi: NOMI_CPUPROFILE: %v\n", err)
		return func() {}
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintf(os.Stderr, "nomi: NOMI_CPUPROFILE: %v\n", err)
		f.Close()
		return func() {}
	}
	return func() {
		pprof.StopCPUProfile()
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "nomi: NOMI_CPUPROFILE: %v\n", err)
		}
	}
}
