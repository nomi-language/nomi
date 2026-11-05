//go:build darwin || freebsd || netbsd || openbsd

package progcache

import (
	"os"
	"syscall"
)

// changeTime is info's inode change time in nanoseconds and its inode
// number. Any write to a file moves its change time, and no tool sets it
// back, so an equal one means the file was not written.
func changeTime(info os.FileInfo) (int64, uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return st.Ctimespec.Nano(), uint64(st.Ino)
}
