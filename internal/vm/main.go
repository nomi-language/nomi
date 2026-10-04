package vm

import (
	"context"
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"

	"github.com/nomi-language/nomi/rt"
)

// Main runs the program as `nomi run` does: boot (when the program records
// one), then `main`, on one rt frame. Shutdown is three steps in order — the
// supervisor drain runs whether `main` returned or faulted, then boot cleanup,
// then a supervisor's recorded fatal is read — and a boot failure is wrapped
// as `boot() failed`.
//
// handleSignals installs rt's SIGINT/SIGTERM handler after boot; only a caller
// that owns the process passes true.
//
// The error is either the program's own failure or this machine's limit;
// ProgramFailure tells them apart.
func (m *Machine) Main(ctx context.Context, args []string, handleSignals bool) error {
	result, err := m.entry(ctx, args, handleSignals, func(run *Machine) (any, error) { return run.Run("main") })
	if err != nil {
		return err
	}
	return mainResultError(result)
}

// mainResultError is the failure `main`'s own result reports, or nil. A
// failed `assert` whose boundary is `main` makes main return
// `Err(AssertionFailure)`, and that is the program failing, reported as the
// assertion. Any other `Err` main returns is ordinary data and exits 0.
func mainResultError(result any) error {
	e, isRec := enumRecord(result)
	if !isRec || !isEnum(e, "results.Result") || variantName(e) != "Err" {
		return nil
	}
	payload, has := payloadOf(e)
	if !has {
		return nil
	}
	if f, isFailure := nomiFailureOf(payload); isFailure {
		return &assertFailure{failure: f.Report()}
	}
	return nil
}

// Call runs f with args as an embedding host calls a Nomi function: boot,
// then f, on one rt frame of ctx, with Main's shutdown. It answers f's result.
func (m *Machine) Call(ctx context.Context, f *ir.Func, args []any) (any, error) {
	return m.entry(ctx, nil, false, func(run *Machine) (any, error) {
		return run.callWithFrame(f, args, run.hostFrame)
	})
}

// entry is Main and Call: boot, body, shutdown.
func (m *Machine) entry(ctx context.Context, args []string, handleSignals bool, body func(*Machine) (any, error)) (result any, err error) {
	fr := rt.NewFrame(m.withDiagnostics(ctx))
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			err = &Fault{err: e}
		}
		rt.DrainSupervisors()
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err == nil {
						err = &Fault{err: fmt.Errorf("%v", r)}
					} else {
						err = fmt.Errorf("%w; cleanup error: %v", err, r)
					}
				}
			}()
			rt.RunBootCleanup(fr)
		}()
		if err == nil {
			if fatal := rt.SupervisorFatal(); fatal != nil {
				err = &Fault{err: fatal}
			}
		}
		// rt's supervisor registry is process-wide and a drained one takes
		// no new work, so it is dropped for the next program this process
		// runs — the REPL's next input, the tour's next block.
		rt.ResetSupervisors()
	}()
	run := *m
	run.hostFrame = fr
	if m.boot != nil {
		booted, bootErr := m.bootWith(m.boot, startupValue(args, m.hostEnv), fr)
		if bootErr != nil {
			if failed, isFailed := bootErr.(*bootFailed); isFailed {
				return nil, &Fault{err: failed}
			}
			if _, isLimit := ProgramFailure(bootErr); isLimit {
				return nil, bootErr
			}
			return nil, &Fault{err: fmt.Errorf("boot() failed: %w", bootErr)}
		}
		run = *booted
	}
	if handleSignals {
		stop := rt.InstallShutdownSignals(fr)
		defer stop()
	}
	return body(&run)
}

// ProgramFailure sorts an error this machine answered. A Nomi fault or a
// failed assertion is the PROGRAM's failure, answered as the error `nomi run`
// reports (an assertion as its `*rt.AssertionFailure`, which the shared
// renderer draws). Anything else is this machine's limit — an instruction it
// has no arm for, a callee it cannot resolve — and limit is true: the program
// did not fail, it could not run here.
func ProgramFailure(err error) (failure error, limit bool) {
	if err == nil {
		return nil, false
	}
	if a, isAssertion := asAssertFailure(err); isAssertion {
		return a, false
	}
	if _, isFault := asFault(err); isFault {
		return err, false
	}
	return err, true
}
