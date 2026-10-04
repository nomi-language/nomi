//go:build unix

package ffirun

import (
	"os"
	"syscall"
)

// forwardedSignals are the signals runForwardingSignals passes on to the
// wrapper: interrupt, terminate, and a hangup from a closed terminal.
var forwardedSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
