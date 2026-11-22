package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The record of a kept program holds the text of the sources the program
// loads, and the presence of every other (vmhost's sourceTree,
// progcache.Tree.NamedSources). These tests hold what that does after an
// edit in a project of several programs.

// sourcesProject writes a directory of three test files, one of which
// imports lib.nomi, and answers it. display_test.nomi interpolates
// strings, so its program reaches std's Display impls, and plain_test.nomi
// reaches none.
func sourcesProject(t *testing.T) string {
	t.Helper()
	// The cache names a file by its path with links resolved, as the
	// command finds it.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "lib.nomi"), "pub fn two() -> Int\n    2\nend\n")
	mustWrite(t, filepath.Join(dir, "uses_test.nomi"), "import lib\n\ntest \"lib\"\n    assert lib.two() > 0\nend\n")
	mustWrite(t, filepath.Join(dir, "display_test.nomi"), "test \"display\"\n    name = \"nomi\"\n    assert \"${name}!\" == \"nomi!\"\nend\n")
	mustWrite(t, filepath.Join(dir, "plain_test.nomi"), "test \"plain\"\n    assert 1 + 1 == 2\nend\n")
	return dir
}

// sourcesTests are sourcesProject's test files.
var sourcesTests = []string{"uses_test.nomi", "display_test.nomi", "plain_test.nomi"}

// requireSourcesRun runs `nomi test` on file in dir with the cache and
// with none, requires both to print the same and nothing to report a
// relink that panicked, and answers the cached run.
func requireSourcesRun(t *testing.T, dir, cache, file, step string) cacheRun {
	t.Helper()
	got := runNomiIn(t, dir, cache, "test", "--all", file)
	fresh := runFreshIn(t, dir, cache, "test", "--all", file)
	if got.String() != fresh.String() {
		t.Fatalf("after %s, `nomi test %s` with the artifact cache (%v) printed\n%s\nand with none\n%s", step, file, got.report, got, fresh)
	}
	if strings.Contains(strings.Join(got.report, "\n"), "panic") || strings.Contains(got.stderr, "nomi: relinking") {
		t.Fatalf("after %s, relinking %s panicked: %v\n%s", step, file, got.report, got.stderr)
	}
	return got
}

// notRelinked is the reason the run's report gives for not relinking, or
// "".
func notRelinked(r cacheRun) string {
	for _, line := range r.report {
		if _, after, ok := strings.Cut(line, "not relinked: "); ok {
			return after
		}
	}
	return ""
}

// TestArtifactCache_AnEditToASourceAProgramDoesNotLoadLeavesItKept edits
// lib.nomi's body: uses_test.nomi's program is relinked, and the two that
// do not load lib.nomi read their kept programs. A relink of those, whose
// files had not changed, panicked on the lowering it never made, or found
// a missing impl of std's Display where it checked no file against std.
// A source that comes or goes is a change to every program of the project
// (an import may name it), which each refuses to relink with its reason.
func TestArtifactCache_AnEditToASourceAProgramDoesNotLoadLeavesItKept(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	dir := sourcesProject(t)
	cache := newCacheDir(t)
	for _, f := range sourcesTests {
		requireSourcesRun(t, dir, cache, f, "writing the project")
		if r := requireSourcesRun(t, dir, cache, f, "a second run"); !r.read() {
			t.Fatalf("the second `nomi test %s` read no kept program: %v", f, r.report)
		}
	}

	mustWrite(t, filepath.Join(dir, "lib.nomi"), "pub fn two() -> Int\n    1 + 1\nend\n")
	for _, f := range sourcesTests {
		r := requireSourcesRun(t, dir, cache, f, "a body edit of lib.nomi")
		switch {
		case f == "uses_test.nomi" && !r.relinked():
			t.Errorf("after a body edit of lib.nomi, `nomi test %s` did not relink its program: %v", f, r.report)
		case f != "uses_test.nomi" && !r.read():
			t.Errorf("after a body edit of lib.nomi, which it does not load, `nomi test %s` read no kept program: %v", f, r.report)
		}
	}

	extra := filepath.Join(dir, "extra.nomi")
	mustWrite(t, extra, "pub fn three() -> Int\n    3\nend\n")
	for _, f := range sourcesTests {
		r := requireSourcesRun(t, dir, cache, f, "extra.nomi added")
		if want := extra + " came, and no file of the program imports it"; notRelinked(r) != want {
			t.Errorf("after extra.nomi was added, `nomi test %s` reports %q, want %q: %v", f, notRelinked(r), want, r.report)
		}
		if r := requireSourcesRun(t, dir, cache, f, "a second run"); !r.read() {
			t.Fatalf("the second `nomi test %s` after extra.nomi was added read no kept program: %v", f, r.report)
		}
	}

	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	for _, f := range sourcesTests {
		r := requireSourcesRun(t, dir, cache, f, "extra.nomi removed")
		if want := "no file of the program changed"; notRelinked(r) != want {
			t.Errorf("after extra.nomi was removed, `nomi test %s` reports %q, want %q: %v", f, notRelinked(r), want, r.report)
		}
	}
}

