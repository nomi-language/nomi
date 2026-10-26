package vmhost_test

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// loggedFiles is an embedder's FileSystem: it records each call it answers
// and hands it to inner, and refuses any path under /secret with an
// io.Error it picks (rt.IOFailure).
type loggedFiles struct {
	inner vmhost.FileSystem
	mu    sync.Mutex
	calls []string
}

func (l *loggedFiles) log(op, p string) error {
	l.mu.Lock()
	l.calls = append(l.calls, op+" "+p)
	l.mu.Unlock()
	if strings.HasPrefix(p, "/secret") {
		return rt.IOFailure{E: rt.IOError{Tag: rt.TagIOPermissionDenied, Path: p}}
	}
	return nil
}

func (l *loggedFiles) Open(p string) (io.ReadCloser, error) {
	if err := l.log("open", p); err != nil {
		return nil, err
	}
	return l.inner.Open(p)
}

func (l *loggedFiles) Create(p string, mode vmhost.WriteMode) (io.WriteCloser, error) {
	if err := l.log("create", p); err != nil {
		return nil, err
	}
	return l.inner.Create(p, mode)
}

func (l *loggedFiles) OpenFile(p string, access vmhost.Access) (vmhost.File, error) {
	if err := l.log("open_file", p); err != nil {
		return nil, err
	}
	return l.inner.OpenFile(p, access)
}

func (l *loggedFiles) List(dir string) ([]vmhost.DirEntry, error) {
	if err := l.log("list", dir); err != nil {
		return nil, err
	}
	return l.inner.List(dir)
}

func (l *loggedFiles) CreateDir(p string, parents bool) error {
	if err := l.log("create_dir", p); err != nil {
		return err
	}
	return l.inner.CreateDir(p, parents)
}

func (l *loggedFiles) Remove(p string) error {
	if err := l.log("remove", p); err != nil {
		return err
	}
	return l.inner.Remove(p)
}

func (l *loggedFiles) RemoveAll(p string) error {
	if err := l.log("remove_all", p); err != nil {
		return err
	}
	return l.inner.RemoveAll(p)
}

func (l *loggedFiles) Rename(from, to string) error {
	if err := l.log("rename", from); err != nil {
		return err
	}
	return l.inner.Rename(from, to)
}

func (l *loggedFiles) Stat(p string) (vmhost.FileInfo, error) {
	if err := l.log("stat", p); err != nil {
		return vmhost.FileInfo{}, err
	}
	return l.inner.Stat(p)
}

func (l *loggedFiles) Lstat(p string) (vmhost.FileInfo, error) {
	if err := l.log("lstat", p); err != nil {
		return vmhost.FileInfo{}, err
	}
	return l.inner.Lstat(p)
}

func (l *loggedFiles) Symlink(p, target string) error {
	if err := l.log("symlink", p); err != nil {
		return err
	}
	return l.inner.Symlink(p, target)
}

func (l *loggedFiles) ReadLink(p string) (string, error) {
	if err := l.log("read_link", p); err != nil {
		return "", err
	}
	return l.inner.ReadLink(p)
}

func (l *loggedFiles) Chmod(p string, mode fs.FileMode) error {
	if err := l.log("chmod", p); err != nil {
		return err
	}
	return l.inner.Chmod(p, mode)
}

func (l *loggedFiles) CurrentDir() (string, error) { return l.inner.CurrentDir() }
func (l *loggedFiles) TempDir() string             { return l.inner.TempDir() }
func (l *loggedFiles) MakeTempDir() (string, error) {
	dir, err := l.inner.MakeTempDir()
	_ = l.log("make_temp_dir", dir)
	return dir, err
}

