package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// signalled runs `nomi run` on src and answers the process and a function
// that waits for its next line of output to be want.
func signalled(t *testing.T, src string) (*exec.Cmd, func(want string), *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGHUP to send, and std::signals answers Unsupported there")
	}
	prog := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return watched(t, exec.Command(nomiBin, "run", prog))
}

// watched starts cmd and answers it and a function that waits for its next
// line of output to be want.
func watched(t *testing.T, cmd *exec.Cmd) (*exec.Cmd, func(want string), *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	lines := make(chan string, 64)
	go func() {
		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			lines <- scan.Text()
		}
		close(lines)
	}()
	next := func(want string) {
		t.Helper()
		select {
		case got, open := <-lines:
			if !open {
				t.Fatalf("the program ended before printing %q:\n%s", want, stderr.String())
			}
			if got != want {
				t.Fatalf("the program printed %q, not %q:\n%s", got, want, stderr.String())
			}
		case <-time.After(time.Minute):
			t.Fatalf("the program did not print %q:\n%s", want, stderr.String())
		}
	}
	return cmd, next, &stderr
}

func send(t *testing.T, cmd *exec.Cmd, sig os.Signal) {
	t.Helper()
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// killedBy waits for cmd to end and requires the system to have ended it
// with sig: the signal's default action, not an exit.
func killedBy(t *testing.T, cmd *exec.Cmd, sig syscall.Signal, stderr *bytes.Buffer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatalf("the program did not end:\n%s", stderr.String())
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != sig {
		t.Fatalf("the program ended with %v, not killed by %v:\n%s", cmd.ProcessState, sig, stderr.String())
	}
}

// exited waits for cmd to exit with code.
func exited(t *testing.T, cmd *exec.Cmd, code int, stderr *bytes.Buffer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatalf("the program did not end:\n%s", stderr.String())
	}
	if got := cmd.ProcessState.ExitCode(); got != code {
		t.Fatalf("the program exited with %d, not %d:\n%s", got, code, stderr.String())
	}
}

const signalsShow = `
fn show(got: Option<Signal>) -> String
    case got
        Some(signal) -> "${signal}"
        None         -> "closed"
    end
end
`

// A program that receives Hangup gets one value per SIGHUP and keeps
// running.
func TestSignals_EachSIGHUPIsAValue(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::channels.Receiver
    std::io
    std::signals.{self, Signal}
end

fn main() -> Result<Unit, signals.Error>
    hups = signals.receiver([Signal.Hangup])?
    io.print("ready")
    io.print(show(Receiver.receive(hups)))
    io.print(show(Receiver.receive(hups)))
    io.print(show(Receiver.receive(hups)))
    io.print("done")
    Ok(Unit)
end
`+signalsShow)
	next("ready")
	for range 3 {
		send(t, cmd, syscall.SIGHUP)
		next("SIGHUP")
		time.Sleep(50 * time.Millisecond)
	}
	next("done")
	exited(t, cmd, 0, stderr)
}

// A program that has not subscribed is ended by SIGHUP, as any process
// that does not handle it is.
func TestSignals_SIGHUPEndsAProgramThatDoesNotReceiveIt(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::duration.Duration
    std::io
    std::timer
end

fn main()
    io.print("ready")
    timer.sleep(Duration.seconds(60))
    io.print("slept")
end
`)
	next("ready")
	send(t, cmd, syscall.SIGHUP)
	killedBy(t, cmd, syscall.SIGHUP, stderr)
}