// TestArtifactCache_ABuildRelinkedFromItsRecordsNamesTheProgramsFiles
// edits lib.nomi's body twice, building after each: the first build is
// relinked from the file records, and the record it keeps must name
// lib.nomi, or the second build would read the first's image.
func TestArtifactCache_ABuildRelinkedFromItsRecordsNamesTheProgramsFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs executables; -short")
	}
	dir := t.TempDir()
	lib := func(n string) string { return "pub fn n() -> Int\n    " + n + "\nend\n" }
	mustWrite(t, filepath.Join(dir, "lib.nomi"), lib("1"))
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import\n    std::io\n    lib\nend\n\nfn main()\n    io.print(\"${lib.n()}\")\nend\n")
	cache := newCacheDir(t)
	app := exe(filepath.Join(t.TempDir(), "app"))
	for i, n := range []string{"1", "2", "3"} {
		mustWrite(t, filepath.Join(dir, "lib.nomi"), lib(n))
		b := runNomiIn(t, dir, cache, "build", "main.nomi", "-o", app)
		if b.exit != 0 {
			t.Fatalf("nomi build with lib.n() = %s:\n%s", n, b)
		}
		if i == 1 && !b.relinked() {
			t.Fatalf("the build after a body edit of lib.nomi was not relinked from its file records: %v", b.report)
		}
		out, err := exec.Command(app).Output()
		if err != nil || string(out) != n+"\n" {
			t.Fatalf("the executable built with lib.n() = %s printed %q (%v); cache: %v", n, out, err, b.report)
		}
	}
}

// TestArtifactCache_AFileWhereADirectoryImportWasIsReadAgain adds
// views/admin.nomi beside the directory views/admin/ that main.nomi
// imports, with an edit to lib.nomi's body: the import now names the file
// (spec §14 *Directory imports*), and the program's uses of it are errors.
// A relink that read only the files the kept interfaces import ran the
// program as it was.
func TestArtifactCache_AFileWhereADirectoryImportWasIsReadAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "views", "admin", "users.nomi"), "pub fn index() -> Int\n    1\nend\n")
	mustWrite(t, filepath.Join(dir, "lib.nomi"), "pub fn two() -> Int\n    2\nend\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import\n    std::io\n    views::admin\n    lib\nend\n\nfn main()\n    io.print(\"${admin::users.index() + lib.two()}\")\nend\n")
	cache := newCacheDir(t)
	if read, _ := requireCachedMatchesFresh(t, dir, "main.nomi", cache, "writing the project"); read == 0 {
		t.Fatal("no command read a kept program, so the comparisons say nothing about the cache")
	}
	mustWrite(t, filepath.Join(dir, "views", "admin.nomi"), "pub fn other() -> Int\n    3\nend\n")
	mustWrite(t, filepath.Join(dir, "lib.nomi"), "pub fn two() -> Int\n    20\nend\n")
	requireCachedMatchesFresh(t, dir, "main.nomi", cache, "views/admin.nomi added and lib.nomi edited")
	fresh := runFreshIn(t, dir, cache, "run", "main.nomi")
	if fresh.exit == 0 || !strings.Contains(fresh.stderr, "`admin::users` is not imported: `admin` here is the file views::admin, not a directory") {
		t.Fatalf("with views/admin.nomi added, a fresh `nomi run` does not report the import that names the file:\n%s", fresh)
	}
}
