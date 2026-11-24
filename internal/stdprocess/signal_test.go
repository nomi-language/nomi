package stdprocess_test

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalHelper is the helper program's commands for the tests here: `pid`
// prints its process id, `trap` prints `ready` and exits with 0 on a
// terminate signal, `ignore` prints `ready` and ignores one, and `group`
// starts a `sleep` helper, prints its process id and waits an hour.
func signalHelper(cmd string) int {
	switch cmd {
	case "pid":
		fmt.Println(os.Getpid())
	case "trap":
		terms := make(chan os.Signal, 1)
		signal.Notify(terms, syscall.SIGTERM)
		fmt.Println("ready")
		<-terms
	case "ignore":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "group":
		exe, err := os.Executable()
		if err != nil {
			return 2
		}
		sleeper := exec.Command(exe, helperArg, "sleep")
		if err := sleeper.Start(); err != nil {
			return 2
		}
		fmt.Println(sleeper.Process.Pid)
		time.Sleep(time.Hour)
	default:
		return 2
	}
	return 0
}

// imports is the import block of a program here, which names the std
// names it uses: an unused import is an error.
func imports(names ...string) string {
	return "import\n    " + strings.Join(names, "\n    ") + "\nend\n"
}

const (
	stopImports  = "std::duration.Duration"
	clockImports = "std::instant.Monotonic"
)

func skipWithoutTerminate(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows sends no terminate signal: Child.signal answers Unsupported for it, and Child.stop kills at once")
	}
}

// A program that ends on a terminate signal is stopped by it, well within
// the grace, and reports its own clean exit.
func TestStop_AProgramThatTrapsTerminateExitsCleanly(t *testing.T) {
	skipWithoutTerminate(t)
	src := imports(stopImports, clockImports, "std::io.{self, Error, Reader}", "std::process.{self, Child, Command}") + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "trap"]})?
    defer Child.kill(child)
    _ = Reader.read_line(Child.stdout(child))?

    started = Monotonic.now()
    status = Child.stop(child, Duration.seconds(30))?
    io.print("${status} ${Monotonic.elapsed(started) < Duration.seconds(30)}")
    Ok(Unit)
end
`, helperPath(t))
	if got := strings.TrimSpace(run(t, src)); got != "exited with code 0 True" {
		t.Fatalf("printed %q, want a clean exit within the grace", got)
	}
}

// A program that ignores a terminate signal is killed once the grace has
// passed, and not before.
func TestStop_AProgramThatIgnoresTerminateIsKilledAfterTheGrace(t *testing.T) {
	skipWithoutTerminate(t)
	src := imports(stopImports, clockImports, "std::io.{self, Error, Reader}", "std::process.{self, Child, Command}") + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "ignore"]})?
    defer Child.kill(child)
    _ = Reader.read_line(Child.stdout(child))?

    started = Monotonic.now()
    status = Child.stop(child, Duration.milliseconds(500))?
    io.print("${status} ${Monotonic.elapsed(started) >= Duration.milliseconds(500)}")
    Ok(Unit)
end
`, helperPath(t))
	if got := strings.TrimSpace(run(t, src)); got != "ended by signal 9 True" {
		t.Fatalf("printed %q, want a kill after the grace", got)
	}
}

// Child.id is the id the program has for itself, and a signal to a
// program that has ended is an error, not a crash.
func TestChildID_IsTheProgramsOwnAndASignalAfterItEndsIsAnError(t *testing.T) {
	src := imports(stopImports, "std::io.{self, Error, Reader}", "std::process.{self, Child, Command, Signal}") + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "pid"]})?
    defer Child.kill(child)
    printed = Reader.read_line(Child.stdout(child))?
    _ = Child.wait(child)?

    io.print("${printed == Some("${Child.id(child)}")}")
    io.print("${Child.signal(child, Signal.Kill) is Err(Error.Other{code: "ESRCH"})}")
    io.print("${Child.stop(child, Duration.seconds(5))}")
    io.print("${process.id() >= 1}")
    Ok(Unit)
end
`, helperPath(t))
	want := "True\nTrue\nOk(exited with code 0)\nTrue"
	if got := strings.TrimSpace(strings.ReplaceAll(run(t, src), "\r\n", "\n")); got != want {
		t.Fatalf("printed:\n%s\nwant:\n%s", got, want)
	}
}

// A program started with new_group is stopped with its group: the program
// it started ends too.
func TestStop_AGroupsGrandchildStopsWithIt(t *testing.T) {
	skipWithoutTerminate(t)
	src := imports(stopImports, "std::io.{self, Error, Reader}", "std::process.{self, Child, Command}") + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "group"], new_group: True})?
    defer Child.kill(child)
    grandchild = Reader.read_line(Child.stdout(child))?

    status = Child.stop(child, Duration.seconds(30))?
    io.print("${Option.with_default(grandchild, "")} ${status}")
    Ok(Unit)
end
`, helperPath(t))
	got := strings.Fields(run(t, src))
	if len(got) < 2 || strings.Join(got[1:], " ") != "ended by signal 15" {
		t.Fatalf("printed %q, want the grandchild's id and the leader ended by the terminate signal", got)
	}
	pid, err := strconv.Atoi(got[0])
	if err != nil {
		t.Fatalf("the grandchild's id %q is not a number", got[0])
	}
	sleeper, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	// The grandchild ended on the signal; its new parent reaps it soon
	// after, and until then it answers a signal 0 as a zombie.
	for deadline := time.Now().Add(10 * time.Second); sleeper.Signal(syscall.Signal(0)) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = sleeper.Kill()
			t.Fatalf("the grandchild %d still runs after its group was stopped", pid)
		}
	}
}

// A group signal to a program started without new_group is refused: its
// group is this program's own.
func TestSignalGroup_WithoutANewGroupIsUnsupported(t *testing.T) {
	src := imports("std::io.{self, Error}", "std::process.{self, Child, Command, Signal}") + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "sleep"]})?
    defer Child.kill(child)

    case Child.signal_group(child, Signal.Terminate)
        Ok(_)  -> io.print("sent")
        Err(e) -> io.print("${e}")
    end

    Ok(Unit)
end
`, helperPath(t))
	want := "process.signal_group is not supported on a child not started with new_group"
	if got := strings.TrimSpace(run(t, src)); got != want {
		t.Fatalf("printed %q, want %q", got, want)
	}
}
