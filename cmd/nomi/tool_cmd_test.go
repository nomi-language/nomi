package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The generator fixture, from the repository root, where TestMain runs the
// tests: module csvdata ships tools, and module rates requires it.
var toolFixture = filepath.Join("testdata", "tools")

// `nomi tool <module> <name>` runs the module's tools/<name>.nomi with every
// effect a program has, in the current directory.
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
