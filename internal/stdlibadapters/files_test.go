package stdlibadapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/rt"
)

// TestRtFuncs_HandlesAndSandboxes holds the stream rows over handles, the
// std/fs rows that answer a handle or read the machine's state, and the
// sandbox rows to what they do, through the generated adapters: a writer
// over a real file buffers until it closes, a reader reads it back, an
// atomic write that is given up leaves the old file, a closed handle
// answers Closed, and the same calls on a frame that reaches a sandbox
// touch the sandbox and not the disk.
func TestRtFuncs_HandlesAndSandboxes(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	ok := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v := call(fr, key, args...)
		r, isRec := v.(*rt.Record)
		if !isRec || r.Variant().Name != "Ok" {
			t.Fatalf("%s answered %s, want an Ok", key, canonText(v, false))
		}
		return r.Field(0)
	}
	text := func(v any) string { return canonText(v, false) }

	disk := rt.NewFrame(context.Background())
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	w := ok(disk, "fs_host.create", out, int64(0))
	ok(disk, "io.Writer.write_text", w, "one\n")
	ok(disk, "io.Writer.write", w, rt.Bytes("two\n"))
	if data, _ := os.ReadFile(out); len(data) != 0 {
		t.Errorf("a file writer wrote %q before a flush; it buffers", data)
	}
	ok(disk, "io.Writer.flush", w)
	ok(disk, "io.Writer.close", w)
	if got := text(call(disk, "io.Writer.close", w)); got != "results.Result/4.Err{0=io.Error/4.Closed{}}" {
		t.Errorf("a second close answered %s, want Closed", got)
	}
	if data, _ := os.ReadFile(out); string(data) != "one\ntwo\n" {
		t.Errorf("the file holds %q", data)
	}

	r := ok(disk, "fs_host.open", out)
	if got := text(ok(disk, "io.read_up_to", r, int64(2))); got != `maybe.Maybe/4.Some{0=Bytes("on")}` {
		t.Errorf("read_up_to answered %s", got)
	}
	if got := text(ok(disk, "io.Reader.read_all_text", r)); got != `String("e\ntwo\n")` {
		t.Errorf("read_all_text answered %s", got)
	}
	ok(disk, "io.Reader.close", r)
	if got := text(call(disk, "io.Reader.read_all", r)); got != "results.Result/4.Err{0=io.Error/4.Closed{}}" {
		t.Errorf("a read after close answered %s, want Closed", got)
	}

	// An atomic write that is given up leaves the old contents and nothing
	// beside them.
	w = ok(disk, "fs_host.create", out, int64(1))
	ok(disk, "io.Writer.write_text", w, "half")
	call(disk, "fs_host.discard", w)
	if data, _ := os.ReadFile(out); string(data) != "one\ntwo\n" {
		t.Errorf("a discarded atomic write left %q", data)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a discarded atomic write left %d entries in its directory", len(entries))
	}

	held := call(disk, "io.Reader.from_text", "a\nb")
	sink := ok(disk, "fs_host.create", filepath.Join(dir, "copy.txt"), int64(0))
	if got := text(ok(disk, "io.Reader.copy_to", held, sink)); got != "Int(3)" {
		t.Errorf("copy_to answered %s", got)
	}
	ok(disk, "io.Writer.close", sink)
	if got := text(ok(disk, "io.Reader.read_all", call(disk, "io.Reader.from_bytes", rt.Bytes("xy")))); got != `Bytes("xy")` {
		t.Errorf("read_all of from_bytes answered %s", got)
	}

	if got := call(disk, "fs_host.temp_dir").(string); got != filepath.ToSlash(os.TempDir()) {
		t.Errorf("temp_dir answered %q", got)
	}
	tmp := ok(disk, "fs_host.make_temp_dir").(string)
	if info, err := os.Stat(tmp); err != nil || !info.IsDir() {
		t.Errorf("make_temp_dir answered %q, which is not a directory: %v", tmp, err)
	}
	os.RemoveAll(tmp)
	if wd, _ := os.Getwd(); ok(disk, "fs_host.current_dir").(string) != filepath.ToSlash(wd) {
		t.Errorf("current_dir is not the working directory %q", wd)
	}

	// The same rows on a frame that reaches a sandbox.
	rb := newRecordBuilder()
	files := rt.MapOf(rt.Hash, rt.Equal, []rt.MapEntry[any, any]{{Key: "in.txt", Val: rt.Bytes("seed")}})
	links := rt.MapOf(rt.Hash, rt.Equal, []rt.MapEntry[any, any]{{Key: "to-in.txt", Val: "in.txt"}})
	sandbox := call(disk, "fs_host.new_sandbox", "/work", files, rtList("empty"), links).(rt.FileSystem)
	call(disk, "fs_host.sandbox_fail", sandbox, "/work/locked.txt", int64(stdio.OpCreate),
		rb.FieldVariant("io.Error", "PermissionDenied", "path", "locked.txt"), int64(0))
	boxed := rt.EnterFiles(disk, sandbox)
	if got := text(ok(boxed, "fs_host.read_text", "in.txt")); got != `String("seed")` {
		t.Errorf("a sandboxed read answered %s", got)
	}
	leaked := ok(boxed, "fs_host.create", "/work/empty/new.txt", int64(0))
	ok(boxed, "io.Writer.write_text", leaked, "x")
	if got := text(call(boxed, "fs_host.create", "locked.txt", int64(0))); got != `results.Result/4.Err{0=io.Error/4.PermissionDenied{path=String("locked.txt")}}` {
		t.Errorf("an injected failure answered %s", got)
	}
	if got := ok(boxed, "fs_host.current_dir").(string); got != "/work" {
		t.Errorf("a sandbox's current_dir is %q", got)
	}
	if got := text(call(disk, "fs_host.sandbox_open", sandbox)); got != `[String("/work/empty/new.txt")]` {
		t.Errorf("sandbox_open answered %s", got)
	}
	ok(boxed, "io.Writer.close", leaked)
	if got := text(call(disk, "fs_host.sandbox_files", sandbox)); got != `{String("/work/empty/new.txt")=>Bytes("x"), String("/work/in.txt")=>Bytes("seed")}` {
		t.Errorf("sandbox_files answered %s", got)
	}
	if got := text(call(disk, "fs_host.sandbox_log", sandbox)); got != `[String("read /work/in.txt"), String("write /work/empty/new.txt"), String("write /work/locked.txt")]` {
		t.Errorf("sandbox_log answered %s", got)
	}
	if _, err := os.Stat("/work/empty/new.txt"); err == nil {
		t.Error("a sandboxed write reached the disk")
	}
	if got := text(ok(boxed, "fs_host.read_text", "to-in.txt")); got != `String("seed")` {
		t.Errorf("a read through a seeded link answered %s", got)
	}
}