const embedFilesSrc = `import {
    std/fs
    std/io.{self, File}
    std/path
}

fn main() -> Result<Unit, io.Error> {
    io.print(fs.read_text("seed.txt"))
    try fs.with_writer("out.txt", mode: .ReplaceAtomically, fn w -> io.Writer.write_text(w, "written"))
    try fs.create_dir("d/e", parents: True)
    try fs.write_text("d/e/f.txt", "deep")
    io.print(fs.list("d") |> Result.map(fn es -> es |> Iter.map(fn e -> e.name) |> Iter.to_list()))
    io.print((try fs.metadata("d/e/f.txt")).size)
    io.print(fs.with_file("d/e/f.txt", fn f -> File.read_at(f, 1, max: 2)) == Ok(Some(String.to_bytes("ee"))))
    try fs.rename("d/e/f.txt", "moved.txt")
    try fs.remove_all("d")
    io.print(fs.read_text("missing.txt"))
    io.print(fs.read_text("/secret/key"))
    io.print(fs.within("/work", fn -> Ok(1)))
    made = fs.with_temp_dir(fn dir -> fs.write_text(path.join(dir, "t.txt"), "t"))
    io.print(made == Ok(Unit))
    Ok(Unit)
}
`

// An embedder's own FileSystem, here one that logs each call and refuses a
// directory, is what every std/fs call reaches: reads, an atomic write,
// directories, metadata, positional reads, renames, removal, errors it
// returns as Go errors and as an io.Error it picks, `fs.within` (which it
// does not serve) and a temporary directory.
func TestEmbedFiles_AnEmbeddersFileSystemAnswersStdFs(t *testing.T) {
	memory := vmhost.NewMemoryFiles(map[string][]byte{"seed.txt": []byte("seeded")})
	logged := &loggedFiles{inner: memory}
	p, err := vmhost.LoadSource("main", embedFilesSrc, vmhost.WithFileSystem(logged))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	want := "Ok(seeded)\n" +
		"Ok([e])\n" +
		"4\n" +
		"True\n" +
		"Err(missing.txt: not found)\n" +
		"Err(/secret/key: permission denied)\n" +
		"Err(fs.within is not supported on this program's file system)\n" +
		"True\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	files := memory.Files()
	if string(files["out.txt"]) != "written" || string(files["moved.txt"]) != "deep" || len(files) != 3 {
		t.Fatalf("the files are %v", files)
	}
	for _, call := range []string{"open seed.txt", "create out.txt", "create_dir d/e", "list d", "stat d/e/f.txt",
		"open_file d/e/f.txt", "rename d/e/f.txt", "remove_all d", "open /secret/key", "make_temp_dir /tmp/"} {
		if !slices.ContainsFunc(logged.calls, func(c string) bool { return strings.HasPrefix(c, call) }) {
			t.Errorf("no call %q reached the embedder's file system: %v", call, logged.calls)
		}
	}
}

// DiskFiles is the disk as a FileSystem an embedder can wrap; a program
// given it wrapped reaches the disk through the wrapper.
func TestEmbedFiles_DiskFilesWrapped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `import {
    std/fs
    std/io
}

fn main() -> Result<Unit, io.Error> {
    text = try fs.read_text("` + filepath.ToSlash(filepath.Join(dir, "in.txt")) + `")
    try fs.write_text("` + filepath.ToSlash(filepath.Join(dir, "out.txt")) + `", text + "!")
    m = try fs.metadata("` + filepath.ToSlash(filepath.Join(dir, "out.txt")) + `")
    io.print(m.size)
    Ok(Unit)
}
`
	logged := &loggedFiles{inner: vmhost.DiskFiles()}
	p, err := vmhost.LoadSource("main", src, vmhost.WithFileSystem(logged))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if out.String() != "5\n" {
		t.Fatalf("output %q", out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(data) != "disk!" {
		t.Fatalf("out.txt holds %q, %v", data, err)
	}
	if len(logged.calls) != 3 {
		t.Fatalf("calls through the wrapper: %v", logged.calls)
	}
	info, err := logged.Stat(filepath.ToSlash(filepath.Join(dir, "out.txt")))
	if err != nil || info.Kind != vmhost.KindFile || info.Size != 5 || time.Since(info.Modified) > time.Hour {
		t.Fatalf("Stat through DiskFiles answered %+v, %v", info, err)
	}
}