// Two receivers each get each arrival. The one made in an inner
// `concurrent` block closes when the block ends and leaves the other
// working, and when the outer one closes too, SIGHUP ends the program again.
func TestSignals_TwoReceiversAndClosingThem(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::channels.Receiver
    std::duration.Duration
    std::io
    std::signals.{self, Signal}
    std::timer
end

fn main()
    concurrent
        outer = signals.receiver([Signal.Hangup])!

        concurrent
            inner = signals.receiver([Signal.Hangup])!
            io.print("two")
            io.print("outer ${show(Receiver.receive(outer))}")
            io.print("inner ${show(Receiver.receive(inner))}")
        end

        io.print("one")
        io.print("outer ${show(Receiver.receive(outer))}")
    end

    io.print("none")
    timer.sleep(Duration.seconds(60))
    io.print("slept")
end
`+signalsShow)
	next("two")
	send(t, cmd, syscall.SIGHUP)
	next("outer SIGHUP")
	next("inner SIGHUP")
	next("one")
	send(t, cmd, syscall.SIGHUP)
	next("outer SIGHUP")
	next("none")
	// The block's end closes its receiver on a goroutine of its own
	// (context.AfterFunc); give it a moment before the signal that must
	// now end the program.
	time.Sleep(200 * time.Millisecond)
	send(t, cmd, syscall.SIGHUP)
	killedBy(t, cmd, syscall.SIGHUP, stderr)
}

// A receiver waits in Channel.select beside a timer.
func TestSignals_ASelectBetweenASignalAndATimer(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::channels.{Arm, Channel, Receiver}
    std::duration.Duration
    std::io
    std::signals.{self, Signal}
end

fn wait(hups: Receiver<Signal>, timeout: Duration) -> String
    Channel.select([
        Arm.receive(hups, show),
        Arm.after(timeout, fn -> "tick"),
    ])
end

fn main() -> Result<Unit, signals.Error>
    hups = signals.receiver([Signal.Hangup])?
    io.print(wait(hups, Duration.milliseconds(10)))
    io.print("ready")
    io.print(wait(hups, Duration.seconds(60)))
    Ok(Unit)
end
`+signalsShow)
	next("tick")
	next("ready")
	send(t, cmd, syscall.SIGHUP)
	next("SIGHUP")
	exited(t, cmd, 0, stderr)
}

// SIGTERM is shutdown's: a supervised task blocked on a signal receiver
// ends when the drain closes it, boot's cleanup runs, and the program
// exits with 128 plus SIGTERM's number, as it does without a receiver.
func TestSignals_SIGTERMDrainsATaskBlockedOnAReceiver(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::channels.Receiver
    std::duration.Duration
    std::io
    std::signals.{self, Signal}
    std::supervisors.Supervisor
    std::timer
end

struct App
    work: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    defer io.print("cleanup")
    Ok(App{work: Supervisor.new(max_running: 1)})
end

fn wait() -> Unit
    hups = signals.receiver([Signal.Hangup])!
    io.print("ready")
    io.print("task ${show(Receiver.receive(hups))}")
end

fn main()
    _ = Supervisor.spawn(App.work, wait)
    timer.sleep(Duration.seconds(60))
    io.print("slept")
end
`+signalsShow)
	next("ready")
	send(t, cmd, syscall.SIGTERM)
	next("task closed")
	next("cleanup")
	exited(t, cmd, 128+int(syscall.SIGTERM), stderr)
	if stderr.Len() != 0 {
		t.Errorf("the drain wrote to stderr:\n%s", stderr.String())
	}
}

// A SIGHUP that arrives while SIGTERM's drain runs is dropped, in a program
// that receives Hangup: the drain closed its receivers and keeps the signal
// caught, so the program still exits with SIGTERM's status.
func TestSignals_ASIGHUPDuringTheDrainIsDropped(t *testing.T) {
	cmd, next, stderr := signalled(t, `import
    std::duration.Duration
    std::io
    std::signals.{self, Signal}
    std::supervisors.Supervisor
    std::timer
end

struct App
    work: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{work: Supervisor.new(max_running: 1, shutdown_timeout: Duration.seconds(2))})
end

fn main()
    _hups = signals.receiver([Signal.Hangup])!
    _ = Supervisor.spawn(App.work, fn -> timer.sleep(Duration.seconds(60)))
    io.print("ready")
    timer.sleep(Duration.seconds(60))
end
`)
	next("ready")
	send(t, cmd, syscall.SIGTERM)
	// The sleeping task holds the drain for its two-second budget.
	time.Sleep(500 * time.Millisecond)
	send(t, cmd, syscall.SIGHUP)
	exited(t, cmd, 128+int(syscall.SIGTERM), stderr)
}
