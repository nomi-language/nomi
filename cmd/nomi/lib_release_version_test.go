package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestModules_AReleaseTakesTheLibModulesAtItsVersion: a release `nomi` adds
// a first-party lib/ module at its own version, and one named on the
// command line at that version. While nomi.lock takes a lib/ module at
// another version, `run`, `check`, `test` and `build` say so in one note
// before anything else and still run; `nomi update` moves it back, and a
// dir entry, or a `nomi` built from source, prints no note. A release
// whose tag the proxy lacks is refused with that tag named.
func TestModules_AReleaseTakesTheLibModulesAtItsVersion(t *testing.T) {
	c := newModuleCLI(t)
	c.bin = releaseNomi(t)
	c.env = append(c.env, "NOMI_RELEASE_BASE_URL="+unreachableRelease)
	const (
		md     = "github.com/nomi-language/nomi/lib/markdown"
		sqlite = "github.com/nomi-language/nomi/lib/sql/sqlite"
		commit = "v0.0.0-20261120000000-cccccccccccc"
	)
	markdown := func(v string) map[string]string {
		return map[string]string{
			"go.mod":        "module " + md + "\n",
			"nomi.toml":     "[module]\nname = \"markdown\"\npath = \"" + md + "\"\n",
			"markdown.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	publishFiles(t, c.proxy, md, releaseTestVersion, markdown(releaseTestVersion))
	publishFiles(t, c.proxy, md, commit, markdown(commit))
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    markdown\nend\n\nfn main()\n    io.print(markdown.version())\nend\n")
	mustWrite(t, filepath.Join(app, "version_test.nomi"), "import markdown\n\ntest \"a version\"\n    assert markdown.version() != \"\"\nend\n")

	if out := c.must(app, "add", md); !strings.HasPrefix(out, "added markdown, "+md+" "+releaseTestVersion+" (import markdown)") {
		t.Fatalf("a release's nomi add of markdown printed %q", out)
	}
	note := "note: nomi.lock takes " + md + " " + commit + ", and this nomi is release " + releaseTestVersion +
		", which takes the first-party libraries at " + releaseTestVersion + "; run `nomi update` to move them to it"
	noNote := func(cli *moduleCLI, when string) {
		t.Helper()
		out, stderr, err := cli.run(app, "run", "main.nomi")
		if err != nil || strings.Contains(stderr, "note:") {
			t.Errorf("nomi run %s: %v\nstdout: %s\nstderr: %s", when, err, out, stderr)
		}
	}
	noNote(c, "with markdown at the release")

	c.must(app, "add", md+"@"+commit)
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, `version = "`+commit+`"`) {
		t.Fatalf("nomi add %s@%s did not take the version named:\n%s", md, commit, manifest)
	}
	for _, args := range [][]string{{"run", "main.nomi"}, {"check", "main.nomi"}, {"test", "."}} {
		out, stderr, err := c.run(app, args...)
		if err != nil {
			t.Errorf("nomi %s with markdown off the release: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, stderr)
		}
		if !strings.HasPrefix(stderr, note) || strings.Count(stderr, "note:") != 1 {
			t.Errorf("nomi %s with markdown off the release wrote stderr\n%s\nwant it to open with the one note\n%s", strings.Join(args, " "), stderr, note)
		}
		if args[0] == "run" && out != commit {
			t.Errorf("nomi run printed %q, want %q", out, commit)
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

	if out := c.must(app, "update"); !strings.HasPrefix(out, md+" "+commit+" -> "+releaseTestVersion) {
		t.Errorf("a release's nomi update printed %q", out)
	}
	noNote(c, "after nomi update")

	// A dir entry keeps its version, which is not read.
	c.must(app, "add", md+"@"+commit)
	local := filepath.Join(work, "md")
	for name, text := range markdown("local") {
		mustWrite(t, filepath.Join(local, name), text)
	}
	if out := c.must(app, "add", "../md"); out == "" || !strings.Contains(mustRead(t, filepath.Join(app, "nomi.toml")), `version = "`+commit+`", dir = "../md"`) {
		t.Fatalf("nomi add ../md printed %q and wrote\n%s", out, mustRead(t, filepath.Join(app, "nomi.toml")))
	}
	noNote(c, "with markdown from a directory")

	publishFiles(t, c.proxy, sqlite, commit, map[string]string{
		"nomi.toml":   "[module]\nname = \"sqlite\"\npath = \"" + sqlite + "\"\n",
		"sqlite.nomi": "pub fn version() -> String\n    \"" + commit + "\"\nend\n",
	})
	_, stderr, err := c.run(app, "add", sqlite)
	want := "nomi add: this nomi is release " + releaseTestVersion + ", which takes " + sqlite + " at " + releaseTestVersion +
		", and the module proxy does not have that version: the release was published without the tag lib/sql/sqlite/" + releaseTestVersion +
		"; `nomi add " + sqlite + "@<version>` takes another version"
	if err == nil || stderr != want {
		t.Errorf("a release's nomi add of a module its release did not tag: %v\n%s\nwant\n%s", err, stderr, want)
	}
}
