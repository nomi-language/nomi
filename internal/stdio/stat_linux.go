package stdio

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// ownerTarget and diskUsageTarget are empty: Linux has numeric owners and
// statfs.
const ownerTarget, diskUsageTarget = "", ""

// sysInfo adds to out what Linux's stat record holds: the access and
// status change times, the link count, the device and inode, and the
// owner. The birth time is statx's alone, which Go's syscall package does
// not call, so it is left out.
func sysInfo(out *Info, info fs.FileInfo, _ func() (*os.File, error)) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	out.Accessed = time.Unix(st.Atim.Unix())
	out.Changed = time.Unix(st.Ctim.Unix())
	out.Links = uint64(st.Nlink)
	out.ID = &FileID{Device: uint64(st.Dev), Inode: uint64(st.Ino)}
	out.Owner = &Owner{User: uint64(st.Uid), Group: uint64(st.Gid)}
}

// blockSize is the unit statfs counts blocks in: the fragment size, or the
// block size on a kernel that reports none.
func blockSize(st *syscall.Statfs_t) uint64 {
	if st.Frsize > 0 {
		return uint64(st.Frsize)
	}
	return uint64(st.Bsize)
}
