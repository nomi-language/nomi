package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// moduleCLI runs nomiBin in a directory with a file:// module proxy, no
// checksum database, a private module cache and no `go` on PATH.
type moduleCLI struct {
	t     *testing.T
	proxy string
	env   []string
}

func newModuleCLI(t *testing.T) *moduleCLI {
	t.Helper()
	tmp := t.TempDir()
	proxy := filepath.Join(tmp, "proxy")
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=" + bin,
		"NOMI_COLOR=never",
		"NOMI_FFIRUN_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
		"NOMI_TOOLCHAIN_ROOT=" + filepath.Join(tmp, "toolchains"),
		"GOPROXY=file://" + filepath.ToSlash(proxy),
		"GOSUMDB=off",
		"GOMODCACHE=" + filepath.Join(tmp, "modcache"),
		"GOFLAGS=-modcacherw",
	}
	if p := os.Getenv("NOMI_STD_PATH"); p != "" {
		env = append(env, "NOMI_STD_PATH="+p)
	}
	return &moduleCLI{t: t, proxy: proxy, env: env}
}

func (c *moduleCLI) run(dir string, args ...string) (stdout, stderr string, err error) {
	c.t.Helper()
	cmd := exec.Command(nomiBin, args...)
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

// `nomi new` writes nomi.toml, go.mod and a main.nomi that runs, in a new
// directory named for the path, and refuses one that has a go.mod.
func TestModules_NewStartsAProject(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	out := c.must(work, "new", "github.com/you/hello-world/v2")
	want := "wrote hello_world/nomi.toml, hello_world/go.mod and hello_world/main.nomi\nnext: cd hello_world && nomi run main.nomi"
	if out != want {
		t.Fatalf("nomi new printed\n%s\nwant\n%s", out, want)
	}
	dir := filepath.Join(work, "hello_world")
	if got := mustRead(t, filepath.Join(dir, "nomi.toml")); got != "[module]\nname = \"hello_world\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if got, want := mustRead(t, filepath.Join(dir, "go.mod")), "module github.com/you/hello-world/v2\n\ngo "+gotoolchain.GoLine()+"\n"; got != want {
		t.Errorf("go.mod is\n%s\nwant\n%s", got, want)
	}
	if out := c.must(dir, "run", "main.nomi"); out != "hello" {
		t.Errorf("the new project's main printed %q", out)
	}
	if out := c.must(dir, "fmt", "-l", "main.nomi"); out != "" {
		t.Errorf("the new main.nomi is not formatted: %s", out)
	}

	_, stderr, err := c.run(work, "new", "github.com/you/hello-world/v2", "hello_world")
	if err == nil || stderr != "nomi new: hello_world already holds a go.mod" {
		t.Errorf("nomi new over a project: %v\n%s", err, stderr)
	}

	// In the current directory, the next command needs no cd; a module
	// with only a nomi.toml gets its go.mod and keeps the manifest.
	lib := filepath.Join(work, "lib")
	mustWrite(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"lib\"\n")
	out = c.must(lib, "new", "example.com/lib", ".")
	if out != "wrote go.mod\nkept nomi.toml, which was already there\nnext: nomi add <module>" {
		t.Errorf("nomi new beside a nomi.toml printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(lib, "nomi.toml")); got != "[module]\nname = \"lib\"\n" {
		t.Errorf("nomi new rewrote a nomi.toml:\n%s", got)
	}

	_, stderr, err = c.run(work, "new", "not a path")
	if err == nil || !strings.Contains(stderr, `nomi new: "not a path" is not a module path`) {
		t.Errorf("nomi new with a bad path: %v\n%s", err, stderr)
	}
}

// `nomi add <directory>` writes a require and a replace for the Nomi module
// there, and refuses a directory with no nomi.toml or no go.mod.
func TestModules_AddALocalModule(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	app := filepath.Join(work, "app")
	c.must(work, "new", "example.com/app")
	lib := filepath.Join(app, "macros", "csvdata")
	mustWrite(t, filepath.Join(lib, "go.mod"), "module example.com/csvdata\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"csvdata\"\n")
	mustWrite(t, filepath.Join(lib, "csvdata.nomi"), "pub fn rows() -> Int {\n    3\n}\n")

	if out := c.must(app, "add", "./macros/csvdata"); out != "added example.com/csvdata from ./macros/csvdata (module csvdata; import csvdata)" {
		t.Fatalf("nomi add ./macros/csvdata printed %q", out)
	}
	goMod := mustRead(t, filepath.Join(app, "go.mod"))
	for _, want := range []string{"require example.com/csvdata v0.0.0\n", "replace example.com/csvdata => ./macros/csvdata\n"} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, goMod)
		}
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import {\n    std/io\n    csvdata\n}\n\nfn main() {\n    io.print(csvdata.rows())\n}\n")
	if out := c.must(app, "run", "main.nomi"); out != "3" {
		t.Errorf("a program using the local module printed %q", out)
	}

	plain := filepath.Join(app, "plain")
	mustWrite(t, filepath.Join(plain, "go.mod"), "module example.com/plain\n\ngo 1.27.0\n")
	_, stderr, err := c.run(app, "add", "./plain")
	if err == nil || stderr != "nomi add: ./plain has no nomi.toml, so it is not a Nomi module" {
		t.Errorf("nomi add of a directory with no nomi.toml: %v\n%s", err, stderr)
	}
	bare := filepath.Join(app, "bare")
	mustWrite(t, filepath.Join(bare, "nomi.toml"), "[module]\nname = \"bare\"\n")
	_, stderr, err = c.run(app, "add", "./bare")
	if err == nil || stderr != "nomi add: ./bare has no go.mod, so it has no module path; run `nomi new <module path> ./bare` to give it one" {
		t.Errorf("nomi add of a directory with no go.mod: %v\n%s", err, stderr)
	}
}

