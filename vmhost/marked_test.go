package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// shopBlock is testdata/macros' shop and csvdata as one marked source, the
// macro module under macros/csvdata/ as the Macros chapter lays it out,
// with prices.csv's text replaced by prices.
func shopBlock(t *testing.T, prices string) string {
	t.Helper()
	read := func(path string) string {
		data, err := os.ReadFile("../testdata/macros/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sections := []struct{ name, text string }{
		{"main.nomi", read("shop/main.nomi")},
		{"tables.nomi", read("shop/tables.nomi")},
		{"data/prices.csv", prices},
		{"data/names.csv", read("shop/data/names.csv")},
		{"nomi.toml", read("shop/nomi.toml")},
		{"go.mod", strings.Replace(read("shop/go.mod"), "../csvdata", "./macros/csvdata", 1)},
		{"macros/csvdata/csvdata.nomi", read("csvdata/csvdata.nomi")},
		{"macros/csvdata/nomi.toml", "[module]\nname = \"csvdata\"\n"},
		{"macros/csvdata/go.mod", read("csvdata/go.mod")},
	}
	var b strings.Builder
	for _, s := range sections {
		b.WriteString("// FILE: " + s.name + "\n" + s.text + "\n")
	}
	return b.String()
}

// A marked source whose root module requires a macro module of its own
// loads as the same files on disk would: the macro expands from the CSV
// section, and an error in a section is named by the section's path.
func TestLoadMarked_AProjectOfModulesLoadsAsItsFiles(t *testing.T) {
	src := shopBlock(t, "sku,cents\nmug,1450\nposter,2200\ntote,1800\n")
	p, hasTests, err := vmhost.LoadMarked(src, t.TempDir())
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if hasTests {
		t.Fatal("the shop declares no test")
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "Coffee mug: 1450 cents\n" {
		t.Fatalf("the shop printed %q", got)
	}

	// The same directory again, with a row in dollars: the directory is
	// emptied and written anew, and the error is at the cell.
	dir := t.TempDir()
	if _, _, err := vmhost.LoadMarked(src, dir); err != nil {
		t.Fatal(err)
	}
	_, _, err = vmhost.LoadMarked(shopBlock(t, "sku,cents\nmug,1450\nposter,2200\ntote,18.00\n"), dir)
	if err == nil {
		t.Fatal("a price in dollars loads")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "data/prices.csv:4:6: case branch type mismatch") || strings.Contains(msg, dir) {
		t.Fatalf("the error does not name data/prices.csv from the project root:\n%s", msg)
	}
}

// A marked source that names modules is written under a directory, and a
// run-time failure names a file by its path from there, as the source
// marks it and its diagnostics name it: a `todo` reached in main and in a
// test case says `app/main.nomi`, never the directory.
func TestLoadMarked_ARunNamesFilesFromTheProjectRoot(t *testing.T) {
	src := "// FILE: app/main.nomi\n" +
		"import std/io\n\nfn main() {\n  io.print(\"start\")\n  todo \"finish main\"\n}\n\n" +
		"test \"unfinished\" {\n  assert 1 == 1\n  todo\n}\n" +
		"// FILE: nomi.toml\n[module]\nname = \"demo\"\nentry_points = [\"app/main\"]\n" +
		"// FILE: go.mod\nmodule demo\n\ngo 1.27.0\n"
	dir := t.TempDir()
	p, hasTests, err := vmhost.LoadMarked(src, dir)
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if !hasTests {
		t.Fatal("the entry's test is not found")
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if err == nil {
		t.Fatal("main ran past its todo")
	}
	if got, want := vmhost.FormatFailure(err), "todo reached at app/main.nomi:5: finish main"; got != want {
		t.Errorf("main's failure is %q, want %q", got, want)
	}
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 1 || cases[0].Err == nil {
		t.Fatalf("the test case did not fail: %+v", cases)
	}
	if got, want := cases[0].Err.Error(), "todo reached at app/main.nomi:10"; got != want {
		t.Errorf("the case's failure is %q, want %q", got, want)
	}
	if strings.Contains(out.String(), dir) {
		t.Errorf("the run's output names the directory %s:\n%s", dir, out.String())
	}
}

// A block runs the tests of every file that declares them, as `nomi test`
// runs a project's: the entry's, then each other test file's in block
// order, including one whose group boots the entry's app.
func TestLoadMarked_RunsEveryFilesTests(t *testing.T) {
	const files = `// FILE: main.nomi
import std/io

pub struct App {
    greeting: String
    context: Context
}

pub fn boot(_startup: Startup) -> Result<App, String> {
    Ok(#App{greeting: "hi", context: Context.root()})
}

pub fn greet(name: String) -> String {
    "${App.greeting} ${name}"
}

fn main() {
    io.print(greet("ada"))
}

group "entry" {
    boot boot(#Startup{})

    test "in the entry" {
        assert greet("bo") == "hi bo"
    }
}

// FILE: main_test.nomi
import main

group "greeting" {
    boot main.boot(#Startup{})

    test "booted from the entry" {
        assert main.greet("cy") == "hi cy"
    }
}

// FILE: more_test.nomi
test "on its own" {
    assert 1 + 1 == 2
}
`
	for _, tc := range []struct{ name, src string }{
		{"in memory", files},
		{"of modules", files + "// FILE: go.mod\nmodule example.com/app\n\ngo 1.27.0\n// FILE: nomi.toml\n[module]\nname = \"app\"\nentry_points = [\"main\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, hasTests, err := vmhost.LoadMarked(tc.src, t.TempDir())
			if err != nil {
				t.Fatalf("LoadMarked: %v", err)
			}
			if !hasTests {
				t.Fatal("hasTests is false")
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if out.String() != "hi ada\n" {
				t.Fatalf("main printed %q", out.String())
			}
			var names []string
			for _, c := range p.Cases(&out, vmhost.TestOptions{}) {
				if c.Err != nil || c.Blocked != nil {
					t.Errorf("%s: err %v, blocked %v", c.Name, c.Err, c.Blocked)
				}
				names = append(names, c.Name)
			}
			if got, want := strings.Join(names, " | "), "entry / in the entry | greeting / booted from the entry | on its own"; got != want {
				t.Fatalf("cases %q, want %q", got, want)
			}
		})
	}
}
