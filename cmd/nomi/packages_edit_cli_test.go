package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi new` writes nomi.toml, a main.nomi that runs and an AGENTS.md that
// links the cheat sheet, in a new directory named for the module, and
// refuses one that has a nomi.toml.
func TestPackages_NewStartsAProject(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	out := c.must(work, "new", "hello_world")
	want := "wrote hello_world/nomi.toml, hello_world/main.nomi and hello_world/AGENTS.md\nnext: cd hello_world && nomi run main.nomi"
	if out != want {
		t.Fatalf("nomi new printed\n%s\nwant\n%s", out, want)
	}
	dir := filepath.Join(work, "hello_world")
	if got := mustRead(t, filepath.Join(dir, "nomi.toml")); got != "[package]\nname = \"hello_world\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	agents := mustRead(t, filepath.Join(dir, "AGENTS.md"))
	for _, want := range []string{"# hello_world\n", "https://nomi-lang.org/cheatsheet.md", "`nomi test`", "`nomi fmt -w .`", "`nomi add <repository>`"} {
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

	_, stderr, err := c.run(work, "new", "hello_world", "hello_world")
	if err == nil || stderr != "nomi new: hello_world already holds a nomi.toml, so it is a project already" {
		t.Errorf("nomi new over a project: %v\n%s", err, stderr)
	}

	// In the current directory, the next command needs no cd, and a
	// main.nomi already there is kept.
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "fn main() end\n")
	out = c.must(app, "new", "app", ".")
	if out != "wrote nomi.toml and AGENTS.md\nkept main.nomi, which was already there\nnext: nomi run main.nomi" {
		t.Errorf("nomi new . printed\n%s", out)
	}
}

// `nomi new <name>` writes no go.mod, and the project runs, checks and
// builds by its directory.
func TestNew_ANameAloneStartsAProject(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	out := c.must(work, "new", "hello")
	if want := "wrote hello/nomi.toml, hello/main.nomi and hello/AGENTS.md\nnext: cd hello && nomi run main.nomi"; out != want {
		t.Fatalf("nomi new hello printed\n%s\nwant\n%s", out, want)
	}
	dir := filepath.Join(work, "hello")
	if got := mustRead(t, filepath.Join(dir, "nomi.toml")); got != "[package]\nname = \"hello\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if isRegular(filepath.Join(dir, "go.mod")) {
		t.Error("nomi new hello wrote a go.mod")
	}
	agents := mustRead(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(agents, "`nomi run .`") || strings.Contains(agents, "go.mod") {
		t.Errorf("AGENTS.md of a new module:\n%s", agents)
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
// repository, and std.
func TestNew_ARefusedNewWritesNothing(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	for _, tc := range []struct{ arg, want string }{
		{"not a name", `nomi new: "not a name" is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world`},
		{"Hello", `nomi new: "Hello" is not a package name`},
		{"hello-world", `nomi new: "hello-world" is not a package name`},
		{"2fast", `nomi new: "2fast" is not a package name`},
		{"github.com/you/hello", `nomi new: "github.com/you/hello" is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world; ` +
			"a package names no repository: a project that depends on it names the repository it is published in"},
		{"std", "nomi new: std is the standard library's name; choose another"},
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

// `nomi add <directory>` writes a dir entry for the package there, under
// its own name or the name before `=`, and refuses a directory with no
// nomi.toml.
func TestPackages_AddALocalPackage(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	app := filepath.Join(work, "app")
	c.must(work, "new", "app")
	lib := filepath.Join(app, "csvdata")
	mustWrite(t, filepath.Join(lib, "nomi.toml"), "[package]\nname = \"csvdata\"\n")
	mustWrite(t, filepath.Join(lib, "csvdata.nomi"), "pub fn rows() -> Int\n    3\nend\n")

	if out := c.must(app, "add", "./csvdata"); out != "added csvdata from ./csvdata (import csvdata)\n\npackages\n  csvdata\n    not imported yet" {
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
	if err == nil || stderr != "nomi add: ./plain has no nomi.toml, so it is not a Nomi package" {
		t.Errorf("nomi add of a directory with no nomi.toml: %v\n%s", err, stderr)
	}
}

// A module added by its directory brings the modules its own dir entries
// name, from their directories: the project runs with one entry, nothing
// is fetched, and nomi.lock, which holds only fetched packages, is not
// written. A fetched package's dir entries name its neighbors in its
// repository at its tag, fetched with it.
func TestPackages_ADirectoryPackageBringsItsDirectoryDependencies(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	mustWrite(t, filepath.Join(work, "lib", "kit", "nomi.toml"), "[package]\nname = \"kit\"\n\n[dependencies]\n"+
		"util = { dir = \"../util\" }\n")
	mustWrite(t, filepath.Join(work, "lib", "kit", "kit.nomi"), "import util\n\npub fn twice(n: Int) -> Int\n    util.add(n, n)\nend\n")
	mustWrite(t, filepath.Join(work, "lib", "util", "nomi.toml"), "[package]\nname = \"util\"\n")
	mustWrite(t, filepath.Join(work, "lib", "util", "util.nomi"), "pub fn add(a: Int, b: Int) -> Int\n    a + b\nend\n")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(kit.twice(21))\nend\n")

	if out := c.must(app, "add", "../lib/kit"); !strings.HasPrefix(out, "added kit from ../lib/kit (import kit)") {
		t.Fatalf("nomi add ../lib/kit printed %q", out)
	}
	if out := c.must(app, "run", "main.nomi"); out != "42" {
		t.Fatalf("the program printed %q, want 42", out)
	}
	if out := c.must(app, "fetch"); out != "0 packages in the package cache for ." {
		t.Errorf("nomi fetch printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(app, "nomi.lock")); err == nil {
		t.Errorf("nomi.lock was written for dependencies that are all directories:\n%s", mustRead(t, filepath.Join(app, "nomi.lock")))
	}

	c.host.Tag("example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":            "[package]\nname = \"greet\"\n\n[dependencies]\nstrutil = { dir = \"strutil\" }\n",
		"greet.nomi":           "import strutil\n\npub fn hello() -> String\n    strutil.twice(\"hi\")\nend\n",
		"strutil/nomi.toml":    "[package]\nname = \"strutil\"\n",
		"strutil/strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	c.must(app, "add", "example.com/greet")
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{"example.com/greet . v0.1.0 ", "example.com/greet strutil v0.1.0 "} {
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
func TestPackages_AddTheDependencyAToolTableNames(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	app := filepath.Join(work, "app")
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[sqlkit.database.main]\ndialect = \"sqlite\"\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)
	mustWrite(t, filepath.Join(app, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n")
	kit := filepath.Join(work, "kit")
	mustWrite(t, filepath.Join(kit, "nomi.toml"), "[package]\nname = \"sqlkit\"\n")
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
	if got, want := mustRead(t, filepath.Join(app, "nomi.toml")), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nsqlkit = { dir = \"../kit\" }\n\n[sqlkit.database.main]\ndialect = \"sqlite\"\n"; got != want {
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
func TestPackages_RemoveADependency(t *testing.T) {
	c := newModuleCLI(t)
	c.host.Tag("example.com/strutil", "v1.0.0", map[string]string{
		"nomi.toml":    "[package]\nname = \"strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	c.host.Tag("example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"greet.nomi": "pub fn hello() -> String\n    \"hello\"\nend\n",
	})
	work := t.TempDir()
	c.must(work, "new", "app")
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

// `nomi update` moves dependencies to their repository's latest release in
// the same line, here the same major version, and prints each move.
func TestPackages_UpdateWithinALine(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":  "[package]\nname = \"greet\"\n",
			"greet.nomi": "pub fn hello() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	c.host.Tag("example.com/greet", "v1.0.0", greet("v1.0.0"))
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet")
	if out := c.must(app, "update"); out != "every repository of the build is at the latest release of its line" {
		t.Errorf("nomi update with nothing newer printed %q", out)
	}

	c.host.Tag("example.com/greet", "v1.2.0", greet("v1.2.0"))
	c.host.Tag("example.com/greet", "v2.0.0", greet("v2.0.0"))
	// From v1 a line is the major version: update names v2.0.0 and the
	// `nomi add` that takes it.
	later := "example.com/greet has v2.0.0, past the v1.2.0 the project takes; from v1.0.0 each major version is a line of its own, " +
		"which nomi update does not leave: `nomi add example.com/greet@v2.0.0` takes it"
	if out := c.must(app, "update", "greet"); out != "example.com/greet v1.0.0 -> v1.2.0\n"+later+"\n\npackages\n  greet\n    not imported yet" {
		t.Fatalf("nomi update greet printed %q", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", version = \"v1.2.0\" }\n") {
		t.Errorf("nomi.toml after nomi update:\n%s", manifest)
	}
	lock := mustRead(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "example.com/greet . v1.0.0 ") || !strings.Contains(lock, "example.com/greet . v1.2.0 ") {
		t.Errorf("nomi.lock after nomi update:\n%s", lock)
	}
	if out := c.must(app, "update", "example.com/greet"); out != "greet is at the latest release of its line\n"+later {
		t.Errorf("a second nomi update printed %q", out)
	}
	_, stderr, err := c.run(app, "update", "none")
	if err == nil || stderr != "nomi update: nomi.toml has no dependency none" {
		t.Errorf("nomi update of a dependency nomi.toml does not name: %v\n%s", err, stderr)
	}
}

// Below v0.1 each release is a line of its own: `nomi update` leaves
// v0.0.3 where it is and names v0.0.5 and the `nomi add` that takes it,
// which then moves the entry there. Below v1 each minor version is a line:
// at v0.1.0 it names v0.2.0 the same way.
func TestPackages_UpdateNamesAReleasePastItsLine(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":  "[package]\nname = \"greet\"\n",
			"greet.nomi": "pub fn hello() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	for _, v := range []string{"v0.0.3", "v0.0.4", "v0.0.5"} {
		c.host.Tag("example.com/greet", v, greet(v))
	}
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet@v0.0.3")
	later := "example.com/greet has v0.0.5, past the v0.0.3 the project takes; below v0.1.0 each release is a line of its own, " +
		"which nomi update does not leave: `nomi add example.com/greet@v0.0.5` takes it"
	if out := c.must(app, "update"); out != "every repository of the build is at the latest release of its line\n"+later {
		t.Errorf("nomi update at v0.0.3 printed %q", out)
	}
	if out := c.must(app, "update", "greet"); out != "greet is at the latest release of its line\n"+later {
		t.Errorf("nomi update greet at v0.0.3 printed %q", out)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, `version = "v0.0.3"`) {
		t.Errorf("nomi update moved a 0.0.x entry:\n%s", manifest)
	}
	c.must(app, "add", "example.com/greet@v0.0.5")
	if out := c.must(app, "update"); out != "every repository of the build is at the latest release of its line" {
		t.Errorf("nomi update at v0.0.5 printed %q", out)
	}

	for _, v := range []string{"v0.1.0", "v0.2.0"} {
		c.host.Tag("example.com/greet", v, greet(v))
	}
	c.must(app, "add", "example.com/greet@v0.1.0")
	later = "example.com/greet has v0.2.0, past the v0.1.0 the project takes; below v1.0.0 each minor version is a line of its own, " +
		"which nomi update does not leave: `nomi add example.com/greet@v0.2.0` takes it"
	if out := c.must(app, "update"); out != "every repository of the build is at the latest release of its line\n"+later {
		t.Errorf("nomi update at v0.1.0 printed %q", out)
	}
}

// One tag versions a repository: `nomi add` of another package of a
// repository the project takes at a version adds it there and says so,
// and a version named moves the repository's other entries with it.
func TestPackages_AddAnotherPackageOfARepository(t *testing.T) {
	c := newModuleCLI(t)
	mono := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml":       "[package]\nname = \"mono\"\n",
			"lib/a/nomi.toml": "[package]\nname = \"a\"\n",
			"lib/a/a.nomi":    "pub fn v() -> String\n    \"" + v + "\"\nend\n",
			"lib/b/nomi.toml": "[package]\nname = \"b\"\n",
			"lib/b/b.nomi":    "pub fn v() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	c.host.Tag("example.com/mono", "v1.0.0", mono("v1.0.0"))
	c.host.Tag("example.com/mono", "v2.0.0", mono("v2.0.0"))
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/mono/lib/a@v1.0.0")

	out := c.must(app, "add", "example.com/mono/lib/b")
	if want := "added b, example.com/mono/lib/b v1.0.0 (import b), at the version the project already takes example.com/mono at\n"; !strings.HasPrefix(out, want) {
		t.Errorf("nomi add of another module of the repository printed\n%s\nwant it to start\n%s", out, want)
	}
	out = c.must(app, "add", "example.com/mono/lib/a@v2.0.0")
	if want := "added a, example.com/mono/lib/a v2.0.0 (import a)\nmoved b to v2.0.0 with it, since one tag versions example.com/mono\n"; !strings.HasPrefix(out, want) {
		t.Errorf("nomi add of a new major version printed\n%s\nwant it to start\n%s", out, want)
	}
	if manifest := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "b = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"v2.0.0\" }\n") {
		t.Errorf("nomi.toml after adding a@v2.0.0:\n%s", manifest)
	}
}

// A nomi.toml edited by hand to another line than nomi.lock holds stops
// `nomi check` and `nomi fetch` with an error that names the lock and the
// command that selects again; `nomi fetch --update` then takes the
// manifest's version.
func TestPackages_ALockInAnotherLineThanTheManifestStops(t *testing.T) {
	c := newModuleCLI(t)
	greet := map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String\n    \"hello\"\nend\n",
	}
	c.host.Tag("example.com/greet", "v1.0.0", greet)
	c.host.Tag("example.com/greet", "v2.0.0", greet)
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	c.must(app, "add", "example.com/greet@v2.0.0")
	manifest := filepath.Join(app, "nomi.toml")
	if err := os.WriteFile(manifest, []byte(strings.Replace(mustRead(t, manifest), "v2.0.0", "v1.0.0", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	clash := "nomi.lock holds example.com/greet at v2.0.0 and app requires example.com/greet v1.0.0: " +
		"one tag versions a whole repository, so every requirement on it must be in one line: " +
		"a major version from v1 on, a minor version from v0.1 on, and below v0.1 one release; " +
		"`nomi fetch --update` selects again from the manifests"
	for _, args := range [][]string{{"fetch"}, {"check", "."}, {"run", "."}} {
		_, stderr, err := c.run(app, args...)
		if err == nil || !strings.Contains(stderr, clash) {
			t.Errorf("nomi %s with the lock in another line: %v\n%s", args[0], err, stderr)
		}
	}
	c.must(app, "fetch", "--update")
	if lock := mustRead(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/greet . v1.0.0 ") {
		t.Errorf("nomi.lock after nomi fetch --update:\n%s", lock)
	}
}

// With no nomi.toml, the package commands say to start a project with nomi
// new.
func TestPackages_CommandsWithNoManifestNameNomiNew(t *testing.T) {
	c := newModuleCLI(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"add", "example.com/x"}, {"remove", "x"}, {"update"}} {
		_, stderr, err := c.run(dir, args...)
		if err == nil || !strings.Contains(stderr, "no nomi.toml in") || !strings.Contains(stderr, "nomi new <name>") {
			t.Errorf("nomi %s with no nomi.toml: %v\n%s", args[0], err, stderr)
		}
	}
}

func TestPackages_HelpListsThePackageCommands(t *testing.T) {
	help, err := exec.Command(nomiBin, "help").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nomi new <name> [dir]", "nomi init <name>", "nomi add [<name>=]<repository>[/<subdir>][@version]", "nomi add [<name>=]<directory>", "nomi remove <name>", "nomi update [name]", "nomi outdated [dir]"} {

		if !strings.Contains(string(help), want) {
			t.Errorf("nomi help does not list %q", want)
		}
	}
}
