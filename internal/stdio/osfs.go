//go:build !(js && wasm)

package stdio

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// The operating system's file system, over Go's os package, and the same
// over an *os.Root inside `fs.within`. Paths cross it through ospath.go, so a
// program writes `/` on every system.
//
// One implementation (disk) serves both: osOps is the set of calls it makes,
// which Go's os package answers for the whole disk (hostOS) and an *os.Root
// answers with the same shapes for what is under its directory, refusing a
// name that leaves it by `..` or by a symlink.

// osOps is what the disk backend calls.
type osOps interface {
	Open(name string) (*os.File, error)
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldname, newname string) error
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
	Chmod(name string, mode fs.FileMode) error
}

// hostOS is osOps over Go's os package: the whole disk.
type hostOS struct{}

func (hostOS) Open(n string) (*os.File, error) { return os.Open(n) }
func (hostOS) OpenFile(n string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(n, flag, perm)
}
func (hostOS) Mkdir(n string, perm fs.FileMode) error    { return os.Mkdir(n, perm) }
func (hostOS) MkdirAll(n string, perm fs.FileMode) error { return os.MkdirAll(n, perm) }
func (hostOS) Remove(n string) error                     { return os.Remove(n) }
func (hostOS) RemoveAll(n string) error                  { return os.RemoveAll(n) }
func (hostOS) Rename(from, to string) error              { return os.Rename(from, to) }
func (hostOS) Stat(n string) (fs.FileInfo, error)        { return os.Stat(n) }
func (hostOS) Lstat(n string) (fs.FileInfo, error)       { return os.Lstat(n) }
func (hostOS) Readlink(n string) (string, error)         { return os.Readlink(n) }
func (hostOS) Symlink(target, n string) error            { return os.Symlink(target, n) }
func (hostOS) Chmod(n string, mode fs.FileMode) error    { return os.Chmod(n, mode) }

// disk is the operating system's file system: the whole of it, or inside
// `fs.within` what is under one directory.
type disk struct {
	ops osOps
	// root is, inside `fs.within`, the directory's absolute Nomi path, which
	// an absolute path must be under and which current_dir answers. It is ""
	// for the whole disk.
	root string
	// cwd is, outside `fs.within`, the absolute directory a relative path
	// is under and current_dir answers, in the operating system's form;
	// "" for the process's working directory.
	cwd string
}

// OS is the operating system's whole file system.
var OS Backend = &disk{ops: hostOS{}}

// OSAt is the operating system's whole file system with relative paths
// under dir, an absolute directory, instead of the process's working
// directory: what `nomi test` gives a test file (its module root).
func OSAt(dir string) Backend { return &disk{ops: hostOS{}, cwd: dir} }

// defaultBackend is a native build's file system: the operating system's.
func defaultBackend() Backend { return OS }

// native is p as d's calls take it: the operating system's form, and inside
// a root relative to it. An absolute path outside the root escapes.
func (d *disk) native(p string) (string, *rt.IOError) {
	if d.root == "" {
		if d.cwd != "" && !filepath.IsAbs(toOS(p)) {
			return filepath.Join(d.cwd, toOS(p)), nil
		}
		return toOS(p), nil
	}
	if !filepath.IsAbs(toOS(p)) {
		if p == "" {
			return ".", nil
		}
		return toOS(p), nil
	}
	clean := path.Clean(fromOS(toOS(p)))
	switch {
	case clean == d.root:
		return ".", nil
	case d.root == "/":
		return toOS(clean[1:]), nil
	case len(clean) > len(d.root) && clean[:len(d.root)] == d.root && clean[len(d.root)] == '/':
		return toOS(clean[len(d.root)+1:]), nil
	}
	return "", escapes(p)
}

func (d *disk) Open(p string) (rt.ReadSource, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return rt.ReadSource{}, e
	}
	f, err := d.ops.Open(n)
	if err != nil {
		return rt.ReadSource{}, classify("open", p, err)
	}
	if info, err := f.Stat(); err == nil && info.IsDir() {
		_ = f.Close()
		return rt.ReadSource{}, errOf(rt.TagIOIsADirectory, p)
	}
	return rt.ReadSource{Name: p, R: f, Close: f.Close, Fail: failer(p)}, nil
}

