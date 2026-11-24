package stdio

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/rt"
)

// The std/fs crossings, std/fs/host's host functions. Each but Here acts
// through the file system it is handed (an rt.FileSystem): it validates its
// paths, takes the backend the value wraps (reach), and answers what std/fs
// builds its values from. Here is the one that obtains a file system by no
// handle, the frame's, which every path-taking std/fs function hands on and
// `fs.open_dir` narrows. internal/stdlibbindings binds each as an RtFuncs
// row: Here in the `files` effect class, the others held.

func failed[T any](e *rt.IOError) rt.Result[T, rt.IOError] { return rt.Err[T](*e) }

func ok[T any](v T) rt.Result[T, rt.IOError] { return rt.Ok[T, rt.IOError](v) }

func unit(e *rt.IOError) rt.Result[rt.Unit, rt.IOError] {
	if e != nil {
		return failed[rt.Unit](e)
	}
	return ok(rt.Unit{})
}

// Here is std/fs/host's `here`: the file system the frame reaches, the
// innermost `std::fs::testing.run` or `fs.within` replacement, else the
// machine's, else this build's. It is held when the frame's is.
func Here(fr *rt.Frame) rt.FileSystem { return rt.NewFileSystem(backendOf(fr), fr.FilesHeld()) }

// reach is the backend files wraps, once each of paths is valid: Closed
// for a directory `Dir.close` let go of.
func reach(files rt.FileSystem, paths ...string) (Backend, *rt.IOError) {
	for _, p := range paths {
		if e := validPath(p); e != nil {
			return nil, e
		}
	}
	switch b := files.Files().(type) {
	case *within:
		if b.closed.Load() {
			return nil, &rt.IOError{Tag: rt.TagIOClosed}
		}
		return b, nil
	case Backend:
		return b, nil
	}
	return defaultBackend(), nil
}

// readAll opens path and reads all of it.
func readAll(fr *rt.Frame, files rt.FileSystem, path string) ([]byte, *rt.IOError) {
	b, e := reach(files, path)
	if e != nil {
		return nil, e
	}
	src, e := b.Open(path)
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
func Read(fr *rt.Frame, files rt.FileSystem, path string) rt.Result[rt.Bytes, rt.IOError] {
	data, e := readAll(fr, files, path)
	if e != nil {
		return failed[rt.Bytes](e)
	}
	return ok(rt.Bytes(data))
}

// ReadText is `fs.read_text`: the whole file as text, or InvalidText with
// the offset of the first byte that is not UTF-8.
func ReadText(fr *rt.Frame, files rt.FileSystem, path string) rt.Result[string, rt.IOError] {
	data, e := readAll(fr, files, path)
	if e != nil {
		return failed[string](e)
	}
	if !utf8.Valid(data) {
		return rt.Err[string](rt.IOError{Tag: rt.TagIOInvalidText, Path: path,
			Offset: rt.SmallInt(int64(rt.InvalidUTF8Offset(string(data))))})
	}
	return ok(string(data))
}

// Write is `fs.write` and `fs.write_text`: data into path in mode. A
// failed write gives the write up, so with ReplaceAtomically the old file is
// untouched.
func Write(fr *rt.Frame, files rt.FileSystem, path string, data rt.Bytes, mode int64) rt.Result[rt.Unit, rt.IOError] {
	created := Create(fr, files, path, mode)
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
func Open(files rt.FileSystem, path string) rt.Result[rt.Reader, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Reader](e)
	}
	src, e := b.Open(path)
	if e != nil {
		return failed[rt.Reader](e)
	}
	return ok(rt.NewReader(src))
}

// Create is `fs.create`: a Writer over the file, in mode.
func Create(fr *rt.Frame, files rt.FileSystem, path string, mode int64) rt.Result[rt.Writer, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Writer](e)
	}
	if mode < int64(Replace) || mode > int64(New) {
		rt.Trap("fs.create: write mode " + strconv.FormatInt(mode, 10) + " is not a WriteMode")
	}
	sink, e := b.Create(path, WriteMode(mode))
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
func List(files rt.FileSystem, dir string) rt.Result[*rt.List[string], rt.IOError] {
	b, e := reach(files, dir)
	if e != nil {
		return failed[*rt.List[string]](e)
	}
	entries, e := b.List(dir)
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
func CreateDir(files rt.FileSystem, path string, parents bool) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.CreateDir(path, parents))
}

