package stdcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func stdFS(maybe string) fstest.MapFS {
	return fstest.MapFS{
		"maybe.nomi":                          {Data: []byte(maybe)},
		"strings.nomi":                        {Data: []byte("pub type Text = String\n")},
		"_fixtures/nested/deeper/module.nomi": {Data: []byte("fn f() {}\n")},
		"std.go":                              {Data: []byte("package std\n")},
	}
}

// Two std versions write to two directories, and neither rewrites the
// other's files: what an old server's editor buffer was opened from keeps
// the old server's text.
func TestDir_TwoVersionsWriteTwoDirectories(t *testing.T) {
	root := t.TempDir()
	older := New(root, stdFS("pub enum Maybe<T> {\n  Some(T)\n  None\n}\n"))
	newer := New(root, stdFS("pub enum Maybe<T> {\n  None\n  Some(T)\n}\n"))
	if older.Version() == newer.Version() {
		t.Fatalf("different std content has one version %s", older.Version())
	}
	oldPath := older.Path("maybe.nomi")
	newPath := newer.Path("maybe.nomi")
	if filepath.Dir(oldPath) == filepath.Dir(newPath) {
		t.Fatalf("both versions write to %s", filepath.Dir(oldPath))
	}
	for path, want := range map[string]string{oldPath: "  Some(T)\n  None", newPath: "  None\n  Some(T)"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), want) {
			t.Errorf("%s = %q, want its own version's text", path, got)
		}
	}
	if got := filepath.Base(filepath.Dir(oldPath)); !IsVersion(got) {
		t.Errorf("version directory %q is not shaped like a version", got)
	}
	nested := older.Path("_fixtures/nested/deeper/module.nomi")
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("nested file not written: %v", err)
	}
	if _, err := os.Stat(older.Location("std.go")); err == nil {
		t.Error("a non-.nomi file was written")
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			t.Errorf("temporary directory %s left behind", e.Name())
		}
	}
}

// The version depends on the files' content and names, not on anything else
// in the tree.
func TestVersion_IsTheNomiFilesContent(t *testing.T) {
	a := stdFS("x\n")
	b := stdFS("x\n")
	b["std.go"] = &fstest.MapFile{Data: []byte("package other\n")}
	if Version(a) != Version(b) {
		t.Error("a non-.nomi file changed the version")
	}
	b["extra.nomi"] = &fstest.MapFile{Data: []byte("\n")}
	if Version(a) == Version(b) {
		t.Error("a new .nomi file left the version unchanged")
	}
}

// A directory removed under a running server (an older server wiped the
// whole root, or a prune ran while this one was idle) is written again the
// next time a path in it is asked for.
func TestDir_RewritesARemovedDirectory(t *testing.T) {
	root := t.TempDir()
	d := New(root, stdFS("a\n"))
	path := d.Path("maybe.nomi")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if got := d.Path("maybe.nomi"); got != path {
		t.Fatalf("path moved from %s to %s", path, got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("not rewritten: %v", err)
	}
}

// Pruning removes another version and leftovers of the unversioned layout
// once they are older than staleAfter, a temporary directory older than
// tempAfter, and nothing recent: a version another server touched within
// staleAfter stays.
func TestDir_PrunesOnlyStaleEntries(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-staleAfter - time.Hour)
	mk := func(name string, dir bool, mtime time.Time) string {
		p := filepath.Join(root, name)
		if dir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p, "maybe.nomi"), nil, 0o444); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	staleVersion := mk("0123456789abcdef", true, old)
	liveVersion := mk("fedcba9876543210", true, time.Now().Add(-time.Hour))
	legacyFile := mk("maybe.nomi", false, old)
	staleTemp := mk(tempPrefix+"x", true, time.Now().Add(-2*tempAfter))
	freshTemp := mk(tempPrefix+"y", true, time.Now())

	d := New(root, stdFS("a\n"))
	d.Path("maybe.nomi")

	for _, p := range []string{staleVersion, legacyFile, staleTemp} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was not pruned", filepath.Base(p))
		}
	}
	for _, p := range []string{liveVersion, freshTemp, filepath.Join(root, d.Version())} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was pruned: %v", filepath.Base(p), err)
		}
	}
}

// An existing version directory is not written again; its modification time
// is what keeps other servers' prunes away, and a first use refreshes it.
func TestDir_TouchesAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	New(root, stdFS("a\n")).Path("maybe.nomi")
	d := New(root, stdFS("a\n"))
	dir := filepath.Join(root, d.Version())
	old := time.Now().Add(-staleAfter - time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	d.Path("maybe.nomi")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("first use left the directory's mtime at %v", info.ModTime())
	}
}

// Under go test, the default root is never the user's cache.
func TestDefaultRoot_UnderTestIsNotTheUserCache(t *testing.T) {
	t.Setenv(RootEnv, "")
	home, _ := os.UserHomeDir()
	if got := DefaultRoot(); home != "" && strings.HasPrefix(got, filepath.Join(home, ".cache")) {
		t.Fatalf("DefaultRoot() = %s under go test", got)
	}
	if filepath.Base(DefaultRoot()) != "std" {
		t.Fatalf("DefaultRoot() = %s, whose files the analyzer would not take for std", DefaultRoot())
	}
	t.Setenv(RootEnv, "/elsewhere")
	if got, want := DefaultRoot(), filepath.Join("/elsewhere", "std"); got != want {
		t.Fatalf("DefaultRoot() = %s, want %s", got, want)
	}
}