// TestRtFuncs_FilesAndWithin holds the File rows, link_metadata of a link,
// and fs.within's crossings to what they do through the generated adapters:
// a File reads and writes at a position on the disk, a write past the end
// fills the gap, a handle opened to read refuses a write, a closed one
// answers Closed, and a frame that enters what `within` answers reaches
// only the directory, until `release`.
func TestRtFuncs_FilesAndWithin(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	text := func(v any) string { return canonText(v, false) }
	ok := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v := call(fr, key, args...)
		r, isRec := v.(*rt.Record)
		if !isRec || r.Variant().Name != "Ok" {
			t.Fatalf("%s answered %s, want an Ok", key, text(v))
		}
		return r.Field(0)
	}

	disk := rt.NewFrame(context.Background())
	dir := filepath.ToSlash(t.TempDir())
	p := dir + "/f.bin"
	f := ok(disk, "fs_host.open_file", p, int64(2))
	ok(disk, "io.File.write", f, rt.Bytes("hello"))
	if got := text(ok(disk, "io.File.position", f)); got != "Int(5)" {
		t.Errorf("position after a write is %s", got)
	}
	ok(disk, "io.File.write_at", f, int64(7), rt.Bytes("!"))
	if data, _ := os.ReadFile(p); string(data) != "hello\x00\x00!" {
		t.Errorf("the file holds %q; a File writes through, filling a gap with zeros", data)
	}
	ok(disk, "io.File.seek", f, int64(1))
	if got := text(ok(disk, "io.file_read", f, int64(3))); got != `maybe.Maybe/4.Some{0=Bytes("ell")}` {
		t.Errorf("file_read answered %s", got)
	}
	if got := text(ok(disk, "io.File.read_at", f, int64(8), int64(4))); got != "maybe.Maybe/4.None{}" {
		t.Errorf("read_at the end answered %s", got)
	}
	ok(disk, "io.File.truncate", f, int64(2))
	if got := text(ok(disk, "io.File.size", f)); got != "Int(2)" {
		t.Errorf("size after truncate is %s", got)
	}
	ok(disk, "io.File.close", f)
	if got := text(call(disk, "io.File.close", f)); got != "results.Result/4.Err{0=io.Error/4.Closed{}}" {
		t.Errorf("a second close answered %s", got)
	}
	ro := ok(disk, "fs_host.open_file", p, int64(0))
	if got := text(call(disk, "io.File.write", ro, rt.Bytes("x"))); got != `results.Result/4.Err{0=io.Error/4.PermissionDenied{path=String("`+p+`")}}` {
		t.Errorf("a write to a read-only File answered %s", got)
	}
	ok(disk, "io.File.close", ro)

	if err := os.Symlink("f.bin", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if got := text(ok(disk, "fs_host.link_metadata", dir+"/link")); !strings.HasPrefix(got, "[Int(2), Int(5), ") {
		t.Errorf("link_metadata of a link answered %s, want kind 2 (Symlink) and size 5", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "index.txt"), []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}
	confined := ok(disk, "fs_host.within", dir+"/site").(rt.FileSystem)
	if confined.Held() {
		t.Error("a within over the disk is held; a replay would write the disk twice")
	}
	inside := rt.EnterFiles(disk, confined)
	if got := text(ok(inside, "fs_host.read_text", "index.txt")); got != `String("i")` {
		t.Errorf("a read inside answered %s", got)
	}
	if got := text(call(inside, "fs_host.read_text", "../secret.txt")); !strings.Contains(got, "InvalidPath") {
		t.Errorf("a read outside answered %s, want InvalidPath", got)
	}
	call(disk, "fs_host.release", confined)
	if got := text(call(disk, "fs_host.within", dir+"/none")); !strings.Contains(got, "NotFound") {
		t.Errorf("within of a missing directory answered %s", got)
	}
}

// TestRtFuncs_MemoryWriterCollects: the writer `Writer.collect` hands its
// body holds every byte written to it, through the generated adapters, and
// keeps them after it is closed, when a write is Closed.
func TestRtFuncs_MemoryWriterCollects(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(context.Background())
	call := func(key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	w := call("io.memory_writer")
	call("io.Writer.write_text", w, "one ")
	call("io.Writer.write", w, rt.Bytes("two"))
	call("io.Writer.close", w)
	if got := canonText(call("io.collected", w), false); got != canonText(rt.Bytes("one two"), false) {
		t.Errorf("collected %s, want one two", got)
	}
	if got := canonText(call("io.Writer.write_text", w, "late"), false); got != "results.Result/4.Err{0=io.Error/4.Closed{}}" {
		t.Errorf("a write after close answered %s, want Closed", got)
	}
}
