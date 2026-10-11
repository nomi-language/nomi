package dap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// setBreakpoints with each breakpoint's own fields (condition, hitCondition,
// logMessage).
func (h *harness) breakWith(file string, bps ...map[string]any) {
	h.t.Helper()
	h.request("setBreakpoints", map[string]any{"source": map[string]any{"path": testdata(file)}, "breakpoints": bps})
}

func (h *harness) eval(frame int, context, expr string) map[string]any {
	h.t.Helper()
	return h.request("evaluate", map[string]any{"expression": expr, "frameId": frame, "context": context})
}

// Watches and hovers of locals and fields, console expressions with a
// structured result, an expression refused for its effect and then run with
// the override, one that loops forever, and one that does not compile.
func TestSession_EvaluatesInTheStoppedFrame(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("eval.nomi"))
	h.breakAt("eval.nomi", 33)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	top := frames[0]
	body := h.eval(top, "watch", "p")
	h.children(body)
	h.eval(top, "hover", "p.x")
	h.eval(top, "hover", "label")
	h.eval(top, "watch", "norm(p) + Iter.count(xs)")
	body = h.eval(top, "repl", "Point{x: p.y, y: p.x}")
	h.children(body)
	h.eval(top, "repl", "xs |> Iter.map(|x| x * label.length) |> Iter.to_list()")
	h.eval(top, "repl", "xs |> Iter.map(|x| x * 10) |> Iter.to_list()")
	h.eval(top, "repl", `io.print("from the console")`)
	h.eval(top, "repl", `io.print("from the console") // allow effects`)
	h.eval(top, "hover", `io.print("from a hover") // allow effects`)
	h.eval(top, "repl", "spin(0)")
	h.eval(top, "repl", "nosuch + 1")
	h.eval(top, "clipboard", "xs")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("eval")
}

// A condition true at one pass, a condition never true, a hit count, a
// logpoint, a condition that does not compile, one that is not a Bool, and
// one that traps when it runs.
func TestSession_ConditionsHitCountsAndLogpoints(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("eval.nomi"))
	h.breakWith("eval.nomi",
		map[string]any{"line": 29, "condition": "i == 3"},
		map[string]any{"line": 33, "condition": "p.x > 100"},
		map[string]any{"line": 23, "condition": "nosuch > 1"},
		map[string]any{"line": 24, "condition": "p.x + 1"},
		map[string]any{"line": 25, "hitCondition": "every other"},
	)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.eval(frames[0], "watch", "i")
	h.breakWith("eval.nomi", map[string]any{"line": 29, "hitCondition": "% 2"})
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.eval(frames[0], "watch", "i")
	h.breakWith("eval.nomi",
		map[string]any{"line": 29, "logMessage": "{label} {{i}} is {i}, doubled {i * 2}, over zero {i / 0}"},
		map[string]any{"line": 33, "hitCondition": ">= 1", "condition": "10 / (p.x - 3) > 0"},
	)
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("conditions")
}

// children requests the variables of an evaluation's result, when it has
// any.
func (h *harness) children(body map[string]any) {
	h.t.Helper()
	if ref, ok := body["variablesReference"].(float64); ok && ref > 0 {
		h.variables(map[string]any{"variablesReference": int(ref)})
		return
	}
	h.note("no children")
}

// The pipeline scope: an eager pipeline stopped at its last stage, a lazy
// one before its forcing stage runs, and the lazy one after it, with what
// each lazy stage passed on.
func TestSession_ThePipelineScope(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("pipeline.nomi"))
	h.breakAt("pipeline.nomi", 17, 23, 25)
	h.configure()
	for range 3 {
		thread := h.stopped()
		frames := h.stack(thread)
		body := h.request("scopes", map[string]any{"frameId": frames[0]})
		for _, s := range body["scopes"].([]any) {
			s := s.(map[string]any)
			if s["name"] == "Pipeline" {
				refs := h.variables(map[string]any{"variablesReference": int(s["variablesReference"].(float64))})
				if ref := refs["|> Iter.filter(|x| x > 15)"]; ref > 0 {
					h.variables(map[string]any{"variablesReference": ref})
				}
			}
		}
		h.do("continue", thread)
	}
	h.wait("terminated")
	h.disconnect()
	h.check("pipeline")
}

// Pause while the only thread waits for input: the program stops at once,
// and the thread's stack starts at the read.
func TestSession_PauseWhileBlockedOnInput(t *testing.T) {
	h := newHarness(t)
	h.start(true, map[string]any{"mode": "run", "program": testdata("interactive.nomi")})
	h.configure()
	if h.termIn == nil {
		t.Fatalf("no terminal was opened; transcript:\n%s", h.log.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.termOut.String(), "type lines") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// The read starts right after the prompt is printed.
	time.Sleep(300 * time.Millisecond)
	h.request("pause", map[string]any{"threadId": 1})
	thread := h.stopped()
	h.request("threads", nil)
	h.stack(thread)
	h.do("continue", thread)
	h.termIn.Write([]byte("alpha\n"))
	h.termIn.Close()
	h.wait("terminated")
	h.disconnect()
	h.check("pause_blocked")
}

// Pause while main awaits a task and the task waits for input: both threads
// are in calls into Go, and each shows its stack from its call.
func TestSession_PauseWhileEveryThreadIsBlocked(t *testing.T) {
	h := newHarness(t)
	h.start(true, map[string]any{"mode": "run", "program": testdata("blocked_task.nomi")})
	h.configure()
	if h.termIn == nil {
		t.Fatalf("no terminal was opened; transcript:\n%s", h.log.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.termOut.String(), "waiting") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	h.request("pause", map[string]any{"threadId": 1})
	thread := h.stopped()
	h.request("threads", nil)
	h.stack(1)
	h.stack(2)
	h.do("continue", thread)
	h.termIn.Write([]byte("alpha\n"))
	h.termIn.Close()
	h.wait("terminated")
	// The program's last line reaches the terminal stub after the session
	// ends; the stub ends once it has written everything.
	select {
	case <-h.termDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal stub did not end with the program")
	}
	h.disconnect()
	h.note("terminal: %q", h.termOut.String())
	h.check("pause_blocked_task")
}

// Restart the top frame; refuse main's frame below an Iter.map callback,
// and a frame a tail call replaced.
func TestSession_RestartFrame(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("restart.nomi"))
	h.breakAt("restart.nomi", 6)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.request("restartFrame", map[string]any{"frameId": frames[0]})
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.stopped()
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.request("restartFrame", map[string]any{"frameId": frames[len(frames)-1]})
	h.request("restartFrame", map[string]any{"frameId": frames[0]})
	h.breakAt("restart.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("restart")
}

// A launch's line may be a string of digits, as Zed writes `$ZED_ROW` into a
// configuration.
func TestLaunchLineMayBeAString(t *testing.T) {
	for _, c := range []struct {
		json string
		line int
	}{{`{"line": 12}`, 12}, {`{"line": "12"}`, 12}, {`{}`, 0}} {
		var a launchArgs
		if err := json.Unmarshal([]byte(c.json), &a); err != nil || int(a.Line) != c.line {
			t.Errorf("%s: line %d, error %v; want %d", c.json, a.Line, err, c.line)
		}
	}
	var a launchArgs
	if err := json.Unmarshal([]byte(`{"line": "$ZED_ROW"}`), &a); err == nil {
		t.Errorf("an unsubstituted variable parsed as line %d", a.Line)
	}
}
