package vm

// RESTART FRAME: run a stopped frame's function again from its first
// statement, with the same arguments.
//
// Values are immutable and a parameter's register is written once, at
// entry, so the arguments are still in the frame's registers, and running
// its bytecode from the start again is the call made again. What the
// function and its callees already did outside the machine (output, files)
// stays done.
//
// The activations above the restarted one are unwound by an error,
// restartUnwind, which the stopped thread raises from its hook as it
// resumes. Each activation it leaves passes it on to its caller without
// running a fault handler or stopping for it, and the restarted
// activation, receiving it, jumps to its first instruction. Go's stack
// cannot be unwound past a call from Go (an Iter.map callback, a host
// function's), so a frame below one cannot restart; nor can a frame whose
// function a tail call replaced, nor one with deferred calls pending in the
// activations to unwind, which would run as they left.

import (
	"errors"
	"strconv"

	"github.com/nomi-language/nomi/internal/ir"
)

// restartUnwind is the error that unwinds the activations above target.
type restartUnwind struct {
	target *frame
}

func (*restartUnwind) Error() string { return "vm: restarting a frame" }

// RestartFrame restarts a frame Stack handed out: its thread resumes, every
// activation above the frame is left, and the frame runs its function again
// from the first statement with the same arguments, stopping there with
// StopRestart. Every other thread resumes too, as a step does. It refuses,
// saying why, a frame that cannot restart.
func (d *Debugger) RestartFrame(frameID int) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return "", errors.New("no frame " + strconv.Itoa(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr == nil {
		return "", errors.New("this frame is a call into Go; restart the Nomi frame below it")
	}
	th, target := ref.th, ref.fr
	switch {
	case d.killed:
		return "", errors.New("the program has ended")
	case th == nil || th.at == nil:
		return "", errors.New("this thread is inside a call into Go (waiting for input, a channel or a task), " +
			"and Go's stack cannot be unwound; restart it once it stops at a statement")
	case !th.restartable:
		return "", errors.New("`main` has returned, so its frame cannot run again")
	case target.tail:
		return "", errors.New("a tail call replaced this frame's function, so its own first statement and " +
			"arguments are gone")
	case target.c == nil || target.c.dbg == nil:
		return "", errors.New("this frame's function was compiled without a debugger's hooks")
	}
	for fr := th.at; fr != target; fr = fr.parent {
		if fr == nil {
			return "", errors.New("this frame is not on the stopped thread's stack")
		}
		if fr.via != "" || fr.stk != target.stk {
			return "", errors.New("a call from Go (" + viaName(fr) + ") is between this frame and the top " +
				"of the stack, and Go's stack cannot be unwound")
		}
		if len(fr.deferred) > 0 || len(fr.releases) > 0 {
			return "", errors.New(frameName(fr.fn) + " above this frame has deferred calls pending, " +
				"which would run as it is left")
		}
	}
	if len(target.deferred) > 0 || len(target.releases) > 0 {
		return "", errors.New("this frame has deferred calls pending, which a restart would drop unrun")
	}
	if writesParams(target.fn) {
		return "", errors.New("this function writes a parameter's register, so its arguments are not kept")
	}
	th.restart = target
	if d.reportPendingLocked() {
		th.restart = nil
		return "", errors.New("another thread stopped meanwhile; restart again once it is shown")
	}
	d.recordRestartLocked(th, target.ldepth)
	for _, other := range d.threads {
		other.step = stepState{}
	}
	th.step = stepState{mode: stepRestart}
	d.halted = false
	d.frames = nil
	d.gen++
	d.refreshLocked()
	d.cond.Broadcast()
	return frameName(target.fn), nil
}

// viaName names the Go-driven operation between fr and its parent.
func viaName(fr *frame) string {
	if fr.via != "" {
		return fr.via
	}
	return "a host function"
}

// writesParams reports whether an instruction of f writes one of its
// parameters' temporaries.
func writesParams(f *ir.Func) bool {
	for _, p := range f.Params() {
		if f.Def(p.Temp) != nil {
			return true
		}
	}
	return false
}

// takeRestart is the unwinding a parked thread raises as it resumes when a
// restart was asked of it. Called with d.mu held.
func (th *thread) takeRestart() error {
	if th.restart == nil {
		return nil
	}
	target := th.restart
	th.restart = nil
	return &restartUnwind{target: target}
}

// restarting is a fault's route when err is a restart's unwinding: fr is
// the restarted activation, which starts again at its first instruction
// (pc 0), or one above it, which it leaves.
func restarting(fr *frame, err error) (pc int, out error, is bool) {
	ru, isRestart := err.(*restartUnwind)
	if !isRestart {
		return 0, nil, false
	}
	if ru.target != fr {
		return -1, err, true
	}
	fr.trace, fr.stages, fr.next, fr.bad, fr.taps = nil, nil, nil, nil, nil
	return 0, nil, true
}
