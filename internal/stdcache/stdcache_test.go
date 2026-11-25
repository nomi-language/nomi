package stdcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
)

func stdFS(option string) fstest.MapFS {
	return fstest.MapFS{
		"option.nomi":                         {Data: []byte(option)},
		"strings.nomi":                        {Data: []byte("pub type Text = String\n")},
		"_fixtures/nested/deeper/module.nomi": {Data: []byte("fn f() end\n")},
		"std.go":                              {Data: []byte("package std\n")},
	}
}

// Two std versions write to two directories, and neither rewrites the
// other's files: what an old server's editor buffer was opened from keeps
// the old server's text.
func TestDir_TwoVersionsWriteTwoDirectories(t *testing.T) {
	root := t.TempDir()
	older := New(root, stdFS("pub enum Option<T>\n  Some(T)\n  None\nend\n"))
	newer := New(root, stdFS("pub enum Option<T>\n  None\n  Some(T)\nend\n"))
	if older.Version() == newer.Version() {
		t.Fatalf("different std content has one version %s", older.Version())
	}
	oldPath := older.Path("option.nomi")
	newPath := newer.Path("option.nomi")
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
		if strings.HasPrefix(e.Name(), artifactcache.TempPrefix) {
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
	path := d.Path("option.nomi")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if got := d.Path("option.nomi"); got != path {
		t.Fatalf("path moved from %s to %s", path, got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("not rewritten: %v", err)
	}
}

// A version directory that lost one of its files (a removal that stopped
// part way) is written again whole when a path in it is asked for, and
// leaves no temporary directory; a path that names no file of the version
// writes nothing.
func TestDir_RewritesADirectoryMissingAFile(t *testing.T) {
	root := t.TempDir()
	d := New(root, stdFS("a\n"))
	option := d.Path("option.nomi")
	strs := d.Location("strings.nomi")
	if err := os.Remove(strs); err != nil {
		t.Fatal(err)
	}
	if got := d.Path("option.nomi"); got != option {
		t.Fatalf("path moved from %s to %s", option, got)
	}
	if _, err := os.Stat(strs); err == nil {
		t.Fatal("asking for a file the directory holds rewrote it")
	}
	if got := d.Path("strings.nomi"); got != strs {
		t.Fatalf("path moved from %s to %s", strs, got)
	}
	for _, p := range []string{strs, option, d.Location("_fixtures/nested/deeper/module.nomi")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not rewritten: %v", err)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), artifactcache.TempPrefix) {
			t.Errorf("temporary directory %s left behind", e.Name())
		}
	}
	d.Path("std.go")
	if _, err := os.Stat(d.Location("std.go")); err == nil {
		t.Error("a path that is no .nomi file of the version wrote it")
	}
}

// Pruning removes another version and leftovers of the unversioned layout
// once they are older than staleAfter, a temporary directory older than an
// hour, and nothing recent: a version another server touched within
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
			if err := os.WriteFile(filepath.Join(p, "option.nomi"), nil, 0o444); err != nil {
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
	legacyFile := mk("option.nomi", false, old)
	staleTemp := mk(artifactcache.TempPrefix+"x", true, time.Now().Add(-2*time.Hour))
	freshTemp := mk(artifactcache.TempPrefix+"y", true, time.Now())

	d := New(root, stdFS("a\n"))
	d.Path("option.nomi")

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
	New(root, stdFS("a\n")).Path("option.nomi")
	d := New(root, stdFS("a\n"))
	dir := filepath.Join(root, d.Version())
	old := time.Now().Add(-staleAfter - time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	d.Path("option.nomi")
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

// Locate finds the version directory a written file is under, a nested
// module's included, and no directory for a path that has a version-shaped
// name anywhere else.
func TestLocate_FindsTheVersionDirectory(t *testing.T) {
	dir := New(filepath.Join(t.TempDir(), "std"), stdFS("pub enum Option<T>\n  Some(T)\n  None\nend\n"))
	for _, rel := range []string{"option.nomi", "_fixtures/nested/deeper/module.nomi"} {
		got, version, gotRel, ok := Locate(dir.Location(rel))
		if !ok || got != filepath.Dir(dir.Location("option.nomi")) || version != dir.Version() || gotRel != rel {
			t.Errorf("Locate(%s) = %s, %s, %s, %v; want the version directory, %s, %s", dir.Location(rel), got, version, gotRel, ok, dir.Version(), rel)
		}
	}
	for _, path := range []string{
		filepath.Join(t.TempDir(), "std", "option.nomi"),
		filepath.Join(t.TempDir(), "lib", dir.Version(), "option.nomi"),
	} {
		if _, _, _, ok := Locate(path); ok {
			t.Errorf("Locate(%s) found a version directory", path)
		}
	}
}
