package vm

import (
	"errors"

	"github.com/nomi-language/nomi/internal/ir"
)

// # TAIL CALLS
//
// Nomi guarantees that a call in tail position runs in constant stack space
// (spec §12 "Tail-call optimization"). The graph says which calls may be run
// that way: `ir.TailTransfers` is a tail-marked call whose result reaches a
// plain Return unchanged. The bytecode compiler reads that set once per
// function and compiles a direct one to `tail` (bytecode.go). This machine
// runs such a call by REPLACING the current activation rather than starting a
// new one under it: the loop reads the operands, runs the caller's pending
// deferred calls, resizes the frame's register window at the same base for
// the callee, writes the operands into its parameter registers and continues
// at the callee's first instruction (exec.go's `tail` arm and `transfer`). A
// dispatched or indirect one runs through its handler, which hands the
// resolved callee and the boxed operands back to the loop the same way.
//
// So the Go stack does not grow and neither does the call depth: the frame
// keeps its depth, and a tail-recursive loop that never ends runs forever
// instead of reaching rt.MaxCallDepth, as the spec says. A fault inside the
// loop is reported once, at its own line, because there is only one frame to
// report it from.
//
// WHAT THE FRAME KEEPS ACROSS A TRANSFER. Its runtime frame, so an app-field or
// Context rebind the caller made is in force for the callee; the deadlines those rebinds entered stay entered until the whole chain exits,
// because the callee runs under them. Its depth. Once-forcing lineage and the
// cancellation context ride the runtime frame, so they are kept by keeping it.
//
// WHAT IT DROPS. Registers and the assertion trace, which belong
// to the function that was running.
//
// DEFERRED CALLS RUN AT THE TRANSFER, after the operands and before the
// callee: the tail call has already left every block, and a block's exit runs
// its deferred calls. See `internal/ir/tail.go`.
//
// NOT TRANSFERRED: a call that crosses into Go, a function value whose body is
// a host function, and any call from a test case's own body, whose `try`
// propagation leaves the activation differently (see activate). Those run as
// ordinary calls: a `tail` instruction in a test body calls instead.

// tailTransfer is a tail call a handler will not make itself: the callee's
// body and the operands it is to be entered with.
type tailTransfer struct {
	fn   *ir.Func
	args []any
}

// transfers reports whether n is a call this machine runs by replacing fr's
// activation.
func (m *Machine) transfers(fr *frame, n *ir.Call) bool {
	if !n.Tail() || n.Crosses() || fr.testBody {
		return false
	}
	return fr.c.tails[n]
}

// tailTarget resolves a transferring call's callee over operands already
// read. It answers nil when the callee turns out to be Go, and the call is
// then made as an ordinary one.
func (m *Machine) tailTarget(fr *frame, n *ir.Call, args []any, fn any) (*tailTransfer, error) {
	switch n.Form() {
	case ir.CalleeIndirect:
		f, ok := fn.(*functionValue)
		if !ok || f.host != nil || len(args) != f.arity {
			return nil, nil
		}
		return &tailTransfer{fn: f.body, args: append(args, f.captures...)}, nil
	case ir.CalleeDispatched:
		callee, err := m.dispatchTarget(fr, n, args)
		if err != nil {
			return nil, err
		}
		return &tailTransfer{fn: callee, args: args}, nil
	case ir.CalleeDirect:
		callee, err := m.resolveFunc(n.Callee(), fr.fn.Name())
		if err != nil {
			return nil, err
		}
		return &tailTransfer{fn: callee, args: args}, nil
	}
	return nil, nil
}

// errTailTransfer is callInstr's answer when it left a tail call in fr.next
// for the loop to enter. It never leaves the loop.
var errTailTransfer = errors.New("vm: tail transfer")
