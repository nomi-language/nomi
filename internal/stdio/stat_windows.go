package stdio

import (
	"io/fs"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// ownerTarget is the target set_owner names: Windows owns files by SIDs,
// not numbers. Its disk usage is GetDiskFreeSpaceEx's.
const ownerTarget, diskUsageTarget = "windows", ""

// sysInfo adds to out what Windows keeps: the access and creation times
// from the stat record, and, from GetFileInformationByHandle on the file
// opened again, the link count and the volume serial and file index as
// the device and inode. Windows keeps no status change time, and owns a
// file by a SID, which is no number. A symlink described as itself gets
// the times alone, since opening it opens its target.
func sysInfo(out *Info, info fs.FileInfo, open func() (*os.File, error)) {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		out.Accessed = time.Unix(0, d.LastAccessTime.Nanoseconds())
		out.Created = time.Unix(0, d.CreationTime.Nanoseconds())
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return
	}
	f, err := open()
	if err != nil {
		return
	}
	defer f.Close()
	var h syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &h) != nil {
		return
	}
	out.Links = uint64(h.NumberOfLinks)
	out.ID = &FileID{Device: uint64(h.VolumeSerialNumber), Inode: uint64(h.FileIndexHigh)<<32 | uint64(h.FileIndexLow)}
}

// diskUsage is GetDiskFreeSpaceEx of the volume f is on, found from its
// handle, so a path inside a root is never joined again: the volume's
// size, its free bytes, and those free to this user under any quota.
func diskUsage(f *os.File) (DiskUsage, error) {
	name := make([]uint16, syscall.MAX_LONG_PATH)
	n, _, err := procGetFinalPathNameByHandleW.Call(f.Fd(), uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)), 0)
	if n == 0 {
		return DiskUsage{}, err
	}
	if int(n) >= len(name) {
		return DiskUsage{}, syscall.ENAMETOOLONG
	}
	volume := make([]uint16, syscall.MAX_LONG_PATH)
	if r, _, err := procGetVolumePathNameW.Call(uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&volume[0])), uintptr(len(volume))); r == 0 {
		return DiskUsage{}, err
	}
	var available, total, free uint64
	if r, _, err := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(&volume[0])), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free))); r == 0 {
		return DiskUsage{}, err
	}
	return DiskUsage{Total: total, Free: free, Available: available}, nil
}
