package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// ratesBlock is testdata/tools' rates and csvdata as one marked source,
// the generator's module under csvdata/, with tables.nomi's
// text passed through edit.
func ratesBlock(t *testing.T, edit func(string) string) string {
	t.Helper()
	read := func(path string) string {
		data, err := os.ReadFile("../testdata/tools/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sections := []struct{ name, text string }{
		{"main.nomi", read("rates/main.nomi")},
		{"tables.nomi", edit(read("rates/tables.nomi"))},
		{"data/rates.csv", read("rates/data/rates.csv")},
		{"data/names.csv", read("rates/data/names.csv")},
		{"nomi.toml", read("rates/nomi.toml")},
		{"go.mod", strings.Replace(read("rates/go.mod"), "../csvdata", "./csvdata", 1)},
		{"csvdata/csvdata.nomi", read("csvdata/csvdata.nomi")},
		{"csvdata/nomi.toml", "[module]\nname = \"csvdata\"\n"},
		{"csvdata/go.mod", read("csvdata/go.mod")},
	}
	var b strings.Builder
	for _, s := range sections {
		b.WriteString("// FILE: " + s.name + "\n" + s.text + "\n")
	}
	return b.String()
}

// siblingsBlock is testdata/tools' rates and csvdata as they are on disk,
// two projects side by side in one marked source, rates first, with
// rates/data/rates.csv's text passed through edit. csvdata's tools come
// with it, so a file that declares `fn main` is in the project that is not
// the block's.
func siblingsBlock(t *testing.T, edit func(string) string) string {
	t.Helper()
	read := func(path string) string {
		data, err := os.ReadFile("../testdata/tools/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var b strings.Builder
	for _, name := range []string{
		"rates/tables_test.nomi", "rates/main.nomi", "rates/tables.nomi", "rates/data/rates.csv",
		"rates/data/names.csv", "rates/nomi.toml", "rates/go.mod",
		"csvdata/csvdata.nomi", "csvdata/tools/generate.nomi", "csvdata/tools/rows.nomi",
		"csvdata/nomi.toml", "csvdata/go.mod",
	} {
		text := read(name)
		if name == "rates/data/rates.csv" {
			text = edit(text)
		}
		b.WriteString("// FILE: " + name + "\n" + text + "\n")
	}
	return b.String()
}

// A block of two projects side by side, each with its own go.mod, runs
// the project of its first file, which reaches the other through its
// go.mod's `replace csvdata => ../csvdata` as the same files on disk do.
// Its tests read data/ from the project's directory, and a diagnostic
// names a file by its path from the block's root, as its tab does.
func TestLoadMarked_SiblingProjectsRunTheFirstFilesProject(t *testing.T) {
	src := siblingsBlock(t, func(s string) string { return s })
	dir := t.TempDir()
	p, hasTests, err := vmhost.LoadMarked(src, dir)
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if !hasTests {
		t.Fatal("rates/tables_test.nomi's test is not found")
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "9200\nPound \"sterling\"\nYen ${not interpolated}\nFalse\n" {
		t.Fatalf("rates printed %q", got)
	}
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
		t.Fatalf("the digest test does not pass: %+v\n%s", cases, out.String())
	}

	// A changed rate fails the digest test, which read the file from the
	// project's directory.
	p, _, err = vmhost.LoadMarked(siblingsBlock(t, func(s string) string { return strings.Replace(s, "9200", "9300", 1) }), dir)
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if cases := p.Cases(&out, vmhost.TestOptions{}); len(cases) != 1 || cases[0].Err == nil {
		t.Fatalf("the digest test passes with a changed rate: %+v", cases)
	}

	broken := strings.Replace(src, "Some(9200)", "Some(92.00)", 1)
	_, _, err = vmhost.LoadMarked(broken, dir)
	if err == nil {
		t.Fatal("a Float rate loads")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "rates/tables.nomi:") || strings.Contains(msg, dir) {
		t.Fatalf("the error does not name rates/tables.nomi from the block's root:\n%s", msg)
	}
}

// A block of sibling projects holds every file in one of them.
func TestLoadMarked_SiblingProjectsHoldEveryFile(t *testing.T) {
	src := siblingsBlock(t, func(s string) string { return s }) + "// FILE: notes.txt\nloose\n"
	_, _, err := vmhost.LoadMarked(src, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "notes.txt is in none of the block's projects") {
		t.Fatalf("a file outside the projects loads, or fails otherwise: %v", err)
	}
}

// A marked source whose root module requires a module of its own loads as
// the same files on disk would, and an error in a section is named by the
// section's path.
func TestLoadMarked_AProjectOfModulesLoadsAsItsFiles(t *testing.T) {
	src := ratesBlock(t, func(s string) string { return s })
	p, hasTests, err := vmhost.LoadMarked(src, t.TempDir())
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if hasTests {
		t.Fatal("main.nomi declares no test")
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "9200\nPound \"sterling\"\nYen ${not interpolated}\nFalse\n" {
		t.Fatalf("rates printed %q", got)
	}

	// The same directory again, with a rate in a Float: the directory is
	// emptied and written anew, and the error is at the cell.
	dir := t.TempDir()
	if _, _, err := vmhost.LoadMarked(src, dir); err != nil {
		t.Fatal(err)
	}
	_, _, err = vmhost.LoadMarked(ratesBlock(t, func(s string) string { return strings.Replace(s, "Some(9200)", "Some(92.00)", 1) }), dir)
	if err == nil {
		t.Fatal("a Float rate loads")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "tables.nomi:") || strings.Contains(msg, dir) {
		t.Fatalf("the error does not name tables.nomi from the project root:\n%s", msg)
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
