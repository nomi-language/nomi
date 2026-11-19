package vm

import (
	"context"
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Started is a program kept running for a host that calls its functions
// one at a time (`export fn`): Start boots it once, Main runs `fn main` on
// the booted frame, and each Call runs on that frame too, so every call
// reads the app boot published and the `once` values earlier calls forced.
// Stop ends it as Main's end does.
//
// A call that faults answers the fault and leaves the program running: the
// next Call starts from the same booted frame. So does a call its context
// stopped (within.go): it answers the context's error, and the program
// serves the next call.
type Started struct {
	run *Machine
	fr  *rt.Frame
	// end cancels the context of fr, the program's own, once it stops.
	end context.CancelFunc
}

// Start boots the program on a fresh frame, with args as Startup.args, and
// answers it started. ctx bounds the boot alone (within.go): a boot it
// stops answers ctx's error, and nothing is started. A boot failure is the
// error Main would report.
func (m *Machine) Start(ctx context.Context, args []string) (s *Started, err error) {
	life, end := context.WithCancel(m.withDiagnostics(context.Background()))
	fr := rt.NewFrame(life)
	if m.dbg != nil {
		m.dbg.adopt(fr)
	}
	// The boot runs on the program's own frame, where it publishes the
	// app, so a stop cancels the program's context: a stopped boot leaves
	// nothing started.
	view, release := m.within(ctx, end)
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			s, err = nil, &Fault{err: e}
		}
		err = release(err)
		if err != nil {
			end()
		}
	}()
	run, err := view.bootOn(fr, args)
	if err != nil {
		// The program never started: shut down what boot started and run
		// the cleanup it registered, as Main does. Its first failure stays
		// the error.
		if stopErr := (&Started{fr: fr}).Stop(); stopErr != nil {
			err = fmt.Errorf("%w; cleanup error: %v", err, stopErr)
		}
		return nil, err
	}
	// Calls run on the program's own fuel and task limit, not the boot's
	// bound.
	run.fuel, run.tasks = m.fuel, m.tasks
	return &Started{run: run, fr: fr, end: end}, nil
}

// Main runs the entry's `fn main` and answers its failure, as Main does,
// without shutting the program down. ctx bounds it (within.go).
func (s *Started) Main(ctx context.Context) error {
	_, err := s.bounded(ctx, func(run *Machine) (any, error) {
		result, err := run.Run("main")
		if err != nil {
			return nil, err
		}
		return nil, run.mainResultError(result)
	})
	return err
}

// Call runs f with args on the started program's frame and answers its
// result. A fault is answered as an error ProgramFailure sorts, and the
// program stays started. ctx bounds it (within.go).
func (s *Started) Call(ctx context.Context, f *ir.Func, args []any) (any, error) {
	return s.bounded(ctx, func(run *Machine) (any, error) {
		return run.callWithFrame(f, args, run.hostFrame)
	})
}

// bounded is body run on the started program, bounded by ctx: on a view
// whose fuel ctx's end stops, over a child of the program's frame whose
// context the stop cancels.
func (s *Started) bounded(ctx context.Context, body func(run *Machine) (any, error)) (any, error) {
	if ctx == nil || ctx.Done() == nil {
		return s.call(func() (any, error) { return body(s.run) })
	}
	child, cancel := rt.WithCancel(s.fr)
	defer cancel()
	view, release := s.run.within(ctx, cancel)
	view.hostFrame = child
	result, err := s.call(func() (any, error) { return body(view) })
	return result, release(err)
}

func (s *Started) call(body func() (any, error)) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			result, err = nil, &Fault{err: e}
		}
	}()
	return body()
}

// Stop shuts the program down as Main's end does: the supervisors drain,
// boot's cleanup runs, and a supervisor's recorded fatal is the error.
func (s *Started) Stop() (err error) {
	defer rt.ResetSupervisors()
	if s.end != nil {
		defer s.end()
	}
	rt.DrainSupervisors()
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = &Fault{err: fmt.Errorf("%v", r)}
			}
		}()
		rt.RunBootCleanup(s.fr)
	}()
	if err == nil {
		if fatal := rt.SupervisorFatal(); fatal != nil {
			err = &Fault{err: fatal}
		}
	}
	return err
}
