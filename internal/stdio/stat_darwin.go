package stdio

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// ownerTarget and diskUsageTarget are empty: macOS has numeric owners and
// statfs.
const ownerTarget, diskUsageTarget = "", ""

// sysInfo adds to out what macOS's stat record holds: the access, status
// change and birth times, the link count, the device and inode, and the
// owner.
func sysInfo(out *Info, info fs.FileInfo, _ func() (*os.File, error)) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	out.Accessed = time.Unix(st.Atimespec.Unix())
	out.Changed = time.Unix(st.Ctimespec.Unix())
	if st.Birthtimespec.Sec != 0 || st.Birthtimespec.Nsec != 0 {
		out.Created = time.Unix(st.Birthtimespec.Unix())
	}
	out.Links = uint64(st.Nlink)
	// dev_t is an int32 on macOS; its bits are the device's number.
	out.ID = &FileID{Device: uint64(uint32(st.Dev)), Inode: st.Ino}
	out.Owner = &Owner{User: uint64(st.Uid), Group: uint64(st.Gid)}
}

// blockSize is the unit statfs counts blocks in.
func blockSize(st *syscall.Statfs_t) uint64 { return uint64(st.Bsize) }
