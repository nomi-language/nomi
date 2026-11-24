package main

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ptyGetTermios = unix.TIOCGETA
	ptySetTermios = unix.TIOCSETA
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
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("grant: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("unlock: %w", err)
	}
	var name [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		master.Close()
		return nil, "", fmt.Errorf("name: %w", errno)
	}
	return master, string(bytes.TrimRight(name[:], "\x00")), nil
}
