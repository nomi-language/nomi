//go:build linux || darwin

package stdio

import (
	"io"
	"os"
	"syscall"

	"github.com/nomi-language/nomi/rt"
)

// osFile is how a File over f syncs and locks on Linux and macOS. A lock is
// an open file description lock (F_OFD_SETLK): it belongs to the open file,
// not to the process, so two handles in one program exclude each other as
// two programs do, a lock on bytes the handle holds replaces it, and the
// kernel releases the handle's locks when it closes. A plain POSIX record
// lock (F_SETLK) belongs to the process instead, and any close of the file
// by the process drops it, which is why it is not used.
func osFile(f *os.File, _ bool) (sync func(bool) error, lock func(rt.LockMode, int64, int64) (bool, error), release func()) {
	sync = func(dataOnly bool) error {
		if dataOnly {
			return syncData(f)
		}
		return f.Sync()
	}
	lock = func(mode rt.LockMode, start, length int64) (bool, error) { return ofdLock(f, mode, start, length) }
	return sync, lock, func() {}
}

func ofdLock(f *os.File, mode rt.LockMode, start, length int64) (bool, error) {
	kind := int16(syscall.F_UNLCK)
	switch mode {
	case rt.LockShared:
		kind = syscall.F_RDLCK
	case rt.LockExclusive:
		kind = syscall.F_WRLCK
	}
	var failed error
	if err := control(f, func(fd uintptr) {
		lk := syscall.Flock_t{Type: kind, Whence: io.SeekStart, Start: start, Len: length}
		failed = retryInterrupted(func() error { return syscall.FcntlFlock(fd, ofdSetLock, &lk) })
	}); err != nil {
		return false, err
	}
	if failed == syscall.EAGAIN || failed == syscall.EACCES {
		return false, nil
	}
	return failed == nil, failed
}

// control runs body on f's descriptor, which stays open while it runs.
func control(f *os.File, body func(fd uintptr)) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(body)
}

// retryInterrupted calls call again while a signal interrupts it.
func retryInterrupted(call func() error) error {
	for {
		if err := call(); err != syscall.EINTR {
			return err
		}
	}
}
