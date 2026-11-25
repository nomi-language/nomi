package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// `nomi add <repository>@<commit>` takes a package at a commit, which the
// program then runs; `nomi update` leaves the entry as it is and says so;
// a short commit is refused with what to write.
func TestPackages_AddAtACommit(t *testing.T) {
	c := newModuleCLI(t)
	commit := c.host.Commit("example.com/greet", map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String\n    \"dev\"\nend\n",
	})
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    greet\nend\n\nfn main()\n    io.print(greet.hello())\nend\n")

	if out := c.must(app, "add", "example.com/greet@"+commit); !strings.HasPrefix(out, "added greet, example.com/greet "+commit+" (import greet)\n") {
		t.Errorf("nomi add at a commit printed %q", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", commit = \""+commit+"\" }\n") {
		t.Errorf("nomi.toml after nomi add at a commit:\n%s", manifest)
	}
	if out := c.must(app, "run", "main.nomi"); out != "dev" {
		t.Errorf("nomi run printed %q", out)
	}
	if out := c.must(app, "update"); out != "greet is at commit "+commit+", which nomi update leaves as it is\nevery repository of the build is at the latest release of its line" {
		t.Errorf("nomi update printed %q", out)
	}
	if out := c.must(app, "update", "greet"); out != "greet is at commit "+commit+", which nomi update leaves as it is" {
		t.Errorf("nomi update greet printed %q", out)
	}
	_, stderr, err := c.run(app, "add", "example.com/greet@"+commit[:12])
	if want := "nomi add: example.com/greet@" + commit[:12] + `: "` + commit[:12] + `" is a short commit: write all 40 hex digits of it`; err == nil || stderr != want {
		t.Errorf("nomi add of a short commit: %v\n%s\nwant %s", err, stderr, want)
	}
}
