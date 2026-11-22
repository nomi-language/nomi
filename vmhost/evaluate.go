package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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

// Unbounded is Evaluate's answer for a function it will not run, because
// the function, or something it can call, calls a pure function whose cost
// grows with an argument's value, which the limits cannot stop.
type Unbounded struct {
	// Calls names each such call reached, one per line, sorted.
	Calls []string
}

func (e *Unbounded) Error() string {
	return "the function calls functions whose cost grows with their arguments:\n  " + strings.Join(e.Calls, "\n  ")
}

// ReadsApp is Evaluate's answer for a function it will not run, because
// the function, or something it can call, reads or replaces an app field,
// and an evaluation for an editor runs with no app.
type ReadsApp struct {
	// Fields names each app field reached, as the program writes it
	// (`App.limit`), sorted.
	Fields []string
}

func (e *ReadsApp) Error() string {
	return "the function reads " + quotedNames(e.Fields) + ", and an app field exists only while the program runs"
}

// appFieldNames is fields, app fields as vm.Effects names them, as the
// program writes them, sorted: each after the application type that
// declares it (`App.limit`), as the checker recorded the program's
// application types, or alone when no check recorded them.
func (p *Program) appFieldNames(fields []string) []string {
	apps := p.appTypes
	if fa := p.prog.Entry().FA; fa != nil {
		apps = fa.Program().AppTypes
	}
	out := make([]string, len(fields))
	for i, field := range fields {
		out[i] = field
	find:
		for _, app := range apps {
			for _, f := range app.Fields {
				if f.Name == field {
					out[i] = app.Name + "." + field
					break find
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// quotedNames is names, each in backticks, joined with commas.
func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, ", ")
}

// WithUnusedBindingsAllowed drops the front end's unused-binding errors, for
// a host that evaluates parts of a program while it is being edited.
func WithUnusedBindingsAllowed() Option {
	return func(c *config) { c.allowUnusedBindings = true }
}

// Evaluate calls the entry's function name, which takes no parameters, the
// way an editor evaluates a constant: the program is not booted, a function
// that can reach an effect (output, files, the network, the clock, tasks,
// channels) is refused with *Effectful before anything runs, one that
// reads an app field, which exists only while the program runs, with
// *ReadsApp, one that can call a function whose cost grows with an
// argument's value (`String.repeat`) with *Unbounded, and the call runs
// under lim, answering ErrEvalLimit when it runs out. Otherwise it answers
// the function's result or its failure.
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
	found := m.Effects(f)
	if len(found.Effects) > 0 {
		return nil, &Effectful{Effects: found.Effects}
	}
	if len(found.AppFields) > 0 {
		return nil, &ReadsApp{Fields: p.appFieldNames(found.AppFields)}
	}
	if len(found.Unbounded) > 0 {
		return nil, &Unbounded{Calls: found.Unbounded}
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
