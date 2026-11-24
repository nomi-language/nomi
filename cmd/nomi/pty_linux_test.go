package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	ptyGetTermios = unix.TCGETS
	ptySetTermios = unix.TCSETS
)

// openPTY opens a pseudo-terminal's controlling side and answers it with
// the path of its terminal side, as posix_openpt, grantpt, unlockpt and
// ptsname do.
func openPTY() (*os.File, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("unlock: %w", err)
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, "", fmt.Errorf("number: %w", err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", n), nil
}
