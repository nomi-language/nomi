//go:build !unix

package rt

import (
	"os"
	"runtime"
)

// systemSignals is empty: this system delivers none of the signals a
// program may receive.
var systemSignals = map[string]os.Signal{}

// signalsUnsupported is why: Windows has no SIGHUP, SIGUSR1, SIGUSR2 or
// SIGWINCH, and
// a browser or a WASI host sends a program no signal at all.
var signalsUnsupported = func() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows has no SIGHUP, SIGUSR1, SIGUSR2 or SIGWINCH"
	case "js":
		return "a browser sends a program no signals"
	case "wasip1":
		return "WASI sends a program no signals"
	}
	return runtime.GOOS + " has no SIGHUP, SIGUSR1, SIGUSR2 or SIGWINCH"
}()
