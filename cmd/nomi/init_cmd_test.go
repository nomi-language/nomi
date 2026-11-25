package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi init <name>` makes the current directory a project: it writes its
// nomi.toml, lists each file with a `fn main` as an entry point, writes no
// source and no go.mod, reads no go.mod already there, and never replaces
// a nomi.toml.
func TestPackages_InitMakesTheDirectoryAProject(t *testing.T) {
	c := newModuleCLI(t)

	// An empty directory gets a nomi.toml, with `main` as its entry point.
	empty := t.TempDir()
	if out := c.must(empty, "init", "hello_world"); out != "wrote nomi.toml" {
		t.Errorf("nomi init in an empty directory printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(empty, "nomi.toml")); got != "[package]\nname = \"hello_world\"\nentry_points = [\"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	for _, name := range []string{"main.nomi", "go.mod"} {
		if isRegular(filepath.Join(empty, name)) {
			t.Errorf("nomi init wrote a %s", name)
		}
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
	if out := c.must(tools, "init", "tools"); out != "wrote nomi.toml\nnext: nomi run cmd/seed.nomi" {
		t.Errorf("nomi init beside two entries printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(tools, "nomi.toml")); got != "[package]\nname = \"tools\"\nentry_points = [\"cmd/seed\", \"main\"]\n" {
		t.Errorf("nomi.toml is\n%s", got)
	}
	if out := c.must(tools, "check", "main.nomi"); out != "ok main.nomi" {
		t.Errorf("nomi check after nomi init printed %q", out)
	}

	// A go.mod already there names nothing nomi reads, and stays as it is.
	half := t.TempDir()
	const goMod = "module example.com/half\n\ngo 1.26\n"
	mustWrite(t, filepath.Join(half, "go.mod"), goMod)
	if out := c.must(half, "init", "half"); out != "wrote nomi.toml" {
		t.Errorf("nomi init beside a go.mod printed\n%s", out)
	}
	if got := mustRead(t, filepath.Join(half, "go.mod")); got != goMod {
		t.Errorf("nomi init changed go.mod:\n%s", got)
	}

	// With no name, one is needed, and a bad one is refused before anything
	// is written.
	bare := t.TempDir()
	_, stderr, err = c.run(bare, "init")
	if err == nil || stderr != "nomi init: give the package's name, such as `nomi init hello`" {
		t.Errorf("nomi init with no name: %v\n%s", err, stderr)
	}
	for _, tc := range []struct{ arg, want string }{
		{"not a name", `nomi init: "not a name" is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world`},
		{"github.com/you/hello", `nomi init: "github.com/you/hello" is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world; ` +
			"a package names no repository: a project that depends on it names the repository it is published in"},
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
}
