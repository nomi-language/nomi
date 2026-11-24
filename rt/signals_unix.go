//go:build unix

package rt

import (
	"os"
	"syscall"
)

// systemSignals are the signals a program may receive as values, by POSIX
// name. SIGINT and SIGTERM are not among them: they are shutdown's.
var systemSignals = map[string]os.Signal{
	"SIGHUP":   syscall.SIGHUP,
	"SIGUSR1":  syscall.SIGUSR1,
	"SIGUSR2":  syscall.SIGUSR2,
	"SIGWINCH": syscall.SIGWINCH,
}

// signalsUnsupported is "": a Unix system delivers each of them.
const signalsUnsupported = ""
