//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package stdterminal

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
