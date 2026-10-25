package main

import (
	"os"
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

const timeoutFixture = `fn spin(n: Int) -> Int {
    spin(n + 1)
}

test "spins" {
    assert spin(0) == 1
}

test "passes" {
    assert 1 == 1
}
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

// An FFI project's cases run in its wrapper process, which is handed the
// limit.
func TestTestCommand_TimeoutReachesTheFFIWrapper(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, _ := stageEchoFixture(t)
	testPath := filepath.Join(projectRoot, "main_test.nomi")
	mustWrite(t, testPath, `gopkg "echobinding" as ffi

fn echo_upper(s: String) -> String go ffi.EchoUpper

fn spin(s: String) -> String {
  spin(echo_upper(s))
}

test "ffi spins" {
  assert spin("a") == "b"
}
`)
	stdout, _, err := runNomiSplit(t, cacheRoot, projectRoot, "test", testPath, "--timeout", "300ms", "--format", "json")
	if err == nil {
		t.Fatalf("expected a failing exit status")
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 2 {
		t.Fatalf("want a test and a summary:\n%s", stdout)
	}
	if r := records[0]; r.Status != "failed" || r.ErrorLine != 6 ||
		!strings.HasPrefix(deref(r.Message), "  line 6: did not finish within 300ms, so it was stopped") {
		t.Errorf("record = %+v", r)
	}
	if entries, _ := os.ReadDir(cacheRoot); len(entries) == 0 {
		t.Errorf("no wrapper cache entry, so the file never took the FFI path this test is about")
	}
}
