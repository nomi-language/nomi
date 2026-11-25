package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPackages_AReleaseTakesTheLibPackagesAtItsVersion: a release `nomi`
// takes a first-party library at its own version whatever version
// nomi.toml or the command line names, and `nomi add` writes that version
// and says so. A `nomi` built from source takes the version written. A
// release that finds nomi.lock at another version refuses `run`, `check`
// and `test` with the error that says why and to run `nomi fetch`, which
// locks the release's version; `nomi update` then writes it into
// nomi.toml. A dir entry keeps its repository and version and has no lock
// line. A library the release's tag does not hold is refused with the
// directory named.
func TestPackages_AReleaseTakesTheLibPackagesAtItsVersion(t *testing.T) {
	c := newModuleCLI(t)
	c.bin = releaseNomi(t)
	c.env = append(c.env, "NOMI_RELEASE_BASE_URL="+unreachableRelease)
	source := *c
	source.bin = nomiBin
	const (
		repo   = "github.com/nomi-language/nomi"
		md     = repo + "/lib/markdown"
		sqlite = repo + "/lib/sql/sqlite"
		later  = "v0.0.1"
	)
	markdown := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":     "[package]\nname = \"markdown\"\n",
			"markdown.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	tag := func(v string, withSqlite bool) {
		files := map[string]string{}
		for name, text := range markdown(v) {
			files["lib/markdown/"+name] = text
		}
		if withSqlite {
			files["lib/sql/sqlite/nomi.toml"] = "[package]\nname = \"sqlite\"\n"
		}
		c.host.Tag(repo, v, files)
	}
	tag(releaseTestVersion, false)
	tag(later, true)
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    markdown\nend\n\nfn main()\n    io.print(markdown.version())\nend\n")
	mustWrite(t, filepath.Join(app, "version_test.nomi"), "import markdown\n\ntest \"a version\"\n    assert markdown.version() != \"\"\nend\n")
	manifest := func() string { return mustRead(t, filepath.Join(app, "nomi.toml")) }
	runs := func(cli *moduleCLI, want, when string) {
		t.Helper()
		if out := cli.must(app, "run", "main.nomi"); out != want {
			t.Errorf("nomi run %s printed %q, want %q", when, out, want)
		}
	}

	if out := c.must(app, "add", md); !strings.HasPrefix(out, "added markdown, "+md+" "+releaseTestVersion+" (import markdown)\n") {
		t.Fatalf("a release's nomi add of markdown printed %q", out)
	}
	pinned := "added markdown, " + md + " " + releaseTestVersion + " (import markdown), at this nomi's own release: a release takes the first-party libraries at its version\n"
	if out := c.must(app, "add", md+"@"+later); !strings.HasPrefix(out, pinned) {
		t.Errorf("a release's nomi add %s@%s printed\n%s\nwant it to start\n%s", md, later, out, pinned)
	}
	if m := manifest(); !strings.Contains(m, `version = "`+releaseTestVersion+`"`) {
		t.Fatalf("a release's nomi add %s@%s did not write its own version:\n%s", md, later, m)
	}
	runs(c, releaseTestVersion, "at the release")

	// A nomi built from source takes the version named, and a release then
	// finds the lock at it.
	source.must(app, "add", md+"@"+later)
	runs(&source, later, "from source")
	stale := "nomi.lock has no entry for package " + md + " " + releaseTestVersion + ": this nomi is release " + releaseTestVersion +
		", and takes the first-party libraries at that version; run `nomi fetch` to add it"
	for _, args := range [][]string{{"run", "main.nomi"}, {"check", "main.nomi"}, {"test", "."}} {
		out, stderr, err := c.run(app, args...)
		if err == nil || !strings.Contains(stderr+out, stale) {
			t.Errorf("a release's nomi %s with nomi.lock at %s: %v\nstdout: %s\nstderr: %s\nwant the error\n%s", strings.Join(args, " "), later, err, out, stderr, stale)
		}
	}
	c.must(app, "fetch")
	runs(c, releaseTestVersion, "after nomi fetch")
	if m := manifest(); !strings.Contains(m, `version = "`+later+`"`) {
		t.Errorf("nomi fetch changed nomi.toml:\n%s", m)
	}
	c.must(app, "update")
	if m := manifest(); !strings.Contains(m, `version = "`+releaseTestVersion+`"`) {
		t.Errorf("a release's nomi update did not write its own version:\n%s", m)
	}

	// A dir entry keeps the repository and version, which stand for the
	// directory, and has no lock line.
	source.must(app, "add", md+"@"+later)
	local := filepath.Join(work, "md")
	for name, text := range markdown("local") {
		mustWrite(t, filepath.Join(local, name), text)
	}
	if out := c.must(app, "add", "../md"); out == "" || !strings.Contains(manifest(), `repo = "`+repo+`", subdir = "lib/markdown", version = "`+later+`", dir = "../md"`) {
		t.Fatalf("nomi add ../md printed %q and wrote\n%s", out, manifest())
	}
	runs(c, "local", "with markdown from a directory")

	_, stderr, err := c.run(app, "add", sqlite)
	want := "nomi add: repository " + repo + " has no directory lib/sql/sqlite at " + releaseTestVersion
	if err == nil || stderr != want {
		t.Errorf("a release's nomi add of a library its tag does not hold: %v\n%s\nwant\n%s", err, stderr, want)
	}
}
