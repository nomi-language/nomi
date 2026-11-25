package vmhost

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/rt"
)

// FileSystem is a file system a program's std/fs calls reach
// (WithFileSystem). DiskFiles and NewMemoryFiles answer the two Nomi builds
// in; an embedder implements it to give a program files of its own: a
// store, a remote volume, a view over another FileSystem.
//
// # Paths
//
// Every path is a Nomi path, as the program wrote it: `/` separates names
// on every operating system, and a relative path is relative to
// CurrentDir. No path is empty or holds a NUL byte; std/fs answers those
// itself.
//
// # Errors
//
// A method's error becomes the io.Error the program sees, naming the path
// the program passed: an error that wraps fs.ErrNotExist is NotFound,
// fs.ErrExist AlreadyExists, fs.ErrPermission PermissionDenied and
// fs.ErrClosed Closed, an *fs.PathError over a syscall.Errno the variant
// that errno names (ENOTDIR is NotADirectory, ENOTEMPTY DirectoryNotEmpty,
// and so on), and any other error Other with its errno's name, never the
// error's text. To answer a variant exactly, return rt.IOFailure{E:
// rt.IOError{...}}.
//
// # What each method must do
//
// Open of a directory is IsADirectory. Create in WriteReplaceAtomically
// leaves the old file as it was until the writer closes; a writer that
// also has an `Abort()` method is given up through it when the program
// gives the write up (`Writer.discard` before a close), and is closed
// otherwise. List is sorted by name and lists a symlink as one. Remove
// removes a file, a symlink or an empty directory; RemoveAll removes a
// tree, following no symlink, and a missing path is not an error. Stat
// follows symlinks and Lstat does not.
//
// A FileSystem that also implements WithinFileSystem serves `fs.within`;
// one that does not answers it Unsupported.
type FileSystem interface {
	Open(path string) (io.ReadCloser, error)
	Create(path string, mode WriteMode) (io.WriteCloser, error)
	OpenFile(path string, access Access) (File, error)
	List(dir string) ([]DirEntry, error)
	CreateDir(path string, parents bool) error
	Remove(path string) error
	RemoveAll(path string) error
	Rename(from, to string) error
	Stat(path string) (FileInfo, error)
	Lstat(path string) (FileInfo, error)
	Symlink(path, target string) error
	ReadLink(path string) (string, error)
	Chmod(path string, mode fs.FileMode) error
	// CurrentDir is the absolute path relative paths are under.
	CurrentDir() (string, error)
	// TempDir is where MakeTempDir makes directories.
	TempDir() string
	// MakeTempDir makes a fresh directory under TempDir and answers its
	// path.
	MakeTempDir() (string, error)
}

// WithinFileSystem is a FileSystem that serves `fs.within`: Within answers
// a file system that reaches only what is under dir, and the function that
// releases it. A relative path is under dir, an absolute one must be, and
// one that leaves dir, by `..` or through a symlink, must fail; answer
// rt.IOFailure{E: rt.IOError{Tag: rt.TagIOInvalidPath, ...}} for it.
type WithinFileSystem interface {
	FileSystem
	Within(dir string) (FileSystem, func(), error)
}

// File is one file open for reads and writes at a position, as
// FileSystem.OpenFile answers it: what `io.File` is over. *os.File has
// every method but Size.
//
// A File that also has a `Sync() error` method, as *os.File does, serves
// `File.sync` and `File.sync_data`, and one that also implements LockFile
// serves `File.lock`, `File.try_lock` and `File.unlock`. A File without one
// answers its calls Unsupported.
type File interface {
	io.ReaderAt
	io.WriterAt
	// Truncate changes the size, dropping what is past it or adding zeros.
	Truncate(size int64) error
	// Size is the file's size now.
	Size() (int64, error)
	Close() error
}

// LockFile is a File that locks ranges of its bytes for its own handle.
// Lock makes the handle's lock on length bytes from start (0: from start
// to past any end the file can have) mode, and reports whether it could:
// false, with nothing changed, when another handle holds a lock there that
// conflicts, an exclusive lock with any lock and a shared lock with an
// exclusive one. It never waits. A lock on bytes the handle holds replaces
// it, an unlock of bytes it does not hold does nothing, and Close releases
// every lock the handle holds.
type LockFile interface {
	File
	Lock(mode LockMode, start, length int64) (bool, error)
}

