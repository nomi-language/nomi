package artifactcache

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestReadBuildIDOnEveryReleaseTarget cross-builds a hello-world program
// for each target a release ships `nomi` for, one ELF, Mach-O or PE file
// each, and requires ReadBuildID to answer what `go tool buildid` prints
// for it. A target whose format the reader misses gives a compiler there
// no identity, so it caches nothing.
func TestReadBuildIDOnEveryReleaseTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program per target")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod":  "module hello\n\ngo 1.24\n",
		"main.go": "package main\n\nfunc main() { println(\"hello\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The targets of scripts/release.sh that `nomi` runs on.
	for _, target := range []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64", "windows/amd64"} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			t.Parallel()
			goos, goarch, _ := strings.Cut(target, "/")
			exe := filepath.Join(dir, "hello-"+goos+"-"+goarch)
			build := exec.Command(goBin, "build", "-o", exe, ".")
			build.Dir = dir
			build.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build for %s: %v\n%s", target, err, out)
			}
			out, err := exec.Command(goBin, "tool", "buildid", exe).Output()
			if err != nil {
				t.Fatalf("go tool buildid: %v", err)
			}
			want := strings.TrimSpace(string(out))
			got, err := ReadBuildID(exe)
			if err != nil || got == "" || got != want {
				t.Fatalf("ReadBuildID of the %s program = %q, %v; go tool buildid prints %q", target, got, err, want)
			}
		})
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
	for _, name := range []string{"stale.bundle", TempPrefix + "x"} {
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

// TestPruneRemovesDirectoriesNothingUsed holds Prune over a cache whose
// entries are directories: one nothing has used for the limit goes, with
// its files; one used within it stays, and so does one Touch marked used
// though its files are old; the running process's own stays however old;
// and an entry not yet past the limit stays.
func TestPruneRemovesDirectoriesNothingUsed(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-Unused - time.Hour)
	mk := func(name string, mtime time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "lib.so"), []byte("x"), 0o444); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{filepath.Join(p, "lib.so"), p} {
			if err := os.Chtimes(q, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	stale := mk("stale", old)
	own := mk("own", old)
	recent := mk("recent", time.Now().Add(-Unused+time.Hour))
	touched := mk("touched", old)
	info, err := os.Stat(touched)
	if err != nil {
		t.Fatal(err)
	}
	Touch(touched, info)

	Prune(dir, "own", Unused)

	if _, err := os.Stat(stale); err == nil {
		t.Error("a directory nothing used for Unused was kept")
	}
	for _, p := range []string{own, recent, touched} {
		if _, err := os.Stat(filepath.Join(p, "lib.so")); err != nil {
			t.Errorf("%s was pruned: %v", filepath.Base(p), err)
		}
	}
}

// TestDueMarksOnePrunePerPeriod holds Due: a directory never pruned is
// due, and marking it makes it not due again until the period has passed,
// unless the prune is forced. A directory that does not exist is never
// due, and Due does not make it.
func TestDueMarksOnePrunePerPeriod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if Due(dir, time.Hour, true) {
		t.Fatal("a directory that does not exist is due")
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Due made a directory that did not exist (%v)", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if !Due(dir, time.Hour, false) {
		t.Fatal("a directory never pruned is not due")
	}
	if Due(dir, time.Hour, false) {
		t.Fatal("a directory pruned just now is due again")
	}
	if !Due(dir, time.Hour, true) {
		t.Fatal("a forced prune of a directory pruned just now is not due")
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, dueMarker), old, old); err != nil {
		t.Fatal(err)
	}
	if !Due(dir, time.Hour, false) {
		t.Fatal("a directory pruned two hours ago is not due an hourly prune")
	}
	Prune(dir, "", 0)
	if _, err := os.Stat(filepath.Join(dir, dueMarker)); err != nil {
		t.Fatalf("Prune removed the directory's own mark: %v", err)
	}
}
