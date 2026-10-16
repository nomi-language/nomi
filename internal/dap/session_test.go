package dap

import (
	"strings"
	"testing"
	"time"
)

// Breakpoints in main and in an Iter callback; step over, in and out; the
// logical stack across Iter.map; output events; a clean exit.
func TestSession_StepsThroughMainACallAndACallback(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("steps.nomi"))
	h.breakAt("steps.nomi", 9, 15, 99)
	h.configure()
	thread := h.stopped()
	h.request("threads", nil)
	h.stack(thread)
	h.do("next", thread)
	h.stopped()
	h.do("stepIn", thread)
	h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.locals(frames[1])
	h.do("stepOut", thread)
	h.stopped()
	h.stack(thread)
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.request("scopes", map[string]any{"frameId": frames[1]})
	h.do("continue", thread)
	h.stopped()
	h.breakAt("steps.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("steps")
}

// A 1000-element list paged, a struct, an enum, and a shadowed name.
func TestSession_ShowsValuesAsATree(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("values.nomi"))
	h.breakAt("values.nomi", 21, 25)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	refs := h.locals(frames[0])
	h.variables(map[string]any{"variablesReference": refs["big"], "filter": "indexed", "start": 500, "count": 3})
	h.variables(map[string]any{"variablesReference": refs["big"], "filter": "named"})
	h.variables(map[string]any{"variablesReference": refs["p"]})
	h.variables(map[string]any{"variablesReference": refs["s"]})
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("values")
}

// A breakpoint in a tail-called function; then a trap, with its exception
// info, and the program's failure on the error stream.
func TestSession_ATailCallAndATrap(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("failures.nomi"))
	h.breakAt("failures.nomi", 9)
	h.configure()
	thread := h.stopped()
	h.stack(thread)
	h.do("continue", thread)
	h.stopped()
	h.stack(thread)
	h.breakAt("failures.nomi")
	h.do("continue", thread)
	thread = h.stopped()
	h.request("exceptionInfo", map[string]any{"threadId": thread})
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("failures")
}

// A trap inside the standard library stops where it arose, in std's frame
// (deemphasized, so a client focuses the program's call below it), and its
// exception text names the program's line, as the program's failure on the
// error stream does.
func TestSession_ATrapInsideTheStdlib(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("stdtrap.nomi"))
	h.configure()
	thread := h.stopped()
	h.request("exceptionInfo", map[string]any{"threadId": thread})
	h.stack(thread)
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("stdtrap")
}

func TestSession_AnErrFromMain(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("mainerr.nomi"))
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("mainerr")
}

// One test of a file, selected by line, stopping at its failed assertion;
// the run's report is the program's output.
func TestSession_AFailedAssertionInATest(t *testing.T) {
	h := newHarness(t)
	h.start(false, map[string]any{"mode": "test", "program": testdata("half_test.nomi"), "line": 9})
	h.configure()
	thread := h.stopped()
	h.request("exceptionInfo", map[string]any{"threadId": thread})
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("test_assertion")
}

// A breakpoint in a `subtests` body stops once per row, each row its own
// frame under the test's, with the row's pattern names as its locals; the
// failed row stops at its assertion and the run carries on.
func TestSession_SubtestsRowsStopInTurn(t *testing.T) {
	h := newHarness(t)
	h.start(false, map[string]any{"mode": "test", "program": testdata("subtests_test.nomi")})
	h.breakAt("subtests_test.nomi", 3)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("next", thread)
	h.stopped()
	h.do("next", thread)
	h.stopped()
	h.request("exceptionInfo", map[string]any{"threadId": thread})
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("subtests")
}

// format "json" reports a test run as `nomi test --format json` does, which
// is what neotest's dap strategy reads from the session's output.
func TestSession_ATestRunReportsJSONRecords(t *testing.T) {
	h := newHarness(t)
	h.start(false, map[string]any{"mode": "test", "program": testdata("half_test.nomi"), "line": 5, "format": "json"})
	h.configure()
	h.wait("terminated")
	h.disconnect()
	h.check("test_json")
}

