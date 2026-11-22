//go:build !(js && wasm)

package stdio

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// The locks and syncs a File offers, on the disk and in memory. The rules are
// the ones std::fs::testing's conformance suite holds both file systems to
// from Nomi; these hold them from Go, across two processes and between two
// goroutines too, which the suite cannot reach.

// lockBackends is each file system a lock test runs on: a frame that reaches
// it, and a directory in it to work in.
func lockBackends(t *testing.T) map[string]func(t *testing.T) (*rt.Frame, string) {
	return map[string]func(t *testing.T) (*rt.Frame, string){
		"disk": func(t *testing.T) (*rt.Frame, string) {
			return rt.NewFrame(context.Background()), filepath.ToSlash(t.TempDir())
		},
		"memory": func(t *testing.T) (*rt.Frame, string) {
			m := NewMemory("/work")
			return rt.EnterFiles(rt.NewFrame(context.Background()), rt.NewFileSystem(m, true)), "/work"
		},
	}
}

func openFile(t *testing.T, fr *rt.Frame, p string, access Access) rt.File {
	t.Helper()
	got := OpenFile(Here(fr), p, int64(access))
	if got.Tag != rt.TagOk {
		t.Fatalf("open_file %s: %+v", p, got.Err)
	}
	t.Cleanup(func() { rt.FileClose(got.Ok) })
	return got.Ok
}

// lock is FileLock asked once, or for wait.
func lock(t *testing.T, fr *rt.Frame, f rt.File, mode rt.LockMode, start, length int64, wait time.Duration) bool {
	t.Helper()
	got := rt.FileLock(fr, f, int64(mode), rt.SmallInt(start), rt.SmallInt(length), rt.Duration(wait))
	if got.Tag != rt.TagOk {
		t.Fatalf("lock %d [%d, +%d): %+v", mode, start, length, got.Err)
	}
	return got.Ok
}

func TestFileLock_TwoHandlesExcludeEachOther(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/db"
			a := openFile(t, fr, p, AccessCreate)
			b := openFile(t, fr, p, AccessReadWrite)
			for _, c := range []struct {
				what  string
				f     rt.File
				mode  rt.LockMode
				start int64
				n     int64
				want  bool
			}{
				{"exclusive", a, rt.LockExclusive, 0, 0, true},
				{"shared beside it", b, rt.LockShared, 0, 0, false},
				{"exclusive beside it", b, rt.LockExclusive, 0, 0, false},
				{"unlock", a, rt.LockNone, 0, 0, true},
				{"shared after the unlock", b, rt.LockShared, 0, 0, true},
				{"shared beside a shared one", a, rt.LockShared, 0, 0, true},
				{"exclusive beside a shared one", a, rt.LockExclusive, 0, 0, false},
				{"unlock b", b, rt.LockNone, 0, 0, true},
				{"unlock a", a, rt.LockNone, 0, 0, true},
				{"the first ten bytes", a, rt.LockExclusive, 0, 10, true},
				{"the next ten", b, rt.LockExclusive, 10, 10, true},
				{"bytes across both", b, rt.LockShared, 5, 10, false},
				{"past the end", a, rt.LockExclusive, 1 << 40, 0, true},
				{"a byte among them", b, rt.LockShared, 1<<40 + 7, 1, false},
			} {
				if got := lock(t, fr, c.f, c.mode, c.start, c.n, 0); got != c.want {
					t.Errorf("%s: got %v, want %v", c.what, got, c.want)
				}
			}
		})
	}
}

