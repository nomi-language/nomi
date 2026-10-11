package dap

import (
	"strings"
	"testing"
)

// Function breakpoints: a file-qualified plain function, an owner function
// with a condition, a generic function stopping in both its instances, and
// unknown names reported with the closest ones.
func TestFunctionBreakpoints_StopAtAFunctionsFirstStatement(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("tiny_bank.nomi"))
	h.request("setFunctionBreakpoints", map[string]any{"breakpoints": []map[string]any{
		{"name": "tiny_bank.transact"},
		{"name": "Account.deposit", "condition": "n > 10"},
		{"name": "ident"},
		{"name": "trnsact"},
		{"name": "depost"},
		{"name": "Account.deposit", "condition": "n +"},
	}})
	h.configure()
	thread := h.stopped()
	h.stack(thread)
	for range 4 {
		h.do("continue", thread)
		h.stopped()
		frames := h.stack(thread)
		h.locals(frames[0])
	}
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	h.check("function_breakpoints")
}

// Reverse continue goes back to the latest earlier entry a function
// breakpoint stopped at, and to the program's start before the first.
func TestFunctionBreakpoints_ReverseContinueToOne(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("tiny_bank.nomi"))
	h.request("setFunctionBreakpoints", map[string]any{"breakpoints": []map[string]any{{"name": "Account.deposit"}}})
	h.configure()
	thread := h.stopped()
	for range 2 {
		h.do("continue", thread)
		h.stopped()
	}
	for range 2 {
		h.do("reverseContinue", thread)
		h.stopped()
		frames := h.stack(thread)
		h.locals(frames[0])
	}
	h.do("reverseContinue", thread)
	h.stopped()
	h.request("setFunctionBreakpoints", map[string]any{"breakpoints": []map[string]any{}})
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()
	if !strings.Contains(h.log.String(), `"reason":"function breakpoint"`) {
		t.Fatalf("no stop was a function breakpoint's:\n%s", h.log.String())
	}
	h.check("function_breakpoints_reverse")
}
