package vmrunner_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmrunner"
)

// funcNames is every function name in a decoded image, by module.
func funcNames(t *testing.T, img []byte) (map[string]bool, []string) {
	t.Helper()
	im, err := ir.DecodeImage(img)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var mods []string
	for _, m := range im.Modules {
		mods = append(mods, m.Name())
		for _, f := range m.Funcs() {
			names[f.Name()] = true
		}
	}
	return names, mods
}

const prunedProgram = `import std/io

interface Shape {
  fn area(s: self): Int
}

struct Square {
  side: Int
}

struct Circle {
  r: Int
}

impl Shape for Square {
  fn area(s: Square): Int {
    s.side * s.side
  }
}

impl Shape for Circle {
  fn area(c: Circle): Int {
    3 * c.r * c.r
  }
}

once table = build_table()

fn build_table(): Int {
  42
}

fn never_called(): Int {
  7
}

fn total<T>(xs: List<T>): Int where T: Shape {
  xs |> Iter.map(|x| T.area(x)) |> Iter.reduce(|a = 0, b| a + b)
}

fn main() {
  io.print(total([Square{side: 2}]))
  io.print(table)
}
`

// A built image holds what main reaches, through a call, a dispatch, a
// once and a closure, and nothing else: no unused function of the program
// and none of the stdlib it never calls.
func TestBuild_TheImageHoldsOnlyWhatTheProgramReaches(t *testing.T) {
	img := image(t, prunedProgram)
	names, mods := funcNames(t, img)
	for _, want := range []string{"main", "total", "Square.area", "build_table"} {
		if !names[want] {
			t.Errorf("the image lacks %s, which main reaches; it holds %v", want, names)
		}
	}
	if names["never_called"] || names["Circle.area"] {
		t.Errorf("the image holds a function nothing reaches: %v", names)
	}
	for _, m := range mods {
		if strings.Contains(m, "json") || strings.Contains(m, "regex") || strings.Contains(m, "toml") {
			t.Errorf("the image holds module %s, which the program never calls", m)
		}
	}
	var out, errOut bytes.Buffer
	if code := vmrunner.Run(img, &out, &errOut, nil, nil, false); code != 0 || out.String() != "4\n42\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// An image that lacks a function its program calls is refused by name
// before the program's first effect, and a machine that calls such a
// function anyway fails with an error that names it rather than running
// something else. A pruning bug therefore cannot pass as a different output.
func TestRun_AnImageMissingACalleeFailsLoudly(t *testing.T) {
	img := image(t, "import std/io\n\nfn helper() {\n  io.print(\"after\")\n}\n\nfn main() {\n  io.print(\"before\")\n  helper()\n}\n")
	im, err := ir.DecodeImage(img)
	if err != nil {
		t.Fatal(err)
	}
	entry := im.EntryModule()
	var helper *ir.Func
	for _, f := range entry.Funcs() {
		if f.Name() == "helper" {
			helper = f
		}
	}
	if helper == nil {
		t.Fatal("the image lacks helper before the test removes it")
	}
	entry.RemoveFunc(helper)
	damaged, err := ir.EncodeImage(im)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := vmrunner.Run(damaged, &out, &errOut, nil, nil, false)
	if code == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "helper") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want a refusal naming helper before any output",
			code, out.String(), errOut.String())
	}

	// Past the runner's check: the call itself fails, naming the callee.
	again, err := ir.DecodeImage(damaged)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	booted, err := vm.NewProgram(again.EntryModule(), again.Modules, &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	_, err = booted.Run("main")
	if err == nil || !strings.Contains(err.Error(), "helper") || !strings.Contains(err.Error(), "did not retain") {
		t.Fatalf("calling a function the image lacks answered %v; want an error naming helper", err)
	}
	if strings.Contains(out.String(), "after") {
		t.Fatalf("the machine ran something for the missing helper: %q", out.String())
	}
}

// A built image names its source files as `go build -trimpath` does: the
// entry by its path from the project root, a stdlib module as std/<file>,
// and no directory of the machine that built it, in a module name or a
// position.
func TestBuild_TheImageNamesFilesWithoutTheBuildMachinesDirectories(t *testing.T) {
	img := image(t, prunedProgram)
	_, mods := funcNames(t, img)
	if len(mods) < 2 || mods[0] != "main.nomi" {
		t.Fatalf("the image's modules are %v; want main.nomi first, then std modules", mods)
	}
	for _, m := range mods[1:] {
		if !strings.HasPrefix(m, "std/") || !strings.HasSuffix(m, ".nomi") {
			t.Errorf("the image names a stdlib module %q; want std/<file>.nomi", m)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{os.TempDir(), filepath.Dir(wd)} {
		if bytes.Contains(img, []byte(dir)) {
			t.Errorf("the image holds the build machine's directory %s", dir)
		}
	}
}
