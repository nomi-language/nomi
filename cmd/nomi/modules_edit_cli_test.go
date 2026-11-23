package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/modfetch"
)

// moduleCLI runs bin, nomiBin unless a test sets it, in a directory with a
// file:// module proxy, no checksum database, a private module cache and no
// `go` on PATH.
type moduleCLI struct {
	t     *testing.T
	bin   string
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
		"NOMI_RUNNER_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
		"GOPROXY=" + fileuri.FromPath(proxy),
		"GOSUMDB=off",
		"GOMODCACHE=" + filepath.Join(tmp, "modcache"),
		"GOFLAGS=-modcacherw",
	}
	env = withUserDirs(env)
	if p := os.Getenv("NOMI_STD_PATH"); p != "" {
		env = append(env, "NOMI_STD_PATH="+p)
	}
	return &moduleCLI{t: t, bin: nomiBin, proxy: proxy, env: env}
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

// `nomi new` writes nomi.toml, a main.nomi that runs, an AGENTS.md that
// links the cheat sheet and the go.mod nomi keeps, in a new directory named
// for the path, and refuses one that has a nomi.toml.
func TestModules_NewStartsAProject(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	out := c.must(work, "new", "github.com/you/hello-world/v2")
	want := "wrote hello_world/nomi.toml, hello_world/main.nomi, hello_world/AGENTS.md and hello_world/go.mod\nnext: cd hello_world && nomi run main.nomi"
	if out != want {
		t.Fatalf("nomi new printed\n%s\nwant\n%s", out, want)
	}
	dir := filepath.Join(work, "hello_world")
	if got := mustRead(t, filepath.Join(dir, "nomi.toml")); got != "[module]\nname = \"hello_world\"\npath = \"github.com/you/hello-world/v2\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if got, want := mustRead(t, filepath.Join(dir, "go.mod")), string(modfetch.GoModText("github.com/you/hello-world/v2")); got != want {
		t.Errorf("go.mod is\n%s\nwant\n%s", got, want)
	}
	agents := mustRead(t, filepath.Join(dir, "AGENTS.md"))
	for _, want := range []string{"# hello_world\n", "https://nomi-lang.org/cheatsheet.md", "`nomi test`", "`nomi fmt -w .`"} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md has no %q:\n%s", want, agents)
		}
	}
	if out := c.must(dir, "run", "main.nomi"); out != "hello" {
		t.Errorf("the new project's main printed %q", out)
	}
	if out := c.must(dir, "fmt", "-l", "main.nomi"); out != "" {
		t.Errorf("the new main.nomi is not formatted: %s", out)
	}

	_, stderr, err := c.run(work, "new", "github.com/you/hello-world/v2", "hello_world")
	if err == nil || stderr != "nomi new: hello_world already holds a nomi.toml, so it is a project already" {
		t.Errorf("nomi new over a project: %v\n%s", err, stderr)
	}

	// In the current directory, the next command needs no cd, and a
	// main.nomi already there is kept.
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "fn main() end\n")
	out = c.must(app, "new", "example.com/app", ".")
	if out != "wrote nomi.toml, AGENTS.md and go.mod\nkept main.nomi, which was already there\nnext: nomi run main.nomi" {
		t.Errorf("nomi new . printed\n%s", out)
	}

}

// `nomi new <name>` starts a module nobody downloads: its nomi.toml has no
// path and it has no go.mod, and it runs, checks and builds by its
// directory.
func TestNew_ANameAloneStartsAProject(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	out := c.must(work, "new", "hello")
	if want := "wrote hello/nomi.toml, hello/main.nomi and hello/AGENTS.md\nnext: cd hello && nomi run main.nomi"; out != want {
		t.Fatalf("nomi new hello printed\n%s\nwant\n%s", out, want)
	}
	dir := filepath.Join(work, "hello")
	if got := mustRead(t, filepath.Join(dir, "nomi.toml")); got != "[module]\nname = \"hello\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if isRegular(filepath.Join(dir, "go.mod")) {
		t.Error("nomi new hello wrote a go.mod")
	}
	agents := mustRead(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(agents, "`nomi run .`") || strings.Contains(agents, "go.mod") {
		t.Errorf("AGENTS.md of a module with no path:\n%s", agents)
	}
	for _, args := range [][]string{{"run", "."}, {"run", "main.nomi"}} {
		if out := c.must(dir, args...); out != "hello" {
			t.Errorf("nomi %s printed %q", strings.Join(args, " "), out)
		}
	}
	if out := c.must(dir, "check", "."); out != "ok main.nomi" {
		t.Errorf("nomi check . printed %q", out)
	}
	if out := c.must(work, "run", "hello"); out != "hello" {
		t.Errorf("nomi run hello printed %q", out)
	}
}

