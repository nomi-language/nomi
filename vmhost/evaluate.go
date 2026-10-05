package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// EvalLimits bounds an Evaluate: the number of calls and backward branches it
// may take, and a wall-clock deadline checked as it runs.
type EvalLimits = vm.Limits

// ErrEvalLimit is Evaluate's answer for a function that used up its limits.
var ErrEvalLimit = vm.ErrLimit

// Effectful is Evaluate's answer for a function it will not run, because the
// function, or something it can call, acts outside the machine.
type Effectful struct {
	// Effects names each effect reached, one per line, sorted.
	Effects []string
}

func (e *Effectful) Error() string {
	return "the function has effects:\n  " + strings.Join(e.Effects, "\n  ")
}

// WithUnusedBindingsAllowed drops the front end's unused-binding errors, for
// a host that evaluates parts of a program while it is being edited.
func WithUnusedBindingsAllowed() Option {
	return func(c *config) { c.allowUnusedBindings = true }
}

// Evaluate calls the entry's function name, which takes no parameters, the
// way an editor evaluates a constant: the program is not booted, a function
// that can reach an effect (output, files, the network, the clock, tasks,
// channels, the running app's fields) is refused with *Effectful before
// anything runs, and the call runs under lim, answering ErrEvalLimit when it
// runs out. Otherwise it answers the function's result or its failure.
func (p *Program) Evaluate(ctx context.Context, name string, lim EvalLimits) (Value, error) {
	f := p.entryFunc(name)
	if f == nil {
		if p.declares(name) {
			return nil, p.blockedName(name)
		}
		return nil, fmt.Errorf("the program declares no function %s without parameters", name)
	}
	m := p.machine(io.Discard)
	if found := m.Unretained([]*ir.Func{f}, nil); len(found) > 0 {
		return nil, p.blocked(found)
	}
	if effects := m.Effects(f); len(effects) > 0 {
		return nil, &Effectful{Effects: effects}
	}
	v, err := m.Evaluate(ctx, f, nil, lim)
	if errors.Is(err, vm.ErrLimit) {
		return nil, ErrEvalLimit
	}
	failure, limit := programFailure(err)
	if limit {
		return nil, &Blocked{Reasons: []string{machineLimit(failure)}}
	}
	if failure != nil {
		return nil, failure
	}
	return v, nil
}