// Remove is `fs.remove`: a file or an empty directory.
func Remove(files rt.FileSystem, path string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.Remove(path))
}

// SyncDir is `fs.sync_dir`: the entries of the directory are durable.
func SyncDir(files rt.FileSystem, path string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.SyncDir(path))
}

// RemoveAll is `fs.remove_all`: Ok when nothing is at path.
func RemoveAll(files rt.FileSystem, path string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.RemoveAll(path))
}

// Rename is `fs.rename`, which replaces a file at to.
func Rename(files rt.FileSystem, from, to string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, from, to)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.Rename(from, to))
}

// Copy is `fs.copy`: the file at from into to, replacing what is there.
func Copy(fr *rt.Frame, files rt.FileSystem, from, to string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, from, to)
	if e != nil {
		return failed[rt.Unit](e)
	}
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

// Metadata is `fs.metadata`, following symlinks: Info's fields in order
// (infoList), which std/fs builds a Metadata from.
func Metadata(files rt.FileSystem, path string) rt.Result[*rt.List[rt.Option[rt.Int]], rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[*rt.List[rt.Option[rt.Int]]](e)
	}
	info, e := b.Stat(path)
	if e != nil {
		return failed[*rt.List[rt.Option[rt.Int]]](e)
	}
	return ok(infoList(info))
}

// infoList is info as the metadata crossings carry it: the kind, the size,
// the modification time, the mode, the access, status change and birth
// times, the link count, the device, the inode, and the owner's user and
// group. A time is in nanoseconds since the Unix epoch, an Int of any size
// (rt.InstantFromTime). A field the file system does not keep is None.
func infoList(info Info) *rt.List[rt.Option[rt.Int]] {
	some := func(v rt.Int) rt.Option[rt.Int] { return rt.Some(v) }
	count := func(v uint64) rt.Int { return rt.IntOf(rt.IntFromUint64(v)) }
	when := func(t time.Time) rt.Option[rt.Int] {
		if t.IsZero() {
			return rt.None[rt.Int]()
		}
		return some(rt.InstantFromTime(t))
	}
	fields := []rt.Option[rt.Int]{
		some(rt.SmallInt(int64(info.Kind))), some(rt.SmallInt(info.Size)),
		some(rt.InstantFromTime(info.Modified)), some(rt.SmallInt(info.Mode)),
		when(info.Accessed), when(info.Changed), when(info.Created),
		rt.None[rt.Int](), rt.None[rt.Int](), rt.None[rt.Int](), rt.None[rt.Int](), rt.None[rt.Int](),
	}
	if info.Links != 0 {
		fields[7] = some(count(info.Links))
	}
	if info.ID != nil {
		fields[8], fields[9] = some(count(info.ID.Device)), some(count(info.ID.Inode))
	}
	if info.Owner != nil {
		fields[10], fields[11] = some(count(info.Owner.User)), some(count(info.Owner.Group))
	}
	var out *rt.List[rt.Option[rt.Int]]
	for i := len(fields) - 1; i >= 0; i-- {
		out = rt.Cons(fields[i], out)
	}
	return out
}

// LinkMetadata is `fs.link_metadata`: Metadata's fields, describing a
// symlink at path as itself.
func LinkMetadata(files rt.FileSystem, path string) rt.Result[*rt.List[rt.Option[rt.Int]], rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[*rt.List[rt.Option[rt.Int]]](e)
	}
	info, e := b.Lstat(path)
	if e != nil {
		return failed[*rt.List[rt.Option[rt.Int]]](e)
	}
	return ok(infoList(info))
}

// SetTimes is `fs.set_times`: the modification and access times, in
// nanoseconds since the Unix epoch, of what path names; None leaves one as
// it is.
func SetTimes(files rt.FileSystem, path string, modified, accessed rt.Option[rt.Int]) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	at := func(o rt.Option[rt.Int]) *time.Time {
		if o.Tag != rt.TagSome {
			return nil
		}
		t := rt.InstantTimeOf(o.Some)
		return &t
	}
	return unit(b.Chtimes(path, at(modified), at(accessed)))
}

