package dap

import "testing"

// A variable whose type is declared in a file the frame's file does not
// import, and an expression of such a type: the expression compiles with
// the imports it needs added. In a standard library frame, an expression
// that needs nothing private to that file is evaluated too.
func TestSession_EvaluatesTypesFromFilesTheFrameDoesNotImport(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("imports/main.nomi"))
	h.breakAt("imports/main.nomi", 8)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	top := frames[0]
	h.locals(top)
	h.eval(top, "watch", "c.x + c.y")
	h.children(h.eval(top, "repl", "geo.corner(c.x)"))
	h.eval(top, "repl", "[c, c] |> Iter.count()")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()

	// Maybe.map's frame, in std/maybe.nomi.
	h2 := newHarness(t)
	config := runConfig("stdstep.nomi")
	config["justMyCode"] = false
	h2.start(false, config)
	h2.breakAt("stdstep.nomi", 6)
	h2.configure()
	thread = h2.stopped()
	h2.do("stepIn", thread)
	h2.stopped()
	frames = h2.stack(thread)
	h2.locals(frames[0])
	h2.eval(frames[0], "watch", "Maybe.with_default(maybe, 0) + 1")
	h2.eval(frames[0], "repl", "f(5)")
	h2.eval(frames[0], "repl", "maybe |> Maybe.map(f)")
	h2.do("continue", thread)
	h2.wait("terminated")
	h2.disconnect()
	h.log.WriteString("-------------------- a standard library frame\n" + h2.log.String())
	h.check("eval_imports")
}
