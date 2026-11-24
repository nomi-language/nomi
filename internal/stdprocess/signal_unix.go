//go:build unix

package stdprocess

import (
	"os"
	"os/exec"
	"syscall"
)

// signals are the signals std/process's `Signal` names, by their POSIX
// names, which its Display writes.
var signals = map[string]syscall.Signal{
	"SIGTERM": syscall.SIGTERM,
	"SIGINT":  syscall.SIGINT,
	"SIGHUP":  syscall.SIGHUP,
	"SIGKILL": syscall.SIGKILL,
	"SIGUSR1": syscall.SIGUSR1,
	"SIGUSR2": syscall.SIGUSR2,
	"SIGSTOP": syscall.SIGSTOP,
	"SIGCONT": syscall.SIGCONT,
	"SIGQUIT": syscall.SIGQUIT,
}

// sendSignal sends the signal named name to p, or to the process group p
// leads when group is set. A group outlives its leader while any process
// in it runs, so a group signal reaches a script's children after the
// script has ended.
func sendSignal(p *os.Process, name string, group bool) error {
	sig, ok := signals[name]
	if !ok {
		return errUnsupported
	}
	if group {
		return syscall.Kill(-p.Pid, sig)
	}
	return p.Signal(sig)
}

// newGroup makes cmd's child the leader of a process group of its own, so
// a signal to the group reaches every process it starts, and a signal a
// terminal sends its foreground group (an interrupt typed at it) does not
// reach the child.
func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