// A handle's lock on bytes it holds replaces it, and an unlock of part of a
// range keeps the rest: the open file description lock's rules, which
// Windows does not follow (osfile_windows.go).
func TestFileLock_AHandleChangesItsOwnLock(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			if name == "disk" && runtime.GOOS == "windows" {
				t.Skip("a LockFileEx lock is not replaced by another on the same bytes")
			}
			fr, dir := at(t)
			p := dir + "/db"
			a := openFile(t, fr, p, AccessCreate)
			b := openFile(t, fr, p, AccessReadWrite)
			for _, c := range []struct {
				what  string
				f     rt.File
				mode  rt.LockMode
				start int64
				n     int64
				want  bool
			}{
				{"shared", a, rt.LockShared, 0, 0, true},
				{"exclusive over it", a, rt.LockExclusive, 0, 0, true},
				{"shared beside the exclusive one", b, rt.LockShared, 0, 0, false},
				{"shared again", a, rt.LockShared, 0, 0, true},
				{"shared beside it", b, rt.LockShared, 0, 0, true},
				{"unlock b", b, rt.LockNone, 0, 0, true},
				{"exclusive again", a, rt.LockExclusive, 0, 0, true},
				{"unlock the first ten bytes", a, rt.LockNone, 0, 10, true},
				{"exclusive on them", b, rt.LockExclusive, 0, 10, true},
				{"exclusive on the rest", b, rt.LockExclusive, 10, 0, false},
				{"unlock bytes a holds no lock on", a, rt.LockNone, 0, 5, true},
				{"still the rest", b, rt.LockExclusive, 10, 0, false},
			} {
				if got := lock(t, fr, c.f, c.mode, c.start, c.n, 0); got != c.want {
					t.Errorf("%s: got %v, want %v", c.what, got, c.want)
				}
			}
		})
	}
}

func TestFileLock_CloseReleasesTheHandlesLocks(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/db"
			a := openFile(t, fr, p, AccessCreate)
			b := openFile(t, fr, p, AccessReadWrite)
			lock(t, fr, a, rt.LockExclusive, 0, 0, 0)
			lock(t, fr, a, rt.LockShared, 100, 1, 0)
			if got := rt.FileClose(a); got.Tag != rt.TagOk {
				t.Fatalf("close: %+v", got.Err)
			}
			// Windows releases a closed handle's locks when it can, so the
			// lock may wait a moment there.
			if !lock(t, fr, b, rt.LockExclusive, 0, 0, time.Second) {
				t.Error("a lock the closed handle held is still held")
			}
			if got := rt.FileLock(fr, a, int64(rt.LockShared), rt.SmallInt(0), rt.SmallInt(0), 0); got.Tag != rt.TagErr || got.Err.Tag != rt.TagIOClosed {
				t.Errorf("a lock on a closed handle answered %+v, want Closed", got)
			}
		})
	}
}

func TestFileLock_ExclusiveNeedsAWritableHandle(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/r.db"
			openFile(t, fr, p, AccessCreate)
			r := openFile(t, fr, p, AccessRead)
			got := rt.FileLock(fr, r, int64(rt.LockExclusive), rt.SmallInt(0), rt.SmallInt(0), 0)
			if got.Tag != rt.TagErr || got.Err != (rt.IOError{Tag: rt.TagIOPermissionDenied, Path: p}) {
				t.Errorf("an exclusive lock on a read-only handle answered %+v, want PermissionDenied", got)
			}
			if !lock(t, fr, r, rt.LockShared, 0, 0, 0) {
				t.Error("a shared lock on a read-only handle is refused")
			}
		})
	}
}

