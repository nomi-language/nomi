package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// `nomi outdated` lists each repository of the build with a later release
// and exits 0, `nomi update` raises a repository only a dependency names
// within its line and records it in nomi.lock alone, and `nomi outdated`
// then lists only what is behind in another line. `nomi check` and `nomi
// fetch` keep the raise; `nomi fetch --update` returns the repository to
// the version its dependency asks for and says so. An [indirect] table is
// no table of nomi.toml's.
func TestPackages_OutdatedAndUpdateCoverTheGraph(t *testing.T) {
	c := newModuleCLI(t)
	strutil := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":    "[package]\nname = \"strutil\"\n",
			"strutil.nomi": "pub fn v() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	for _, v := range []string{"v1.0.0", "v1.4.0", "v2.0.0"} {
		c.host.Tag("example.com/strutil", v, strutil(v))
	}
	c.host.Tag("example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
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
	manifest := mustRead(t, filepath.Join(app, "nomi.toml"))
	if out := c.must(app, "update"); !strings.HasPrefix(out, "example.com/strutil v1.0.0 -> v1.4.0 (only a dependency names it)\n") {
		t.Errorf("nomi update printed\n%s", out)
	}
	if after := mustRead(t, filepath.Join(app, "nomi.toml")); after != manifest {
		t.Errorf("nomi.toml after nomi update:\n%s\nwant it unchanged:\n%s", after, manifest)
	}
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	if !strings.Contains(lock, "example.com/strutil . v1.4.0 ") {
		t.Errorf("nomi.lock after nomi update:\n%s", lock)
	}
	want = "repository           version  latest in its line  latest  reached\n" +
		"example.com/strutil  v1.4.0   v1.4.0              v2.0.0  through greet"
	if out := c.must(app, "outdated"); out != want {
		t.Errorf("nomi outdated after nomi update printed\n%s\nwant\n%s", out, want)
	}
	_, stderr, err := c.run(app, "outdated", "--offline")
	if err == nil || !strings.Contains(stderr, `nomi outdated: unknown flag "--offline"`) {
		t.Errorf("nomi outdated --offline: %v\n%s", err, stderr)
	}
	if out := c.must(app, "check", "."); !strings.Contains(out, "ok") {
		t.Errorf("nomi check after nomi update printed %q", out)
	}
	if out := c.must(app, "fetch"); out != "2 packages in the package cache for ." {
		t.Errorf("nomi fetch printed %q", out)
	}
	if after := mustRead(t, filepath.Join(app, "nomi.lock")); after != lock {
		t.Errorf("nomi.lock after nomi check and nomi fetch:\n%s\nwant it unchanged:\n%s", after, lock)
	}
	if out := c.must(app, "fetch", "--update"); out != "example.com/strutil v1.4.0 -> v1.0.0 (down: no package asks for more)\n2 packages in the package cache for ." {
		t.Errorf("nomi fetch --update printed %q", out)
	}
	if lock := mustRead(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . v1.0.0 ") {
		t.Errorf("nomi.lock after nomi fetch --update:\n%s", lock)
	}

	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest+"\n[indirect]\n\"example.com/strutil\" = \"v1.4.0\"\n")
	_, stderr, err = c.run(app, "check", ".")
	if err == nil || !strings.Contains(stderr, "nomi.toml has no [indirect] table; its tables are [dependencies], [effects], [i18n], [money], [package]") {
		t.Errorf("nomi check with an [indirect] table: %v\n%s", err, stderr)
	}
}
