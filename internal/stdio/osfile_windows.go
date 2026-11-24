package stdio

import (
	"math"
	"os"
	"syscall"
	"unsafe"

	"github.com/nomi-language/nomi/rt"
)

// osFile is how a File over f syncs and locks on Windows. Sync is
// FlushFileBuffers for the data and the metadata alike. Windows refuses it
// on a handle that cannot write, and a handle opened only to read has
// written nothing to flush, so there it does nothing.
//
// A lock is LockFileEx's, which belongs to the handle, and Windows releases
// a handle's locks when it closes. It differs from Linux's and macOS's in
// three ways: it is mandatory, so another handle's read or write of bytes
// locked exclusively fails; a lock on bytes the handle already holds is a
// second lock rather than a replacement, so a handle changes a lock by
// unlocking it first; and an unlock names a range exactly as it was locked.
func osFile(f *os.File, writable bool) (sync func(bool) error, lock func(rt.LockMode, int64, int64) (bool, error), release func()) {
	sync = func(bool) error {
		if !writable {
			return nil
		}
		return f.Sync()
	}
	lock = func(mode rt.LockMode, start, length int64) (bool, error) { return windowsLock(f, mode, start, length) }
	return sync, lock, func() {}
}

// kernel32's procedures this package calls, through the syscall package
// rather than golang.org/x/sys/windows, which imports net: the lean runner
// links this package and none of net (cmd/nomi-vm's
// TestLeanVMImportsNoFamilyCode).
var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx                = kernel32.NewProc("LockFileEx")
	procUnlockFileEx              = kernel32.NewProc("UnlockFileEx")
	procGetFinalPathNameByHandleW = kernel32.NewProc("GetFinalPathNameByHandleW")
	procGetVolumePathNameW        = kernel32.NewProc("GetVolumePathNameW")
	procGetDiskFreeSpaceExW       = kernel32.NewProc("GetDiskFreeSpaceExW")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2

	errorLockViolation syscall.Errno = 33
	errorNotLocked     syscall.Errno = 158
)

func windowsLock(f *os.File, mode rt.LockMode, start, length int64) (bool, error) {
	n := uint64(length)
	if length == 0 {
		n = math.MaxUint64 - uint64(start)
	}
	ol := syscall.Overlapped{Offset: uint32(start), OffsetHigh: uint32(uint64(start) >> 32)}
	var failed error
	rc, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	if err := rc.Control(func(h uintptr) {
		if mode == rt.LockNone {
			r, _, e := procUnlockFileEx.Call(h, 0, uintptr(uint32(n)), uintptr(uint32(n>>32)), uintptr(unsafe.Pointer(&ol)))
			if r == 0 && e != errorNotLocked {
				failed = e
			}
			return
		}
		flags := uintptr(lockfileFailImmediately)
		if mode == rt.LockExclusive {
			flags |= lockfileExclusiveLock
		}
		r, _, e := procLockFileEx.Call(h, flags, 0, uintptr(uint32(n)), uintptr(uint32(n>>32)), uintptr(unsafe.Pointer(&ol)))
		if r == 0 {
			failed = e
		}
	}); err != nil {
		return false, err
	}
	if failed == errorLockViolation || failed == syscall.ERROR_IO_PENDING {
		return false, nil
	}
	return failed == nil, failed
}

// syncDirectory does nothing: Windows has no call that flushes a directory's
// entries (FlushFileBuffers refuses a directory handle), and its file
// systems write a directory's changes to their own journal.
func syncDirectory(*os.File) error { return nil }
