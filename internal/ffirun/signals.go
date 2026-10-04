package ffirun

import (
	"os"
	"os/exec"
	"os/signal"
)

// runForwardingSignals starts cmd, passes the interrupt and terminate signals
// this process receives on to it, and waits for it to exit.
//
// The wrapper is a child process, so a signal sent to `nomi` alone (a
// `kill <pid>`, a supervisor stopping a service) would otherwise end `nomi`
// and leave the wrapper running: a server kept its port after `nomi run`
// exited. A terminal's Ctrl-C reaches both processes already, and the
// forwarded copy only repeats it.
func runForwardingSignals(cmd *exec.Cmd) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, forwardedSignals...)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	return cmd.Wait()
}