// `nomi remove` refuses a module a file imports, naming the file, and
// removes one nothing imports, with what only it needed.
func TestModules_RemoveADependency(t *testing.T) {
	c := newModuleCLI(t)
	publishFiles(t, c.proxy, "example.com/strutil", "v1.0.0", map[string]string{
		"go.mod":     "module example.com/strutil\n\ngo 1.22\n",
		"strutil.go": "package strutil\n",
	})
	publishFiles(t, c.proxy, "example.com/greet", "v0.1.0", map[string]string{
		"go.mod":     "module example.com/greet\n\ngo 1.22\n\nrequire example.com/strutil v1.0.0\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String {\n    \"hello\"\n}\n",
	})
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet")
	mustWrite(t, filepath.Join(app, "lib", "talk.nomi"), "import greet\n\npub fn talk() -> String {\n    greet.hello()\n}\n")

	_, stderr, err := c.run(app, "remove", "example.com/greet")
	if err == nil || stderr != "nomi remove: lib/talk.nomi:1 imports greet; remove the import first" {
		t.Fatalf("nomi remove of an imported module: %v\n%s", err, stderr)
	}
	if !strings.Contains(mustRead(t, filepath.Join(app, "go.mod")), "example.com/greet") {
		t.Fatal("a refused nomi remove changed go.mod")
	}

	if err := os.Remove(filepath.Join(app, "lib", "talk.nomi")); err != nil {
		t.Fatal(err)
	}
	out := c.must(app, "remove", "greet")
	if out != "removed example.com/greet v0.1.0\nremoved example.com/strutil v1.0.0, which nothing else needs" {
		t.Fatalf("nomi remove printed\n%s", out)
	}
	if goMod := mustRead(t, filepath.Join(app, "go.mod")); strings.Contains(goMod, "require") {
		t.Errorf("go.mod after nomi remove:\n%s", goMod)
	}
	if goSum := mustRead(t, filepath.Join(app, "go.sum")); goSum != "" {
		t.Errorf("go.sum after nomi remove:\n%s", goSum)
	}
	_, stderr, err = c.run(app, "remove", "greet")
	if err == nil || stderr != "nomi remove: go.mod does not require greet" {
		t.Errorf("nomi remove of a module go.mod does not require: %v\n%s", err, stderr)
	}
}

// `nomi update` moves requirements to their latest release of the same
// module path and prints each move.
func TestModules_UpdateWithinAMajorVersion(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(v string) map[string]string {
		return map[string]string{
			"go.mod":     "module example.com/greet\n\ngo 1.22\n",
			"nomi.toml":  "[module]\nname = \"greet\"\n",
			"greet.nomi": "pub fn hello() -> String {\n    \"" + v + "\"\n}\n",
		}
	}
	publishFiles(t, c.proxy, "example.com/greet", "v1.0.0", greet("v1.0.0"))
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet")
	if out := c.must(app, "update"); out != "every requirement is at its latest release" {
		t.Errorf("nomi update with nothing newer printed %q", out)
	}

	publishFiles(t, c.proxy, "example.com/greet", "v1.2.0", greet("v1.2.0"))
	publishFiles(t, c.proxy, "example.com/greet/v2", "v2.0.0", map[string]string{
		"go.mod":     "module example.com/greet/v2\n\ngo 1.22\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String {\n    \"v2\"\n}\n",
	})
	if out := c.must(app, "update", "greet"); out != "example.com/greet v1.0.0 -> v1.2.0" {
		t.Fatalf("nomi update greet printed %q", out)
	}
	if goMod := mustRead(t, filepath.Join(app, "go.mod")); !strings.Contains(goMod, "require example.com/greet v1.2.0\n") {
		t.Errorf("go.mod after nomi update:\n%s", goMod)
	}
	goSum := mustRead(t, filepath.Join(app, "go.sum"))
	if strings.Contains(goSum, "example.com/greet v1.0.0 h1:") || !strings.Contains(goSum, "example.com/greet v1.2.0 h1:") {
		t.Errorf("go.sum after nomi update:\n%s", goSum)
	}
	if out := c.must(app, "update", "example.com/greet"); out != "example.com/greet is at its latest release" {
		t.Errorf("a second nomi update printed %q", out)
	}
	_, stderr, err := c.run(app, "update", "example.com/none")
	if err == nil || stderr != "nomi update: go.mod does not require example.com/none" {
		t.Errorf("nomi update of a module go.mod does not require: %v\n%s", err, stderr)
	}
}

// With no go.mod, the module commands say to start a project with nomi new.
func TestModules_CommandsWithNoGoModNameNomiNew(t *testing.T) {
	c := newModuleCLI(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"add", "example.com/x"}, {"remove", "x"}, {"update"}} {
		_, stderr, err := c.run(dir, args...)
		if err == nil || !strings.Contains(stderr, "no go.mod in") || !strings.Contains(stderr, "nomi new <module path>") {
			t.Errorf("nomi %s with no go.mod: %v\n%s", args[0], err, stderr)
		}
	}
}

func TestModules_HelpListsTheModuleCommands(t *testing.T) {
	help, err := exec.Command(nomiBin, "help").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nomi new <module path> [dir]", "nomi add <directory>", "nomi remove <module>", "nomi update [module]"} {
		if !strings.Contains(string(help), want) {
			t.Errorf("nomi help does not list %q", want)
		}
	}
}
