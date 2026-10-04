package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// pendingDefer is one registered deferred call with the operands read at its
// registration, and the runtime frame in effect then, so an app field replaced
// after the `defer` statement is not what the deferred call reads.
type pendingDefer struct {
	id      int
	call    *ir.Call
	args    []any
	fn      any
	runtime *rt.Frame
}

// deferInstr registers a deferred call. Its operands are read now, at the
// `defer` statement.
func deferInstr(fr *frame, n *ir.Defer) error {
	args, fn, err := callOperands(fr, n.Call())
	if err != nil {
		return err
	}
	fr.deferred = append(fr.deferred, pendingDefer{id: n.ID(), call: n.Call(), args: args, fn: fn, runtime: fr.runtime})
	return nil
}

// runDeferInstr runs the most recent registration at its scope's normal exit.
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
				err = fmt.Errorf("%v; cleanup error: %w", err, wrapped)
			} else {
				v, err = nil, wrapped
			}
		}
	}
	return v, err
}

// invokeDeferred runs a registration under the runtime frame it was
// registered in, and restores the activation's own afterwards.
func (m *Machine) invokeDeferred(fr *frame, d pendingDefer) (any, error) {
	current := fr.runtime
	fr.runtime = d.runtime
	defer func() { fr.runtime = current }()
	return m.invoke(fr, d.call, d.args, d.fn)
}
