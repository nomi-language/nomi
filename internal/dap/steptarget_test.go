package dap

import "testing"

// Step into a target: a line with three calls (two to one function), into
// the second; a pipeline with two lambdas, into the second's callback; and a
// callback an empty list never calls, which stops at the next statement and
// says so.
func TestStepInTargets_StepIntoTheSecondOfTwoCallsAndCallbacks(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("targets.nomi"))
	h.breakAt("targets.nomi", 12, 14, 21)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	h.request("stepInTargets", map[string]any{"frameId": frames[0]})
	h.request("stepIn", map[string]any{"threadId": thread, "targetId": 2})
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.request("stepIn", map[string]any{"threadId": thread, "targetId": 2})
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.request("stepInTargets", map[string]any{"frameId": frames[0]})
	h.request("stepIn", map[string]any{"threadId": thread, "targetId": 2})
	h.stopped()
	frames = h.stack(thread)
	h.locals(frames[0])
	h.do("continue", thread)
	h.stopped()
	frames = h.stack(thread)
	h.request("stepInTargets", map[string]any{"frameId": frames[0]})
	h.request("stepIn", map[string]any{"threadId": thread, "targetId": 1})
	h.stopped()
	h.stack(thread)
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("step_in_targets")
}
