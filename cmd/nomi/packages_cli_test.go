package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
)

// moduleCLI runs bin, nomiBin unless a test sets it, with a server of
// repositories standing for example.com and github.com
// (NOMI_TEST_PACKAGE_HOSTS), a package cache of its own, and neither `go`
// nor `git` on PATH, so no command reaches past the server.
type moduleCLI struct {
	t     *testing.T
	bin   string
	host  *pkgfetchtest.Server
	cache string
	env   []string
}

func newModuleCLI(t *testing.T) *moduleCLI {
	t.Helper()
	tmp := t.TempDir()
	host := pkgfetchtest.New(t)
	cache := pkgfetchtest.CacheDir(t)
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=" + bin,
		"NOMI_COLOR=never",
		"NOMI_RUNNER_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
		"NOMI_TEST_PACKAGE_HOSTS=" + host.Env("example.com", "github.com"),
		"NOMI_PACKAGE_CACHE=" + cache,
	}
	env = withUserDirs(env)
	if p := os.Getenv("NOMI_STD_PATH"); p != "" {
		env = append(env, "NOMI_STD_PATH="+p)
	}
	return &moduleCLI{t: t, bin: nomiBin, host: host, cache: cache, env: env}
}

// setEnv sets name to value in the environment c's commands run with.
func (c *moduleCLI) setEnv(name, value string) {
	for i, e := range c.env {
		if strings.HasPrefix(e, name+"=") {
			c.env[i] = name + "=" + value
			return
		}
	}
	c.env = append(c.env, name+"="+value)
}

func (c *moduleCLI) run(dir string, args ...string) (stdout, stderr string, err error) {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = dir
	cmd.Env = c.env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return strings.TrimSpace(out.String()), strings.TrimSpace(errOut.String()), err
}

func (c *moduleCLI) must(dir string, args ...string) string {
	c.t.Helper()
	out, stderr, err := c.run(dir, args...)
	if err != nil {
		c.t.Fatalf("nomi %s: %v\n%s\n%s", strings.Join(args, " "), err, out, stderr)
	}
	return out
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A pure-Nomi dependency is fetched from its repository's host with no Go
// toolchain, no git and no go.mod: `nomi add` names it in nomi.toml,
// fetches it and locks it in nomi.lock, `nomi run` and `nomi check` use
// it by its name, `--offline` refuses a cold package cache by naming the
// package, `nomi fetch` fills the cache so `--offline` works, and a run
// whose lock is current and whose module is cached asks the host nothing.
func TestPackages_APureNomiDependencyNeedsNoGo(t *testing.T) {
	c := newModuleCLI(t)
	c.host.Tag("example.com/strings2", "v1.2.0", map[string]string{
		"nomi.toml":     "[package]\nname = \"strings2\"\n",
		"strings2.nomi": "import strings2::twice\n\npub fn shout(s: String) -> String\n    twice.twice(String.uppercase(s))\nend\n",
		"twice.nomi":    "pub fn twice(s: String) -> String\n    \"${s}${s}\"\nend\n",
	})
	app := filepath.Join(t.TempDir(), "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    strings2\nend\n\nfn main()\n    io.print(strings2.shout(\"hey\"))\nend\n")

	// Offline with nothing cached: refused, naming the repository.
	_, stderr, err := c.run(app, "add", "--offline", "example.com/strings2@v1.2.0")
	if err == nil || !strings.Contains(stderr, "example.com/strings2") || !strings.Contains(stderr, "--offline") {
		t.Fatalf("nomi add --offline with a cold cache: %v\n%s", err, stderr)
	}

	// The program already imports the module, so add also says what its own
	// code reaches: nothing.
	if out := c.must(app, "add", "example.com/strings2"); out != "added strings2, example.com/strings2 v1.2.0 (import strings2)\n\npackages\n  strings2\n    no effects" {
		t.Fatalf("nomi add printed %q", out)
	}
	if out := c.must(app, "run", "main.nomi"); out != "HEYHEY" {
		t.Fatalf("nomi run printed %q, want HEYHEY", out)
	}
	before := c.host.Requests()
	if out := c.must(app, "check", "main.nomi"); !strings.Contains(out, "ok") {
		t.Fatalf("nomi check printed %q", out)
	}
	if n := c.host.Requests() - before; n != 0 {
		t.Errorf("nomi check with a current lock and a warm cache made %d requests", n)
	}

	// Another machine: the project as committed, an empty package cache.
	c.setEnv("NOMI_PACKAGE_CACHE", pkgfetchtest.CacheDir(t))
	_, stderr, err = c.run(app, "run", "--offline", "main.nomi")
	if err == nil {
		t.Fatal("nomi run --offline with a cold package cache succeeded")
	}
	for _, want := range []string{"package example.com/strings2 v1.2.0", "--offline"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("nomi run --offline with a cold cache does not say %q:\n%s", want, stderr)
		}
	}
	if out := c.must(app, "fetch"); !strings.Contains(out, "1 package in the package cache") {
		t.Fatalf("nomi fetch printed %q", out)
	}
	if out := c.must(app, "run", "--offline", "main.nomi"); out != "HEYHEY" {
		t.Fatalf("nomi run --offline after nomi fetch printed %q", out)
	}
	// A program argument spelled --offline is the program's.
	if out := c.must(app, "run", "main.nomi", "--offline"); out != "HEYHEY" {
		t.Fatalf("nomi run main.nomi --offline printed %q", out)
	}

	if _, err := os.Stat(filepath.Join(app, "go.mod")); err == nil {
		t.Errorf("a pure-Nomi project has a go.mod")
	}

	// No nomi.toml: nomi add says what to create.
	bare := filepath.Join(t.TempDir(), "bare")
	mustWrite(t, filepath.Join(bare, "main.nomi"), "fn main() end\n")
	_, stderr, err = c.run(bare, "add", "example.com/strings2")
	if err == nil || !strings.Contains(stderr, "no nomi.toml") {
		t.Fatalf("nomi add with no nomi.toml: %v\n%s", err, stderr)
	}
}
