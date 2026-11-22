package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/std"
)

// TestMain pins what every comparison in this package reads from the process
// environment, and reads the checks that are only final after m.Run().
//
// Test reports are colourized for a terminal, and the two sides of a
// comparison decide that independently, so NOMI_COLOR is pinned to never.
//
// It also reads the corpus-sharing counter, because after m.Run() is the
// only place it is final. See corpusSharingViolation.
func TestMain(m *testing.M) {
	if err := os.Setenv("NOMI_COLOR", "never"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	irDeclineUnnamed = recordUnnamedDecline
	// This package's tests ask about the whole stdlib (its spec tables,
	// its lowering's index, every module's declarations), so they run on
	// every module built. internal/expectation runs the corpus and the
	// stdlib's cases on a stdlib built a module at a time, and so does the
	// child measuring the stdlib's builds (TestStdLoadCount_Child).
	if os.Getenv(stdLoadChildEnv) != "1" {
		std.All()
	}
	code := m.Run()
	if msg := unnamedDeclineViolation(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	if msg := flushIRBuildGolden(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	if msg := corpusSharingViolation(); msg != "" {
		fmt.Fprintln(os.Stderr, "irbuild: "+msg)
		code = 1
	}
	os.Exit(code)
}

// observation is everything a run is checked on: what the program wrote,
// where it wrote it, and how it exited. Nothing else about a Nomi program is
// observable from outside it, so matching the golden record on these three is
// the definition of the run being right.
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
// `nomi test` when it declares tests, `nomi run` otherwise — and answers what it printed and its exit
// status. It is internal/vmcmd, installed by vmcommand_ext_test.go: vmcmd
// imports vmhost, which imports this package, so only the external test
// package can reach it.
var VMCommand func(path string) (stdout, stderr string, exit int)

// vmReference is the irbuild population's writer: path run as its command runs
// it on the VM.
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
// by test name, with the text of the line that stops it. The literal
// stays pinned and is checked on the VM once the VM runs the program.
var vmKnownBlockers = map[string]string{}

// vmSkipIfKnownBlocked skips t when obs is the VM command refusing the
// program with the line vmKnownBlockers lists for t, and fails t when
// the VM is blocked by anything else. A listed test the VM now runs fails too,
// so the entry is removed and the literal is checked.
func vmSkipIfKnownBlocked(t *testing.T, obs observation) {
	t.Helper()
	blocker, listed := vmKnownBlockers[t.Name()]
	_, blocked := expectation.VMGap(obs.stderr)
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
