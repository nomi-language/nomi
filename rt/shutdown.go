package rt

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// InstallShutdownSignals is called by generated executable entry points after
// boot. Libraries and test runners do not install process-wide signal handlers.
func InstallShutdownSignals(fr *Frame) func() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		var first os.Signal
		select {
		case first = <-signals:
		case <-done:
			return
		}
		code := 130
		if s, ok := first.(syscall.Signal); ok {
			code = 128 + int(s)
		}
		go func() {
			select {
			case <-signals:
				os.Exit(code)
			case <-done:
			}
		}()
		DrainSupervisors()
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "nomi: %v\n", r)
				}
			}()
			RunBootCleanup(fr)
		}()
		os.Exit(code)
	}()
	return func() { signal.Stop(signals); close(done) }
}
