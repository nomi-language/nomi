//go:build windows

package stdterminal

import (
	"syscall"
	"unsafe"
)

// kernel32's procedures this package calls that the syscall package does
// not wrap. They go through syscall rather than golang.org/x/sys/windows,
// which imports net: the lean runner links this package and none of net
// (cmd/nomi-vm's TestLeanVMImportsNoFamilyCode).
var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

// The console input mode's flags raw mode and a password read change.
const (
	enableProcessedInput       = 0x1
	enableLineInput            = 0x2
	enableEchoInput            = 0x4
	enableVirtualTerminalInput = 0x200
)

// state is a console input handle's mode.
type state = uint32

// errNoModes is never returned here: this build has terminal modes.
var errNoModes error

func getState(fd uintptr) (state, error) {
	var mode uint32
	err := syscall.GetConsoleMode(syscall.Handle(fd), &mode)
	return mode, err
}

func setState(fd uintptr, s state) error {
	if r, _, err := procSetConsoleMode.Call(fd, uintptr(s)); r == 0 {
		return err
	}
	return nil
}

// isTerminal is whether fd is a console: whether it has a console mode.
func isTerminal(fd uintptr) bool {
	_, err := getState(fd)
	return err == nil
}

// size is the columns and rows of the console window fd's screen buffer
// shows.
func size(fd uintptr) (columns, rows int, err error) {
	var info struct {
		size, cursor             [2]int16
		attributes               uint16
		left, top, right, bottom int16
		maximum                  [2]int16
	}
	if r, _, err := procGetConsoleScreenBufferInfo.Call(fd, uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, err
	}
	return int(info.right - info.left + 1), int(info.bottom - info.top + 1), nil
}

// rawState is s with echo and line input off and virtual terminal input
// on, so keys arrive as the escape sequences a Unix terminal sends.
// keepSignals leaves processed input on, so Ctrl-C still interrupts. The
// console's output mode belongs to its output handle, which raw mode does
// not change, and a console moves "\n" to the next line's start either
// way, so keepOutput changes nothing here.
func rawState(s state, keepSignals, _ bool) state {
	s &^= enableEchoInput | enableLineInput
	if !keepSignals {
		s &^= enableProcessedInput
	}
	return s | enableVirtualTerminalInput
}

// noEchoState is s reading lines with echo off, as a password prompt
// reads: line input and Ctrl-C stay on.
func noEchoState(s state) state {
	s &^= enableEchoInput
	return s | enableProcessedInput | enableLineInput
}