// A refused `nomi new` writes nothing: a name that is no module name, a
// path that is no module path, std, and a directory that is a project.
func TestNew_ARefusedNewWritesNothing(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	for _, tc := range []struct{ arg, want string }{
		{"not a path", `nomi new: "not a path" is not a module name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world; ` +
			"a module others download is named by the path it is published at, such as github.com/you/hello_world"},
		{"Hello", `nomi new: "Hello" is not a module name`},
		{"hello-world", `nomi new: "hello-world" is not a module name`},
		{"2fast", `nomi new: "2fast" is not a module name`},
		{"hello/world", `nomi new: "hello/world" is not a module path: malformed module path "hello/world": missing dot in first path element; ` +
			"a module path is usually its repository's address, such as github.com/you/project"},
		{"std", "nomi new: std is the standard library's name; choose another"},
		{"example.com/std", "nomi new: example.com/std gives no usable module name (its last element names the module); choose another path"},
	} {
		_, stderr, err := c.run(work, "new", tc.arg)
		if err == nil || !strings.HasPrefix(stderr, tc.want) {
			t.Errorf("nomi new %q: %v\n%s\nwant it to start\n%s", tc.arg, err, stderr, tc.want)
		}
	}
	if entries, err := os.ReadDir(work); err != nil || len(entries) != 0 {
		t.Errorf("a refused nomi new wrote %v (%v)", entries, err)
	}
}

