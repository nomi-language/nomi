//go:build unix

package ffirun

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// The helper child for TestRunForwardingSignals_TerminateReachesTheChild:
// it reports readiness, then exits 7 when SIGTERM arrives and 1 if it never
// does.
func TestSignalHelperProcess(t *testing.T) {
	if os.Getenv("NOMI_FFIRUN_SIGNAL_HELPER") != "1" {
		t.Skip("helper child, run only by TestRunForwardingSignals_TerminateReachesTheChild")
	}
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	os.Stdout.WriteString("ready\n")
	select {
	case <-terms:
		os.Exit(7)
	case <-time.After(10 * time.Second):
		os.Exit(1)
	}
}

// A SIGTERM sent to this process alone reaches the child it waits on, as
// `kill <nomi pid>` must reach the wrapper `nomi run` started.
func TestRunForwardingSignals_TerminateReachesTheChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalHelperProcess$")
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_SIGNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- runForwardingSignals(cmd) }()

	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper child did not report ready: %q, %v", line, err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 7 {
			t.Fatalf("child ended with %v, want exit 7 from the forwarded SIGTERM", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the child never received the SIGTERM sent to this process")
	}
}
