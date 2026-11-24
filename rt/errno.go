package rt

import (
	"errors"
	"strconv"
	"syscall"
)

// ErrnoName is the operating system's name for the error code err carries
// (`EIO`, `ELOOP`), which `io.Error`'s `Other` keeps in place of Go's prose.
// An error with no code, or a code this table does not name, answers
// `errno <n>` or `unknown`. The names are POSIX's; on Windows, whose codes
// Go maps onto these constants only in part, an unmapped code answers its
// number.
func ErrnoName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "unknown"
	}
	if name, ok := errnoNames[errno]; ok {
		return name
	}
	return "errno " + strconv.FormatUint(uint64(errno), 10)
}

// errnoNames names each code. Where two names share a code (AIX's EEXIST
// and ENOTEMPTY are both 17), the first listed names it.
var errnoNames = func() map[syscall.Errno]string {
	m := map[syscall.Errno]string{}
	for _, e := range []struct {
		code syscall.Errno
		name string
	}{
		{syscall.EACCES, "EACCES"},
		{syscall.EAGAIN, "EAGAIN"},
		{syscall.EBADF, "EBADF"},
		{syscall.EBUSY, "EBUSY"},
		{syscall.EEXIST, "EEXIST"},
		{syscall.EFBIG, "EFBIG"},
		{syscall.EINTR, "EINTR"},
		{syscall.EINVAL, "EINVAL"},
		{syscall.EIO, "EIO"},
		{syscall.EISDIR, "EISDIR"},
		{syscall.ELOOP, "ELOOP"},
		{syscall.EMFILE, "EMFILE"},
		{syscall.EMLINK, "EMLINK"},
		{syscall.ENAMETOOLONG, "ENAMETOOLONG"},
		{syscall.ENFILE, "ENFILE"},
		{syscall.ENOENT, "ENOENT"},
		{syscall.ENOSPC, "ENOSPC"},
		{syscall.ENOTDIR, "ENOTDIR"},
		{syscall.ENOTEMPTY, "ENOTEMPTY"},
		{syscall.ENOTSUP, "ENOTSUP"},
		{syscall.ENXIO, "ENXIO"},
		{syscall.EPERM, "EPERM"},
		{syscall.EPIPE, "EPIPE"},
		{syscall.EROFS, "EROFS"},
		{syscall.ESRCH, "ESRCH"},
		{syscall.ETIMEDOUT, "ETIMEDOUT"},
		{syscall.EXDEV, "EXDEV"},
	} {
		if _, ok := m[e.code]; !ok {
			m[e.code] = e.name
		}
	}
	return m
}()
