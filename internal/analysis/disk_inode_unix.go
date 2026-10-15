//go:build unix

package analysis

import (
	"os"
	"syscall"
)

// inodeOf is info's inode number.
func inodeOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
