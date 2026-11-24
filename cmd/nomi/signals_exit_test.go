package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// A built program whose main returns while SIGTERM's drain runs (its main
// was blocked on a signal receiver the drain closed) still spends its
// supervisor's shutdown budget, runs boot's deferred calls and exits with
// SIGTERM's status: main's own return waits for the drain, and the run
// answers the signal's status rather than main's success.
func TestSignals_ABuiltProgramWhoseMainReturnsDuringTheDrainExitsWithTheSignalsStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sends no SIGTERM a program can catch, and std::signals answers Unsupported there")
	}
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import
    std::channels.{Channel, Receiver, Sender}
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
    Ok(App{work: Supervisor.new(max_running: 1, shutdown_timeout: Duration.milliseconds(200))})
end

fn work(started: Sender<Unit>) -> Unit
    defer io.print("work unwound")
    _ = Sender.send(started, Unit)
    timer.sleep(Duration.seconds(60))
end

fn main()
    started = Channel.unbuffered<Unit>()
    _ = Supervisor.spawn(App.work, fn -> work(started.sender))
    _ = Receiver.receive(started.receiver)
    arrivals = signals.receiver([Signal.Hangup])!
    io.print("waiting")
    io.print("main got ${Receiver.receive(arrivals)}")
end
`)
	env := buildEnv(t.TempDir())
	bin := exe(filepath.Join(t.TempDir(), "app"))
	if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}
	for range 3 {
		cmd, next, stderr := watched(t, exec.Command(bin))
		next("waiting")
		send(t, cmd, syscall.SIGTERM)
		next("main got None")
		next("work unwound")
		next("cleanup")
		exited(t, cmd, 128+int(syscall.SIGTERM), stderr)
	}
}