func (d *disk) Create(p string, mode WriteMode) (rt.WriteSink, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return rt.WriteSink{}, e
	}
	if mode == ReplaceAtomically {
		return d.createAtomically(p, n)
	}
	flags := os.O_WRONLY | os.O_CREATE
	switch mode {
	case Replace:
		flags |= os.O_TRUNC
	case Append:
		flags |= os.O_APPEND
	case New:
		flags |= os.O_EXCL
	}
	f, err := d.ops.OpenFile(n, flags, 0o644)
	if err != nil {
		return rt.WriteSink{}, d.writeFailure("open", p, err)
	}
	return rt.WriteSink{Name: p, W: f, Close: f.Close, Fail: failer(p)}, nil
}

// createAtomically writes a temporary file in p's directory and renames it
// over p when the write is closed, so a reader sees the old contents or the
// new, never part of either. The new file keeps the old one's permissions,
// or 0o644.
func (d *disk) createAtomically(p, native string) (rt.WriteSink, *rt.IOError) {
	perm := fs.FileMode(0o644)
	if info, err := d.ops.Stat(native); err == nil {
		if info.IsDir() {
			return rt.WriteSink{}, errOf(rt.TagIOIsADirectory, p)
		}
		perm = info.Mode().Perm()
	}
	dir, base := filepath.Split(native)
	var f *os.File
	var tmp string
	for i := 0; ; i++ {
		tmp = filepath.Join(dir, "."+base+".tmp-"+strconv.FormatInt(time.Now().UnixNano()+int64(i), 36))
		var err error
		f, err = d.ops.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || i > 100 {
			return rt.WriteSink{}, d.writeFailure("open", p, err)
		}
	}
	commit := func() error {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			_ = d.ops.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = d.ops.Remove(tmp)
			return err
		}
		if err := d.ops.Chmod(tmp, perm); err != nil {
			_ = d.ops.Remove(tmp)
			return err
		}
		if err := d.ops.Rename(tmp, native); err != nil {
			_ = d.ops.Remove(tmp)
			return err
		}
		return nil
	}
	abort := func() {
		_ = f.Close()
		_ = d.ops.Remove(tmp)
	}
	return rt.WriteSink{Name: p, W: f, Close: commit, Abort: abort, Fail: failer(p)}, nil
}

func (d *disk) OpenFile(p string, access Access) (rt.FileSource, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return rt.FileSource{}, e
	}
	flags := os.O_RDONLY
	switch access {
	case AccessReadWrite:
		flags = os.O_RDWR
	case AccessCreate:
		flags = os.O_RDWR | os.O_CREATE
	}
	f, err := d.ops.OpenFile(n, flags, 0o644)
	if err != nil {
		if access == AccessCreate {
			return rt.FileSource{}, d.writeFailure("open", p, err)
		}
		return rt.FileSource{}, classify("open", p, err)
	}
	if info, err := f.Stat(); err == nil && info.IsDir() {
		_ = f.Close()
		return rt.FileSource{}, errOf(rt.TagIOIsADirectory, p)
	}
	size := func() (int64, error) {
		info, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	return rt.FileSource{Name: p, F: f, Size: size, Writable: access != AccessRead, Close: f.Close, Fail: failer(p)}, nil
}

func (d *disk) List(dir string) ([]DirEntry, *rt.IOError) {
	n, e := d.native(dir)
	if e != nil {
		return nil, e
	}
	f, err := d.ops.Open(n)
	if err != nil {
		return nil, classify("open", dir, err)
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return nil, classify("readdir", dir, err)
	}
	out := make([]DirEntry, len(entries))
	for i, e := range entries {
		out[i] = DirEntry{Name: fromOSName(e.Name()), Kind: kindOf(e.Type())}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *disk) CreateDir(p string, parents bool) *rt.IOError {
	n, e := d.native(p)
	if e != nil {
		return e
	}
	if parents {
		if err := d.ops.MkdirAll(n, 0o755); err != nil {
			return classify("mkdir", p, err)
		}
		return nil
	}
	if err := d.ops.Mkdir(n, 0o755); err != nil {
		return d.writeFailure("mkdir", p, err)
	}
	return nil
}

func (d *disk) Remove(p string) *rt.IOError {
	n, e := d.native(p)
	if e != nil {
		return e
	}
	if err := d.ops.Remove(n); err != nil {
		return classify("remove", p, err)
	}
	return nil
}

func (d *disk) RemoveAll(p string) *rt.IOError {
	n, e := d.native(p)
	if e != nil {
		return e
	}
	if err := d.ops.RemoveAll(n); err != nil {
		return classify("remove", p, err)
	}
	return nil
}

func (d *disk) Rename(from, to string) *rt.IOError {
	nativeFrom, e := d.native(from)
	if e != nil {
		return e
	}
	nativeTo, e := d.native(to)
	if e != nil {
		return e
	}
	if _, err := d.ops.Lstat(nativeFrom); err != nil {
		return classify("rename", from, err)
	}
	if err := d.ops.Rename(nativeFrom, nativeTo); err != nil {
		return d.writeFailure("rename", to, err)
	}
	return nil
}

func infoOf(info fs.FileInfo) Info {
	return Info{Kind: kindOf(info.Mode().Type()), Size: info.Size(), Modified: info.ModTime().UnixNano(),
		Mode: int64(info.Mode().Perm())}
}

func (d *disk) Stat(p string) (Info, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return Info{}, e
	}
	info, err := d.ops.Stat(n)
	if err != nil {
		return Info{}, classify("stat", p, err)
	}
	return infoOf(info), nil
}

