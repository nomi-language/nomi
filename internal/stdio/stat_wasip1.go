package stdio

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// ownerTarget and diskUsageTarget name wasip1: WASI has no owners and no
// statfs.
const ownerTarget, diskUsageTarget = "wasip1/wasm", "wasip1/wasm"

// sysInfo adds to out what WASI's filestat holds: the access and status
// change times, the link count, and the device and inode. WASI keeps no
// birth time and no owner.
func sysInfo(out *Info, info fs.FileInfo, _ func() (*os.File, error)) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	out.Accessed = time.Unix(0, int64(st.Atime))
	out.Changed = time.Unix(0, int64(st.Ctime))
	out.Links = st.Nlink
	out.ID = &FileID{Device: st.Dev, Inode: st.Ino}
}

func diskUsage(*os.File) (DiskUsage, error) { return DiskUsage{}, syscall.ENOSYS }
