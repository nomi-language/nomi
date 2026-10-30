package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generator fixture, from the repository root, where TestMain runs the
// tests: module csvdata ships tools, and module rates requires it; module
// cafe runs a tool of its own.
var toolFixture = filepath.Join("testdata", "tools")

// `nomi tool <module> <name>` runs the module's tools/<name>.nomi in the
// current directory, with the access to files, the network and output that
// `nomi run` gives a program.
func TestToolCommand_RunsAModulesTool(t *testing.T) {
	rates, err := filepath.Abs(filepath.Join(toolFixture, "rates"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "rows", "data/rates.csv", "data/names.csv")
	if err != nil {
		t.Fatalf("nomi tool: %v\n%s", err, out)
	}
	if want := "data/rates.csv: 3 rows\ndata/names.csv: 3 rows\n"; out != want {
		t.Errorf("nomi tool printed:\n%s\nwant:\n%s", out, want)
	}
	out, err = runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "nope")
	if err == nil || !strings.Contains(out, "module csvdata has no tool nope: there is no tools/nope.nomi in") {
		t.Errorf("an unknown tool: %v\n%s", err, out)
	}
	out, err = runNomiInDir(t, t.TempDir(), rates, "", "tool", "nomodule", "rows")
	if err == nil || !strings.Contains(out, "no module nomodule: it is neither this module nor one its go.mod requires") {
		t.Errorf("an unknown module: %v\n%s", err, out)
	}
}

// A generator's --check finds the fixture's generated files current, so
// they are what the generator writes.
func TestToolCommand_TheFixturesGeneratedFilesAreCurrent(t *testing.T) {
	rates, err := filepath.Abs(filepath.Join(toolFixture, "rates"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "generate", "--check")
	if err != nil || out != "up to date\n" {
		t.Errorf("nomi tool csvdata generate --check: %v\n%s", err, out)
	}
}

// A tool whose main returns an Err fails with the error after "error: " and,
// for the cafe's own choices tool, writes nothing: a word that is not
// snake_case stops it before it writes enums.nomi.
func TestToolCommand_AToolsErrStopsIt(t *testing.T) {
	cafe := filepath.Join(t.TempDir(), "cafe")
	if err := os.CopyFS(cafe, os.DirFS(filepath.Join(toolFixture, "cafe"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cafe, "enums.choices"), []byte("Size: Small medium large\nRoast: light medium dark\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enums := filepath.Join(cafe, "enums.nomi")
	before, err := os.ReadFile(enums)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), cafe, "", "tool", "cafe", "choices")
	if code := exitCode(err); code != 1 {
		t.Errorf("nomi tool cafe choices exited %d (%v), want 1\n%s", code, err, out)
	}
	if want := "error: enums.choices:1: `Small` is not a snake_case word\n"; out != want {
		t.Errorf("nomi tool printed:\n%s\nwant:\n%s", out, want)
	}
	after, err := os.ReadFile(enums)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the tool wrote enums.nomi after its error:\n%s", after)
	}
}

// A module's tools are entry points without being listed in nomi.toml. The
// cafe lists only main: its tool runs, `nomi check .` passes and checks the
// tool with main, and main runs. Listing the tool as well changes nothing.
func TestToolCommand_AToolIsAnEntryWithoutBeingListed(t *testing.T) {
	for _, entries := range []string{`["main"]`, `["main", "tools/choices"]`} {
		cafe := filepath.Join(t.TempDir(), "cafe")
		if err := os.CopyFS(cafe, os.DirFS(filepath.Join(toolFixture, "cafe"))); err != nil {
			t.Fatal(err)
		}
		manifest := "[module]\nname = \"cafe\"\nentry_points = " + entries + "\n"
		if err := os.WriteFile(filepath.Join(cafe, "nomi.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		cache := t.TempDir()
		if out, err := runNomiInDir(t, cache, cafe, "", "tool", "cafe", "choices"); err != nil || out != "wrote enums.nomi\n" {
			t.Errorf("entry_points = %s: nomi tool cafe choices: %v\n%s", entries, err, out)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "check", "."); err != nil {
			t.Errorf("entry_points = %s: nomi check .: %v\n%s", entries, err, out)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "run", "main.nomi"); err != nil || out != "12\nTrue\n" {
			t.Errorf("entry_points = %s: nomi run main.nomi: %v\n%s", entries, err, out)
		}
		// `nomi check .` checks the tool, which nothing imports.
		tool := filepath.Join(cafe, "tools", "choices.nomi")
		src, err := os.ReadFile(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tool, append(src, "\nfn broken() -> Int {\n    \"text\"\n}\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "check", "."); err == nil || !strings.Contains(out, "choices.nomi") {
			t.Errorf("entry_points = %s: nomi check . of a broken tool: %v\n%s", entries, err, out)
		}
	}
}
