package vm

import (
	"errors"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// # THE CALL-DEPTH LIMIT
//
// Every Nomi call this machine makes is a Go call: activate runs the callee's
// blocks on the caller's goroutine stack. Unbounded recursion therefore grows
// that stack until the Go runtime's maximum, and a Go stack overflow is not a
// panic. It prints "goroutine stack exceeds" and kills the process, so nothing
// above it, not `nomi test`'s per-case recovery, not a task's Failed outcome,
// not a test binary, gets to run.
//
// So each activation carries its depth, the number of activations its
// goroutine has in progress counting itself, and an activation whose caller is
// already at rt.MaxCallDepth does not start. The limit and the fault text live
// in rt, which owns every positioned fault text.
//
// WHERE THE DEPTH COMES FROM. A frame's depth is its caller's plus one. A call
// instruction knows its caller: it is the frame the instruction runs in
// (callFrom). A callee started from Go, by a host call such as `Result.map_err`
// or `Task.spawn`, by an iteration driver running a callback, by `dbg`
// rendering through a Debug impl, or by a `once` being forced, has no frame in
// hand, so the Machine view it runs on carries the depth instead
// (Machine.depth): invoke gives a host its bound view at the calling frame's
// depth, and irInstr gives an iteration the same through at. A task body runs on a
// goroutine whose stack starts empty, so runTask starts it at zero.
//
// WHERE THE POSITION COMES FROM. A callee does not know which instruction asked
// for it, so it answers callDepthExceeded with no line. The first run loop the
// refusal reaches is the caller's, and the instruction at its pc is the
// call that went one level too deep, or the host call or iteration whose
// callback did. positionCallDepth turns it into rt's positioned fault there,
// already marked a Fault, so every later frame passes it through unchanged and
// every consumer that tells a Nomi fault from a machine limit sees a fault.
//
// TAIL CALLS DO NOT COUNT. A call the graph marks as a tail transfer, direct,
// through a function value or dispatched, replaces the caller's activation in
// the same frame, which keeps its depth (tail.go). So a tail-recursive loop
// runs forever rather than reaching the limit, as the spec says, and only
// calls that keep their caller's frame count toward it.

// callDepthExceeded is an activation's refusal to start past the limit, before
// a caller has given it a position.
type callDepthExceeded struct{}

func (callDepthExceeded) Error() string { return "call depth exceeded before a position was known" }

// positionCallDepth replaces an unpositioned depth refusal with rt's
// positioned fault at in, and passes every other error through.
func positionCallDepth(err error, pos ir.Pos) error {
	var refused callDepthExceeded
	if !errors.As(err, &refused) {
		return err
	}
	return &Fault{err: rt.CallDepthError(pos.Line())}
}

// at is m as seen from the activation fr: the view a callback started from Go
// on fr's behalf runs on, so its callee counts from fr's depth.
func (m *Machine) at(fr *frame) *Machine {
	if m.depth == fr.depth {
		return m
	}
	view := *m
	view.depth = fr.depth
	return &view
}
