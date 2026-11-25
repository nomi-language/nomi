package rt

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

// IOErrorOf is the io.Error a program sees for err, a failure of operation
// op on path: a file's, a standard stream's, a process's stream's, a
// socket's or any other stream's. An IOFailure is the io.Error it carries.
//
// It is the one table that turns a Go error into an io.Error. A stream
// with no Fail hook fails through it (ReadSource), and so does standard
// input. A package that links the package an error comes from names that
// error first and hands the rest here: internal/stdio names the error of a
// path that leaves its root, and internal/stdhttp the errors of net/http,
// net and crypto/tls, which rt does not link.
//
// The errno decides first, since Go's fs.ErrExist also matches ENOTEMPTY,
// and it is the one Unix answers for the failure on every system (Errno).
// A connection reset or aborted under a read or write is Reset. Then the
// sentinels of io/fs, io, os and context a stream returns: a closed file
// or pipe, or a cancelled wait, is Closed, a stream that ends inside its
// framing (io.ErrUnexpectedEOF) Truncated, and any timeout that is not an
// errno's TimedOut. What no variant names is Other, with Go's operation word
// (`open`, `read`) and the errno's name, or `unknown` when the error
// carries none, never Go's prose.
func IOErrorOf(op, path string, err error) IOError {
	var f IOFailure
	if errors.As(err, &f) {
		return f.E
	}
	tag := func(t uint8) IOError { return IOError{Tag: t, Path: path} }
	errno := Errno(err)
	switch errno {
	case syscall.EISDIR:
		return tag(TagIOIsADirectory)
	case syscall.ENOTDIR:
		return tag(TagIONotADirectory)
	case syscall.ENOTEMPTY:
		return tag(TagIODirectoryNotEmpty)
	case syscall.ENOSPC:
		return tag(TagIONoSpace)
	case syscall.EROFS:
		return tag(TagIOReadOnly)
	case syscall.EMFILE, syscall.ENFILE:
		return IOError{Tag: TagIOTooManyOpen}
	case syscall.EPIPE:
		return IOError{Tag: TagIOBrokenPipe}
	case syscall.ECONNRESET, syscall.ECONNABORTED:
		// The connection ended under the read or write, as net.Error's
		// Reset groups the two.
		return tag(TagIOReset)
	case syscall.ETIMEDOUT:
		return IOError{Tag: TagIOTimedOut}
	}
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return tag(TagIONotFound)
	case errors.Is(err, fs.ErrExist):
		return tag(TagIOAlreadyExists)
	case errors.Is(err, fs.ErrPermission):
		return tag(TagIOPermissionDenied)
	case errors.Is(err, fs.ErrClosed), errors.Is(err, io.ErrClosedPipe), errors.Is(err, context.Canceled):
		// A context cancelled under a wait is the task waiting being
		// stopped, which closes what it waits on, as net.Error's Closed
		// says.
		return IOError{Tag: TagIOClosed}
	case errors.Is(err, io.ErrUnexpectedEOF):
		// A stream that ended inside what its framing promised, as a
		// request body shorter than its Content-Length.
		return tag(TagIOTruncated)
	case errors.Is(err, os.ErrDeadlineExceeded), errno == 0 && errors.As(err, &timeout) && timeout.Timeout():
		// A read or write past a deadline: one the stream set (a
		// connection's `set_read_deadline`, a pipe's), a server's read
		// or write timeout, or a context's. An errno that Go calls a
		// timeout (EAGAIN) is the errno's, below.
		return IOError{Tag: TagIOTimedOut}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Op != "" {
		op = pe.Op
	}
	var le *os.LinkError
	if errors.As(err, &le) && le.Op != "" {
		op = le.Op
	}
	code := ErrnoName(err)
	if errno != 0 {
		code = ErrnoName(errno)
	}
	return IOError{Tag: TagIOOther, Operation: op, Path: path, Code: code}
}