func TestSession_ALineWithNoTestFailsTheLaunch(t *testing.T) {
	h := newHarness(t)
	h.request("initialize", map[string]any{"adapterID": "nomi"})
	h.request("launch", map[string]any{"mode": "test", "program": testdata("half_test.nomi"), "line": 3})
	h.request("launch", map[string]any{"mode": "run", "program": testdata("half_test.nomi")})
	h.request("launch", map[string]any{"mode": "run", "program": testdata("missing.nomi")})
	h.disconnect()
	h.check("launch_errors")
}

// A breakpoint in a task: the task is its own thread, and the main thread,
// blocked awaiting it, shows where.
func TestSession_ATaskIsAThread(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("task.nomi"))
	h.breakAt("task.nomi", 7)
	h.configure()
	thread := h.stopped()
	h.request("threads", nil)
	frames := h.stack(thread)
	h.locals(frames[0])
	h.stack(1)
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("task")
}

// An interactive program reading lines, through a terminal the client opens
// with runInTerminal.
func TestSession_AnInteractiveProgramReadsFromTheTerminal(t *testing.T) {
	h := newHarness(t)
	h.start(true, map[string]any{"mode": "run", "program": testdata("interactive.nomi")})
	h.breakAt("interactive.nomi", 9)
	h.configure()
	if h.termIn == nil {
		t.Fatalf("no terminal was opened; transcript:\n%s", h.log.String())
	}
	h.termIn.Write([]byte("alpha\n"))
	thread := h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.termIn.Write([]byte("beta\n"))
	h.stopped()
	h.breakAt("interactive.nomi")
	h.do("continue", thread)
	h.termIn.Close()
	h.wait("terminated")
	select {
	case <-h.termDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal stub did not end with the program")
	}
	h.note("terminal: %q", h.termOut.String())
	h.disconnect()
	h.check("interactive")
}

// Without a terminal the program reads end of input, and the debug console
// says why.
func TestSession_TheDebugConsoleHasNoInput(t *testing.T) {
	h := newHarness(t)
	h.start(false, map[string]any{"mode": "run", "program": testdata("interactive.nomi")})
	h.configure()
	h.wait("terminated")
	h.disconnect()
	h.check("console_input")
}

// Step in passes through the standard library to the user's lambda, unless
// justMyCode is false.
func TestSession_JustMyCode(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("stdstep.nomi"))
	h.breakAt("stdstep.nomi", 6)
	h.configure()
	thread := h.stopped()
	h.do("stepIn", thread)
	h.stopped()
	h.stack(thread)
	h.do("next", thread)
	h.stopped()
	h.do("next", thread)
	h.stopped()
	h.stack(thread)
	h.disconnect()

	h2 := newHarness(t)
	config := runConfig("stdstep.nomi")
	config["justMyCode"] = false
	h2.start(false, config)
	h2.breakAt("stdstep.nomi", 6)
	h2.configure()
	thread = h2.stopped()
	h2.do("stepIn", thread)
	h2.stopped()
	h2.stack(thread)
	h2.disconnect()
	h.log.WriteString(strings.Repeat("-", 20) + " justMyCode: false\n" + h2.log.String())
	h.check("just_my_code")
}

// Stop on entry, pause a running loop, and disconnect while it runs.
func TestSession_PauseAndDisconnectMidRun(t *testing.T) {
	h := newHarness(t)
	config := runConfig("spin.nomi")
	config["stopOnEntry"] = true
	h.start(false, config)
	h.configure()
	thread := h.stopped()
	h.stack(thread)
	h.do("continue", thread)
	h.request("pause", map[string]any{"threadId": thread})
	h.stopped()
	h.do("continue", thread)
	h.disconnect()
	h.check("pause_disconnect")
}
