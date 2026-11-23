package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modfetch"
)

// `nomi init` makes the current directory a project: it writes its
// nomi.toml, lists each file with a `fn main` as an entry point, writes the
// go.mod nomi keeps (the module line alone), writes no source, and never
// replaces a nomi.toml.
func TestModules_InitMakesTheDirectoryAProject(t *testing.T) {
	c := newModuleCLI(t)
	goMod := func(path string) string { return "module " + path + "\n\ngo 1.26\n" }
	nomiGoMod := func(path string) string { return string(modfetch.GoModText(path)) }

	// An empty directory gets a nomi.toml, with `main` as its entry point.
	empty := t.TempDir()
	if out := c.must(empty, "init", "github.com/you/hello-world"); out != "wrote nomi.toml and go.mod" {
		t.Errorf("nomi init in an empty directory printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(empty, "nomi.toml")); got != "[module]\nname = \"hello_world\"\npath = \"github.com/you/hello-world\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if got, want := mustRead(t, filepath.Join(empty, "go.mod")), nomiGoMod("github.com/you/hello-world"); got != want {
		t.Errorf("go.mod is\n%s\nwant\n%s", got, want)
	}
	if isRegular(filepath.Join(empty, "main.nomi")) {
		t.Error("nomi init wrote a main.nomi")
	}
	_, stderr, err := c.run(empty, "init")
	if err == nil || stderr != "nomi init: this directory is already a Nomi project: it holds nomi.toml" {
		t.Errorf("nomi init in a project: %v\n%s", err, stderr)
	}

	// Every file with a fn main is an entry point, a test file is not, and
	// the program then checks with no further step.
	tools := t.TempDir()
	mustWrite(t, filepath.Join(tools, "main.nomi"), "fn main() end\n")
	mustWrite(t, filepath.Join(tools, "cmd", "seed.nomi"), "fn main() end\n")
	mustWrite(t, filepath.Join(tools, "util.nomi"), "pub fn two() -> Int\n    2\nend\n")
	mustWrite(t, filepath.Join(tools, "util_test.nomi"), "test \"two\"\n    assert 2 == 2\nend\n")
	if out := c.must(tools, "init", "example.com/tools"); out != "wrote nomi.toml and go.mod\nnext: nomi run cmd/seed.nomi" {
		t.Errorf("nomi init beside two entries printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(tools, "nomi.toml")); got != "[module]\nname = \"tools\"\npath = \"example.com/tools\"\nentry_points = [\"cmd/seed\", \"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if out := c.must(tools, "check", "main.nomi"); out != "ok main.nomi" {
		t.Errorf("nomi check after nomi init printed %q", out)
	}

	// A go.mod already there gives the module path, and is kept when it is
	// the one nomi writes.
	half := t.TempDir()
	mustWrite(t, filepath.Join(half, "go.mod"), nomiGoMod("example.com/half"))
	if out := c.must(half, "init"); out != "wrote nomi.toml\nkept go.mod, which was already there" {
		t.Errorf("nomi init beside a go.mod printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(half, "nomi.toml")); got != "[module]\nname = \"half\"\npath = \"example.com/half\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}

	// A go.mod that says more than its module line is nomi's too, and is
	// rewritten as the one nomi keeps.
	more := t.TempDir()
	mustWrite(t, filepath.Join(more, "go.mod"), goMod("example.com/more"))
	if out := c.must(more, "init"); out != "wrote nomi.toml and go.mod" {
		t.Errorf("nomi init beside a go.mod with a go line printed\n%s", out)
	}
	if got, want := mustRead(t, filepath.Join(more, "go.mod")), nomiGoMod("example.com/more"); got != want {
		t.Errorf("go.mod is\n%s\nwant\n%s", got, want)
	}
	other := t.TempDir()
	mustWrite(t, filepath.Join(other, "go.mod"), nomiGoMod("example.com/half"))
	_, stderr, err = c.run(other, "init", "example.com/other")
	if err == nil || stderr != "nomi init: go.mod names module example.com/half, not example.com/other; run `nomi init` with no path to keep example.com/half" {
		t.Errorf("nomi init with a path go.mod does not name: %v\n%s", err, stderr)
	}
	if isRegular(filepath.Join(other, "nomi.toml")) {
		t.Error("a refused nomi init wrote nomi.toml")
	}

	// With no name or path anywhere, one is needed, and a bad one is
	// refused before anything is written.
	bare := t.TempDir()
	_, stderr, err = c.run(bare, "init")
	if err == nil || stderr != "nomi init: give the module's name, such as `nomi init hello`, or the module path it is published at, such as `nomi init github.com/you/hello`" {
		t.Errorf("nomi init with no path: %v\n%s", err, stderr)
	}
	for _, tc := range []struct{ arg, want string }{
		{"not a path", `nomi init: "not a path" is not a module name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world`},
		{"hello/world", `nomi init: "hello/world" is not a module path: malformed module path "hello/world": missing dot in first path element`},
		{"std", "nomi init: std is the standard library's name; choose another"},
	} {
		_, stderr, err = c.run(bare, "init", tc.arg)
		if err == nil || !strings.HasPrefix(stderr, tc.want) {
			t.Errorf("nomi init %q: %v\n%s\nwant it to start\n%s", tc.arg, err, stderr, tc.want)
		}
	}
	if entries, err := os.ReadDir(bare); err != nil || len(entries) != 0 {
		t.Errorf("a refused nomi init wrote %v (%v)", entries, err)
	}

	// A name alone makes a module nobody downloads: no path, and no go.mod.
	if out := c.must(bare, "init", "hello"); out != "wrote nomi.toml" {
		t.Errorf("nomi init hello printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(bare, "nomi.toml")); got != "[module]\nname = \"hello\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if isRegular(filepath.Join(bare, "go.mod")) {
		t.Error("nomi init with a name alone wrote a go.mod")
	}
}
