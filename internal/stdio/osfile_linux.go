package stdio

import (
	"os"
	"syscall"
)

// ofdSetLock is Linux's F_OFD_SETLK, which package syscall does not name.
const ofdSetLock = 37

// syncData is fdatasync: the data, and the size when it changed, without
// the times.
func syncData(f *os.File) error {
	var failed error
	if err := control(f, func(fd uintptr) {
		failed = retryInterrupted(func() error { return syscall.Fdatasync(int(fd)) })
	}); err != nil {
		return err
	}
	return failed
}

// syncDirectory is fsync of the directory: its entries are durable.
func syncDirectory(f *os.File) error { return f.Sync() }
