package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

func TestParseTestArgs_Timeout(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
	}{
		{nil, vmhost.DefaultTestTimeout},
		{[]string{"--timeout", "30s"}, 30 * time.Second},
		{[]string{"tests", "--timeout=500ms"}, 500 * time.Millisecond},
		{[]string{"--timeout", "2m", "--format", "json"}, 2 * time.Minute},
		{[]string{"--timeout", "0"}, 0},
	} {
		parsed, err := parseTestArgs(c.args)
		if err != nil || parsed.timeout != c.want {
			t.Errorf("%v: timeout %s, err %v; want %s", c.args, parsed.timeout, err, c.want)
		}
	}
	for _, bad := range [][]string{{"--timeout", "ten"}, {"--timeout", "-1s"}, {"--timeout=5"}, {"--timeout"}} {
		if _, err := parseTestArgs(bad); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
}

const timeoutFixture = `fn spin(n: Int) -> Int
    spin(n + 1)
end

test "spins"
    assert spin(0) == 1
end

test "passes"
    assert 1 == 1
end
`

// A case that does not finish within --timeout fails at the line it was
// running, with the stack under its FAIL line, and the next case runs.
func TestTestCommand_TimeoutFailsTheCaseAtItsLine(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	mustWrite(t, filepath.Join(dir, "loop_test.nomi"), timeoutFixture)

	stdout, _, err := runNomiSplit(t, t.TempDir(), dir, "test", "loop_test.nomi", "--timeout", "200ms")
	if err == nil {
		t.Fatalf("a timed-out case passed:\n%s", stdout)
	}
	want := "FAIL loop_test.nomi :: spins\n" +
		"  line 2: did not finish within 200ms, so it was stopped\n" +
		"  it was running:\n" +
		"    loop_test.nomi:2 in spin\n" +
		"    loop_test.nomi:6 in spins\n" +
		"  help: " + vmhost.TestTimeoutHint + "\n" +
		"ok loop_test.nomi :: passes\n" +
		"test result: FAILED. 1 passed, 1 failed\n"
	if stdout != want {
		t.Errorf("text report:\n%s\nwant:\n%s", stdout, want)
	}

	stdout, _, _ = runNomiSplit(t, t.TempDir(), dir, "test", "loop_test.nomi", "--timeout", "200ms", "--format", "json")
	records := parseJSONRecords(t, stdout)
	if len(records) != 3 {
		t.Fatalf("want two tests and a summary:\n%s", stdout)
	}
	if r := records[0]; r.Status != "failed" || r.Line != 5 || r.ErrorLine != 2 ||
		!strings.HasPrefix(deref(r.Message), "  line 2: did not finish within 200ms, so it was stopped") {
		t.Errorf("timed-out record = %+v", r)
	}
	if r := records[1]; r.Status != "passed" {
		t.Errorf("the case after = %+v", r)
	}
}

const waitingOutsideFixture = `import std::net
import std::net.{Listener}
import std::testing.Clock

group "outside"
    clock Clock.Virtual

    test "waits for a connection"
        listener = net.listen("127.0.0.1:0")!
        assert Listener.accept(listener) |> Result.is_ok()
    end

    test "passes"
        assert 1 == 1
    end
end
`

// A case under a virtual clock that waits outside the program (a socket
// no one connects to) cannot be stopped: its limit cannot wake the wait
// from outside the bubble, so the bubble cannot end. The case fails at its
// limit, naming where it waits, no case after it runs, and the command
// ends instead of hanging. A test's own standard input is its capture's,
// which is empty, so a socket stands for the outside world here.
func TestTestCommand_ACaseWaitingOutsideUnderAVirtualClockEndsTheRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	mustWrite(t, filepath.Join(dir, "wait_test.nomi"), waitingOutsideFixture)
	cacheRoot := t.TempDir()
	// The first lowering of the stdlib is the process's, not the case's:
	// a check pays for it before the timed run.
	if _, stderr, err := runNomiSplit(t, cacheRoot, dir, "check", "wait_test.nomi"); err != nil {
		t.Fatalf("nomi check: %v\n%s", err, stderr)
	}

	const limit = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, nomiBin, "test", "wait_test.nomi", "--timeout", limit.String())
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+cacheRoot, "NO_COLOR=1", "NOMI_COLOR=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("nomi test did not end:\n%s%s", stdout.String(), stderr.String())
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("nomi test exited with %v, want status 1:\n%s%s", err, stdout.String(), stderr.String())
	}
	// The limit, the two seconds rt gives a stopped bubble to end, and
	// the command's own start and report.
	if bound := limit + 2*time.Second + 5*time.Second; took > bound {
		t.Errorf("nomi test took %s, over %s", took, bound)
	}
	out := stdout.String()
	for _, want := range []string{
		"FAIL wait_test.nomi :: outside / waits for a connection\n" +
			"  did not finish within 500ms and could not be stopped: it was waiting outside the program\n" +
			"  it was waiting at:\n    ",
		// The stdlib's path is where this nomi finds it.
		"std/net.nomi:",
		" in Listener.accept\n  help: " + vmhost.TestTimeoutHint + "\n" +
			"FAIL wait_test.nomi :: outside / passes\n" +
			"  did not run: a case before it was still waiting outside the program when its time ran out, and no other case can start beside it\n" +
			"test result: FAILED. 0 passed, 2 failed\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks\n%s\nreport:\n%s", want, out)
		}
	}
}
