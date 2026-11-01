//go:build !(js && wasm)

package stdio

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// windowsOps answers as Windows does: a missing name whose directory exists
// is ERROR_FILE_NOT_FOUND (2), a missing directory above it or a file there
// is ERROR_PATH_NOT_FOUND (3), and removing a directory with entries is
// ERROR_DIR_NOT_EMPTY (145). Over it, with windowsCodes set, the disk
// backend is held to the answers it gives on this system.
type windowsOps struct{ hostOS }

func (windowsOps) code(name string, err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		return err
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	switch errno {
	case syscall.ENOENT:
		if _, perr := os.Stat(filepath.Dir(name)); perr != nil {
			return &fs.PathError{Op: pe.Op, Path: pe.Path, Err: syscall.Errno(3)}
		}
		return &fs.PathError{Op: pe.Op, Path: pe.Path, Err: syscall.Errno(2)}
	case syscall.ENOTDIR:
		return &fs.PathError{Op: pe.Op, Path: pe.Path, Err: syscall.Errno(3)}
	case syscall.ENOTEMPTY, syscall.EEXIST:
		return &fs.PathError{Op: pe.Op, Path: pe.Path, Err: syscall.Errno(145)}
	}
	return err
}

func (w windowsOps) Open(n string) (*os.File, error) {
	f, err := w.hostOS.Open(n)
	return f, w.code(n, err)
}

func (w windowsOps) OpenFile(n string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := w.hostOS.OpenFile(n, flag, perm)
	return f, w.code(n, err)
}

func (w windowsOps) Mkdir(n string, perm fs.FileMode) error {
	return w.code(n, w.hostOS.Mkdir(n, perm))
}

func (w windowsOps) Remove(n string) error { return w.code(n, w.hostOS.Remove(n)) }

// sizeless is a symlink's FileInfo as Windows answers it: size 0.
type sizeless struct{ fs.FileInfo }

func (sizeless) Size() int64 { return 0 }

func (w windowsOps) Lstat(n string) (fs.FileInfo, error) {
	info, err := w.hostOS.Lstat(n)
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return sizeless{info}, nil
	}
	return info, w.code(n, err)
}

func (w windowsOps) Stat(n string) (fs.FileInfo, error) {
	info, err := w.hostOS.Stat(n)
	return info, w.code(n, err)
}

// TestDiskAnswersWindowsCodesAsUnixErrnos holds the disk backend, given the
// codes Windows reports, to the io.Errors the in-memory backend answers for
// the same failures: Windows' one code for a missing directory and for a
// file where a directory should be is told apart by looking.
func TestDiskAnswersWindowsCodesAsUnixErrnos(t *testing.T) {
	windowsCodes = true
	t.Cleanup(func() { windowsCodes = false })

	dir := filepath.ToSlash(t.TempDir())
	backends := map[string]struct {
		b    Backend
		root string
	}{
		"disk":   {&disk{ops: windowsOps{}}, dir},
		"memory": {NewMemory("/work"), "/work"},
	}
	for name, tc := range backends {
		t.Run(name, func(t *testing.T) {
			b, root := tc.b, tc.root
			if e := writeAll(b, root+"/f.txt", "f"); e != nil {
				t.Fatalf("write f.txt: %+v", e)
			}
			if e := b.CreateDir(root+"/full", false); e != nil {
				t.Fatalf("create_dir: %+v", e)
			}
			if e := writeAll(b, root+"/full/x", "x"); e != nil {
				t.Fatalf("write full/x: %+v", e)
			}
			_, openMissing := b.Open(root + "/none/x.txt")
			_, openUnder := b.Open(root + "/f.txt/x")
			_, openLeaf := b.Open(root + "/gone.txt")
			_, create := b.Create(root+"/none/x.txt", Replace)
			if e := b.Symlink(root+"/link", "f.txt"); e != nil {
				t.Fatalf("symlink: %+v", e)
			}
			if info, e := b.Lstat(root + "/link"); e != nil || info.Kind != KindSymlink || info.Size != 5 {
				t.Errorf("link_metadata of a link to f.txt: %+v %+v, want a Symlink of size 5", info, e)
			}
			cases := []struct {
				what      string
				got, want *rt.IOError
			}{
				{"open under a missing directory", openMissing, errOf(rt.TagIONotFound, root+"/none/x.txt")},
				{"open under a file", openUnder, errOf(rt.TagIONotADirectory, root+"/f.txt/x")},
				{"open a missing file", openLeaf, errOf(rt.TagIONotFound, root+"/gone.txt")},
				{"write under a missing directory", create, errOf(rt.TagIONotFound, root+"/none")},
				{"mkdir under a missing directory", b.CreateDir(root+"/none/sub", false), errOf(rt.TagIONotFound, root+"/none")},
				{"remove a directory with entries", b.Remove(root + "/full"), errOf(rt.TagIODirectoryNotEmpty, root+"/full")},
			}
			for _, c := range cases {
				if c.got == nil || *c.got != *c.want {
					t.Errorf("%s: got %+v, want %+v", c.what, c.got, c.want)
				}
			}
		})
	}
}

// TestWindowsCodesNameTheirUnixErrno holds the codes Windows reports where
// Unix names its own errno to the io.Error a Unix system answers.
func TestWindowsCodesNameTheirUnixErrno(t *testing.T) {
	windowsCodes = true
	t.Cleanup(func() { windowsCodes = false })

	for _, c := range []struct {
		code syscall.Errno
		want rt.IOError
	}{
		{1921, rt.IOError{Tag: rt.TagIOOther, Operation: "open", Path: "a", Code: "ELOOP"}},
		{4390, rt.IOError{Tag: rt.TagIOOther, Operation: "readlink", Path: "a", Code: "EINVAL"}},
		{123, rt.IOError{Tag: rt.TagIOOther, Operation: "mkdir", Path: "a", Code: "EINVAL"}},
		{145, rt.IOError{Tag: rt.TagIODirectoryNotEmpty, Path: "a"}},
		{267, rt.IOError{Tag: rt.TagIONotADirectory, Path: "a"}},
	} {
		got := Classify(c.want.Operation, "a", &fs.PathError{Op: c.want.Operation, Path: "a", Err: c.code})
		if got != c.want {
			t.Errorf("Windows code %d: got %+v, want %+v", c.code, got, c.want)
		}
	}
}
