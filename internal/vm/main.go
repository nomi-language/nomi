package vm

import (
	"context"
	"errors"
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
	_, err := m.entry(ctx, args, handleSignals, func(run *Machine) (any, error) {
		result, err := run.Run("main")
		if err != nil {
			return nil, err
		}
		// Rendered here, before shutdown, so a Display impl that reads an
		// application field still finds the app boot published.
		return nil, run.mainResultError(result)
	})
	return err
}

// MainError is a `fn main` that returned `Err`: the program's failure, which
// `nomi run` prints to stderr before exiting 1. Text is the payload's Display
// rendering when its type implements Display (a String is itself), and its
// Debug rendering otherwise.
type MainError struct {
	Text string
}

// Error is the line `nomi run` prints: `error: ` and the payload's text.
func (e *MainError) Error() string { return "error: " + e.Text }

// mainResultError is the failure `main`'s own result reports, or nil. A
// failed `assert` whose boundary is `main` makes main return
// `Err(AssertionFailure)`, reported as the assertion. Any other `Err` is a
// *MainError carrying the payload's text.
func (m *Machine) mainResultError(result any) error {
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
	text, err := m.mainFailureText(payload)
	if err != nil {
		return err
	}
	return &MainError{Text: text}
}

// mainFailureText renders an `Err` payload main returned through the entry
// module's `main failure` function (ir.Module.SetMainFailure). A module with
// none, because the builder could not render main's error type, gets the
// payload's structural text.
func (m *Machine) mainFailureText(payload any) (string, error) {
	sym := m.mod.MainFailure()
	if sym == nil {
		return rt.RowText(payload), nil
	}
	fn, err := m.resolveFunc(sym, "main")
	if err != nil {
		return rt.RowText(payload), nil
	}
	out, err := m.call(fn, []any{payload})
	if err != nil {
		return "", err
	}
	text, ok := out.(string)
	if !ok {
		return "", fmt.Errorf("vm: %s returned %T, not a String", sym.Name(), out)
	}
	return text, nil
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
			if _, internal := AsCompilePanic(bootErr); internal {
				return nil, bootErr
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
	if cp, internal := AsCompilePanic(err); internal {
		// A compiler bug: neither the program's failure nor a limit.
		return cp, false
	}
	if a, isAssertion := asAssertFailure(err); isAssertion {
		return a, false
	}
	if _, isFault := asFault(err); isFault {
		return err, false
	}
	var mainErr *MainError
	if errors.As(err, &mainErr) {
		return err, false
	}
	return err, true
}

// MainRoots are the functions a run of main can call first, which is what a
// reachability check over a run starts from: main, and the entry module's
// `main failure` renderer when it has one (ir.Module.SetMainFailure).
func MainRoots(entry *ir.Module, main *ir.Func) []*ir.Func {
	roots := []*ir.Func{main}
	if sym := entry.MainFailure(); sym != nil {
		if f := entry.FuncFor(sym); f != nil {
			roots = append(roots, f)
		}
	}
	return roots
}