// LockMode is what LockFile.Lock makes a range: std/io's `LockMode`, or
// LockNone to unlock it.
type LockMode int

const (
	LockNone      LockMode = LockMode(rt.LockNone)
	LockShared    LockMode = LockMode(rt.LockShared)
	LockExclusive LockMode = LockMode(rt.LockExclusive)
)

// TimesFileSystem is a FileSystem that serves `fs.set_times` and
// `fs.touch`: SetTimes sets the modification and access times of what path
// names, following symlinks, and leaves a zero time as it is. One that does
// not answers both Unsupported.
type TimesFileSystem interface {
	FileSystem
	SetTimes(path string, modified, accessed time.Time) error
}

// LinkFileSystem is a FileSystem that serves `fs.create_link`: Link makes
// path a second name of the file at target, following no symlink at
// target. One that does not answers it Unsupported.
type LinkFileSystem interface {
	FileSystem
	Link(path, target string) error
}

// OwnerFileSystem is a FileSystem that serves `fs.set_owner` and
// `fs.set_link_owner`, by Chown, which follows a symlink at path when
// follow is set, and `fs.user_name` and `fs.group_name`, by OwnerName,
// which names the user, or the group when group is set, numbered id. One
// that does not answers the first two Unsupported and the names None.
type OwnerFileSystem interface {
	FileSystem
	Chown(path string, user, group uint64, follow bool) error
	OwnerName(id uint64, group bool) (string, bool)
}

// DiskUsageFileSystem is a FileSystem that serves `fs.disk_usage`:
// DiskUsage is the size, free bytes and bytes available of the file system
// path is on. One that does not answers it Unsupported.
type DiskUsageFileSystem interface {
	FileSystem
	DiskUsage(path string) (DiskUsage, error)
}

// DiskUsage is what DiskUsageFileSystem.DiskUsage answers, in bytes: the
// file system's size, what is free on it, and what of that the program's
// user may use.
type DiskUsage struct{ Total, Free, Available uint64 }

// SyncDirFileSystem is a FileSystem that serves `fs.sync_dir`: SyncDir
// makes the entries of the directory dir durable, and fails for something
// that is not a directory. One that does not answers `fs.sync_dir`
// Unsupported.
type SyncDirFileSystem interface {
	FileSystem
	SyncDir(dir string) error
}

// WriteMode is how FileSystem.Create opens a file: std/fs's `WriteMode`.
type WriteMode int

const (
	// WriteReplace creates the file or truncates it.
	WriteReplace WriteMode = WriteMode(stdio.Replace)
	// WriteReplaceAtomically writes the new contents beside the file and
	// puts them in its place when the writer closes.
	WriteReplaceAtomically WriteMode = WriteMode(stdio.ReplaceAtomically)
	// WriteAppend creates the file when it is missing and writes at its end.
	WriteAppend WriteMode = WriteMode(stdio.Append)
	// WriteNew fails with AlreadyExists when something is at the path.
	WriteNew WriteMode = WriteMode(stdio.New)
)

// Access is how FileSystem.OpenFile opens a file: std/fs's `Access`.
type Access int

const (
	// AccessRead opens an existing file to read it.
	AccessRead Access = Access(stdio.AccessRead)
	// AccessReadWrite opens an existing file to read and write it.
	AccessReadWrite Access = Access(stdio.AccessReadWrite)
	// AccessCreate opens a file to read and write it, making it empty when
	// it is missing.
	AccessCreate Access = Access(stdio.AccessCreate)
)

// FileKind is what a path names: std/fs's `Kind`.
type FileKind int

const (
	KindFile      FileKind = FileKind(stdio.KindFile)
	KindDirectory FileKind = FileKind(stdio.KindDirectory)
	KindSymlink   FileKind = FileKind(stdio.KindSymlink)
	KindOther     FileKind = FileKind(stdio.KindOther)
)

// DirEntry is one entry of FileSystem.List.
type DirEntry struct {
	Name string
	Kind FileKind
}

