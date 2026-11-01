package stdio

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"

	"github.com/nomi-language/nomi/rt"
)

// pathEscapes is the text of the error an *os.Root answers for a name that
// leaves its directory. Go does not export the error, so it is matched by
// its text; TestWithinRefusesEscapes holds the match to Go's behaviour.
const pathEscapes = "path escapes from parent"

// Classify is the io.Error a program sees for err, a failure of operation
// op on path, from any Go file system: the operating system's, or one an
// embedder implements (vmhost.FileSystem). An rt.IOFailure is the io.Error
// it carries.
func Classify(op, path string, err error) rt.IOError {
	var f rt.IOFailure
	if errors.As(err, &f) {
		return f.E
	}
	return *classify(op, path, err)
}

// classify is the io.Error for err, a failure of operation op on path. The
// errno decides first, since Go's fs.ErrExist also matches ENOTEMPTY.
func classify(op, path string, err error) *rt.IOError {
	tag := func(t uint8) *rt.IOError { return errOf(t, path) }
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if windowsCodes {
			errno = fromWindows(errno)
		}
		switch errno {
		case syscall.EISDIR:
			return tag(rt.TagIOIsADirectory)
		case syscall.ENOTDIR:
			return tag(rt.TagIONotADirectory)
		case syscall.ENOTEMPTY:
			return tag(rt.TagIODirectoryNotEmpty)
		case syscall.ENOSPC:
			return tag(rt.TagIONoSpace)
		case syscall.EROFS:
			return tag(rt.TagIOReadOnly)
		case syscall.EMFILE, syscall.ENFILE:
			return &rt.IOError{Tag: rt.TagIOTooManyOpen}
		case syscall.EPIPE:
			return &rt.IOError{Tag: rt.TagIOBrokenPipe}
		case syscall.ETIMEDOUT:
			return &rt.IOError{Tag: rt.TagIOTimedOut}
		}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == pathEscapes {
		return escapes(path)
	}
	var le *os.LinkError
	if errors.As(err, &le) && le.Err != nil && le.Err.Error() == pathEscapes {
		return escapes(path)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return tag(rt.TagIONotFound)
	case errors.Is(err, fs.ErrExist):
		return tag(rt.TagIOAlreadyExists)
	case errors.Is(err, fs.ErrPermission):
		return tag(rt.TagIOPermissionDenied)
	case errors.Is(err, fs.ErrClosed):
		return &rt.IOError{Tag: rt.TagIOClosed}
	}
	if pe != nil && pe.Op != "" {
		op = pe.Op
	}
	if le != nil && le.Op != "" {
		op = le.Op
	}
	code := rt.ErrnoName(err)
	if errno != 0 {
		code = rt.ErrnoName(errno)
	}
	return &rt.IOError{Tag: rt.TagIOOther, Operation: op, Path: path, Code: code}
}

// windowsCodes is whether a syscall.Errno is a Windows error code, which
// fromWindows reads as the errno a Unix system answers for the same
// failure. A test sets it to read Windows codes on another system.
var windowsCodes = runtime.GOOS == "windows"

// windowsErrnos are the Windows error codes whose failure Unix names with
// another errno, by number, so the table means the same on every system.
// Go's syscall package on Windows names a few of them itself (ENOENT is
// ERROR_FILE_NOT_FOUND, ENOTDIR is ERROR_PATH_NOT_FOUND); the others it
// leaves as codes no case below matches, and errors.Is reads
// ERROR_DIR_NOT_EMPTY as fs.ErrExist.
var windowsErrnos = map[syscall.Errno]syscall.Errno{
	2:    syscall.ENOENT,    // ERROR_FILE_NOT_FOUND
	3:    syscall.ENOTDIR,   // ERROR_PATH_NOT_FOUND: a missing directory above, or a file there
	123:  syscall.EINVAL,    // ERROR_INVALID_NAME
	145:  syscall.ENOTEMPTY, // ERROR_DIR_NOT_EMPTY
	267:  syscall.ENOTDIR,   // ERROR_DIRECTORY: a file opened as a directory
	1921: syscall.ELOOP,     // ERROR_CANT_RESOLVE_FILENAME: a symlink loop
	4390: syscall.EINVAL,    // ERROR_NOT_A_REPARSE_POINT: readlink of what is not a link
}

// notExist reports whether err says nothing is at a path: Go's
// fs.ErrNotExist, which on Windows matches both of the codes for absence.
func notExist(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var errno syscall.Errno
	return windowsCodes && errors.As(err, &errno) && (errno == 2 || errno == 3)
}

// fromWindows is the errno a Unix system answers for the failure Windows
// reports as code.
func fromWindows(code syscall.Errno) syscall.Errno {
	if errno, ok := windowsErrnos[code]; ok {
		return errno
	}
	return code
}
