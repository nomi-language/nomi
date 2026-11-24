//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package stdterminal

import "golang.org/x/sys/unix"

// state is a terminal's termios.
type state = unix.Termios

// errNoModes is never returned here: this build has terminal modes.
var errNoModes error

func getState(fd uintptr) (state, error) {
	t, err := unix.IoctlGetTermios(int(fd), ioctlGetTermios)
	if err != nil {
		return state{}, err
	}
	return *t, nil
}

func setState(fd uintptr, s state) error {
	return unix.IoctlSetTermios(int(fd), ioctlSetTermios, &s)
}

// rawState is s in raw mode, as cfmakeraw makes it: no echo, no line
// buffering, no input translation, 8-bit characters, a read answered by
// one byte. keepSignals leaves ISIG on, so Ctrl-C, Ctrl-Z and Ctrl-\ still
// send their signals; keepOutput leaves OPOST on, so "\n" still moves to
// the start of the next line.
func rawState(s state, keepSignals, keepOutput bool) state {
	s.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	if !keepOutput {
		s.Oflag &^= unix.OPOST
	}
	s.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
	if !keepSignals {
		s.Lflag &^= unix.ISIG
	}
	s.Cflag &^= unix.CSIZE | unix.PARENB
	s.Cflag |= unix.CS8
	s.Cc[unix.VMIN] = 1
	s.Cc[unix.VTIME] = 0
	return s
}

// noEchoState is s reading lines with echo off, as a password prompt
// reads: line editing and the signal keys stay on.
func noEchoState(s state) state {
	s.Lflag &^= unix.ECHO
	s.Lflag |= unix.ICANON | unix.ISIG
	s.Iflag |= unix.ICRNL
	return s
}
