package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// pendingDefer is one registered deferred call with the operands read at its
// registration, and the runtime frame in effect then, so an app field replaced
// after the `defer` statement is not what the deferred call reads.
//
// onError marks an error-only registration, `defer on error`, which runs
// only on an error exit (dropErrorDefers).
type pendingDefer struct {
	id      int
	call    *ir.Call
	args    []any
	fn      any
	runtime *rt.Frame
	onError bool
}

// deferInstr registers a deferred call. Its operands are read now, at the
// `defer` statement.
func deferInstr(fr *frame, n *ir.Defer) error {
	args, fn, err := callOperands(fr, n.Call())
	if err != nil {
		return err
	}
	fr.deferred = append(fr.deferred, pendingDefer{id: n.ID(), call: n.Call(), args: args, fn: fn, runtime: fr.runtime, onError: n.OnError()})
	return nil
}

// dropErrorDefers removes the error-only registrations of an activation
// returning normally with result, unless result is a failure: an `Err`, or
// a `None`. A callback's `break` or `continue` is no error exit. A fault, or a panic unwinding the activation, is an error exit
// and keeps them.
func dropErrorDefers(fr *frame, result func() any) {
	n := 0
	for _, d := range fr.deferred {
		if d.onError {
			n++
		}
	}
	if n == 0 {
		return
	}
	if carries, isShape := shapeCarries(result()); isShape && !carries {
		return
	}
	kept := fr.deferred[:0]
	for _, d := range fr.deferred {
		if !d.onError {
			kept = append(kept, d)
		}
	}
	clear(fr.deferred[len(kept):])
	fr.deferred = kept
}

// runDeferInstr runs the most recent registration at its scope's normal exit,
// or drops it, unrun, when it is error-only.
// A scope's RunDefers are in reverse registration order, so that registration
// is the one the instruction names. A failing cleanup reports through
// rt.DeferredCleanupError, and the activation's exit runs the rest.
func (m *Machine) runDeferInstr(fr *frame, n *ir.RunDefer) error {
	last := len(fr.deferred) - 1
	if last < 0 || fr.deferred[last].id != n.ID() {
		return fmt.Errorf("vm: %s: %s does not name the most recent registered deferred call",
			fr.fn.Name(), n)
	}
	d := fr.deferred[last]
	fr.deferred = fr.deferred[:last]
	if d.onError {
		return nil
	}
	if _, err := m.invokeDeferred(fr, d); err != nil {
		return rt.DeferredCleanupError(d.call.Pos().Line(), err)
	}
	return nil
}

// unwindDefers runs every deferred call an exit left behind, most recent
// first, as each enclosing scope's cleanup does. The activation's own outcome
// stands unless a cleanup fails; cleanup failures are joined into one error.
func (m *Machine) unwindDefers(fr *frame, v any, err error) (any, error) {
	for len(fr.deferred) != 0 {
		last := len(fr.deferred) - 1
		d := fr.deferred[last]
		fr.deferred = fr.deferred[:last]
		if _, cleanupErr := m.invokeDeferred(fr, d); cleanupErr != nil {
			wrapped := rt.DeferredCleanupError(d.call.Pos().Line(), cleanupErr)
			if err != nil {
				// Both wrapped: the first failure keeps its kind (a fault, an
				// assertion), and the cleanup's is reported after it.
				err = fmt.Errorf("%w; cleanup error: %w", err, wrapped)
			} else {
				v, err = nil, wrapped
			}
		}
	}
	return v, err
}

// invokeDeferred runs a registration under the runtime frame it was
// registered in, and restores the activation's own afterwards. While the
// task or deadline that frame answers to is stopping, the call runs on it
// with no safe point (rt.Unstoppable), so a cleanup the stop unwinds through
// runs to its end instead of stopping at its own first call.
func (m *Machine) invokeDeferred(fr *frame, d pendingDefer) (any, error) {
	current := fr.runtime
	fr.runtime = d.runtime
	if fr.runtime.Stopping() {
		fr.runtime = rt.Unstoppable(fr.runtime)
	}
	defer func() { fr.runtime = current }()
	return m.invoke(fr, d.call, d.args, d.fn)
}
