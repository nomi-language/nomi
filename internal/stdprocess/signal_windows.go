//go:build windows

package stdprocess

import (
	"os"
	"os/exec"
	"syscall"
)

// sendSignal kills p for SIGKILL, which Windows does with
// TerminateProcess: Windows has no signals one process sends another, and
// Go sends none of the others there, so they, and every signal to a group,
// are Unsupported. Go releases a Windows process's handle when its wait
// returns, and a kill after that answers EINVAL where Unix answers
// os.ErrProcessDone, so it is ErrProcessDone here too: the child has
// ended.
func sendSignal(p *os.Process, name string, group bool) error {
	if name != "SIGKILL" || group {
		return errUnsupported
	}
	if err := p.Kill(); err != syscall.EINVAL {
		return err
	}
	return os.ErrProcessDone
}

// newGroup starts cmd's child in a new process group, so a Ctrl-C typed at
// the console does not reach it.
func newGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
