package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestModules_AReleaseTakesTheLibModulesAtItsVersion: a release `nomi` adds
// a first-party library at its own version, and one named on the command
// line at that version. While nomi.lock takes a library at another
// version, `run`, `check`, `test` and `build` say so in one note before
// anything else and still run; `nomi update` moves it back, and a dir
// entry, or a `nomi` built from source, prints no note. A library the
// release's tag does not hold is refused with the directory named.
func TestModules_AReleaseTakesTheLibModulesAtItsVersion(t *testing.T) {
	c := newModuleCLI(t)
	c.bin = releaseNomi(t)
	c.env = append(c.env, "NOMI_RELEASE_BASE_URL="+unreachableRelease)
	const (
		repo   = "github.com/nomi-language/nomi"
		md     = repo + "/lib/markdown"
		sqlite = repo + "/lib/sql/sqlite"
		later  = "v0.0.1"
	)
	markdown := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":     "[module]\nname = \"markdown\"\n",
			"markdown.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	tag := func(v string, withSqlite bool) {
		files := map[string]string{}
		for name, text := range markdown(v) {
			files["lib/markdown/"+name] = text
		}
		if withSqlite {
			files["lib/sql/sqlite/nomi.toml"] = "[module]\nname = \"sqlite\"\n"
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

	if out := c.must(app, "add", md); !strings.HasPrefix(out, "added markdown, "+md+" "+releaseTestVersion+" (import markdown)") {
		t.Fatalf("a release's nomi add of markdown printed %q", out)
	}
	note := "note: nomi.lock takes " + md + " " + later + ", and this nomi is release " + releaseTestVersion +
		", which takes the first-party libraries at " + releaseTestVersion + "; run `nomi update` to move them to it"
	noNote := func(cli *moduleCLI, when string) {
		t.Helper()
		out, stderr, err := cli.run(app, "run", "main.nomi")
		if err != nil || strings.Contains(stderr, "note:") {
			t.Errorf("nomi run %s: %v\nstdout: %s\nstderr: %s", when, err, out, stderr)
		}
	}
	noNote(c, "with markdown at the release")

	c.must(app, "add", md+"@"+later)
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, `version = "`+later+`"`) {
		t.Fatalf("nomi add %s@%s did not take the version named:\n%s", md, later, manifest)
	}
	for _, args := range [][]string{{"run", "main.nomi"}, {"check", "main.nomi"}, {"test", "."}} {
		out, stderr, err := c.run(app, args...)
		if err != nil {
			t.Errorf("nomi %s with markdown off the release: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, stderr)
		}
		if !strings.HasPrefix(stderr, note) || strings.Count(stderr, "note:") != 1 {
			t.Errorf("nomi %s with markdown off the release wrote stderr\n%s\nwant it to open with the one note\n%s", strings.Join(args, " "), stderr, note)
		}
		if args[0] == "run" && out != later {
			t.Errorf("nomi run printed %q, want %q", out, later)
		}
	}
	// This nomi has no runner beside it and its release answers nothing, so
	// the build fails after the note.
	_, stderr, _ := c.run(app, "build", "main.nomi", "-o", exe(filepath.Join(t.TempDir(), "app")))
	if !strings.HasPrefix(stderr, note) || strings.Count(stderr, "note:") != 1 {
		t.Errorf("nomi build with markdown off the release wrote stderr\n%s\nwant it to open with the one note", stderr)
	}
	source := *c
	source.bin = nomiBin
	noNote(&source, "from source with markdown off the release")

	if out := c.must(app, "update"); !strings.HasPrefix(out, md+" "+later+" -> "+releaseTestVersion) {
		t.Errorf("a release's nomi update printed %q", out)
	}
	noNote(c, "after nomi update")

	// A dir entry keeps the repository and version, which stand for the
	// directory, and has no lock line.
	c.must(app, "add", md+"@"+later)
	local := filepath.Join(work, "md")
	for name, text := range markdown("local") {
		mustWrite(t, filepath.Join(local, name), text)
	}
	if out := c.must(app, "add", "../md"); out == "" || !strings.Contains(mustRead(t, filepath.Join(app, "nomi.toml")), `repo = "`+repo+`", subdir = "lib/markdown", version = "`+later+`", dir = "../md"`) {
		t.Fatalf("nomi add ../md printed %q and wrote\n%s", out, mustRead(t, filepath.Join(app, "nomi.toml")))
	}
	noNote(c, "with markdown from a directory")

	_, stderr, err := c.run(app, "add", sqlite)
	want := "nomi add: repository " + repo + " has no directory lib/sql/sqlite at " + releaseTestVersion
	if err == nil || stderr != want {
		t.Errorf("a release's nomi add of a library its tag does not hold: %v\n%s\nwant\n%s", err, stderr, want)
	}
}
