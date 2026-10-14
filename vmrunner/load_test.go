package vmrunner_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/vmrunner"
)

const loadable = `import std/io

once greeting = make_greeting()

fn make_greeting(): String {
    io.print("booted")
    "Hello"
}

export fn greet(name: String): String {
    "${greeting}, ${name}!"
}

export fn add(a: Int, b: Int): Int {
    a + b
}
`

// nomic is src lowered as `nomi build --no-vm` lowers it, in a .nomic file.
func nomic(t *testing.T, src string) []byte {
	t.Helper()
	return vmrunner.EncodeImageFile(webImage(t, src), 0, "test")
}

// TestLoadProgram_BootsAWholeImageFileForCalls: the bytes of a .nomic file
// boot the program, list its exported functions, and serve calls on that
// booted program.
func TestLoadProgram_BootsAWholeImageFileForCalls(t *testing.T) {
	var out bytes.Buffer
	in, err := vmrunner.LoadProgram(nomic(t, loadable), &out, &out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if got, want := string(in.ExportsResult()), `{"ok":["greet(name: String): String","add(a: Int, b: Int): Int"]}`; got != want {
		t.Fatalf("ExportsResult is %s, want %s", got, want)
	}
	for _, name := range []string{"Ada", "Bo"} {
		if got := string(in.CallResult("greet", []byte(`["`+name+`"]`))); got != `{"ok":"Hello, `+name+`!"}` {
			t.Fatalf("greet: %s", got)
		}
	}
	if out.String() != "booted\n" {
		t.Fatalf("the program printed %q, want its once forced once", out.String())
	}
}

// TestLoadProgram_AProgramWithOnlyMainListsNoExports: a program with a
// main and no exported function loads, lists none, and runs its main when
// asked.
func TestLoadProgram_AProgramWithOnlyMainListsNoExports(t *testing.T) {
	var out bytes.Buffer
	in, err := vmrunner.LoadProgram(nomic(t, "import std/io\n\nfn main() {\n    io.print(\"main ran\")\n}\n"), &out, &out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if got := string(in.ExportsResult()); got != `{"ok":[]}` {
		t.Fatalf("ExportsResult is %s", got)
	}
	if out.Len() != 0 {
		t.Fatalf("loading ran main: %q", out.String())
	}
	if err := in.Main(); err != nil || out.String() != "main ran\n" {
		t.Fatalf("Main: %v, printed %q", err, out.String())
	}
}

// TestLoadProgram_Refusals: a payload that is not a .nomic file, one cut
// short, one with bytes after its image, one in another program format,
// and a program with neither a main nor an exported function each answer
// an error, and nothing boots.
func TestLoadProgram_Refusals(t *testing.T) {
	good := nomic(t, loadable)
	other := bytes.Clone(good)
	binary.LittleEndian.PutUint32(other[8:12], ir.FormatVersion+1)

	im, err := ir.DecodeImage(webImage(t, loadable))
	if err != nil {
		t.Fatal(err)
	}
	im.Exports, im.HasMain = nil, false
	empty, err := ir.EncodeImage(im)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "the loaded program is empty: not a compiled Nomi program (.nomic)"},
		{"not a .nomic", []byte("a program, honestly, it is"), `does not start with "NOMIC\x00\x00\x00"`},
		{"truncated", good[:len(good)-1], "the loaded program is truncated"},
		{"trailing bytes", append(bytes.Clone(good), 0), "the loaded program has 1 bytes after its program"},
		{"another format", other, "in program format"},
		{"nothing to run", vmrunner.EncodeImageFile(empty, 0, "test"), "has no `fn main` and no `export fn`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			in, err := vmrunner.LoadProgram(c.data, &out, &out, nil, nil)
			if err == nil {
				in.Close()
				t.Fatal("it loaded")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q, want it to contain %q", err, c.want)
			}
			if out.Len() != 0 {
				t.Fatalf("a refused program printed %q", out.String())
			}
		})
	}
}

// TestRunCompiled_AProgramWithOnlyExportedFunctionsSaysSo: nomi-vm handed
// a .nomic whose program has no main, only exported functions, exits 1 and
// says where such a program runs, rather than exiting quietly. Run, which a
// page's module runs such a program through, runs nothing and exits 0.
func TestRunCompiled_AProgramWithOnlyExportedFunctionsSaysSo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := vmrunner.Run(webImage(t, loadable), &stdout, &stderr, nil, nil, false); code != 0 || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("Run: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	code := vmrunner.RunCompiled(webImage(t, loadable), &stdout, &stderr, nil, nil, false)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "this program has no `fn main`") ||
		!strings.Contains(stderr.String(), "nomi build-vm --reactor") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
