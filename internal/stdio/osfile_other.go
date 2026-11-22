//go:build !linux && !darwin && !windows && !wasip1 && !(js && wasm)

package stdio

import (
	"os"
	"runtime"

	"github.com/nomi-language/nomi/rt"
)

// osFile is how a File over f syncs and locks on a system Nomi has no lock
// for: Sync is Go's, and a lock is Unsupported.
func osFile(f *os.File, _ bool) (sync func(bool) error, lock func(rt.LockMode, int64, int64) (bool, error), release func()) {
	sync = func(bool) error { return f.Sync() }
	lock = func(rt.LockMode, int64, int64) (bool, error) {
		return false, rt.IOFailure{E: rt.IOError{Tag: rt.TagIOUnsupported, Operation: "File.lock", Target: runtime.GOOS}}
	}
	return sync, lock, func() {}
}

// syncDirectory syncs a directory as a file.
func syncDirectory(f *os.File) error { return f.Sync() }
