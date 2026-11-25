package stdio

import (
	"errors"
	"io/fs"
	"os"

	"github.com/nomi-language/nomi/rt"
)

// pathEscapes is the text of the error an *os.Root answers for a name that
// leaves its directory. Go does not export the error, so it is matched by
// its text; TestWithinRefusesEscapes holds the match to Go's behaviour.
const pathEscapes = "path escapes from parent"

// Classify is the io.Error a program sees for err, a failure of operation
// op on path, from any Go file system: the operating system's, or one an
// embedder implements (vmhost.FileSystem). A name that leaves the root it
// is confined to is InvalidPath; rt.IOErrorOf sorts the rest.
func Classify(op, path string, err error) rt.IOError {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == pathEscapes {
		return *escapes(path)
	}
	var le *os.LinkError
	if errors.As(err, &le) && le.Err != nil && le.Err.Error() == pathEscapes {
		return *escapes(path)
	}
	return rt.IOErrorOf(op, path, err)
}

// classify is Classify as the pointer the backends answer.
func classify(op, path string, err error) *rt.IOError {
	e := Classify(op, path, err)
	return &e
}
