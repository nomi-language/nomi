package dap

import (
	"strings"
	"testing"
)

// The "Captured output" scopes (captured.go) and a test's own output, which
// reaches the console as the test writes it.

// captured opens each captured-output scope of a frame, innermost first,
// and every text and list in it, and answers how many scopes it had.
func (h *harness) captured(frame int) int {
	h.t.Helper()
	body := h.request("scopes", map[string]any{"frameId": frame})
	n := 0
	for _, s := range body["scopes"].([]any) {
		s := s.(map[string]any)
		if !strings.HasPrefix(s["name"].(string), "Captured output") {
			continue
		}
		n++
		vars := h.request("variables", map[string]any{"variablesReference": s["variablesReference"]})
		for _, v := range vars["variables"].([]any) {
			if ref := v.(map[string]any)["variablesReference"].(float64); ref > 0 {
				h.variables(map[string]any{"variablesReference": ref})
			}
		}
	}
	return n
}

func tellerConfig(line int) map[string]any {
	return map[string]any{"mode": "test", "program": testdata("teller.nomi"), "line": line}
}

// Stopped inside an `io.replay` after three output lines, one input line
// read and a prompt with no newline: the scope shows the output, the
// transcript as the replay compares it, and the script's input read and to
// come. The test's own frame, outside the replay, has no such scope. What
// the test printed before the replay reached the console before the stop,
// and the report does not print it again.
func TestSession_TheCapturedOutputOfAReplay(t *testing.T) {
	h := newHarness(t)
	h.start(false, tellerConfig(29))
	h.breakAt("teller.nomi", 10)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	if n := h.captured(frames[0]); n != 1 {
		t.Fatalf("teller's frame has %d captured-output scopes, want 1", n)
	}
	test := frames[len(frames)-1]
	if n := h.captured(test); n != 0 {
		t.Fatalf("the test's frame, outside the replay, has %d captured-output scopes", n)
	}
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("captured_replay")
}

// An `io.capture` inside an `io.replay`: the lambda inside both shows two
// scopes, the capture's first; the function that called `io.capture` shows
// the replay's alone.
func TestSession_TheCapturedOutputOfANestedCapture(t *testing.T) {
	h := newHarness(t)
	h.start(false, tellerConfig(46))
	h.breakAt("teller.nomi", 19)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	if n := h.captured(frames[0]); n != 2 {
		t.Fatalf("the lambda's frame has %d captured-output scopes, want 2", n)
	}
	// receipt, which called io.capture.
	if n := h.captured(frames[3]); n != 1 {
		t.Fatalf("receipt's frame has %d captured-output scopes, want 1", n)
	}
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("captured_nested")
}

// A replay whose transcript already differs from its script says where.
func TestSession_ADivergedReplay(t *testing.T) {
	h := newHarness(t)
	h.start(false, tellerConfig(56))
	h.breakAt("teller.nomi", 9)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.captured(frames[0])
	h.breakAt("teller.nomi")
	h.request("setExceptionBreakpoints", map[string]any{"filters": []string{}})
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("captured_diverged")
}

// Stepping back inside a replay runs the test again up to the earlier
// statement: the scope shows the replay as it was there.
func TestTravel_StepBackInsideAReplay(t *testing.T) {
	h := newHarness(t)
	h.start(false, tellerConfig(29))
	h.breakAt("teller.nomi", 10)
	h.configure()
	thread := h.stopped()
	for range 2 {
		h.do("stepBack", thread)
		h.stopped()
	}
	frames := h.stack(thread)
	h.captured(frames[0])
	h.breakAt("teller.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("captured_step_back")
}
