package stdio

import (
	"os"
	"syscall"

	"github.com/nomi-language/nomi/rt"
)

// osFile is how a File over f syncs and locks in a wasip1 module. Sync is
// WASI's fd_sync for the data and the metadata alike. WASI has no locks,
// so a lock is one the module keeps itself (locks.go), on the file's device
// and inode: it excludes the module's own handles, and nothing outside the
// module sees it.
func osFile(f *os.File, _ bool) (sync func(bool) error, lock func(rt.LockMode, int64, int64) (bool, error), release func()) {
	sync = func(bool) error { return f.Sync() }
	var file any = f
	if info, err := f.Stat(); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			file = wasiFile{dev: st.Dev, ino: st.Ino}
		}
	}
	lock, release = inProcessLocker(file)
	return sync, lock, release
}

// wasiFile is a file's identity on a WASI host.
type wasiFile struct{ dev, ino uint64 }

// syncDirectory is fd_sync on the directory.
func syncDirectory(f *os.File) error { return f.Sync() }