// A wait asks again until the lock is free or the wait has passed. The
// first case ends at the unlock, which a goroutine makes through the other
// handle; the second waits its whole time.
func TestFileLock_AWaitEndsAtTheUnlockOrItsTimeout(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/db"
			a := openFile(t, fr, p, AccessCreate)
			b := openFile(t, fr, p, AccessReadWrite)
			lock(t, fr, a, rt.LockExclusive, 0, 0, 0)
			done := make(chan struct{})
			go func() {
				defer close(done)
				time.Sleep(20 * time.Millisecond)
				rt.FileLock(fr, a, int64(rt.LockNone), rt.SmallInt(0), rt.SmallInt(0), 0)
			}()
			if !lock(t, fr, b, rt.LockShared, 0, 0, 10*time.Second) {
				t.Error("a wait that the unlock ends answered false")
			}
			<-done
			lock(t, fr, b, rt.LockNone, 0, 0, 0)
			lock(t, fr, a, rt.LockExclusive, 0, 0, 0)
			const wait = 30 * time.Millisecond
			began := time.Now()
			if lock(t, fr, b, rt.LockShared, 0, 0, wait) {
				t.Error("a lock taken while another handle holds it exclusively")
			}
			if waited := time.Since(began); waited < wait {
				t.Errorf("the wait ended after %v, before its %v", waited, wait)
			}
		})
	}
}

// A cancelled task stops waiting: FileLock unwinds as a sleep does.
func TestFileLock_ACancelledWaitStops(t *testing.T) {
	m := NewMemory("/work")
	ctx, cancel := context.WithCancel(context.Background())
	fr := rt.EnterFiles(rt.NewFrame(ctx), rt.NewFileSystem(m, true))
	a := openFile(t, fr, "/work/db", AccessCreate)
	b := openFile(t, fr, "/work/db", AccessReadWrite)
	lock(t, fr, a, rt.LockExclusive, 0, 0, 0)
	cancel()
	ended := make(chan any, 1)
	go func() {
		defer func() { ended <- recover() }()
		rt.FileLock(fr, b, int64(rt.LockShared), rt.SmallInt(0), rt.SmallInt(0), rt.Duration(time.Hour))
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("a wait on a cancelled frame did not end")
	}
}

func TestFileLock_ARangePastInt64(t *testing.T) {
	fr := rt.EnterFiles(rt.NewFrame(context.Background()), rt.NewFileSystem(NewMemory("/work"), true))
	a := openFile(t, fr, "/work/db", AccessCreate)
	huge := rt.UintInt(1 << 63)
	got := rt.FileLock(fr, a, int64(rt.LockShared), huge, rt.SmallInt(1), 0)
	if got.Tag != rt.TagErr || got.Err.Code != "EOVERFLOW" {
		t.Errorf("a lock from past int64 answered %+v, want EOVERFLOW", got)
	}
	if got := rt.FileLock(fr, a, int64(rt.LockShared), rt.SmallInt(5), huge, 0); got.Tag != rt.TagOk || !got.Ok {
		t.Errorf("a lock whose length reaches past int64 answered %+v, want a lock to past any end", got)
	}
}

// lockHelperEnv names the file a helper process locks.
const lockHelperEnv = "NOMI_STDIO_LOCK_HELPER"

// TestFileLock_HelperProcess is the other process of
// TestFileLock_ExcludesAnotherProcess: it locks the first ten bytes of the
// file, says so, and holds the lock until its input ends.
func TestFileLock_HelperProcess(t *testing.T) {
	p := os.Getenv(lockHelperEnv)
	if p == "" {
		t.Skip("run by TestFileLock_ExcludesAnotherProcess")
	}
	fr := rt.NewFrame(context.Background())
	f := openFile(t, fr, p, AccessReadWrite)
	if !lock(t, fr, f, rt.LockExclusive, 0, 10, 0) {
		os.Stdout.WriteString("busy\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("locked\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestFileLock_ExcludesAnotherProcess(t *testing.T) {
	if runtime.GOOS == "wasip1" {
		t.Skip("WASI has no locks another process sees, and no processes")
	}
	fr := rt.NewFrame(context.Background())
	p := filepath.ToSlash(filepath.Join(t.TempDir(), "db"))
	f := openFile(t, fr, p, AccessCreate)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFileLock_HelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+p)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("the helper answered %q, %v", line, err)
	}
	if lock(t, fr, f, rt.LockShared, 5, 1, 0) {
		t.Error("a lock on bytes another process holds exclusively")
	}
	if !lock(t, fr, f, rt.LockExclusive, 10, 10, 0) {
		t.Error("a lock on bytes beside the other process's is refused")
	}
	_ = in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the helper: %v", err)
	}
	if !lock(t, fr, f, rt.LockExclusive, 0, 10, 5*time.Second) {
		t.Error("the bytes are still locked after the other process ended")
	}
}

