package stdio

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nomi-language/nomi/rt"
)

// The std/fs crossings, std/fs_host's host functions: each validates its
// paths, asks the frame for its backend, and answers what std/fs builds its
// values from. internal/stdlibbindings binds each as an RtFuncs row in the
// `files` capability class.

func failed[T any](e *rt.IOError) rt.Result[T, rt.IOError] { return rt.Err[T](*e) }

func ok[T any](v T) rt.Result[T, rt.IOError] { return rt.Ok[T, rt.IOError](v) }

func unit(e *rt.IOError) rt.Result[rt.Unit, rt.IOError] {
	if e != nil {
		return failed[rt.Unit](e)
	}
	return ok(rt.Unit{})
}

// readAll opens path and reads all of it.
func readAll(fr *rt.Frame, path string) ([]byte, *rt.IOError) {
	if e := validPath(path); e != nil {
		return nil, e
	}
	src, e := backendOf(fr).Open(path)
	if e != nil {
		return nil, e
	}
	r := rt.NewReader(src)
	data := rt.ReaderReadAll(fr, r)
	closed := rt.ReaderClose(r)
	if data.Tag != rt.TagOk {
		return nil, &data.Err
	}
	if closed.Tag != rt.TagOk {
		return nil, &closed.Err
	}
	return []byte(data.Ok), nil
}

// Read is `fs.read`: the whole file.
func Read(fr *rt.Frame, path string) rt.Result[rt.Bytes, rt.IOError] {
	data, e := readAll(fr, path)
	if e != nil {
		return failed[rt.Bytes](e)
	}
	return ok(rt.Bytes(data))
}

// ReadText is `fs.read_text`: the whole file as text, or InvalidText with
// the offset of the first byte that is not UTF-8.
func ReadText(fr *rt.Frame, path string) rt.Result[string, rt.IOError] {
	data, e := readAll(fr, path)
	if e != nil {
		return failed[string](e)
	}
	if !utf8.Valid(data) {
		return rt.Err[string](rt.IOError{Tag: rt.TagIOInvalidText, Path: path,
			Offset: int64(rt.InvalidUTF8Offset(string(data)))})
	}
	return ok(string(data))
}

// Write is `fs.write` and `fs.write_text`: data into path in mode. A
// failed write gives the write up, so with ReplaceAtomically the old file is
// untouched.
func Write(fr *rt.Frame, path string, data rt.Bytes, mode int64) rt.Result[rt.Unit, rt.IOError] {
	created := Create(fr, path, mode)
	if created.Tag != rt.TagOk {
		return failed[rt.Unit](&created.Err)
	}
	w := created.Ok
	if res := rt.WriterWrite(fr, w, data); res.Tag != rt.TagOk {
		rt.WriterDiscard(w)
		return res
	}
	return rt.WriterClose(fr, w)
}

// Open is `fs.open`: a Reader over the file.
func Open(fr *rt.Frame, path string) rt.Result[rt.Reader, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Reader](e)
	}
	src, e := backendOf(fr).Open(path)
	if e != nil {
		return failed[rt.Reader](e)
	}
	return ok(rt.NewReader(src))
}

// Create is `fs.create`: a Writer over the file, in mode.
func Create(fr *rt.Frame, path string, mode int64) rt.Result[rt.Writer, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Writer](e)
	}
	if mode < int64(Replace) || mode > int64(New) {
		rt.Trap("fs.create: write mode " + strconv.FormatInt(mode, 10) + " is not a WriteMode")
	}
	sink, e := backendOf(fr).Create(path, WriteMode(mode))
	if e != nil {
		return failed[rt.Writer](e)
	}
	return ok(rt.NewWriter(sink))
}

// List is `fs.list` and each directory of `fs.walk`: each entry's kind
// letter (kindLetter) and name, sorted by name. std/fs builds the entries.
// A name that is not UTF-8 is `!` and the name with each bad sequence
// replaced by U+FFFD: `fs.list` fails on it and `fs.walk` answers it as an
// InvalidPath item, so neither hands the program a String that is not
// UTF-8.
func List(fr *rt.Frame, dir string) rt.Result[*rt.List[string], rt.IOError] {
	if e := validPath(dir); e != nil {
		return failed[*rt.List[string]](e)
	}
	entries, e := backendOf(fr).List(dir)
	if e != nil {
		return failed[*rt.List[string]](e)
	}
	var out *rt.List[string]
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name
		if !utf8.ValidString(name) {
			out = rt.Cons("!"+strings.ToValidUTF8(name, "�"), out)
			continue
		}
		out = rt.Cons(kindLetter[entries[i].Kind]+name, out)
	}
	return ok(out)
}

// CreateDir is `fs.create_dir`.
func CreateDir(fr *rt.Frame, path string, parents bool) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Unit](e)
	}
	return unit(backendOf(fr).CreateDir(path, parents))
}

// Remove is `fs.remove`: a file or an empty directory.
func Remove(fr *rt.Frame, path string) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Unit](e)
	}
	return unit(backendOf(fr).Remove(path))
}

// RemoveAll is `fs.remove_all`: Ok when nothing is at path.
func RemoveAll(fr *rt.Frame, path string) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Unit](e)
	}
	return unit(backendOf(fr).RemoveAll(path))
}

// Rename is `fs.rename`, which replaces a file at to.
func Rename(fr *rt.Frame, from, to string) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(from); e != nil {
		return failed[rt.Unit](e)
	}
	if e := validPath(to); e != nil {
		return failed[rt.Unit](e)
	}
	return unit(backendOf(fr).Rename(from, to))
}

