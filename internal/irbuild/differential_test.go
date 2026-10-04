package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"

	"github.com/nomi-language/nomi/rt"
)

// TestMain pins what every comparison in this package reads from the process
// environment, and reads the checks that are only final after m.Run().
//
// Test reports are colourized for a terminal, and the two sides of a
// comparison decide that independently, so NOMI_COLOR is pinned to never.
//
// The FFI wrapper cache is pointed at a temp root through
// NOMI_FFIRUN_CACHE_ROOT: a test run must not write into the user's
// ~/Library/Caches/nomi/builds, nor read a wrapper some other worktree's
// `nomi run` left there.
//
// It also reads the corpus-sharing counter and the FFI-preparation record,
// because after m.Run() is the only place either is final. See
// corpusSharingViolation and ffiPrepareViolation.
func TestMain(m *testing.M) {
	if err := os.Setenv("NOMI_COLOR", "never"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ffiDir, err := os.MkdirTemp("", "nomi-ffirun-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("NOMI_FFIRUN_CACHE_ROOT", ffiDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if msg := flushIRBuildGolden(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	os.RemoveAll(ffiDir)
	if msg := corpusSharingViolation(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	if msg := ffiPrepareViolation(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	os.Exit(code)
}

// observation is everything a differential run compares: what the program
// wrote, where it wrote it, and how it exited. Nothing else about a Nomi
// program is observable from outside it, so agreement on these three is the
// definition of the backend being right.
type observation struct {
	stdout string
	stderr string
	exit   int
}

func (o observation) String() string {
	return fmt.Sprintf("exit=%d\nstdout=%q\nstderr=%q", o.exit, o.stdout, o.stderr)
}

// VM command runs share process state: `vmhost.DefaultTestEnv` sets
// NOMI_ENV=test for the length of a VM test run and unsets it after, so
// vmReference serializes runs; see envMu.

// VMCommand runs path on the VM the way the command that owns it would —
// `nomi test` when it declares tests, `nomi run` otherwise, an FFI project
// through its generated wrapper — and answers what it printed and its exit
// status. It is internal/vmcmd, installed by vmcommand_ext_test.go: vmcmd
// imports vmhost, which imports this package, so only the external test
// package can reach it.
var VMCommand func(path string) (stdout, stderr string, exit int)

// vmReference is the irbuild population's writer: path run as its command runs
// it on the VM. A file whose FFI wrapper cannot be prepared answers the
// preparation failure, recorded for TestMain, rather than a run.
func vmReference(path string) (obs observation) {
	// A producer panic is a bug to report against this program, not a reason
	// to end every other comparison in the run.
	defer func() {
		if r := recover(); r != nil {
			// One line, so expectation.VMGap reads the whole message as the
			// gap's reason.
			msg := strings.Join(strings.Fields(fmt.Sprint(r)), " ")
			obs = observation{stderr: fmt.Sprintf("irbuild test harness: panic running %s: %s\n", path, msg), exit: 1}
		}
	}()
	if _, err := ffiPrepare(path); err != nil {
		return recordFFIPrepareFailure(path, err)
	}
	if VMCommand == nil {
		panic("irbuild tests: VMCommand is not installed; vmcommand_ext_test.go sets it")
	}
	// EXCLUSIVE: a test run sets NOMI_ENV for its length. See envMu.
	envMu.Lock()
	defer envMu.Unlock()
	stdout, stderr, exit := VMCommand(path)
	return observation{stdout: stdout, stderr: stderr, exit: exit}
}

// vmKnownBlockers are the tests whose pinned literal the VM cannot run yet,
// by test name, with the text of the BLOCKED line that stops it. The literal
// stays pinned and is checked on the VM once the VM runs the program.
var vmKnownBlockers = map[string]string{}

// vmSkipIfKnownBlocked skips t when obs is the VM command refusing the
// program with the BLOCKED line vmKnownBlockers lists for t, and fails t when
// the VM is blocked by anything else. A listed test the VM now runs fails too,
// so the entry is removed and the literal is checked.
func vmSkipIfKnownBlocked(t *testing.T, obs observation) {
	t.Helper()
	blocker, listed := vmKnownBlockers[t.Name()]
	blocked := strings.HasPrefix(obs.stderr, "BLOCKED ")
	switch {
	case blocked && listed && strings.Contains(obs.stderr, blocker):
		t.Skipf("the VM cannot run this program yet (%s); the pinned literal is checked "+
			"once it can", blocker)
	case blocked:
		t.Fatalf("the VM cannot run this program:\n%s", obs.stderr)
	case listed:
		t.Errorf("vmKnownBlockers lists %s as blocked by %q, and the VM now runs it; take it "+
			"off the list", t.Name(), blocker)
	}
}

// The FFI preparation. Some corpus files are `gopkg` projects, and vmReference
// runs a file the way the command that owns it would: for an FFI project that
// command runs a generated wrapper, and the wrapper is where the externs are
// registered. Without it, a run reports "N externs declared but not
// registered" and the file reads as wrong.

// ffiPrepare is the wrapper for path's project, or nil when the project needs
// none, which is nearly every corpus file and is the FastPath `nomi run`
// takes in-process. A preparation failure is the error return and nothing
// else.
//
// A failure is not folded into the nil. `internal/ffirun`'s cache refuses the
// default `~/.cache/nomi/builds` whenever `testing.Testing()` is true and
// NOMI_FFIRUN_CACHE_ROOT is unset, so a test can never read or evict a
// developer's real build cache. Without that variable every Prepare fails, and
// a fallback would run every FFI project with its externs unregistered: a
// well-formed `nomi test` report, exit 1, with nothing in it naming the cause.
// No corpus file's preparation legitimately fails (ffiprepare_test.go checks
// this over the whole corpus), so a fallback has no case to serve.
func ffiPrepare(path string) (*ffirun.Result, error) {
	res, err := ffirun.Prepare(path)
	if err != nil {
		return nil, err
	}
	if res == nil || res.FastPath {
		return nil, nil
	}
	return res, nil
}

// ffiPrepareFailures is every preparation failure the run has seen.
//
// A `*testing.T` cannot be threaded here: vmReference is called from many
// test files by path alone, and a preparation failure is usually a property
// of the run rather than of one comparison: an unusable cache root fails every
// file at once. So it is recorded and read from TestMain, as other violations
// no test is in scope for are (corpusSharingViolation,
// stdlibTestSharingViolation). Callers also return an
// observation naming the failure, so the comparison that asked for it fails on
// its own terms instead of waiting for the end of the run.
var (
	ffiPrepareFailuresMu sync.Mutex
	ffiPrepareFailures   []string
)

// recordFFIPrepareFailure notes a failure and renders it as the observation the
// caller returns. The text is deliberately not `nomi run`'s shape: a harness
// that could not prepare a wrapper has no program output, and a harness
// failure must not read like one.
func recordFFIPrepareFailure(path string, err error) observation {
	msg := fmt.Sprintf("irbuild test harness: FFI wrapper preparation failed for %s: %v",
		rt.DisplayPath(path), err)
	ffiPrepareFailuresMu.Lock()
	ffiPrepareFailures = append(ffiPrepareFailures, msg)
	ffiPrepareFailuresMu.Unlock()
	return observation{stderr: msg + "\n", exit: 1}
}

// takeFFIPrepareFailures drains the record. TestMain reads it to fail the run;
// the planted positive reads it to assert its own failure and to leave the
// record clean.
//
// Draining is what lets the planted positive exist, and it is the one way this
// guard could be silenced — a test that drained somebody else's failure and
// ignored it. The planted positive asserts the exact message it drains, so a
// foreign entry makes IT fail rather than disappear.
func takeFFIPrepareFailures() []string {
	ffiPrepareFailuresMu.Lock()
	defer ffiPrepareFailuresMu.Unlock()
	out := ffiPrepareFailures
	ffiPrepareFailures = nil
	return out
}

// ffiPrepareViolation reports the run having swallowed a preparation failure,
// as a message, or "" when it has not. Read from TestMain.
func ffiPrepareViolation() string {
	failures := takeFFIPrepareFailures()
	if len(failures) == 0 {
		return ""
	}
	return fmt.Sprintf("%d FFI wrapper preparation(s) failed, so that many files were "+
		"compared against a reference run that never happened:\n  %s\n"+
		"the usual cause is NOMI_FFIRUN_CACHE_ROOT being unset or unusable; "+
		"internal/ffirun refuses the default cache root under `go test` by design",
		len(failures), strings.Join(failures, "\n  "))
}

// envMu guards the one piece of PROCESS state a VM command run reads:
// NOMI_ENV.
//
// `vmhost.DefaultTestEnv` sets NOMI_ENV=test for the duration of a TEST run
// and unsets it after, so two test runs at once means one run's restore lands
// inside the other's body. Exactly one corpus program's answer depends on the
// inherited value: `tests/15-app-and-defer/effects/main.nomi`, a plain
// `fn main` whose `os.get("NOMI_ENV")` falls through to `"dev"`. (The other
// reader, `os_env_boot_only_test.nomi`, is a test-declaring file that defaults
// the variable itself, so no scheduling can move it.) vmReference holds it for
// every run, test or not.
//
// TestMain does not set NOMI_ENV=test, which would remove the window because
// DefaultTestEnv no-ops when the variable is set: the golden record for that
// program holds `deployment=dev`, the positive half of the program-boot
// staging check.
var envMu sync.Mutex

// fixture resolves a testdata program.
//
// Several fixtures fail an assertion on purpose and then assert about the
// report, which embeds the failing line (`observed.line == 27` in
// testing_check.nomi is about testing_check.nomi). So a fixture's length above
// such an assertion is load-bearing. An edit to an existing fixture, including
// a comment edit or a `nomi fmt -w` run (these fixtures are not fmt-clean),
// must be line-count-neutral above every pinned assertion or move the pin in
// the same change. Adding lines below the last such assertion, or to a
// fixture that pins no line, is free.
func fixture(name string) string {
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return abs
}
