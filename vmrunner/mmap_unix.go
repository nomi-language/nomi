//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package vmrunner

import (
	"errors"
	"os"
	"syscall"
)

// mapRange maps n bytes of f at off read-only, and answers them and how to
// unmap them. The mapping starts at the page holding off.
func mapRange(f *os.File, off, n int64) ([]byte, func() error, error) {
	page := int64(os.Getpagesize())
	start := off - off%page
	length := n + (off - start)
	if n <= 0 || int64(int(length)) != length {
		return nil, nil, errors.New("a range this platform cannot map")
	}
	data, err := syscall.Mmap(int(f.Fd()), start, int(length), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return data[off-start:], func() error { return syscall.Munmap(data) }, nil
}