// Touch is `fs.touch`: the modification and access times of what path
// names set to now, and when nothing is there, an empty file made first,
// whose open names a missing directory above it.
func Touch(files rt.FileSystem, path string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	now := time.Now()
	e = b.Chtimes(path, &now, &now)
	if e == nil || e.Tag != rt.TagIONotFound {
		return unit(e)
	}
	src, e := b.OpenFile(path, AccessCreate)
	if e != nil {
		return failed[rt.Unit](e)
	}
	if err := src.Close(); err != nil {
		return rt.Err[rt.Unit](Classify("close", path, err))
	}
	return unit(b.Chtimes(path, &now, &now))
}

// CreateLink is `fs.create_link`: path as a second name of the file at
// target.
func CreateLink(files rt.FileSystem, path, target string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path, target)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.Link(path, target))
}

// SetOwner is `fs.set_owner`, and `fs.set_link_owner` when follow is not
// set: the numeric user and group that own what path names. A number past
// what a Unix id holds (32 bits) is EINVAL, as chown answers one.
func SetOwner(files rt.FileSystem, path string, user, group rt.Int, follow bool) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	u, g := user.Saturated(), group.Saturated()
	if u < 0 || u > math.MaxUint32 || g < 0 || g > math.MaxUint32 {
		return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIOOther, Operation: "chown", Path: path, Code: "EINVAL"})
	}
	return unit(b.Chown(path, uint64(u), uint64(g), follow))
}

// DiskUsageOf is `fs.disk_usage`: the total, free and available bytes of
// the file system path is on.
func DiskUsageOf(files rt.FileSystem, path string) rt.Result[*rt.List[rt.Int], rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[*rt.List[rt.Int]](e)
	}
	u, e := b.DiskUsage(path)
	if e != nil {
		return failed[*rt.List[rt.Int]](e)
	}
	var out *rt.List[rt.Int]
	for _, v := range []uint64{u.Available, u.Free, u.Total} {
		out = rt.Cons(rt.IntOf(rt.IntFromUint64(v)), out)
	}
	return ok(out)
}

// OwnerName is `fs.user_name`, and `fs.group_name` when group is set: the
// name the system gives the number, if it gives one.
func OwnerName(files rt.FileSystem, id rt.Int, group bool) rt.Option[string] {
	b, e := reach(files)
	n := id.Saturated()
	if e != nil || n < 0 || n > math.MaxUint32 {
		return rt.None[string]()
	}
	name, found := b.OwnerName(uint64(n), group)
	if !found {
		return rt.None[string]()
	}
	return rt.Some(name)
}

// CreateSymlink is `fs.create_symlink`: a symlink at path whose target is
// target, as written. The target need not exist.
func CreateSymlink(files rt.FileSystem, path, target string) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path, target)
	if e != nil {
		return failed[rt.Unit](e)
	}
	return unit(b.Symlink(path, target))
}

// ReadLink is `fs.read_link`: the target of the symlink at path.
func ReadLink(files rt.FileSystem, path string) rt.Result[string, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[string](e)
	}
	target, e := b.ReadLink(path)
	if e != nil {
		return failed[string](e)
	}
	return ok(target)
}

// SetMode is `fs.set_mode`: the mode bits, 0 to 0o7777, of what path
// names, numbered as ModeBits numbers them. Any other number is EINVAL, as
// chmod answers a mode it cannot set.
func SetMode(files rt.FileSystem, path string, modeArg rt.Int) rt.Result[rt.Unit, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.Unit](e)
	}
	// A mode past int64 saturates, and is out of range as the bound is.
	mode := modeArg.Saturated()
	if mode < 0 || mode > 0o7777 {
		return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIOOther, Operation: "chmod", Path: path, Code: "EINVAL"})
	}
	return unit(b.Chmod(path, mode))
}