// `nomi add <directory>` writes a dir entry for the Nomi module there, under
// its own name or the name before `=`, and refuses a directory with no
// nomi.toml.
func TestModules_AddALocalModule(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	app := filepath.Join(work, "app")
	c.must(work, "new", "example.com/app")
	lib := filepath.Join(app, "csvdata")
	mustWrite(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"csvdata\"\n")
	mustWrite(t, filepath.Join(lib, "csvdata.nomi"), "pub fn rows() -> Int\n    3\nend\n")

	if out := c.must(app, "add", "./csvdata"); out != "added csvdata from ./csvdata (import csvdata)\n\nmodules\n  csvdata\n    not imported yet" {
		t.Fatalf("nomi add ./csvdata printed %q", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(manifest, "\n\n[dependencies]\ncsvdata = { dir = \"./csvdata\" }\n") {
		t.Errorf("nomi.toml after nomi add ./csvdata:\n%s", manifest)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    csvdata\nend\n\nfn main()\n    io.print(csvdata.rows())\nend\n")
	if out := c.must(app, "run", "main.nomi"); out != "3" {
		t.Errorf("a program using the local module printed %q", out)
	}

	// Under another name, the project's imports use that name.
	if out := c.must(app, "add", "rows=./csvdata"); !strings.HasPrefix(out, "added rows from ./csvdata (import rows)") {
		t.Fatalf("nomi add rows=./csvdata printed %q", out)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    rows::csvdata\nend\n\nfn main()\n    io.print(csvdata.rows())\nend\n")
	if out := c.must(app, "run", "main.nomi"); out != "3" {
		t.Errorf("a program using the local module under another name printed %q", out)
	}

	plain := filepath.Join(app, "plain")
	mustWrite(t, filepath.Join(plain, "go.mod"), "module example.com/plain\n\ngo 1.27.0\n")
	_, stderr, err := c.run(app, "add", "./plain")
	if err == nil || stderr != "nomi add: ./plain has no nomi.toml, so it is not a Nomi module" {
		t.Errorf("nomi add of a directory with no nomi.toml: %v\n%s", err, stderr)
	}
}

// A module added by its directory brings the modules its own dir entries
// name, from their directories: the project runs with one entry, nothing
// is downloaded at the entry's placeholder version, and nomi.lock, which
// holds only downloaded modules, is not written. A downloaded module's
// dir entries are not read, and the module it names comes from the proxy.
func TestModules_ADirectoryModuleBringsItsDirectoryDependencies(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	mustWrite(t, filepath.Join(work, "lib", "kit", "nomi.toml"), "[module]\nname = \"kit\"\npath = \"example.com/lib/kit\"\n\n[dependencies]\n"+
		"util = { module = \"example.com/lib/util\", version = \"v0.0.0-20261102000000-000000000000\", dir = \"../util\" }\n")
	mustWrite(t, filepath.Join(work, "lib", "kit", "kit.nomi"), "import util\n\npub fn twice(n: Int) -> Int\n    util.add(n, n)\nend\n")
	mustWrite(t, filepath.Join(work, "lib", "util", "nomi.toml"), "[module]\nname = \"util\"\npath = \"example.com/lib/util\"\n")
	mustWrite(t, filepath.Join(work, "lib", "util", "util.nomi"), "pub fn add(a: Int, b: Int) -> Int\n    a + b\nend\n")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(kit.twice(21))\nend\n")

	if out := c.must(app, "add", "../lib/kit"); !strings.HasPrefix(out, "added kit from ../lib/kit (import kit)") {
		t.Fatalf("nomi add ../lib/kit printed %q", out)
	}
	if out := c.must(app, "run", "main.nomi"); out != "42" {
		t.Fatalf("the program printed %q, want 42", out)
	}
	if out := c.must(app, "fetch"); out != "0 modules in the module cache for ." {
		t.Errorf("nomi fetch printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(app, "nomi.lock")); err == nil {
		t.Errorf("nomi.lock was written for dependencies that are all directories:\n%s", mustRead(t, filepath.Join(app, "nomi.lock")))
	}

	publishFiles(t, c.proxy, "example.com/strutil", "v1.0.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\npath = \"example.com/strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	publishFiles(t, c.proxy, "example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"greet\"\npath = \"example.com/greet\"\n\n[dependencies]\nstrutil = { module = \"example.com/strutil\", version = \"v1.0.0\", dir = \"../strutil\" }\n",
		"greet.nomi": "import strutil\n\npub fn hello() -> String\n    strutil.twice(\"hi\")\nend\n",
	})
	c.must(app, "add", "example.com/greet")
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{"example.com/greet v0.1.0 ", "example.com/strutil v1.0.0 "} {
		if !strings.Contains(lock, want) {
			t.Errorf("nomi.lock has no line %q:\n%s", want, lock)
		}
	}
	if strings.Contains(lock, "lib/") {
		t.Errorf("nomi.lock holds a module from a directory:\n%s", lock)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    greet\n    kit\nend\n\nfn main()\n    io.print(\"${greet.hello()} ${kit.twice(1)}\")\nend\n")
	if out := c.must(app, "run", "main.nomi"); out != "hihi 2" {
		t.Errorf("the program printed %q, want hihi 2", out)
	}
}

// A [sqlkit] table is the sqlkit tools' only once [dependencies] names
// sqlkit, and `nomi add sqlkit=<dir>` is what names it: the edit is not
// blocked by the error it resolves, nor by one elsewhere in nomi.toml, and
// `nomi remove` is not blocked by the error it leaves. `nomi check`
// reports the table while no dependency names it.
func TestModules_AddTheDependencyAToolTableNames(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	app := filepath.Join(work, "app")
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[sqlkit.database.main]\ndialect = \"sqlite\"\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)
	mustWrite(t, filepath.Join(app, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n")
	kit := filepath.Join(work, "kit")
	mustWrite(t, filepath.Join(kit, "nomi.toml"), "[module]\nname = \"sqlkit\"\n")
	mustWrite(t, filepath.Join(kit, "gen.nomi"), "pub fn rows() -> Int\n    3\nend\n")

	noTable := "nomi.toml has no [sqlkit] table"
	checkFails := func(why string) {
		t.Helper()
		out, stderr, err := c.run(app, "check", ".")
		if err == nil || !strings.Contains(out+stderr, noTable) {
			t.Fatalf("nomi check %s: %v, want an error saying %q:\n%s\n%s", why, err, noTable, out, stderr)
		}
	}
	checkFails("before the dependency is added")

	if out := c.must(app, "add", "sqlkit=../kit"); !strings.HasPrefix(out, "added sqlkit from ../kit (import sqlkit)") {
		t.Fatalf("nomi add sqlkit=<dir> printed %q", out)
	}
	if got, want := mustRead(t, filepath.Join(app, "nomi.toml")), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nsqlkit = { dir = \"../kit\" }\n\n[sqlkit.database.main]\ndialect = \"sqlite\"\n"; got != want {
		t.Fatalf("nomi.toml after nomi add:\n%s\nwant\n%s", got, want)
	}
	if out, stderr, err := c.run(app, "check", "."); err != nil {
		t.Fatalf("nomi check with the dependency named: %v\n%s\n%s", err, out, stderr)
	}

	if out := c.must(app, "remove", "sqlkit"); out != "removed sqlkit (from ../kit)" {
		t.Fatalf("nomi remove sqlkit printed %q", out)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); got != manifest {
		t.Fatalf("nomi.toml after nomi remove:\n%s\nwant\n%s", got, manifest)
	}
	checkFails("after the dependency is removed")

	// An error outside [dependencies] is nomi check's, not the edit's.
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest+"\n[i18n]\nlocales = [\"pt_BR\"]\n")
	if out := c.must(app, "add", "sqlkit=../kit"); !strings.HasPrefix(out, "added sqlkit from ../kit (import sqlkit)") {
		t.Fatalf("nomi add beside an [i18n] error printed %q", out)
	}
	if out, stderr, err := c.run(app, "check", "."); err == nil || !strings.Contains(out+stderr, "pt_BR") {
		t.Fatalf("nomi check with a bad locale: %v, want the [i18n] error:\n%s\n%s", err, out, stderr)
	}
}

// `nomi remove` refuses a dependency a file imports, naming the file, and
// removes one nothing imports, with what only it needed.
func TestModules_RemoveADependency(t *testing.T) {
	c := newModuleCLI(t)
	publishFiles(t, c.proxy, "example.com/strutil", "v1.0.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	publishFiles(t, c.proxy, "example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"greet\"\n\n[dependencies]\nstrutil = { module = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"greet.nomi": "pub fn hello() -> String\n    \"hello\"\nend\n",
	})
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet")
	mustWrite(t, filepath.Join(app, "lib", "talk.nomi"), "import greet\n\npub fn talk() -> String\n    greet.hello()\nend\n")

	_, stderr, err := c.run(app, "remove", "example.com/greet")
	if err == nil || stderr != "nomi remove: lib/talk.nomi:1 imports greet; remove the import first" {
		t.Fatalf("nomi remove of an imported dependency: %v\n%s", err, stderr)
	}
	if !strings.Contains(mustRead(t, filepath.Join(app, "nomi.toml")), "example.com/greet") {
		t.Fatal("a refused nomi remove changed nomi.toml")
	}

	if err := os.Remove(filepath.Join(app, "lib", "talk.nomi")); err != nil {
		t.Fatal(err)
	}
	out := c.must(app, "remove", "greet")
	if out != "removed greet (example.com/greet v0.1.0)\nremoved example.com/strutil v1.0.0, which nothing else needs" {
		t.Fatalf("nomi remove printed\n%s", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); strings.Contains(manifest, "dependencies") {
		t.Errorf("nomi.toml after nomi remove:\n%s", manifest)
	}
	if lock := mustRead(t, filepath.Join(app, "nomi.lock")); strings.Contains(lock, "example.com") {
		t.Errorf("nomi.lock after nomi remove:\n%s", lock)
	}
	_, stderr, err = c.run(app, "remove", "greet")
	if err == nil || stderr != "nomi remove: nomi.toml has no dependency greet" {
		t.Errorf("nomi remove of a dependency nomi.toml does not name: %v\n%s", err, stderr)
	}
}

// `nomi update` moves dependencies to their latest release of the same
// module path and prints each move.
func TestModules_UpdateWithinAMajorVersion(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":  "[module]\nname = \"greet\"\n",
			"greet.nomi": "pub fn hello() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	publishFiles(t, c.proxy, "example.com/greet", "v1.0.0", greet("v1.0.0"))
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet")
	if out := c.must(app, "update"); out != "every dependency is at its latest release" {
		t.Errorf("nomi update with nothing newer printed %q", out)
	}

	publishFiles(t, c.proxy, "example.com/greet", "v1.2.0", greet("v1.2.0"))
	publishFiles(t, c.proxy, "example.com/greet/v2", "v2.0.0", map[string]string{
		"go.mod":     "module example.com/greet/v2\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String\n    \"v2\"\nend\n",
	})
	if out := c.must(app, "update", "greet"); out != "example.com/greet v1.0.0 -> v1.2.0\n\nmodules\n  greet\n    not imported yet" {
		t.Fatalf("nomi update greet printed %q", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { module = \"example.com/greet\", version = \"v1.2.0\" }\n") {
		t.Errorf("nomi.toml after nomi update:\n%s", manifest)
	}
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "example.com/greet v1.0.0 h1:") || !strings.Contains(lock, "example.com/greet v1.2.0 h1:") {
		t.Errorf("nomi.lock after nomi update:\n%s", lock)
	}
	if out := c.must(app, "update", "example.com/greet"); out != "greet is at its latest release" {
		t.Errorf("a second nomi update printed %q", out)
	}
	_, stderr, err := c.run(app, "update", "none")
	if err == nil || stderr != "nomi update: nomi.toml has no dependency none" {
		t.Errorf("nomi update of a dependency nomi.toml does not name: %v\n%s", err, stderr)
	}
}

// With no nomi.toml, the module commands say to start a project with nomi
// new.
func TestModules_CommandsWithNoManifestNameNomiNew(t *testing.T) {
	c := newModuleCLI(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"add", "example.com/x"}, {"remove", "x"}, {"update"}} {
		_, stderr, err := c.run(dir, args...)
		if err == nil || !strings.Contains(stderr, "no nomi.toml in") || !strings.Contains(stderr, "nomi new <name>") {
			t.Errorf("nomi %s with no nomi.toml: %v\n%s", args[0], err, stderr)
		}
	}
}

func TestModules_HelpListsTheModuleCommands(t *testing.T) {
	help, err := exec.Command(nomiBin, "help").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nomi new <module path> [dir]", "nomi add [<name>=]<directory>", "nomi remove <name>", "nomi update [name]"} {
		if !strings.Contains(string(help), want) {
			t.Errorf("nomi help does not list %q", want)
		}
	}
}