func TestFileSync_WhatWasWrittenIsReadAfterReopening(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/s.bin"
			f := openFile(t, fr, p, AccessCreate)
			if got := rt.FileWriteAt(f, rt.SmallInt(0), rt.Bytes("kept")); got.Tag != rt.TagOk {
				t.Fatalf("write: %+v", got.Err)
			}
			if got := rt.FileSync(f); got.Tag != rt.TagOk {
				t.Fatalf("sync: %+v", got.Err)
			}
			if got := rt.FileWriteAt(f, rt.SmallInt(4), rt.Bytes("!")); got.Tag != rt.TagOk {
				t.Fatalf("write: %+v", got.Err)
			}
			if got := rt.FileSyncData(f); got.Tag != rt.TagOk {
				t.Fatalf("sync_data: %+v", got.Err)
			}
			if got := rt.FileClose(f); got.Tag != rt.TagOk {
				t.Fatalf("close: %+v", got.Err)
			}
			if got := rt.FileSync(f); got.Tag != rt.TagErr || got.Err.Tag != rt.TagIOClosed {
				t.Errorf("a sync of a closed file answered %+v, want Closed", got)
			}
			if got := SyncDir(Here(fr), dir); got.Tag != rt.TagOk {
				t.Errorf("sync_dir: %+v", got.Err)
			}
			if got := Read(fr, Here(fr), p); got.Tag != rt.TagOk || string(got.Ok) != "kept!" {
				t.Errorf("the file reads back %+v", got)
			}
		})
	}
}

func TestSyncDir_RefusesWhatIsNotADirectory(t *testing.T) {
	for name, at := range lockBackends(t) {
		t.Run(name, func(t *testing.T) {
			fr, dir := at(t)
			p := dir + "/a.txt"
			if got := Write(fr, Here(fr), p, rt.Bytes("a"), int64(Replace)); got.Tag != rt.TagOk {
				t.Fatalf("write: %+v", got.Err)
			}
			if got := SyncDir(Here(fr), p); got.Tag != rt.TagErr || got.Err != (rt.IOError{Tag: rt.TagIONotADirectory, Path: p}) {
				t.Errorf("sync_dir of a file answered %+v, want NotADirectory", got)
			}
			missing := dir + "/none"
			if got := SyncDir(Here(fr), missing); got.Tag != rt.TagErr || got.Err != (rt.IOError{Tag: rt.TagIONotFound, Path: missing}) {
				t.Errorf("sync_dir of a missing path answered %+v, want NotFound", got)
			}
		})
	}
}

// A sync in a sandbox fails as a Failure for OpSync says, on the file's
// path and on a directory's.
func TestFileSync_ASandboxFailsItWhenAsked(t *testing.T) {
	m := NewMemory("/work")
	broken := rt.IOError{Tag: rt.TagIOOther, Operation: "sync", Path: "db", Code: "EIO"}
	m.Fail("/work/db", OpSync, broken, 0)
	m.Fail("/work", OpSync, broken, 0)
	fr := rt.EnterFiles(rt.NewFrame(context.Background()), rt.NewFileSystem(m, true))
	f := openFile(t, fr, "db", AccessCreate)
	if got := rt.FileSync(f); got.Tag != rt.TagErr || got.Err != broken {
		t.Errorf("a failing sync answered %+v", got)
	}
	if got := SyncDir(Here(fr), "."); got.Tag != rt.TagErr || got.Err != broken {
		t.Errorf("a failing sync_dir answered %+v", got)
	}
}
