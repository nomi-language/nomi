package main

import (
	"path/filepath"
	"testing"
	"time"
)

const deadlockProgram = `import
  std::channels.{Channel, Receiver}
  std::io
  std::tasks.Task
end

fn main()
  ch = Channel.unbuffered<Int>()
  got =
    concurrent
      t = Task.spawn(fn -> Receiver.receive(ch.receiver))
      Task.await(t)
    end
  io.print("${got}")
end
`

// A built program whose every task waits on another stops with exit
// status 1 and the report on stderr, as `nomi run` does, instead of
// hanging. A run that hung would be killed at the limit and fail.
func TestBuildCommand_ADeadlockedProgramReportsAndExitsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	env := buildEnv(t.TempDir())
	entry := filepath.Join(dir, "deadlock.nomi")
	mustWrite(t, entry, deadlockProgram)
	bin := exe(filepath.Join(t.TempDir(), "app"))
	if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}
	got := runFor(t, 2*time.Minute, env, dir, "", bin)
	want := "line 12: every task is waiting, so none can run again:\n" +
		"  main waits for a task to end at deadlock.nomi:12\n" +
		"  the task spawned at deadlock.nomi:11 waits to receive on a channel at deadlock.nomi:11\n"
	if got.exit != 1 || got.stdout != "" || got.stderr != want {
		t.Errorf("the built program exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s\nwant exit 1 and stderr\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

const deadlockFixture = `import
  std::channels.{Channel, Receiver}
end

test "waits on itself"
  ch = Channel.unbuffered<Int>()
  assert Receiver.receive(ch.receiver) == None
end

test "passes"
  assert 1 == 1
end
`

// A deadlocked case fails at once with the report, not at its time limit,
// and the next case runs.
func TestTestCommand_ADeadlockedCaseFailsAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	mustWrite(t, filepath.Join(dir, "deadlock_test.nomi"), deadlockFixture)
	got := runFor(t, 2*time.Minute, buildEnv(t.TempDir()), dir, "", nomiBin, "test", "deadlock_test.nomi", "--timeout", "1h")
	want := "FAIL deadlock_test.nomi :: waits on itself\n" +
		"  line 7: every task is waiting, so none can run again:\n" +
		"    the test waits to receive on a channel at deadlock_test.nomi:7\n" +
		"ok deadlock_test.nomi :: passes\n" +
		"test result: FAILED. 1 passed, 1 failed\n"
	if got.exit != 1 || got.stdout != want {
		t.Errorf("nomi test exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s\nwant exit 1 and\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
