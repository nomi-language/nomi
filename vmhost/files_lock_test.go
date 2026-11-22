package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

const filesLockSrc = `import
    std::fs.{self, Access, LockAttempt, LockMode}
    std::io.{self, File}
end

fn locked() -> Result<(LockAttempt, LockAttempt), io.Error>
    a = fs.open_file("db", access: Access.Create)?
    defer File.close_quietly(a)
    b = fs.open_file("db", access: Access.ReadWrite)?
    defer File.close_quietly(b)
    first = fs.try_lock(a, LockMode.Exclusive)?
    Ok((first, fs.try_lock(b, LockMode.Shared)?))
end

fn synced() -> Result<Unit, io.Error>
    f = fs.open_file("db", access: Access.Create)?
    defer File.close_quietly(f)
    File.sync(f)?
    fs.sync_dir(".")
end

fn main()
    io.inspect(locked())
    io.inspect(synced())
end
`

// wrapped is an embedder's file system over another, adding nothing, with
// the sync of a directory passed on.
type wrapped struct{ vmhost.FileSystem }

func (w wrapped) SyncDir(dir string) error {
	return w.FileSystem.(vmhost.SyncDirFileSystem).SyncDir(dir)
}

// plain is an embedder's file system whose files neither sync nor lock, and
// which syncs no directory.
type plain struct{ vmhost.FileSystem }

type plainFile struct{ vmhost.File }

func (p plain) OpenFile(path string, access vmhost.Access) (vmhost.File, error) {
	f, err := p.FileSystem.OpenFile(path, access)
	if err != nil {
		return nil, err
	}
	return plainFile{f}, nil
}

// An embedder's file system syncs and locks through the File and the
// FileSystem it gives when they have the methods, and answers Unsupported
// when they do not. Wrapping the memory file system passes its locks and
// syncs on.
func TestFileSystem_AnEmbeddersFilesSyncAndLockWhenTheyCan(t *testing.T) {
	for _, c := range []struct {
		name  string
		files vmhost.FileSystem
		want  string
	}{
		{"wrapped", wrapped{vmhost.NewMemoryFiles(nil)}, "Ok((Acquired, Busy))\nOk(Unit)\n"},
		{
			"plain", plain{vmhost.NewMemoryFiles(nil)},
			"Err(Unsupported{operation: \"File.lock\", target: \"this program's file system\"})\n" +
				"Err(Unsupported{operation: \"File.sync\", target: \"this program's file system\"})\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := vmhost.LoadSource("main", filesLockSrc, vmhost.WithFileSystem(c.files))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v\n%s", err, out.String())
			}
			if out.String() != c.want {
				t.Errorf("output %q, want %q", out.String(), c.want)
			}
		})
	}
}
