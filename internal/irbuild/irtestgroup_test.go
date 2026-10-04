package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// Grouped test cases — a `tests` group's `boot` line, setup, a case's pattern
// and `clock` — retained and run by the VM's runner, compared against the
// golden record of `nomi test`. Each fixture is one file that is its own entry:
// it declares `fn main` and the `pub fn boot` its groups' `boot` lines call.

// irTestGroupRun is one grouped file run on the VM beside its golden record.
type irTestGroupRun struct {
	vm, interp observation
	cases      int
	module     *ir.Module
	// all is every module the program retained: the test file's and its
	// siblings'.
	all    []*ir.Module
	vmTime time.Duration
}

// irTestGroupVM lowers src as `<name>_test.nomi`, requires wantCases
// retained cases in its module, and runs it in the VM; the output must equal
// the golden output in irbuild.expect (goldenReference), which `interp` holds.
func irTestGroupVM(t *testing.T, src string, wantCases int) irTestGroupRun {
	t.Helper()
	return irTestGroupVMFiles(t, src, nil, wantCases)
}

// irTestGroupVMFiles is irTestGroupVM with sibling files written beside the
// test file, by name: the entry file whose boot a group's `boot` line calls.
func irTestGroupVMFiles(t *testing.T, src string, siblings map[string]string, wantCases int) irTestGroupRun {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "group_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	for name, body := range siblings {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var run irTestGroupRun
	run.all = res.IR
	for _, m := range res.IR {
		if m.Name() == path {
			run.module = m
		}
	}
	if run.module == nil {
		t.Fatal("the fixture retained nothing")
	}
	run.cases = len(run.module.Tests())
	if run.cases != wantCases {
		var names []string
		for _, c := range run.module.Tests() {
			names = append(names, c.Name())
		}
		t.Fatalf("retained %d case(s) %q; want %d", run.cases, names, wantCases)
	}
	var out bytes.Buffer
	m := vm.NewProgram(run.module, res.IRModules(), &out)
	start := time.Now()
	exit, reason := m.RunTests(path)
	run.vmTime = time.Since(start)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	run.vm = observation{stdout: out.String(), exit: exit}
	t.Logf("VM (exit %d, %v):\n%s", exit, run.vmTime, run.vm.stdout)
	run.interp = goldenReference(t, path)
	if run.vm.stdout != run.interp.stdout || run.vm.exit != run.interp.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			run.vm.exit, run.vm.stdout, run.interp.exit, run.interp.stdout)
	}
	return run
}

const irTestGroupBootSetupSource = `import std/io

pub struct App {
  context: Context
  label: String
}

pub fn boot(startup: Startup): App {
  io.print("boot ${startup.args}")
  App{context: Context.root(), label: "booted"}
}

fn main() {}

fn show(suffix: String): String {
  App.label + suffix
}

tests "group" {
  boot boot(Startup{args: ["group"]})
  setup {n: 1, word: "one"}

  test "reads the app and the setup value", {n, word} {
    io.print(show("!"))
    assert App.label == "booted"
    assert n == 1
    assert word == "one"
  }

  test "binds the value with a name", value {
    assert value.n == 1
  }

  test "binds the value with a nested pattern", {n: count, word} {
    assert count == 1
    assert word == "one"
  }
}
`

// Each case boots its own app (the boot prints once per case, above the whole
// report) with the Startup its group's `boot` line built, runs the setup and
// binds its value.
func TestIRTestGroup_BootSetupAndContextAgree(t *testing.T) {
	run := irTestGroupVM(t, irTestGroupBootSetupSource, 3)
	if got := strings.Count(run.vm.stdout, "boot [group]\n"); got != 3 {
		t.Errorf("the VM ran the group boot %d time(s); want once per case (3):\n%s", got, run.vm.stdout)
	}
	if run.vm.exit != 0 || !strings.Contains(run.vm.stdout, "3 passed, 0 failed") {
		t.Errorf("want all three cases passing:\n%s", run.vm.stdout)
	}
	for _, c := range run.module.Tests() {
		if c.Group().Boot == nil {
			t.Errorf("%s records no group boot", c.Name())
		}
		if c.Group().VirtualClock {
			t.Errorf("%s records a virtual clock it does not declare", c.Name())
		}
	}
}

const irTestGroupFailingSource = `pub struct App {
  context: Context
  label: String
}

pub fn boot(): App {
  App{context: Context.root(), label: "booted"}
}

fn main() {}

tests "group" {
  boot boot()
  setup {word: "one"}

  test "a failing qualified call shows its rows", {word} {
    assert String.length(word) == 5
  }

  test "a failing app read", {word} {
    assert App.label == word
  }
}
`

