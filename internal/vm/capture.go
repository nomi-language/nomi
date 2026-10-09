package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// captureKey is std/io's private `run_captured`, which `io.capture` calls,
// and replayKey its private `run_replayed`, which `io.replay` calls.
const (
	captureKey = "io.run_captured"
	replayKey  = "io.run_replayed"
)

// captureHost is `run_captured(input, run, finish)`: call run on a frame
// whose `io.capture` target reads input and collects output, close the
// capture on every path, then hand run's value, the output and the
// transcript to finish, which builds the `Captured<T>` in Nomi (so this host
// never needs the instantiated struct's layout).
//
// A trap in run propagates unchanged and its partial output is dropped with
// the closed capture. finish runs on the caller's frame, outside the capture.
func captureHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	input, run, finish, err := captureOperands(m, captureKey, args, 3)
	if err != nil {
		return nil, err
	}
	inner, c := rt.EnterCapture(m.hostFrame, input)
	return m.runCaptured(inner, c, run, finish, func(v any, r rt.CaptureResult) []any {
		return []any{v, r.Output, r.Transcript}
	})
}

// replayHost is `run_replayed(script, run, finish)`: captureHost over a
// capture whose input is the script's input lines (rt.ParseReplayScript,
// rt.EnterReplay). finish gets run's value, the output, the transcript and
// the script's expected transcript each as replay compares them
// (rt.ReplayText), and how many input lines no read reached. A script with a
// line that is neither input nor output traps before run runs.
func replayHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	script, run, finish, err := captureOperands(m, replayKey, args, 5)
	if err != nil {
		return nil, err
	}
	parsed, err := rt.ParseReplayScript(script)
	if err != nil {
		return nil, &Fault{err: &rt.Error{Msg: err.Error()}}
	}
	inner, c := rt.EnterReplay(m.hostFrame, parsed)
	return m.runCaptured(inner, c, run, finish, func(v any, r rt.CaptureResult) []any {
		return []any{v, r.Output, rt.ReplayText(r.Transcript), parsed.Expected, int64(r.Unread)}
	})
}

// captureOperands checks the three operands both hosts take: the input
// text, a function of no parameters, and a finish of arity parameters.
func captureOperands(m *Machine, key string, args []any, arity int) (string, *functionValue, *functionValue, error) {
	if len(args) != 3 {
		return "", nil, nil, fmt.Errorf("vm: %s: expected three operands, got %d", key, len(args))
	}
	input, ok := args[0].(string)
	if !ok {
		return "", nil, nil, fmt.Errorf("vm: %s: the input is %T, not a String", key, args[0])
	}
	run, ok := args[1].(*functionValue)
	if !ok || run.arity != 0 {
		return "", nil, nil, fmt.Errorf("vm: %s: run must be a zero-parameter function", key)
	}
	finish, ok := args[2].(*functionValue)
	if !ok || finish.arity != arity {
		return "", nil, nil, fmt.Errorf("vm: %s: finish must take %d parameters", key, arity)
	}
	if m.hostFrame == nil {
		return "", nil, nil, fmt.Errorf("vm: %s: called with no frame", key)
	}
	return input, run, finish, nil
}

// runCaptured calls run on inner, closes c on every path, and calls finish
// on the caller's frame with what operands makes of run's value and c.
func (m *Machine) runCaptured(inner *rt.Frame, c *rt.Capture, run, finish *functionValue,
	operands func(any, rt.CaptureResult) []any) (any, error) {
	v, err := func() (any, error) {
		defer c.Close()
		return m.apply(run, nil, inner)
	}()
	if err != nil {
		return m.settled(nil, err)
	}
	return m.settled(m.apply(finish, operands(v, c.Close()), m.hostFrame))
}