// OpenFile is `fs.open_file`: a File on path, opened as the Access at
// position access says.
func OpenFile(files rt.FileSystem, path string, access int64) rt.Result[rt.File, rt.IOError] {
	b, e := reach(files, path)
	if e != nil {
		return failed[rt.File](e)
	}
	if access < int64(AccessRead) || access > int64(AccessCreate) {
		rt.Trap("fs.open_file: access " + strconv.FormatInt(access, 10) + " is not an Access")
	}
	src, e := b.OpenFile(path, Access(access))
	if e != nil {
		return failed[rt.File](e)
	}
	return ok(rt.NewFile(src))
}

// within is a backend a `Dir` confines its calls to, with its release.
type within struct {
	Backend
	once    sync.Once
	closed  atomic.Bool
	release func()
}

// Within is std/fs/host's `within`, which `fs.open_dir` and `Dir.subdir`
// call: files confined to dir, which a Dir holds, `fs.within` runs a body
// against (`std::fs::host.run_in`), and Release lets go of. It is held, for
// a debugger's replay, when files is (a sandbox).
func Within(files rt.FileSystem, dir string) rt.Result[rt.FileSystem, rt.IOError] {
	b, e := reach(files, dir)
	if e != nil {
		return failed[rt.FileSystem](e)
	}
	inside, release, e := b.Within(dir)
	if e != nil {
		return failed[rt.FileSystem](e)
	}
	return ok(rt.NewFileSystem(&within{Backend: inside, release: release}, files.Held()))
}

// Release is std/fs/host's `release`: a file system `Within` made is let
// go of (its *os.Root closed), and every later call through it is Closed.
// Anything else is left alone.
func Release(fs rt.FileSystem) rt.Unit {
	if w, ok := fs.Files().(*within); ok {
		w.once.Do(func() {
			w.closed.Store(true)
			w.release()
		})
	}
	return rt.Unit{}
}

// TempDir is `fs.temp_dir`.
func TempDir(files rt.FileSystem) string {
	b, e := reach(files)
	if e != nil {
		return defaultBackend().TempDir()
	}
	return b.TempDir()
}

// MakeTempDir is std/fs/host's `make_temp_dir`, which `fs.make_temp_dir`
// calls: a fresh directory under the temporary directory.
func MakeTempDir(files rt.FileSystem) rt.Result[string, rt.IOError] {
	b, e := reach(files)
	if e != nil {
		return failed[string](e)
	}
	dir, e := b.MakeTempDir()
	if e != nil {
		return failed[string](e)
	}
	return ok(dir)
}

// MakeTemp is std/fs/host's `make_temp`, which `Dir.make_temp_dir` and
// `Dir.make_temp_file` call: a directory, or an empty file, made with a
// fresh name in the directory files reaches, and its name. The name is
// `nomi-` and 16 hexadecimal digits of the system's entropy, and the making
// fails when something is there, so a name is never one another caller
// holds; a taken one is drawn again.
func MakeTemp(fr *rt.Frame, files rt.FileSystem, directory bool) rt.Result[string, rt.IOError] {
	b, e := reach(files)
	if e != nil {
		return failed[string](e)
	}
	for tries := 0; ; tries++ {
		var raw [8]byte
		_, _ = rand.Read(raw[:])
		name := "nomi-" + hex.EncodeToString(raw[:])
		if directory {
			e = b.CreateDir(name, false)
		} else {
			e = createEmpty(fr, b, name)
		}
		if e == nil {
			return ok(name)
		}
		if e.Tag != rt.TagIOAlreadyExists || tries == 100 {
			return failed[string](e)
		}
	}
}

// createEmpty makes an empty file at path, failing when something is there.
func createEmpty(fr *rt.Frame, b Backend, path string) *rt.IOError {
	sink, e := b.Create(path, New)
	if e != nil {
		return e
	}
	if res := rt.WriterClose(fr, rt.NewWriter(sink)); res.Tag != rt.TagOk {
		return &res.Err
	}
	return nil
}

// CurrentDir is `fs.current_dir`.
func CurrentDir(files rt.FileSystem) rt.Result[string, rt.IOError] {
	b, e := reach(files)
	if e != nil {
		return failed[string](e)
	}
	dir, e := b.CurrentDir()
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