// Errno is the errno err carries, as a Unix system numbers the failure, on
// every system: a Windows code is read through windowsErrnos. It is 0 when
// err carries none. A file's io.Error and a connection's net.Error are both
// sorted by it, so a failure is the same variant on every system.
func Errno(err error) syscall.Errno {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return 0
	}
	if windowsCodes {
		return fromWindows(errno)
	}
	return errno
}

// IsNotExist reports whether err says nothing is at a path: Go's
// fs.ErrNotExist, which on Windows matches both of the codes for absence.
func IsNotExist(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var errno syscall.Errno
	return windowsCodes && errors.As(err, &errno) && (errno == 2 || errno == 3)
}

// windowsCodes is whether a syscall.Errno is a Windows error code, which
// fromWindows reads as the errno a Unix system answers for the same
// failure. A test sets it to read Windows codes on another system.
var windowsCodes = runtime.GOOS == "windows"

// ReadWindowsCodesForTest makes Errno and IsNotExist read each code as
// Windows numbers it, on any system, until the returned function restores
// the setting. A test of a package that sorts errors by Errno feeds it
// Windows codes with it; nothing else calls it.
func ReadWindowsCodesForTest() (restore func()) {
	was := windowsCodes
	windowsCodes = true
	return func() { windowsCodes = was }
}

// windowsErrnos are the Windows error codes whose failure Unix names with
// an errno, by number, so the table means the same on every system. Go's
// syscall package on Windows names a few of them itself (ENOENT is
// ERROR_FILE_NOT_FOUND, ENOTDIR is ERROR_PATH_NOT_FOUND). It defines the
// other Unix names as numbers of its own that no Windows call returns, so
// syscall.ECONNREFUSED never equals the WSAECONNREFUSED a refused dial
// carries; errors.Is reads ERROR_DIR_NOT_EMPTY as fs.ErrExist. The
// Winsock codes are what Go's net package wraps in a *net.OpError for a
// socket's failure.
var windowsErrnos = map[syscall.Errno]syscall.Errno{
	2:     syscall.ENOENT,       // ERROR_FILE_NOT_FOUND
	3:     syscall.ENOTDIR,      // ERROR_PATH_NOT_FOUND: a missing directory above, or a file there
	109:   syscall.EPIPE,        // ERROR_BROKEN_PIPE
	123:   syscall.EINVAL,       // ERROR_INVALID_NAME
	145:   syscall.ENOTEMPTY,    // ERROR_DIR_NOT_EMPTY
	232:   syscall.EPIPE,        // ERROR_NO_DATA: a write to a pipe its reader closed
	267:   syscall.ENOTDIR,      // ERROR_DIRECTORY: a file opened as a directory
	1921:  syscall.ELOOP,        // ERROR_CANT_RESOLVE_FILENAME: a symlink loop
	4390:  syscall.EINVAL,       // ERROR_NOT_A_REPARSE_POINT: readlink of what is not a link
	10040: syscall.EMSGSIZE,     // WSAEMSGSIZE: a datagram longer than the receive's buffer
	10048: syscall.EADDRINUSE,   // WSAEADDRINUSE
	10051: syscall.ENETUNREACH,  // WSAENETUNREACH
	10053: syscall.ECONNABORTED, // WSAECONNABORTED: this host ended an established connection
	10054: syscall.ECONNRESET,   // WSAECONNRESET
	10058: syscall.EPIPE,        // WSAESHUTDOWN: a send after the socket's sending side shut down
	10060: syscall.ETIMEDOUT,    // WSAETIMEDOUT
	10061: syscall.ECONNREFUSED, // WSAECONNREFUSED
	10065: syscall.EHOSTUNREACH, // WSAEHOSTUNREACH
}

// fromWindows is the errno a Unix system answers for the failure Windows
// reports as code.
func fromWindows(code syscall.Errno) syscall.Errno {
	if errno, ok := windowsErrnos[code]; ok {
		return errno
	}
	return code
}
