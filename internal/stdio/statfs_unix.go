//go:build darwin || linux

package stdio

import (
	"os"
	"syscall"
)

// diskUsage is fstatfs of f's descriptor: the blocks of its file system,
// those free, and those free to a user who is not root, each times the
// block size.
func diskUsage(f *os.File) (DiskUsage, error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return DiskUsage{}, err
	}
	var st syscall.Statfs_t
	var statErr error
	if err := conn.Control(func(fd uintptr) { statErr = syscall.Fstatfs(int(fd), &st) }); err != nil {
		return DiskUsage{}, err
	}
	if statErr != nil {
		return DiskUsage{}, statErr
	}
	size := blockSize(&st)
	return DiskUsage{Total: uint64(st.Blocks) * size, Free: uint64(st.Bfree) * size, Available: uint64(st.Bavail) * size}, nil
}