// Copy is `fs.copy`: the file at from into to, replacing what is there.
func Copy(fr *rt.Frame, from, to string) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(from); e != nil {
		return failed[rt.Unit](e)
	}
	if e := validPath(to); e != nil {
		return failed[rt.Unit](e)
	}
	b := backendOf(fr)
	src, e := b.Open(from)
	if e != nil {
		return failed[rt.Unit](e)
	}
	r := rt.NewReader(src)
	defer rt.ReaderClose(r)
	sink, e := b.Create(to, Replace)
	if e != nil {
		return failed[rt.Unit](e)
	}
	w := rt.NewWriter(sink)
	if res := rt.ReaderCopyTo(fr, r, w); res.Tag != rt.TagOk {
		rt.WriterDiscard(w)
		return failed[rt.Unit](&res.Err)
	}
	return rt.WriterClose(fr, w)
}

// Metadata is `fs.metadata`, following symlinks: the kind, the size, the
// modification time in nanoseconds since the Unix epoch, and the permission
// bits, which std/fs builds a Metadata from.
func Metadata(fr *rt.Frame, path string) rt.Result[*rt.List[int64], rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[*rt.List[int64]](e)
	}
	info, e := backendOf(fr).Stat(path)
	if e != nil {
		return failed[*rt.List[int64]](e)
	}
	return ok(infoList(info))
}

func infoList(info Info) *rt.List[int64] {
	var out *rt.List[int64]
	for _, v := range []int64{info.Mode, info.Modified, info.Size, int64(info.Kind)} {
		out = rt.Cons(v, out)
	}
	return out
}

// LinkMetadata is `fs.link_metadata`: Metadata's fields, describing a
// symlink at path as itself.
func LinkMetadata(fr *rt.Frame, path string) rt.Result[*rt.List[int64], rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[*rt.List[int64]](e)
	}
	info, e := backendOf(fr).Lstat(path)
	if e != nil {
		return failed[*rt.List[int64]](e)
	}
	return ok(infoList(info))
}

// CreateSymlink is `fs.create_symlink`: a symlink at path whose target is
// target, as written. The target need not exist.
func CreateSymlink(fr *rt.Frame, path, target string) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Unit](e)
	}
	if e := validPath(target); e != nil {
		return failed[rt.Unit](e)
	}
	return unit(backendOf(fr).Symlink(path, target))
}

// ReadLink is `fs.read_link`: the target of the symlink at path.
func ReadLink(fr *rt.Frame, path string) rt.Result[string, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[string](e)
	}
	target, e := backendOf(fr).ReadLink(path)
	if e != nil {
		return failed[string](e)
	}
	return ok(target)
}

// SetMode is `fs.set_mode`: the permission bits, 0 to 0o777, of what path
// names. Any other number is EINVAL, as chmod answers a mode it cannot
// set.
func SetMode(fr *rt.Frame, path string, mode int64) rt.Result[rt.Unit, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.Unit](e)
	}
	if mode < 0 || mode > 0o777 {
		return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIOOther, Operation: "chmod", Path: path, Code: "EINVAL"})
	}
	return unit(backendOf(fr).Chmod(path, mode))
}

// OpenFile is `fs.open_file`: a File on path, opened as the Access at
// position access says.
func OpenFile(fr *rt.Frame, path string, access int64) rt.Result[rt.File, rt.IOError] {
	if e := validPath(path); e != nil {
		return failed[rt.File](e)
	}
	if access < int64(AccessRead) || access > int64(AccessCreate) {
		rt.Trap("fs.open_file: access " + strconv.FormatInt(access, 10) + " is not an Access")
	}
	src, e := backendOf(fr).OpenFile(path, Access(access))
	if e != nil {
		return failed[rt.File](e)
	}
	return ok(rt.NewFile(src))
}

// within is a backend `fs.within` confines a call to, with its release.
type within struct {
	Backend
	once    sync.Once
	release func()
}

// Within is std/fs_host's `within`, which `fs.within` calls: the file
// system confined to dir, which the call's body runs against
// (`fs_host.run_in`) and which Release lets go of afterwards. It is held,
// for a debugger's replay, when the file system it confines is (a sandbox).
func Within(fr *rt.Frame, dir string) rt.Result[rt.FileSystem, rt.IOError] {
	if e := validPath(dir); e != nil {
		return failed[rt.FileSystem](e)
	}
	b, release, e := backendOf(fr).Within(dir)
	if e != nil {
		return failed[rt.FileSystem](e)
	}
	return ok(rt.NewFileSystem(&within{Backend: b, release: release}, fr.FilesHeld()))
}

// Release is std/fs_host's `release`: a file system `fs.within` made is let
// go of (its *os.Root closed). Anything else is left alone.
func Release(fs rt.FileSystem) rt.Unit {
	if w, ok := fs.Files().(*within); ok {
		w.once.Do(w.release)
	}
	return rt.Unit{}
}

// TempDir is `fs.temp_dir`.
func TempDir(fr *rt.Frame) string { return backendOf(fr).TempDir() }

// MakeTempDir is std/fs_host's `make_temp_dir`, which `fs.with_temp_dir`
// calls: a fresh directory under the temporary directory.
func MakeTempDir(fr *rt.Frame) rt.Result[string, rt.IOError] {
	dir, e := backendOf(fr).MakeTempDir()
	if e != nil {
		return failed[string](e)
	}
	return ok(dir)
}

// CurrentDir is `fs.current_dir`.
func CurrentDir(fr *rt.Frame) rt.Result[string, rt.IOError] {
	dir, e := backendOf(fr).CurrentDir()
	if e != nil {
		return failed[string](e)
	}
	return ok(dir)
}

// joinPath is dir and name joined with one `/`.
func joinPath(dir, name string) string {
	if dir == "" || dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}
