package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// `nomi outdated` lists each repository of the build with a later release
// and exits 0, `nomi update` raises a repository only a dependency names
// within its line and records it under [indirect], and `nomi outdated`
// then lists only what is behind in another line.
func TestModules_OutdatedAndUpdateCoverTheGraph(t *testing.T) {
	c := newModuleCLI(t)
	strutil := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":    "[module]\nname = \"strutil\"\n",
			"strutil.nomi": "pub fn v() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	for _, v := range []string{"v1.0.0", "v1.4.0", "v2.0.0"} {
		c.host.Tag("example.com/strutil", v, strutil(v))
	}
	c.host.Tag("example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"greet\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"greet.nomi": "pub fn hello() -> String\n    \"hi\"\nend\n",
	})
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet@v0.1.0")

	want := "repository           version  latest in its line  latest  reached\n" +
		"example.com/strutil  v1.0.0   v1.4.0              v2.0.0  through greet"
	if out := c.must(app, "outdated"); out != want {
		t.Errorf("nomi outdated printed\n%s\nwant\n%s", out, want)
	}
	if out := c.must(app, "update"); !strings.HasPrefix(out, "example.com/strutil v1.0.0 -> v1.4.0 (under [indirect]: only a dependency names it)\n") {
		t.Errorf("nomi update printed\n%s", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(manifest, "\n[indirect]\n\"example.com/strutil\" = \"v1.4.0\"\n") {
		t.Errorf("nomi.toml after nomi update:\n%s", manifest)
	}
	want = "repository           version  latest in its line  latest  reached\n" +
		"example.com/strutil  v1.4.0   v1.4.0              v2.0.0  through greet"
	if out := c.must(app, "outdated"); out != want {
		t.Errorf("nomi outdated after nomi update printed\n%s\nwant\n%s", out, want)
	}
	_, stderr, err := c.run(app, "outdated", "--offline")
	if err == nil || !strings.Contains(stderr, "needs the network") {
		t.Errorf("nomi outdated --offline: %v\n%s", err, stderr)
	}
	if out := c.must(app, "check", "."); !strings.Contains(out, "ok") {
		t.Errorf("nomi check with an [indirect] table printed %q", out)
	}
}
