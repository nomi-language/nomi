//go:build !unix

package ffirun

import "os"

// forwardedSignals are the signals runForwardingSignals passes on to the
// wrapper. Outside Unix only an interrupt is delivered to a process.
var forwardedSignals = []os.Signal{os.Interrupt}