// A failing assertion's report, including the rows a qualified call inside the
// subject contributes, is the expected text.
func TestIRTestGroup_FailureReportsAgree(t *testing.T) {
	run := irTestGroupVM(t, irTestGroupFailingSource, 2)
	if run.vm.exit == 0 || !strings.Contains(run.vm.stdout, "word") {
		t.Errorf("want two failing cases whose report names the context binding:\n%s", run.vm.stdout)
	}
}

const irTestGroupVirtualSource = `import {
  std/duration.Duration
  std/io
  std/supervisors.Supervisor
  std/testing.Clock
  std/timer
}

pub struct App {
  context: Context
  work: Supervisor
}

pub fn boot(): App {
  App{context: Context.root(), work: Supervisor.new(max_running: 2)}
}

fn main() {}

fn slow(): Unit {
  timer.sleep(Duration.minutes(10))
  io.print("slow work done")
}

tests "virtual" {
  clock Clock.Virtual

  boot boot()

  test "an hour passes at once" {
    timer.sleep(Duration.minutes(60))
    assert 1 == 1
  }

  test "a group's work is flushed in virtual time" {
    _ = Supervisor.spawn(App.work, || slow())
    _ = Supervisor.flush(App.work)
    assert 2 == 2
  }
}
`

// `clock Clock.Virtual` runs each case, its boot and its drain in the bubble a
// compiled table names: an hour of sleeping and ten minutes of supervised work
// finish at once.
func TestIRTestGroup_VirtualClockRunsInTheBubble(t *testing.T) {
	run := irTestGroupVM(t, irTestGroupVirtualSource, 2)
	for _, c := range run.module.Tests() {
		if !c.Group().VirtualClock {
			t.Errorf("%s does not record its group's virtual clock", c.Name())
		}
	}
	if !strings.Contains(run.vm.stdout, "slow work done") || run.vm.exit != 0 {
		t.Errorf("want the supervised work to finish and both cases to pass:\n%s", run.vm.stdout)
	}
	// Outside a bubble the first case alone sleeps an hour; vclock's watchdog
	// would end it at 30 seconds with a failure instead.
	if run.vmTime > 20*time.Second {
		t.Errorf("the VM took %v, so it did not run the cases in virtual time", run.vmTime)
	}
}

const irTestGroupBootFaultSource = `pub struct App {
  context: Context
  n: Int
}

fn zero(): Int {
  0
}

pub fn boot(): App {
  App{context: Context.root(), n: 10 / zero()}
}

fn main() {}

tests "group" {
  boot boot()

  test "never reaches its body" {
    assert App.n == 10
  }
}
`

// A fault in a group boot fails the case with the fault's text.
func TestIRTestGroup_ABootFaultFailsTheCase(t *testing.T) {
	run := irTestGroupVM(t, irTestGroupBootFaultSource, 1)
	if run.vm.exit == 0 || !strings.Contains(run.vm.stdout, "FAIL") {
		t.Errorf("want the case to fail on the boot's fault:\n%s", run.vm.stdout)
	}
}

// A `with` in a setup holds through each case's body: the setup runs in the
// case's activation and its block extends through the test. A case's own
// `with` holds to the end of its body.
func TestIRTestGroup_ASetupWithReachesTheBody(t *testing.T) {
	const src = `pub struct App {
  context: Context
  label: String
}

pub fn boot(): App {
  App{context: Context.root(), label: "booted"}
}

fn main() {}

fn label(): String {
  App.label
}

tests "group" {
  boot boot()

  test "plain" {
    assert App.label == "booted"
  }
}

tests "overridden" {
  boot boot()
  setup {
    with App.label = "set up"
  }

  test "reads the setup's override" {
    assert App.label == "set up"
    assert label() == "set up"
  }

  test "reads its own override" {
    with App.label = "own"
    assert label() == "own"
  }
}
`
	run := irTestGroupVM(t, src, 3)
	if run.vm.exit != 0 {
		t.Errorf("want every case to pass:\n%s", run.vm.stdout)
	}
}

// The runner refuses a case whose group boot no linked module records, rather
// than running its body with no app.
func TestIRTestGroup_TheRunnerRefusesAnUnrecordedBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "group_test.nomi")
	if err := os.WriteFile(path, []byte(irTestGroupBootSetupSource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			entry = m
		}
	}
	if entry == nil || len(entry.Tests()) == 0 {
		t.Fatal("no case retained, so this checks nothing")
	}
	// A module carrying the cases but not the boot record.
	bare := ir.NewModule(path)
	for _, c := range entry.Tests() {
		bare.DeclareTestIn(c.Name(), c.Fn(), c.Group())
	}
	for _, f := range entry.Funcs() {
		bare.AddFunc(f)
	}
	var out bytes.Buffer
	_, reason := vm.NewProgram(bare, nil, &out).RunTests(path)
	if !strings.Contains(reason, "is not one this program records") {
		t.Fatalf("the runner answered reason %q with output:\n%s", reason, out.String())
	}
}