// FileInfo is what FileSystem.Stat and Lstat answer. Mode holds the
// permission bits and the setuid, setgid and sticky bits. A zero Modified,
// a file system that records no time as an fs.FS from embed does, reads as
// the Unix epoch, as std/embed's files do; any other Modified reaches
// `fs.metadata` exactly, whatever its year. The fields after Mode are what
// a file system may not keep: each left zero (the zero time, 0, nil) is
// None in `fs.Metadata`.
type FileInfo struct {
	Kind     FileKind
	Size     int64
	Modified time.Time
	Mode     fs.FileMode
	// Accessed is the last read, Changed the last change to the contents
	// or the metadata, Created the birth.
	Accessed, Changed, Created time.Time
	// Links is how many names the file has.
	Links uint64
	// ID is the device and the file's number there, which every name of
	// one file shares.
	ID *FileID
	// Owner is the numeric user and group that own the file.
	Owner *FileOwner
}

// FileID is a file's identity: its device and its number there.
type FileID struct{ Device, Inode uint64 }

// FileOwner is the numeric user and group that own a file.
type FileOwner struct{ User, Group uint64 }

// DiskFiles is the file system a program reaches when its host gives it
// none: the operating system's natively and on wasip1, the process's
// in-memory one on js/wasm. An embedder wraps it to give a program the disk
// with something added, a log of the calls or a refusal of some.
func DiskFiles() FileSystem { return backendFiles{stdio.Default()} }

// DiskFilesAt is DiskFiles with a relative path under dir, an absolute
// directory, rather than the process's working directory, and
// `fs.current_dir()` answering dir. On js/wasm it is DiskFiles.
func DiskFilesAt(dir string) FileSystem { return backendFiles{stdio.OSAt(dir)} }

