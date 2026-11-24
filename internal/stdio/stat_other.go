//go:build !darwin && !linux && !windows && !wasip1 && !(js && wasm)

package stdio

import (
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

// ownerTarget and diskUsageTarget: on a system Nomi does not read the stat
// record of, owners are set as on Unix and disk usage is Unsupported.
const ownerTarget, diskUsageTarget = "", runtime.GOOS

// sysInfo adds nothing: the fields past Mode are None.
func sysInfo(*Info, fs.FileInfo, func() (*os.File, error)) {}

func diskUsage(*os.File) (DiskUsage, error) { return DiskUsage{}, syscall.ENOSYS }
