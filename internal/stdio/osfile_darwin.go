package stdio

import (
	"os"
	"syscall"
)

// ofdSetLock is macOS's F_OFD_SETLK (<sys/fcntl.h>), which package syscall
// does not name.
const ofdSetLock = 90

// syncData is fsync(2), which hands the data and the size to the drive and
// does not ask it to write its own cache to the media: SQLite's ordinary
// sync on macOS. Go's Sync there, which File.sync is, is F_FULLFSYNC, which
// does.
func syncData(f *os.File) error {
	var failed error
	if err := control(f, func(fd uintptr) {
		failed = retryInterrupted(func() error { return syscall.Fsync(int(fd)) })
	}); err != nil {
		return err
	}
	return failed
}

// syncDirectory is fsync(2) of the directory, as SQLite's Unix layer syncs
// one: its entries reach the drive, which may still hold them in its own
// cache. F_FULLFSYNC, which File.sync is, asks the drive to write that cache
// to the media, and on a directory cost an SQLite commit about 4 ms of its
// 5; a directory gets no such call.
func syncDirectory(f *os.File) error { return syncData(f) }