// TestDir is the working directory `nomi test` gives the test file at
// path: its package root, the nearest directory at or above it holding a
// nomi.toml, or the file's own directory when there is none. A test that
// reads its module's files by a relative path reads the same files wherever
// `nomi test` runs.
func TestDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Dir(path)
	}
	dir := filepath.Dir(abs)
	for d := dir; ; d = filepath.Dir(d) {
		if info, err := os.Stat(filepath.Join(d, "nomi.toml")); err == nil && !info.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// WithFileSystem makes the program's std/fs calls reach files rather than
// the disk, for every run of the program: DiskFiles, NewMemoryFiles or an
// embedder's own FileSystem. `std::fs::testing.run` still replaces it for its own
// call.
func WithFileSystem(files FileSystem) Option {
	return func(c *config) { c.files = backendOf(files) }
}

// backendOf is files as internal/stdio's backend: the one inside DiskFiles
// or a MemoryFiles, or an embedder's FileSystem adapted.
func backendOf(files FileSystem) stdio.Backend {
	switch f := files.(type) {
	case nil:
		return nil
	case backendFiles:
		return f.b
	case *MemoryFiles:
		return f.m
	}
	return embedded{files}
}

// --- an embedder's FileSystem as internal/stdio's backend ------------------

// embedded is an embedder's FileSystem as the backend std/fs calls.
type embedded struct{ fs FileSystem }

func fsError(op, path string, err error) *rt.IOError {
	e := stdio.Classify(op, path, err)
	return &e
}

func (e embedded) Open(p string) (rt.ReadSource, *rt.IOError) {
	r, err := e.fs.Open(p)
	if err != nil {
		return rt.ReadSource{}, fsError("open", p, err)
	}
	return rt.ReadSource{Name: p, R: r, Close: r.Close}, nil
}

func (e embedded) Create(p string, mode stdio.WriteMode) (rt.WriteSink, *rt.IOError) {
	w, err := e.fs.Create(p, WriteMode(mode))
	if err != nil {
		return rt.WriteSink{}, fsError("open", p, err)
	}
	sink := rt.WriteSink{Name: p, W: w, Close: w.Close}
	if a, ok := w.(interface{ Abort() }); ok {
		sink.Abort = a.Abort
	}
	return sink, nil
}

func (e embedded) OpenFile(p string, access stdio.Access) (rt.FileSource, *rt.IOError) {
	f, err := e.fs.OpenFile(p, Access(access))
	if err != nil {
		return rt.FileSource{}, fsError("open", p, err)
	}
	src := rt.FileSource{Name: p, F: f, Size: f.Size, Writable: access != stdio.AccessRead, Close: f.Close}
	src.Sync = func(bool) error { return unsupportedHere("File.sync") }
	if s, ok := f.(interface{ Sync() error }); ok {
		src.Sync = func(bool) error { return s.Sync() }
	}
	src.Lock = func(rt.LockMode, int64, int64) (bool, error) { return false, unsupportedHere("File.lock") }
	if l, ok := f.(LockFile); ok {
		src.Lock = func(mode rt.LockMode, start, length int64) (bool, error) {
			return l.Lock(LockMode(mode), start, length)
		}
	}
	return src, nil
}

// unsupportedHere is the error for an operation an embedder's file system
// does not serve.
func unsupportedHere(operation string) error {
	return rt.IOFailure{E: rt.IOError{Tag: rt.TagIOUnsupported, Operation: operation, Target: "this program's file system"}}
}

func (e embedded) SyncDir(dir string) *rt.IOError {
	s, ok := e.fs.(SyncDirFileSystem)
	if !ok {
		return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.sync_dir", Target: "this program's file system"}
	}
	return fsFailed("sync", dir, s.SyncDir(dir))
}

func (e embedded) List(dir string) ([]stdio.DirEntry, *rt.IOError) {
	entries, err := e.fs.List(dir)
	if err != nil {
		return nil, fsError("open", dir, err)
	}
	out := make([]stdio.DirEntry, len(entries))
	for i, d := range entries {
		out[i] = stdio.DirEntry{Name: d.Name, Kind: stdio.Kind(d.Kind)}
	}
	slices.SortFunc(out, func(a, b stdio.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (e embedded) CreateDir(p string, parents bool) *rt.IOError {
	return fsFailed("mkdir", p, e.fs.CreateDir(p, parents))
}

func (e embedded) Remove(p string) *rt.IOError    { return fsFailed("remove", p, e.fs.Remove(p)) }
func (e embedded) RemoveAll(p string) *rt.IOError { return fsFailed("remove", p, e.fs.RemoveAll(p)) }
func (e embedded) Rename(from, to string) *rt.IOError {
	return fsFailed("rename", from, e.fs.Rename(from, to))
}

func (e embedded) Stat(p string) (stdio.Info, *rt.IOError) {
	info, err := e.fs.Stat(p)
	if err != nil {
		return stdio.Info{}, fsError("stat", p, err)
	}
	return fsInfo(info), nil
}

func (e embedded) Lstat(p string) (stdio.Info, *rt.IOError) {
	info, err := e.fs.Lstat(p)
	if err != nil {
		return stdio.Info{}, fsError("lstat", p, err)
	}
	return fsInfo(info), nil
}

func (e embedded) Symlink(p, target string) *rt.IOError {
	return fsFailed("symlink", p, e.fs.Symlink(p, target))
}

func (e embedded) ReadLink(p string) (string, *rt.IOError) {
	target, err := e.fs.ReadLink(p)
	if err != nil {
		return "", fsError("readlink", p, err)
	}
	return target, nil
}

func (e embedded) Chmod(p string, mode int64) *rt.IOError {
	return fsFailed("chmod", p, e.fs.Chmod(p, stdio.FileMode(mode)))
}

func (e embedded) Chtimes(p string, modified, accessed *time.Time) *rt.IOError {
	t, ok := e.fs.(TimesFileSystem)
	if !ok {
		return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.set_times", Target: "this program's file system"}
	}
	var mtime, atime time.Time
	if modified != nil {
		mtime = *modified
	}
	if accessed != nil {
		atime = *accessed
	}
	return fsFailed("chtimes", p, t.SetTimes(p, mtime, atime))
}

func (e embedded) Link(p, target string) *rt.IOError {
	l, ok := e.fs.(LinkFileSystem)
	if !ok {
		return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.create_link", Target: "this program's file system"}
	}
	return fsFailed("link", p, l.Link(p, target))
}

func (e embedded) Chown(p string, user, group uint64, follow bool) *rt.IOError {
	o, ok := e.fs.(OwnerFileSystem)
	if !ok {
		operation := "fs.set_owner"
		if !follow {
			operation = "fs.set_link_owner"
		}
		return &rt.IOError{Tag: rt.TagIOUnsupported, Operation: operation, Target: "this program's file system"}
	}
	return fsFailed("chown", p, o.Chown(p, user, group, follow))
}

func (e embedded) OwnerName(id uint64, group bool) (string, bool) {
	if o, ok := e.fs.(OwnerFileSystem); ok {
		return o.OwnerName(id, group)
	}
	return "", false
}

func (e embedded) DiskUsage(p string) (stdio.DiskUsage, *rt.IOError) {
	d, ok := e.fs.(DiskUsageFileSystem)
	if !ok {
		return stdio.DiskUsage{}, &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.disk_usage", Target: "this program's file system"}
	}
	u, err := d.DiskUsage(p)
	if err != nil {
		return stdio.DiskUsage{}, fsError("statfs", p, err)
	}
	return stdio.DiskUsage(u), nil
}

func (e embedded) Within(dir string) (stdio.Backend, func(), *rt.IOError) {
	w, ok := e.fs.(WithinFileSystem)
	if !ok {
		return nil, nil, &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "fs.within", Target: "this program's file system"}
	}
	inner, release, err := w.Within(dir)
	if err != nil {
		return nil, nil, fsError("open", dir, err)
	}
	if release == nil {
		release = func() {}
	}
	return backendOf(inner), release, nil
}

func (e embedded) TempDir() string { return e.fs.TempDir() }

func (e embedded) MakeTempDir() (string, *rt.IOError) {
	dir, err := e.fs.MakeTempDir()
	if err != nil {
		return "", fsError("mkdir", e.fs.TempDir(), err)
	}
	return dir, nil
}

func (e embedded) CurrentDir() (string, *rt.IOError) {
	dir, err := e.fs.CurrentDir()
	if err != nil {
		return "", fsError("getwd", ".", err)
	}
	return dir, nil
}

func fsFailed(op, p string, err error) *rt.IOError {
	if err == nil {
		return nil
	}
	return fsError(op, p, err)
}

func fsInfo(i FileInfo) stdio.Info {
	modified := i.Modified
	if modified.IsZero() {
		modified = time.Unix(0, 0)
	}
	return stdio.Info{Kind: stdio.Kind(i.Kind), Size: i.Size, Modified: modified, Mode: stdio.ModeBits(i.Mode),
		Accessed: i.Accessed, Changed: i.Changed, Created: i.Created, Links: i.Links,
		ID: (*stdio.FileID)(i.ID), Owner: (*stdio.Owner)(i.Owner)}
}

// --- internal/stdio's backend as a FileSystem ------------------------------

// backendFiles is a backend std/fs calls (the disk's, a Memory) as a
// FileSystem, so an embedder can wrap it; WithFileSystem unwraps it again.
type backendFiles struct{ b stdio.Backend }

// fail is a backend's io.Error as a Go error that carries it.
func fsFail(e *rt.IOError) error {
	if e == nil {
		return nil
	}
	return rt.IOFailure{E: *e}
}

func (f backendFiles) Open(p string) (io.ReadCloser, error) {
	src, e := f.b.Open(p)
	if e != nil {
		return nil, fsFail(e)
	}
	return fsReader{src}, nil
}

func (f backendFiles) Create(p string, mode WriteMode) (io.WriteCloser, error) {
	sink, e := f.b.Create(p, stdio.WriteMode(mode))
	if e != nil {
		return nil, fsFail(e)
	}
	return &fsWriter{sink: sink}, nil
}

func (f backendFiles) OpenFile(p string, access Access) (File, error) {
	src, e := f.b.OpenFile(p, stdio.Access(access))
	if e != nil {
		return nil, fsFail(e)
	}
	return fsFile{src}, nil
}

func (f backendFiles) List(dir string) ([]DirEntry, error) {
	entries, e := f.b.List(dir)
	if e != nil {
		return nil, fsFail(e)
	}
	out := make([]DirEntry, len(entries))
	for i, d := range entries {
		out[i] = DirEntry{Name: d.Name, Kind: FileKind(d.Kind)}
	}
	return out, nil
}

func (f backendFiles) CreateDir(p string, parents bool) error {
	return fsFail(f.b.CreateDir(p, parents))
}
func (f backendFiles) SyncDir(dir string) error       { return fsFail(f.b.SyncDir(dir)) }
func (f backendFiles) Remove(p string) error          { return fsFail(f.b.Remove(p)) }
func (f backendFiles) RemoveAll(p string) error       { return fsFail(f.b.RemoveAll(p)) }
func (f backendFiles) Rename(from, to string) error   { return fsFail(f.b.Rename(from, to)) }
func (f backendFiles) Symlink(p, target string) error { return fsFail(f.b.Symlink(p, target)) }
func (f backendFiles) Chmod(p string, mode fs.FileMode) error {
	return fsFail(f.b.Chmod(p, stdio.ModeBits(mode)))
}

func (f backendFiles) SetTimes(p string, modified, accessed time.Time) error {
	at := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	return fsFail(f.b.Chtimes(p, at(modified), at(accessed)))
}

func (f backendFiles) Link(p, target string) error { return fsFail(f.b.Link(p, target)) }

func (f backendFiles) Chown(p string, user, group uint64, follow bool) error {
	return fsFail(f.b.Chown(p, user, group, follow))
}

func (f backendFiles) OwnerName(id uint64, group bool) (string, bool) {
	return f.b.OwnerName(id, group)
}

func (f backendFiles) DiskUsage(p string) (DiskUsage, error) {
	u, e := f.b.DiskUsage(p)
	return DiskUsage(u), fsFail(e)
}

func (f backendFiles) Stat(p string) (FileInfo, error) {
	info, e := f.b.Stat(p)
	return publicInfo(info), fsFail(e)
}

func (f backendFiles) Lstat(p string) (FileInfo, error) {
	info, e := f.b.Lstat(p)
	return publicInfo(info), fsFail(e)
}

func (f backendFiles) ReadLink(p string) (string, error) {
	target, e := f.b.ReadLink(p)
	return target, fsFail(e)
}

func (f backendFiles) Within(dir string) (FileSystem, func(), error) {
	inner, release, e := f.b.Within(dir)
	if e != nil {
		return nil, nil, fsFail(e)
	}
	return backendFiles{inner}, release, nil
}

func (f backendFiles) TempDir() string { return f.b.TempDir() }

func (f backendFiles) MakeTempDir() (string, error) {
	dir, e := f.b.MakeTempDir()
	return dir, fsFail(e)
}

func (f backendFiles) CurrentDir() (string, error) {
	dir, e := f.b.CurrentDir()
	return dir, fsFail(e)
}

func publicInfo(i stdio.Info) FileInfo {
	return FileInfo{Kind: FileKind(i.Kind), Size: i.Size, Modified: i.Modified, Mode: stdio.FileMode(i.Mode),
		Accessed: i.Accessed, Changed: i.Changed, Created: i.Created, Links: i.Links,
		ID: (*FileID)(i.ID), Owner: (*FileOwner)(i.Owner)}
}

type fsReader struct{ src rt.ReadSource }

func (r fsReader) Read(p []byte) (int, error) { return r.src.R.Read(p) }

func (r fsReader) Close() error {
	if r.src.Close == nil {
		return nil
	}
	return r.src.Close()
}

type fsWriter struct {
	sink   rt.WriteSink
	closed bool
}

func (w *fsWriter) Write(p []byte) (int, error) { return w.sink.W.Write(p) }

func (w *fsWriter) Close() error {
	if w.closed {
		return rt.IOFailure{E: rt.IOError{Tag: rt.TagIOClosed}}
	}
	w.closed = true
	if w.sink.Close == nil {
		return nil
	}
	return w.sink.Close()
}

// Abort gives the write up: an atomic write leaves the old file as it was.
func (w *fsWriter) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	if w.sink.Abort != nil {
		w.sink.Abort()
		return
	}
	if w.sink.Close != nil {
		_ = w.sink.Close()
	}
}

type fsFile struct{ src rt.FileSource }

func (f fsFile) ReadAt(p []byte, off int64) (int, error)  { return f.src.F.ReadAt(p, off) }
func (f fsFile) WriteAt(p []byte, off int64) (int, error) { return f.src.F.WriteAt(p, off) }
func (f fsFile) Truncate(size int64) error                { return f.src.F.Truncate(size) }
func (f fsFile) Size() (int64, error)                     { return f.src.Size() }
func (f fsFile) Sync() error                              { return f.src.Sync(false) }
func (f fsFile) Lock(mode LockMode, start, length int64) (bool, error) {
	return f.src.Lock(rt.LockMode(mode), start, length)
}

func (f fsFile) Close() error {
	if f.src.Close == nil {
		return nil
	}
	return f.src.Close()
}
