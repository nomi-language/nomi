package dap

import (
	"testing"
	"time"
)

// Time travel: stepBack and reverseContinue, which replay the program from
// its start (internal/vm's debugtravel.go).

// Step back over a statement and over a call, back to the first statement
// and once more (refused), then forward past the present to the end.
func TestTravel_StepBackOverAStatementAndACall(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("steps.nomi"))
	h.breakAt("steps.nomi", 20)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	for range 3 {
		h.do("stepBack", thread)
		h.stopped()
		frames = h.stack(thread)
		h.locals(frames[0])
	}
	h.do("stepBack", thread)
	h.do("next", thread)
	h.stopped()
	h.do("stepIn", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("stepBack", thread)
	h.stopped()
	h.stack(thread)
	h.breakAt("steps.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_steps")
}

// Reverse continue to the latest earlier pass of a conditional breakpoint,
// twice, then to the start (no breakpoint before it), then forward again
// through the same stops to the end; the output is printed once.
func TestTravel_ReverseContinueToAConditionalBreakpoint(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("travel.nomi"))
	h.breakWith("travel.nomi",
		map[string]any{"line": 4, "condition": "n % 2 == 0"},
		map[string]any{"line": 23},
	)
	h.configure()
	thread := h.stopped()
	h.do("continue", thread)
	h.stopped()
	h.do("continue", thread)
	h.stopped()
	for range 2 {
		h.do("reverseContinue", thread)
		h.stopped()
		frames := h.stack(thread)
		h.locals(frames[0])
	}
	h.do("reverseContinue", thread)
	h.stopped()
	h.stack(thread)
	h.do("reverseContinue", thread)
	for range 3 {
		h.do("continue", thread)
		h.stopped()
		frames := h.stack(thread)
		h.locals(frames[0])
	}
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_reverse_continue")
}

// Going back across io.read_line answers it from the recording: the
// terminal is not read again, and the value is the one typed. Forward past
// the present, the program reads the terminal again.
func TestTravel_StepBackAcrossReadLine(t *testing.T) {
	h := newHarness(t)
	h.start(true, map[string]any{"mode": "run", "program": testdata("interactive.nomi")})
	h.breakAt("interactive.nomi", 9)
	h.configure()
	h.termIn.Write([]byte("alpha\n"))
	thread := h.stopped()
	h.do("continue", thread)
	h.termIn.Write([]byte("beta\n"))
	h.stopped()
	h.do("reverseContinue", thread)
	h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("stepBack", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	// The present: the next read is live.
	h.do("continue", thread)
	h.termIn.Write([]byte("gamma\n"))
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
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
	h.check("travel_read_line")
}

// A restart is part of the history: stepping back after one replays it.
func TestTravel_StepBackAfterARestartFrame(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("restart.nomi"))
	h.breakAt("restart.nomi", 6)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.request("restartFrame", map[string]any{"frameId": frames[0]})
	h.stopped()
	h.do("next", thread)
	h.stopped()
	h.do("next", thread)
	h.stopped()
	h.do("stepBack", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("stepBack", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.breakAt("restart.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_restart")
}

// A program that starts a task cannot step back, and says why.
func TestTravel_StepBackIsOffInATaskProgram(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("task.nomi"))
	h.breakAt("task.nomi", 7)
	h.configure()
	thread := h.stopped()
	h.do("stepBack", thread)
	h.do("reverseContinue", thread)
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_task")
}

// A replay whose crossing does not match the recording (simulated) stops
// with an internal error and goes on live.
func TestTravel_ARecordingMismatchIsAnInternalError(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("travel.nomi"))
	h.breakAt("travel.nomi", 23)
	h.configure()
	thread := h.stopped()
	h.srv.mu.Lock()
	sess := h.srv.sess
	h.srv.mu.Unlock()
	sess.d.RenameCrossingForTest(0, "io.write")
	h.do("stepBack", thread)
	thread = h.stopped()
	h.request("exceptionInfo", map[string]any{"threadId": thread})
	h.do("stepBack", thread)
	h.breakAt("travel.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_mismatch")
}

// The clock and randomness replay exactly: after going back and forward
// again, every local has the value it had.
func TestTravel_TheClockAndRandomnessReplayExactly(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("chance.nomi"))
	h.breakAt("chance.nomi", 13)
	h.configure()
	thread := h.stopped()
	before := h.localValues(h.stack(thread)[0])
	for range 4 {
		h.do("stepBack", thread)
		h.stopped()
	}
	for range 4 {
		h.do("next", thread)
		h.stopped()
	}
	after := h.localValues(h.stack(thread)[0])
	if len(before) != 4 || before["roll"] == "" || before["started"] == "" {
		t.Fatalf("locals at line 13: %v", before)
	}
	for name, v := range before {
		if after[name] != v {
			t.Fatalf("%s is %s after going back and forth, and was %s", name, after[name], v)
		}
	}
	h.breakAt("chance.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
}

// localValues are a frame's locals' values by name.
func (h *harness) localValues(frame int) map[string]string {
	h.t.Helper()
	body := h.request("scopes", map[string]any{"frameId": frame})
	ref := int(body["scopes"].([]any)[0].(map[string]any)["variablesReference"].(float64))
	vars := h.request("variables", map[string]any{"variablesReference": ref})
	out := map[string]string{}
	for _, v := range vars["variables"].([]any) {
		v := v.(map[string]any)
		out[v["name"].(string)] = v["value"].(string)
	}
	return out
}

// In a test run: back from a failed assertion to the start of its
// statement, then forward to the assertion again, which stops again.
func TestTravel_StepBackFromAFailedAssertionInATest(t *testing.T) {
	h := newHarness(t)
	h.start(false, map[string]any{"mode": "test", "program": testdata("half_test.nomi")})
	h.configure()
	thread := h.stopped()
	h.do("stepBack", thread)
	h.stopped()
	frames := h.stack(thread)
	h.locals(frames[0])
	h.do("stepBack", thread)
	h.stopped()
	h.stack(thread)
	h.do("continue", thread)
	h.stopped()
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("travel_test_assertion")
}
