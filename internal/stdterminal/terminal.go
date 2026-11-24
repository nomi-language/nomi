package stdterminal

import (
	"runtime"
	"sync"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// IsTerminal is std/io's `stream_is_terminal`: whether the frame's standard
// stream (0 input, 1 output, 2 error) is a terminal.
func IsTerminal(fr *rt.Frame, stream int64) bool {
	_, ok := terminalOf(fr, stream)
	return ok
}

// terminalOf is the descriptor of the terminal the frame's standard stream
// is, and whether it is one. A stream inside `io.capture` or a test case,
// and one that is not a file, is none (rt.StandardFile).
func terminalOf(fr *rt.Frame, stream int64) (uintptr, bool) {
	f := rt.StandardFile(fr, stream)
	if f == nil {
		return 0, false
	}
	fd := f.Fd()
	return fd, isTerminal(fd)
}

// Size is std/terminal/host's `size`: the columns and rows of the terminal
// the frame's standard stream is, or (0, 0) when it is none or its size
// cannot be read.
func Size(fr *rt.Frame, stream int64) (int64, int64) {
	fd, ok := terminalOf(fr, stream)
	if !ok {
		return 0, 0
	}
	columns, rows, err := size(fd)
	if err != nil {
		return 0, 0
	}
	return int64(columns), int64(rows)
}

// SupportsColor is std/terminal/host's `supports_color`: false for a
// stream that is not a file (a capture's, a test case's, a browser's),
// else what the environment chooses (rt.ColorChosen), else whether the
// stream is a terminal.
func SupportsColor(fr *rt.Frame, stream int64) bool {
	if rt.StandardFile(fr, stream) == nil {
		return false
	}
	if on, decided := rt.ColorChosen(); decided {
		return on
	}
	return IsTerminal(fr, stream)
}

// ReadPassword is std/terminal/host's `read_password`: it writes prompt to
// standard error, turns echo off on standard input's terminal, reads a line
// from the buffer `io.read_line` reads, turns echo back on and ends the
// line the user's Enter did not show. Echo comes back on every path: the
// read's return, its task's cancellation (a Go defer), and the process's
// interruption (the drain, modes.restoreAll).
func ReadPassword(fr *rt.Frame, prompt string) rt.Result[rt.Option[string], rt.IOError] {
	fd, ok := terminalOf(fr, 0)
	if !ok {
		return rt.Err[rt.Option[string]](notATerminal("read_password"))
	}
	rt.WriteError(fr, prompt)
	token, failure := modes.enter(fd, noEchoState, "read_password")
	if failure != nil {
		return rt.Err[rt.Option[string]](*failure)
	}
	line := func() rt.Result[rt.Option[string], rt.IOError] {
		defer modes.leave(token)
		return rt.ReadLine(fr)
	}()
	rt.WriteError(fr, "\n")
	return line
}

// EnterRaw is std/terminal/host's `enter_raw`: standard input's terminal
// in raw mode, keeping its signal keys or its output processing when asked,
// and the token LeaveRaw restores the mode it had with.
func EnterRaw(fr *rt.Frame, keepSignals, keepOutput bool) rt.Result[int64, rt.IOError] {
	fd, ok := terminalOf(fr, 0)
	if !ok {
		return rt.Err[int64](notATerminal("raw_mode"))
	}
	token, failure := modes.enter(fd, func(s state) state { return rawState(s, keepSignals, keepOutput) }, "raw_mode")
	if failure != nil {
		return rt.Err[int64](*failure)
	}
	return rt.Ok[int64, rt.IOError](token)
}

// LeaveRaw is std/terminal/host's `leave_raw`: the terminal back in the
// mode EnterRaw found it in. A second leave does nothing.
func LeaveRaw(token rt.Int) rt.Unit {
	modes.leave(token.Saturated())
	return rt.Unit{}
}

// ReadByte is std/terminal/host's `read_byte`: the next byte of standard
// input, waiting at most within nanoseconds (as long as it takes when
// within is negative): the byte, -1 when none came in time, -2 at the end.
func ReadByte(fr *rt.Frame, within rt.Int) rt.Result[int64, rt.IOError] {
	wait := time.Duration(-1)
	if within.Sign() >= 0 {
		wait = time.Duration(within.Saturated())
	}
	return rt.StdinReadByte(fr, wait)
}

func notATerminal(operation string) rt.IOError {
	return rt.IOError{Tag: rt.TagIOOther, Operation: operation, Path: rt.StdinPath, Code: "ENOTTY"}
}

// modes holds each change this process made to a terminal's mode that has
// not been undone, in the order they were made.
var modes modeStack

// modeStack is the changes to terminals' modes not yet undone. Each holds
// the mode its terminal had before it, so leaving the newest change on a
// terminal restores what that change found. Leaving an older one first, as
// two tasks may, restores nothing then: the change made after it inherits
// the mode it found, so the last change left restores the terminal's
// first mode. While any change is held, a drain hook (rt.OnDrain) restores
// each terminal's first mode, so a process interrupted or ending inside raw
// mode or a password read leaves the terminal as it found it.
type modeStack struct {
	mu      sync.Mutex
	next    int64
	changes []change
	unhook  func()
}

type change struct {
	token int64
	fd    uintptr
	saved state
}

func (m *modeStack) enter(fd uintptr, to func(state) state, operation string) (int64, *rt.IOError) {
	m.mu.Lock()
	saved, err := getState(fd)
	if err == nil {
		err = setState(fd, to(saved))
	}
	if err != nil {
		m.mu.Unlock()
		return 0, modeError(operation, err)
	}
	m.next++
	token := m.next
	m.changes = append(m.changes, change{token: token, fd: fd, saved: saved})
	hook := m.unhook == nil
	if hook {
		m.unhook = func() {}
	}
	m.mu.Unlock()
	if hook {
		// Registered with the lock released: a drain already under way
		// runs the hook at once, and the hook takes the lock.
		unhook := rt.OnDrain(m.restoreAll)
		m.mu.Lock()
		if len(m.changes) > 0 && m.unhook != nil {
			m.unhook = unhook
		} else {
			unhook()
		}
		m.mu.Unlock()
	}
	return token, nil
}

func (m *modeStack) leave(token int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at := -1
	for i, c := range m.changes {
		if c.token == token {
			at = i
		}
	}
	if at < 0 {
		return
	}
	c := m.changes[at]
	newer := -1
	for i := at + 1; i < len(m.changes); i++ {
		if m.changes[i].fd == c.fd {
			newer = i
			break
		}
	}
	if newer >= 0 {
		m.changes[newer].saved = c.saved
	} else {
		_ = setState(c.fd, c.saved)
	}
	m.changes = append(m.changes[:at], m.changes[at+1:]...)
	if len(m.changes) == 0 && m.unhook != nil {
		m.unhook()
		m.unhook = nil
	}
}

// restoreAll puts each terminal a change is held on back in its first
// mode, and forgets the changes: the drain's hook.
func (m *modeStack) restoreAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	done := map[uintptr]bool{}
	for _, c := range m.changes {
		if !done[c.fd] {
			done[c.fd] = true
			_ = setState(c.fd, c.saved)
		}
	}
	m.changes = nil
	m.unhook = nil
}

// modeError is the io.Error for a mode that could not be read or set:
// Unsupported where this build has no terminal modes, else Other with the
// system's code.
func modeError(operation string, err error) *rt.IOError {
	if err == errNoModes {
		return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "terminal." + operation, Target: runtime.GOOS + "/" + runtime.GOARCH}
	}
	return &rt.IOError{Tag: rt.TagIOOther, Operation: operation, Path: rt.StdinPath, Code: rt.ErrnoName(err)}
}
