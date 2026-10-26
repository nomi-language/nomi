package stdio

import (
	"errors"
	"io/fs"
	"os"
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
	return &rt.IOError{Tag: rt.TagIOOther, Operation: op, Path: path, Code: rt.ErrnoName(err)}
}
