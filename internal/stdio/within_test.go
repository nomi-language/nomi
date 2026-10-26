//go:build !(js && wasm)

package stdio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestWithinRefusesEscapes holds both backends' `fs.within` to one rule: a
// path that leaves the directory, by `..`, by a relative symlink, by an
// absolute symlink or by an absolute path, is InvalidPath with the escape
// reason, and a path that stays inside reads. On the disk the refusal is
// an *os.Root's, recognized by its text (pathEscapes), so this fails if Go
// changes the text.
func TestWithinRefusesEscapes(t *testing.T) {
	dir := filepath.ToSlash(t.TempDir())
	mem := NewMemory("/work")
	backends := map[string]struct {
		b    Backend
		root string
	}{
		"disk":   {OS, dir},
		"memory": {mem, "/work"},
	}
	for name, tc := range backends {
		t.Run(name, func(t *testing.T) {
			b, root := tc.b, tc.root
			site := root + "/site"
			if e := b.CreateDir(site, false); e != nil {
				t.Fatalf("create_dir: %+v", e)
			}
			for p, data := range map[string]string{root + "/secret.txt": "s", site + "/index.txt": "i"} {
				if e := writeAll(b, p, data); e != nil {
					t.Fatalf("write %s: %+v", p, e)
				}
			}
			for link, target := range map[string]string{site + "/out": "../secret.txt", site + "/abs": root + "/secret.txt", site + "/in": "index.txt"} {
				if e := b.Symlink(link, target); e != nil {
					t.Fatalf("symlink %s: %+v", link, e)
				}
			}
			inside, release, e := b.Within(site)
			if e != nil {
				t.Fatalf("within: %+v", e)
			}
			defer release()
			for _, p := range []string{"../secret.txt", "out", "abs", root + "/secret.txt", "./../secret.txt"} {
				_, e := inside.Open(p)
				if e == nil || e.Tag != rt.TagIOInvalidPath || e.Reason != escapeReason || e.Path != p {
					t.Errorf("open %q inside answered %+v, want InvalidPath escaping", p, e)
				}
			}
			for _, p := range []string{"index.txt", "in", site + "/index.txt", "./index.txt"} {
				src, e := inside.Open(p)
				if e != nil {
					t.Errorf("open %q inside answered %+v", p, e)
					continue
				}
				_ = src.Close()
			}
			if cwd, _ := inside.CurrentDir(); cwd != site {
				t.Errorf("current_dir inside is %q, want %q", cwd, site)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "index.txt")); err != nil {
		t.Fatal(err)
	}
}

func writeAll(b Backend, p, data string) *rt.IOError {
	sink, e := b.Create(p, Replace)
	if e != nil {
		return e
	}
	if _, err := sink.W.Write([]byte(data)); err != nil {
		return &rt.IOError{Tag: rt.TagIOOther, Path: p}
	}
	if sink.Close != nil {
		if err := sink.Close(); err != nil {
			return &rt.IOError{Tag: rt.TagIOOther, Path: p}
		}
	}
	return nil
}

// TestListMarksNamesThatAreNotUTF8: a listing crossing writes a name that is
// not UTF-8 as `!` and the name with U+FFFD, so `fs.list` and `fs.walk`
// hand the program no String that is not UTF-8. A memory file system can
// hold such a name on every operating system.
func TestListMarksNamesThatAreNotUTF8(t *testing.T) {
	m := NewMemory("/work")
	m.Put("d/ok.txt", nil)
	m.Put("d/b\xffd.txt", nil)
	fr := rt.EnterFiles(rt.NewFrame(context.Background()), rt.NewFileSystem(m, true))
	got := List(fr, "d")
	if got.Tag != rt.TagOk {
		t.Fatalf("list answered %+v", got.Err)
	}
	var names []string
	for l := got.Ok; l != nil; l = l.Tail {
		names = append(names, l.Head)
	}
	if len(names) != 2 || names[0] != "!b�d.txt" || names[1] != "fok.txt" {
		t.Errorf("list answered %q", names)
	}
}
