package artifactcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTestsWriteNoUserCache holds where a test's artifacts go: never the
// user's cache, in this process or in a child it starts, which inherits
// RootEnv.
func TestTestsWriteNoUserCache(t *testing.T) {
	if os.Getenv(RootEnv) == "" {
		t.Fatalf("%s is unset under go test, so a nomi a test runs would write the user's cache", RootEnv)
	}
	if user, err := os.UserCacheDir(); err == nil && Root() == filepath.Join(user, "nomi", "artifacts") {
		t.Fatalf("the cache under go test is the user's, %s", Root())
	}
	t.Setenv(RootEnv, "off")
	if Root() != "" {
		t.Fatalf("%s=off leaves the cache at %s", RootEnv, Root())
	}
}

// TestTestsKeepNoProgramUnlessTheyAsk holds ProgramRoot under go test: off
// at the directory init chose, so a test that loads a program loads it
// afresh, and on at a directory the test set.
func TestTestsKeepNoProgramUnlessTheyAsk(t *testing.T) {
	t.Setenv(RootEnv, testRoot())
	if root := ProgramRoot(); root != "" {
		t.Fatalf("a test that asked for no cache keeps programs under %s", root)
	}
	dir := t.TempDir()
	t.Setenv(RootEnv, dir)
	if root := ProgramRoot(); root != dir {
		t.Fatalf("a test that set %s=%s keeps programs under %q", RootEnv, dir, root)
	}
	t.Setenv(RootEnv, "off")
	if root := ProgramRoot(); root != "" {
		t.Fatalf("%s=off keeps programs under %s", RootEnv, root)
	}
}

// TestCompilerIDIsTheBuildID reads this test binary's Go build ID, which the
// std bundle is cached under.
func TestCompilerIDIsTheBuildID(t *testing.T) {
	id := CompilerID()
	if id == "" {
		t.Fatal("this test binary has no build ID, so nothing is cached under go test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ReadBuildID(exe)
	if err != nil || again != id {
		t.Fatalf("ReadBuildID = %q, %v; CompilerID = %q", again, err, id)
	}
	if _, err := ReadBuildID(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file has a build ID")
	}
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, []byte("no build id here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBuildID(plain); err == nil {
		t.Fatal("a file with no build ID has one")
	}
}

// TestWriteFileReplacesWholeAndPruneKeepsWhatIsUsed covers the two writes
// the cache makes: a file replaced in place, and stale siblings removed.
func TestWriteFileReplacesWholeAndPruneKeepsWhatIsUsed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "std")
	path := filepath.Join(dir, "a.bundle")
	for _, data := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(data)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != data {
			t.Fatalf("read %q after writing %q", got, data)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"stale.bundle", tempPrefix + "x"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "fresh.bundle"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	Prune(dir, "a.bundle", 24*time.Hour)
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 2 || left[0] != "a.bundle" || left[1] != "fresh.bundle" {
		t.Fatalf("after pruning, %v are left; want the kept file and the fresh one", left)
	}
}
