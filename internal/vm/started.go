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
// next Call starts from the same booted frame.
type Started struct {
	run *Machine
	fr  *rt.Frame
}

// Start boots the program on a fresh frame of ctx, with args as
// Startup.args, and answers it started. A boot failure is the error Main
// would report.
func (m *Machine) Start(ctx context.Context, args []string) (s *Started, err error) {
	fr := rt.NewFrame(m.withDiagnostics(ctx))
	if m.dbg != nil {
		m.dbg.adopt(fr)
	}
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			s, err = nil, &Fault{err: e}
		}
	}()
	run, err := m.bootOn(fr, args)
	if err != nil {
		return nil, err
	}
	return &Started{run: run, fr: fr}, nil
}

// Main runs the entry's `fn main` and answers its failure, as Main does,
// without shutting the program down.
func (s *Started) Main() error {
	_, err := s.call(func() (any, error) {
		result, err := s.run.Run("main")
		if err != nil {
			return nil, err
		}
		return nil, s.run.mainResultError(result)
	})
	return err
}

// Call runs f with args on the started program's frame and answers its
// result. A fault is answered as an error ProgramFailure sorts, and the
// program stays started.
func (s *Started) Call(f *ir.Func, args []any) (any, error) {
	return s.call(func() (any, error) {
		return s.run.callWithFrame(f, args, s.fr)
	})
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
