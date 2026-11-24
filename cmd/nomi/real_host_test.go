package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
)

// TestRealHost_FetchesTheFixture fetches modules from a real host: the
// private repository github.com/nomi-language/fetch-test, through git
// with the credentials of whoever runs it, over HTTPS or over SSH. It runs
// only with NOMI_REAL_HOST_TESTS=1, since it needs the network and access
// to that repository; nothing else sets the variable.
//
// The fixture holds module greet at its root (greet.version() is its tag,
// greet.hello(name)) and module words in words/, which names greet by its
// directory, tagged v0.1.0, v0.2.0, v1.0.0, v1.1.0-rc.1 and v2.0.0. The
// test adds greet at v0.1.0, which `nomi update` leaves there, since below
// v1 it stays within the minor version and there is no v0.1.x past it;
// adds it at v0.2.0, and then at v1.0.0, past which `nomi update` finds
// nothing either (it skips the prerelease and does not cross a major
// version); adds words
// with no version, at the v1.0.0 the project takes the repository at;
// checks the lock against the v1.0.0 commit and the modules' hashes; runs
// the program; adds the repository at v1.0.0's commit, which git fetches
// by its id, and checks the lock again; and runs the program again
// --offline with no git and every HTTP request sent to a closed port.
func TestRealHost_FetchesTheFixture(t *testing.T) {
	if os.Getenv("NOMI_REAL_HOST_TESTS") != "1" {
		t.Skip("reads github.com/nomi-language/fetch-test; set NOMI_REAL_HOST_TESTS=1 to run it")
	}
	const (
		repo    = "github.com/nomi-language/fetch-test"
		commit1 = "d9010ecc293a32ce9fca51901964de746395ed59" // v1.0.0
	)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NOMI_TEST_MODULE_HOSTS=") && !strings.HasPrefix(kv, "NOMI_MODULE_CACHE=") {
			env = append(env, kv)
		}
	}
	c := &moduleCLI{t: t, bin: nomiBin, env: append(env, "NOMI_COLOR=never", "NOMI_MODULE_CACHE="+modfetchtest.CacheDir(t))}
	app := t.TempDir()
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    greet\n    std::io\n    words\nend\n\n"+
		"fn main()\n    io.print(greet.hello(\"Ada\"))\n    io.print(words.welcome(\"Bo\"))\nend\n")
	latest := "every repository of the build is at the latest release of its line"

	c.must(app, "add", repo+"@v0.1.0")
	if out := c.must(app, "update"); out != latest {
		t.Errorf("nomi update from v0.1.0 printed %q, want %q: v0.2.0 is another 0.x line", out, latest)
	}
	c.must(app, "add", repo+"@v0.2.0")
	if out := c.must(app, "update"); out != latest {
		t.Errorf("nomi update at v0.2.0 printed %q, want %q", out, latest)
	}
	c.must(app, "add", repo+"@v1.0.0")
	if out := c.must(app, "update"); out != latest {
		t.Errorf("nomi update at v1.0.0 printed %q, want %q: v1.1.0-rc.1 is a prerelease and v2.0.0 another major version", out, latest)
	}
	out := c.must(app, "add", repo+"/words")
	if want := "added words, " + repo + "/words v1.0.0 (import words), at the version the project already takes " + repo + " at"; !strings.HasPrefix(out, want) {
		t.Errorf("nomi add of words printed\n%s\nwant it to start\n%s", out, want)
	}
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{
		repo + " . v1.0.0 " + commit1 + " sha256:3e0785d39c74ba9fba058fe47d635adeae40cdb33585ec4c121069cffa8148e7 direct\n",
		repo + " words v1.0.0 " + commit1 + " sha256:994631d5e99d61e6d93f008810e424d9370f41527df8b2dd815fa91a96b57f7d direct\n",
	} {
		if !strings.Contains(lock, want) {
			t.Errorf("nomi.lock lacks\n%s\nit is\n%s", want, lock)
		}
	}

	want := "welcome, Ada\nwelcome, Bo (greet v1.0.0)"
	if out := c.must(app, "run", "main.nomi"); out != want {
		t.Errorf("nomi run printed %q, want %q", out, want)
	}

	// The same files at v1.0.0's commit, which git fetches by its id: both
	// entries move to it, and the lock holds `commit` and the same hashes.
	c.must(app, "add", repo+"@"+commit1)
	lock = mustRead(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{
		repo + " . commit " + commit1 + " sha256:3e0785d39c74ba9fba058fe47d635adeae40cdb33585ec4c121069cffa8148e7 direct\n",
		repo + " words commit " + commit1 + " sha256:994631d5e99d61e6d93f008810e424d9370f41527df8b2dd815fa91a96b57f7d direct\n",
	} {
		if !strings.Contains(lock, want) {
			t.Errorf("nomi.lock at a commit lacks\n%s\nit is\n%s", want, lock)
		}
	}
	if out := c.must(app, "run", "main.nomi"); out != want {
		t.Errorf("nomi run at a commit printed %q, want %q", out, want)
	}
	c.setEnv("PATH", t.TempDir())
	for _, proxy := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		c.setEnv(proxy, "http://127.0.0.1:1")
	}
	c.setEnv("NO_PROXY", "")
	c.setEnv("no_proxy", "")
	if out := c.must(app, "run", "--offline", "main.nomi"); out != want {
		t.Errorf("nomi run --offline printed %q, want %q", out, want)
	}
}
