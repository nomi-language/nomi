package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// `nomi init` makes the current directory a project: it writes the
// nomi.toml and go.mod it lacks, lists each file with a `fn main` as an
// entry point, writes no source, and never replaces a file.
func TestModules_InitMakesTheDirectoryAProject(t *testing.T) {
	c := newModuleCLI(t)
	goMod := func(path string) string { return "module " + path + "\n\ngo " + gotoolchain.GoLine() + "\n" }

	// An empty directory gets both files and `main` as its entry point.
	empty := t.TempDir()
	if out := c.must(empty, "init", "github.com/you/hello-world"); out != "wrote nomi.toml and go.mod" {
		t.Errorf("nomi init in an empty directory printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(empty, "nomi.toml")); got != "[module]\nname = \"hello_world\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if got, want := mustRead(t, filepath.Join(empty, "go.mod")), goMod("github.com/you/hello-world"); got != want {
		t.Errorf("go.mod is\n%s\nwant\n%s", got, want)
	}
	if isRegular(filepath.Join(empty, "main.nomi")) {
		t.Error("nomi init wrote a main.nomi")
	}
	_, stderr, err := c.run(empty, "init")
	if err == nil || stderr != "nomi init: this directory is already a Nomi project: it holds nomi.toml and go.mod" {
		t.Errorf("nomi init in a project: %v\n%s", err, stderr)
	}

	// Every file with a fn main is an entry point, a test file is not, and
	// the program then checks with no further step.
	tools := t.TempDir()
	mustWrite(t, filepath.Join(tools, "main.nomi"), "fn main() {\n}\n")
	mustWrite(t, filepath.Join(tools, "cmd", "seed.nomi"), "fn main() {\n}\n")
	mustWrite(t, filepath.Join(tools, "util.nomi"), "pub fn two() -> Int {\n    2\n}\n")
	mustWrite(t, filepath.Join(tools, "util_test.nomi"), "test \"two\" {\n    assert 2 == 2\n}\n")
	if out := c.must(tools, "init", "example.com/tools"); out != "wrote nomi.toml and go.mod\nnext: nomi run cmd/seed.nomi" {
		t.Errorf("nomi init beside two entries printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(tools, "nomi.toml")); got != "[module]\nname = \"tools\"\nentry_points = [\"cmd/seed\", \"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if out := c.must(tools, "check", "main.nomi"); out != "ok main.nomi" {
		t.Errorf("nomi check after nomi init printed %q", out)
	}

	// A go.mod already there gives the module path, and is kept.
	half := t.TempDir()
	mustWrite(t, filepath.Join(half, "go.mod"), goMod("example.com/half"))
	if out := c.must(half, "init"); out != "wrote nomi.toml\nkept go.mod, which was already there" {
		t.Errorf("nomi init beside a go.mod printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(half, "nomi.toml")); got != "[module]\nname = \"half\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	other := t.TempDir()
	mustWrite(t, filepath.Join(other, "go.mod"), goMod("example.com/half"))
	_, stderr, err = c.run(other, "init", "example.com/other")
	if err == nil || stderr != "nomi init: go.mod names module example.com/half, not example.com/other; run `nomi init` with no path to keep example.com/half" {
		t.Errorf("nomi init with a path go.mod does not name: %v\n%s", err, stderr)
	}
	if isRegular(filepath.Join(other, "nomi.toml")) {
		t.Error("a refused nomi init wrote nomi.toml")
	}

	// A nomi.toml already there is kept, and the go.mod needs a path.
	manifest := t.TempDir()
	mustWrite(t, filepath.Join(manifest, "nomi.toml"), "[module]\nname = \"lib\"\n")
	_, stderr, err = c.run(manifest, "init")
	if err == nil || !strings.HasPrefix(stderr, "nomi init: give the module path this directory's nomi.toml needs for its go.mod") {
		t.Errorf("nomi init beside a nomi.toml with no path: %v\n%s", err, stderr)
	}
	if out := c.must(manifest, "init", "example.com/lib"); out != "wrote go.mod\nkept nomi.toml, which was already there" {
		t.Errorf("nomi init beside a nomi.toml printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(manifest, "nomi.toml")); got != "[module]\nname = \"lib\"\n" {
		t.Errorf("nomi init rewrote a nomi.toml:\n%s", got)
	}

	// With neither file, the path is needed, and a bad one is refused.
	bare := t.TempDir()
	_, stderr, err = c.run(bare, "init")
	if err == nil || !strings.HasPrefix(stderr, "nomi init: give the module path this directory needs for its go.mod, such as `nomi init github.com/you/project`") {
		t.Errorf("nomi init with no path: %v\n%s", err, stderr)
	}
	_, stderr, err = c.run(bare, "init", "not a path")
	if err == nil || !strings.Contains(stderr, `nomi init: "not a path" is not a module path`) {
		t.Errorf("nomi init with a bad path: %v\n%s", err, stderr)
	}
	if isRegular(filepath.Join(bare, "go.mod")) || isRegular(filepath.Join(bare, "nomi.toml")) {
		t.Error("a refused nomi init wrote a file")
	}
}

const showmiBinding = `gopkg "github.com/netshade/showmi" as go_http

pub fn greet(name: String) -> String go go_http.Greet
`

const showmiGo = `package go_http

func Greet(name string) string { return "hello, " + name }
`

const showmiMain = `import {
    std/io
    go_http.greet
}

fn main() {
    io.print(greet("you"))
}
`

// A binding to the Go code beside it, in a directory that is not a project
// yet: `nomi check` names `nomi init`, and after it the program checks and
// runs with no further step.
func TestFFI_InitMakesABindingToGoCodeBesideItWork(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	mustWrite(t, filepath.Join(dir, "go_http.nomi"), showmiBinding)
	mustWrite(t, filepath.Join(dir, "go_http.go"), showmiGo)
	mustWrite(t, filepath.Join(dir, "main.nomi"), showmiMain)
	env := buildEnv(t.TempDir())

	r := runProcess(t, env, dir, "", nomiBin, "check", "main.nomi")
	want := "go_http.nomi:1:8: Go package \"github.com/netshade/showmi\" is not in the Go standard library, and the Go code beside this file is not in a Nomi project yet; " +
		"run `nomi init github.com/netshade/showmi` in this file's directory to make it one"
	if r.exit == 0 || !strings.Contains(r.stderr, want) {
		t.Fatalf("nomi check before nomi init: exit %d, want stderr to hold\n%s\n%s", r.exit, want, r.transcript())
	}
	if r := runProcess(t, env, dir, "", nomiBin, "init", "github.com/netshade/showmi"); r.exit != 0 || r.stdout != "wrote nomi.toml and go.mod\nnext: nomi run main.nomi\n" {
		t.Fatalf("nomi init: exit %d\n%s", r.exit, r.transcript())
	}
	if r := runProcess(t, env, dir, "", nomiBin, "check", "main.nomi"); r.exit != 0 {
		t.Fatalf("nomi check after nomi init: exit %d\n%s", r.exit, r.transcript())
	}
	if r := runProcess(t, env, dir, "", nomiBin, "run", "main.nomi"); r.exit != 0 || r.stdout != "hello, you\n" {
		t.Fatalf("nomi run after nomi init: exit %d\n%s", r.exit, r.transcript())
	}
}
