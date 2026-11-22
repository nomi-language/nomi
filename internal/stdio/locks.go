package stdio

import (
	"math"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// The byte-range locks this process keeps itself, for files no operating
// system locks: the memory file system's, and a wasip1 module's disk, since
// WASI has no locks. They follow the rules the operating system's open file
// description locks follow (osfile_ofd.go), so a sandbox answers as the disk
// does on Linux and macOS:
//
//   - a lock belongs to one handle, its owner, and conflicts only with
//     another owner's: an exclusive lock with any lock, a shared lock with
//     an exclusive one;
//   - an owner's lock on bytes it already holds replaces what it held there,
//     so a shared lock becomes exclusive, or the reverse, and an unlock of
//     part of a range leaves the rest;
//   - an unlock of bytes the owner holds no lock on does nothing;
//   - closing the handle releases every lock it holds.

// lockOwner is one handle's identity in the table. It holds a byte because
// Go may give two values of a type of size zero one address.
type lockOwner struct{ _ byte }

// heldRange is one owner's lock on the bytes from start to end, end not
// included.
type heldRange struct {
	owner      *lockOwner
	start, end int64
	exclusive  bool
}

// inProcessLocks is every lock the process keeps itself, by the file it is
// on: a memory file's node, or a wasip1 file's device and inode.
var inProcessLocks = struct {
	mu    sync.Mutex
	files map[any][]heldRange
}{files: map[any][]heldRange{}}

// inProcessLocker is a handle's Lock on file, as rt.FileSource.Lock takes
// it, and the release its close calls.
func inProcessLocker(file any) (lock func(rt.LockMode, int64, int64) (bool, error), release func()) {
	owner := &lockOwner{}
	lock = func(mode rt.LockMode, start, length int64) (bool, error) {
		return setInProcessLock(file, owner, mode, start, length), nil
	}
	release = func() { setInProcessLock(file, owner, rt.LockNone, 0, 0) }
	return lock, release
}

// setInProcessLock makes owner's lock on length bytes of file from start (0:
// to past any end) mode, and reports whether it could.
func setInProcessLock(file any, owner *lockOwner, mode rt.LockMode, start, length int64) bool {
	end := int64(math.MaxInt64)
	if length > 0 {
		end = start + length
	}
	inProcessLocks.mu.Lock()
	defer inProcessLocks.mu.Unlock()
	held := inProcessLocks.files[file]
	if mode != rt.LockNone {
		for _, h := range held {
			if h.owner != owner && h.start < end && start < h.end && (mode == rt.LockExclusive || h.exclusive) {
				return false
			}
		}
	}
	var kept []heldRange
	for _, h := range held {
		if h.owner != owner || h.end <= start || end <= h.start {
			kept = append(kept, h)
			continue
		}
		if h.start < start {
			kept = append(kept, heldRange{owner: owner, start: h.start, end: start, exclusive: h.exclusive})
		}
		if end < h.end {
			kept = append(kept, heldRange{owner: owner, start: end, end: h.end, exclusive: h.exclusive})
		}
	}
	if mode != rt.LockNone {
		kept = append(kept, heldRange{owner: owner, start: start, end: end, exclusive: mode == rt.LockExclusive})
	}
	if len(kept) == 0 {
		delete(inProcessLocks.files, file)
	} else {
		inProcessLocks.files[file] = kept
	}
	return true
}
