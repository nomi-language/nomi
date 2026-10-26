// Package stdio is std/fs's file system host implementation, and it is
// deliberately not in rt.
//
// rt is linked by everything that runs Nomi, and it makes no file system
// call. It holds the io.Error value (rt/ioerror.go), the stream handles
// (rt/streams.go) and a slot on each frame for the file system a call
// reaches (rt/files.go), which is opaque to it. This package fills the
// slot: a Backend is a file system, and every std/fs crossing (fs.go) asks
// the frame for its backend and calls it.
//
// # Backends
//
//   - The operating system's (osfs.go), over Go's os package. It is the
//     default of every native build and of wasip1, where WASI's preopened
//     directories decide what it reaches.
//   - Memory (memfs.go): a map-backed file system with handles. It is the
//     default of a js/wasm build (the browser, Go's Node loader, the tour),
//     where Go's os calls answer ENOSYS, and it is the sandbox
//     `fs_testing.run` installs. One implementation serves both, so the
//     browser and the tests cannot drift; fs_testing's conformance suite
//     holds it to the operating system's.
//
// A machine's default is the host's (rt.WithFiles; vmhost.WithFileSystem),
// else this build's (defaultBackend). `fs_testing.run` replaces it for one
// call (rt.EnterFiles).
//
// # Paths
//
// A Nomi path is a String that uses `/` on every operating system
// (ospath.go). Every error names the path as the program passed it.
//
// # Classification
//
// The operating system backend turns a Go error into the io.Error variant
// that names it (classify): absence is NotFound, an existing path
// AlreadyExists, a refusal PermissionDenied, and so on by errno, each
// carrying the path the program passed. What no variant names is `Other`,
// with Go's operation word (`open`, `read`) and the errno's name, never Go's
// prose. A write into a missing directory is NotFound naming the directory.
// The memory backend answers the same variants itself.
package stdio

import (
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// WriteMode is std/fs's `WriteMode`, as its crossings carry it: the
// variant's position in the declaration.
type WriteMode int64

const (
	// Replace creates the file or truncates it.
	Replace WriteMode = iota
	// ReplaceAtomically writes a temporary file beside the path and renames
	// it over the path when the write is closed.
	ReplaceAtomically
	// Append creates the file when it is missing and writes at its end.
	Append
	// New fails with AlreadyExists when something is at the path.
	New
)

// Kind is std/fs's `Kind`, as a listing and a metadata crossing carry it.
type Kind int64

const (
	KindFile Kind = iota
	KindDirectory
	KindSymlink
	KindOther
)

// kindLetter is the letter a listing crossing puts before an entry's name:
// std/fs reads it back into a Kind.
var kindLetter = map[Kind]string{KindFile: "f", KindDirectory: "d", KindSymlink: "l", KindOther: "o"}

// DirEntry is one entry of a directory listing.
type DirEntry struct {
	Name string
	Kind Kind
}

// Info is what a metadata crossing answers.
type Info struct {
	Kind Kind
	Size int64
	// Modified is nanoseconds since the Unix epoch.
	Modified int64
	Mode     int64
}

// Access is std/fs's `Access`, as `fs.open_file`'s crossing carries it:
// the variant's position in the declaration.
type Access int64

const (
	// AccessRead opens an existing file to read it.
	AccessRead Access = iota
	// AccessReadWrite opens an existing file to read and write it.
	AccessReadWrite
	// AccessCreate opens a file to read and write it, making it empty when
	// it is missing. An existing file keeps its contents.
	AccessCreate
)

// Backend is a file system. Every path it is handed is valid (validPath),
// and every error it answers is the io.Error the program sees.
type Backend interface {
	// Open opens path to read it. A directory is IsADirectory.
	Open(path string) (rt.ReadSource, *rt.IOError)
	// Create opens path to write it in mode.
	Create(path string, mode WriteMode) (rt.WriteSink, *rt.IOError)
	// OpenFile opens path for reads and writes at a position, as access
	// says. A directory is IsADirectory.
	OpenFile(path string, access Access) (rt.FileSource, *rt.IOError)
	// List is dir's entries, sorted by name. A symlink is listed as one,
	// whatever it points to.
	List(dir string) ([]DirEntry, *rt.IOError)
	CreateDir(path string, parents bool) *rt.IOError
	// Remove removes a file, a symlink (not what it points to) or an empty
	// directory.
	Remove(path string) *rt.IOError
	// RemoveAll removes path and everything under it, following no symlink;
	// a missing path is not an error.
	RemoveAll(path string) *rt.IOError
	// Rename moves from to to; a symlink at from is moved, not what it
	// points to.
	Rename(from, to string) *rt.IOError
	// Stat follows symlinks.
	Stat(path string) (Info, *rt.IOError)
	// Lstat describes a symlink at path as itself.
	Lstat(path string) (Info, *rt.IOError)
	// Symlink makes a symlink at path whose target is target, as written.
	Symlink(path, target string) *rt.IOError
	// ReadLink is the target of the symlink at path, as it was written.
	ReadLink(path string) (string, *rt.IOError)
	// Chmod sets the permission bits of what path names, following
	// symlinks.
	Chmod(path string, mode int64) *rt.IOError
	// Within is a file system that reaches only what is under dir, and the
	// function that releases it. A relative path is under dir, an absolute
	// one must be, and a path that leaves it, by `..` or by a symlink, is
	// InvalidPath (escapes).
	Within(dir string) (Backend, func(), *rt.IOError)
	// TempDir is where temporary directories are made.
	TempDir() string
	// MakeTempDir makes a fresh directory under TempDir.
	MakeTempDir() (string, *rt.IOError)
	CurrentDir() (string, *rt.IOError)
}

// backendOf is the file system fr reaches.
func backendOf(fr *rt.Frame) Backend {
	if b, ok := rt.FilesOf(fr).(Backend); ok {
		return b
	}
	return defaultBackend()
}

// validPath is the InvalidPath error for a path no file can have.
func validPath(path string) *rt.IOError {
	switch {
	case path == "":
		return &rt.IOError{Tag: rt.TagIOInvalidPath, Path: path, Reason: "the path is empty"}
	case strings.IndexByte(path, 0) >= 0:
		return &rt.IOError{Tag: rt.TagIOInvalidPath, Path: path, Reason: "the path holds a NUL byte"}
	}
	return nil
}

func errOf(tag uint8, path string) *rt.IOError { return &rt.IOError{Tag: tag, Path: path} }

// escapeReason is the InvalidPath reason for a path that leaves the
// directory `fs.within` confines a call to.
const escapeReason = "the path leads outside the directory fs.within confines calls to"

// escapes is the InvalidPath error for such a path.
func escapes(path string) *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOInvalidPath, Path: path, Reason: escapeReason}
}

// withinTempDir is the error a temporary directory gets inside `fs.within`,
// whose confinement it would leave.
func withinTempDir() *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.with_temp_dir", Target: "fs.within"}
}

// loopLimit is how many symlinks one path may pass through before it is
// ELOOP, as Linux's limit is.
const loopLimit = 40

// loop is the error for a path that passes through more symlinks than
// loopLimit, as the operating system reports it.
func loop(op, path string) *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOOther, Operation: op, Path: path, Code: "ELOOP"}
}