func (d *disk) Lstat(p string) (Info, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return Info{}, e
	}
	info, err := d.ops.Lstat(n)
	if err != nil {
		return Info{}, classify("lstat", p, err)
	}
	return infoOf(info), nil
}

func (d *disk) Symlink(p, target string) *rt.IOError {
	n, e := d.native(p)
	if e != nil {
		return e
	}
	if err := d.ops.Symlink(toOS(target), n); err != nil {
		return d.writeFailure("symlink", p, err)
	}
	return nil
}

func (d *disk) ReadLink(p string) (string, *rt.IOError) {
	n, e := d.native(p)
	if e != nil {
		return "", e
	}
	target, err := d.ops.Readlink(n)
	if err != nil {
		return "", classify("readlink", p, err)
	}
	return fromOS(target), nil
}

func (d *disk) Chmod(p string, mode int64) *rt.IOError {
	n, e := d.native(p)
	if e != nil {
		return e
	}
	if err := d.ops.Chmod(n, fs.FileMode(mode)); err != nil {
		return classify("chmod", p, err)
	}
	return nil
}

func (d *disk) Within(dir string) (Backend, func(), *rt.IOError) {
	n, e := d.native(dir)
	if e != nil {
		return nil, nil, e
	}
	var root *os.Root
	var err error
	if r, inside := d.ops.(*os.Root); inside {
		root, err = r.OpenRoot(n)
	} else {
		root, err = os.OpenRoot(n)
	}
	if err != nil {
		return nil, nil, classify("open", dir, err)
	}
	var abs string
	switch {
	case d.root != "":
		abs = path.Clean(d.root + "/" + fromOS(n))
	default:
		a, err := filepath.Abs(n)
		if err != nil {
			_ = root.Close()
			return nil, nil, classify("open", dir, err)
		}
		abs = fromOS(a)
	}
	return &disk{ops: root, root: abs}, func() { _ = root.Close() }, nil
}

func (d *disk) TempDir() string {
	dir, _ := osTempDir()
	return fromOS(dir)
}

func (d *disk) MakeTempDir() (string, *rt.IOError) {
	if d.root != "" {
		return "", withinTempDir()
	}
	base, reachable := osTempDir()
	if !reachable {
		return "", noTempDir()
	}
	dir, err := os.MkdirTemp(base, "nomi-")
	if err != nil {
		return "", classify("mkdir", fromOS(base), err)
	}
	return fromOS(dir), nil
}

func (d *disk) CurrentDir() (string, *rt.IOError) {
	if d.root != "" {
		return d.root, nil
	}
	if d.cwd != "" {
		return fromOS(d.cwd), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", classify("getwd", ".", err)
	}
	return fromOS(dir), nil
}

func kindOf(t fs.FileMode) Kind {
	switch {
	case t.IsRegular():
		return KindFile
	case t.IsDir():
		return KindDirectory
	case t&fs.ModeSymlink != 0:
		return KindSymlink
	}
	return KindOther
}

// failer classifies a read's or a write's failure on path.
func failer(path string) func(string, error) rt.IOError {
	return func(op string, err error) rt.IOError { return *classify(op, path, err) }
}

// writeFailure is classify for an operation that makes something at path:
// when it is NotFound, the file need not exist, its directory must, so the
// error names the directory that is missing.
func (d *disk) writeFailure(op, p string, err error) *rt.IOError {
	e := classify(op, p, err)
	if e.Tag == rt.TagIONotFound {
		if dir := parentOf(p); dir != "" && d.missing(dir) {
			e.Path = dir
		}
	}
	return e
}

// missing reports whether nothing is at p.
func (d *disk) missing(p string) bool {
	n, e := d.native(p)
	if e != nil {
		return false
	}
	_, err := d.ops.Stat(n)
	return errors.Is(err, fs.ErrNotExist)
}
