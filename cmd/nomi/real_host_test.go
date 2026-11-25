package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
)

// TestRealHost_FetchesTheFixture fetches modules from a real host: the
// private repository github.com/nomi-language/fetch-test, through git
// with the credentials of whoever runs it, over HTTPS or over SSH. It runs
// only with NOMI_REAL_HOST_TESTS=1, since it needs the network and access
// to that repository; nothing else sets the variable.
//
// The fixture holds module greet at its root (greet.version() is its tag,
// greet.hello(name)) and module words in words/, which names greet by its
// directory, tagged v0.0.1, v0.0.2, v0.1.0, v0.2.0, v1.0.0, v1.1.0-rc.1 and
// v2.0.0. The test adds greet at v0.0.1 and then v0.0.2, each a line of its
// own, then at v0.1.0 and v0.2.0, each in the line of its minor version,
// and then at v1.0.0, in the line of its major version, where it skips the
// prerelease. At each `nomi update` leaves the project where it is and
// names v2.0.0 with the rule of the line's tier. It adds words
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
		commit1 = "c5e63aeebafc197952101b0753a9ce2e289fe9fb" // v1.0.0
	)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NOMI_TEST_PACKAGE_HOSTS=") && !strings.HasPrefix(kv, "NOMI_PACKAGE_CACHE=") {
			env = append(env, kv)
		}
	}
	c := &moduleCLI{t: t, bin: nomiBin, env: append(env, "NOMI_COLOR=never", "NOMI_PACKAGE_CACHE="+pkgfetchtest.CacheDir(t))}
	app := t.TempDir()
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    greet\n    std::io\n    words\nend\n\n"+
		"fn main()\n    io.print(greet.hello(\"Ada\"))\n    io.print(words.welcome(\"Bo\"))\nend\n")
	latest := "every repository of the build is at the latest release of its line"

	// In each tier update leaves the project where it is and names the
	// newest release there is, past the line.
	for _, step := range []struct{ at, rule string }{
		{"v0.0.1", "below v0.1.0 each release"},
		{"v0.0.2", "below v0.1.0 each release"},
		{"v0.1.0", "below v1.0.0 each minor version"},
		{"v0.2.0", "below v1.0.0 each minor version"},
		{"v1.0.0", "from v1.0.0 each major version"},
	} {
		c.must(app, "add", repo+"@"+step.at)
		want := latest + "\n" + repo + " has v2.0.0, past the " + step.at + " the project takes; " + step.rule +
			" is a line of its own, which nomi update does not leave: `nomi add " + repo + "@v2.0.0` takes it"
		if out := c.must(app, "update"); out != want {
			t.Errorf("nomi update at %s printed\n%s\nwant\n%s", step.at, out, want)
		}
	}
	out := c.must(app, "add", repo+"/words")
	if want := "added words, " + repo + "/words v1.0.0 (import words), at the version the project already takes " + repo + " at"; !strings.HasPrefix(out, want) {
		t.Errorf("nomi add of words printed\n%s\nwant it to start\n%s", out, want)
	}
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{
		repo + " . v1.0.0 " + commit1 + " sha256:541ed7927b15b1d6fd59f2c22ba99d2590fe079e8a0d7e6d9cf9d995fc1291da direct\n",
		repo + " words v1.0.0 " + commit1 + " sha256:a4576d558b742a336a985c902c9135ff216b6ab4d97d1bbf15dea57902e2d55e direct\n",
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
		repo + " . commit " + commit1 + " sha256:541ed7927b15b1d6fd59f2c22ba99d2590fe079e8a0d7e6d9cf9d995fc1291da direct\n",
		repo + " words commit " + commit1 + " sha256:a4576d558b742a336a985c902c9135ff216b6ab4d97d1bbf15dea57902e2d55e direct\n",
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
