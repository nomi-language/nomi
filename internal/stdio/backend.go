package stdio

import (
	"io/fs"
	"strings"
	"time"

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

// Info is what a metadata crossing answers. The fields after Mode are what
// a file system may not keep: each is its zero value (nil, 0, the zero
// time) when it keeps none, and std/fs answers it as None.
type Info struct {
	Kind Kind
	Size int64
	// Modified is the modification time as the file system gives it. It
	// becomes an Instant only where it crosses to std/fs (infoList),
	// exactly, whatever its year (rt.InstantFromTime). So does each time
	// below.
	Modified time.Time
	// Mode is the permission bits with the setuid, setgid and sticky bits
	// as Unix numbers them (ModeBits).
	Mode int64
	// Accessed is the last read, Changed the last change to the contents
	// or the metadata (a status change), Created the birth.
	Accessed, Changed, Created time.Time
	// Links is how many names the file has.
	Links uint64
	// ID is the device and the file's number on it, which every name of
	// one file shares.
	ID *FileID
	// Owner is the numeric user and group that own the file.
	Owner *Owner
}

// FileID is a file's identity: its device and its number there.
type FileID struct{ Device, Inode uint64 }

// Owner is the numeric user and group that own a file.
type Owner struct{ User, Group uint64 }

// DiskUsage is what `fs.disk_usage` answers, in bytes: the size of the file
// system, what is free on it, and what of that this program's user may
// use.
type DiskUsage struct{ Total, Free, Available uint64 }

// ModeBits is m's permission bits, with its setuid, setgid and sticky bits
// as Unix numbers them (0o4000, 0o2000, 0o1000): what `Metadata.mode`
// holds.
func ModeBits(m fs.FileMode) int64 {
	bits := int64(m.Perm())
	if m&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// FileMode is the Go mode of bits as ModeBits writes them.
func FileMode(bits int64) fs.FileMode {
	m := fs.FileMode(bits) & fs.ModePerm
	if bits&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if bits&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if bits&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
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
	// says. A directory is IsADirectory. The source syncs the file and locks
	// ranges of it for its own handle.
	OpenFile(path string, access Access) (rt.FileSource, *rt.IOError)
	// SyncDir makes the entries of the directory dir durable, so a file
	// made, renamed or removed in it outlasts a crash. Something other than
	// a directory is NotADirectory.
	SyncDir(dir string) *rt.IOError
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
	// Chtimes sets the modification and access times of what path names,
	// following symlinks. A nil time is left as it is.
	Chtimes(path string, modified, accessed *time.Time) *rt.IOError
	// Link makes path a second name of the file at target (a hard link),
	// following no symlink at target. A directory at target is
	// PermissionDenied naming it.
	Link(path, target string) *rt.IOError
	// Chown sets the numeric owner of what path names, following a symlink
	// when follow is set.
	Chown(path string, user, group uint64, follow bool) *rt.IOError
	// DiskUsage is the size, free space and space available of the file
	// system path is on.
	DiskUsage(path string) (DiskUsage, *rt.IOError)
	// OwnerName is the name of the user, or the group when group is set,
	// whose number is id, when the system knows it.
	OwnerName(id uint64, group bool) (string, bool)
	// Within is a file system that reaches only what is under dir (a
	// `Dir`'s), and the function that releases it. A relative path is under dir, an absolute
	// one must be, and a path that leaves it, by `..` or by a symlink, is
	// InvalidPath (escapes).
	Within(dir string) (Backend, func(), *rt.IOError)
	// TempDir is where temporary directories are made.
	TempDir() string
	// MakeTempDir makes a fresh directory under TempDir.
	MakeTempDir() (string, *rt.IOError)
	CurrentDir() (string, *rt.IOError)
}

// Default is the file system a program reaches when its host gives it none:
// the operating system's natively and on wasip1, the process's in-memory
// one on js/wasm.
func Default() Backend { return defaultBackend() }

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
// directory a `Dir` holds, or `fs.within` confines a call to.
const escapeReason = "the path leads outside the directory it is confined to"

// escapes is the InvalidPath error for such a path.
func escapes(path string) *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOInvalidPath, Path: path, Reason: escapeReason}
}

// withinTempDir is the error a temporary directory gets inside `fs.within`,
// whose confinement it would leave.
func withinTempDir() *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.make_temp_dir", Target: "fs.within"}
}

// unsupported is the error for an operation the target the program runs
// on cannot do.
func unsupported(operation, target string) *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: operation, Target: target}
}

// noTempDirTarget is what a WASI module with no directory to make a
// temporary one in is, for noTempDir's sentence.
const noTempDirTarget = "wasip1/wasm with no preopened directory (TMPDIR, /tmp or the working directory)"

// noTempDir is the error a temporary directory gets in a WASI module whose
// runtime preopened no directory to make it in (osTempDir).
func noTempDir() *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.make_temp_dir", Target: noTempDirTarget}
}

// loopLimit is how many symlinks one path may pass through before it is
// ELOOP, as Linux's limit is.
const loopLimit = 40

// loop is the error for a path that passes through more symlinks than
// loopLimit, as the operating system reports it.
func loop(op, path string) *rt.IOError {
	return &rt.IOError{Tag: rt.TagIOOther, Operation: op, Path: path, Code: "ELOOP"}
}
