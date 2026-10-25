package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The macro fixture, from the repository root, where TestMain runs the
// tests: module csvdata declares `table` and a tool, and module
// rates uses the macro.
var macroFixture = filepath.Join("testdata", "macros")

// `nomi expand` prints what each macro declaration of a file expands
// to, formatted.
func TestExpandCommand_PrintsTheExpansions(t *testing.T) {
	out, err := runNomi(t, t.TempDir(), "expand", filepath.Join(macroFixture, "rates", "tables.nomi"))
	if err != nil {
		t.Fatalf("nomi expand: %v\n%s", err, out)
	}
	want := "// csvdata.table! rate (line 4), expanded by the macro table of module csvdata\n" +
		"/// Exchange rates to the euro, in basis points.\n" +
		"pub fn rate(code: String) -> Maybe<Int> {\n" +
		"    case code {\n" +
		"        \"usd\" -> Some(9200)\n" +
		"        \"gbp\" -> Some(11700)\n" +
		"        \"jpy\" -> Some(61)\n" +
		"        _ -> None\n" +
		"    }\n" +
		"}\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("nomi expand printed:\n%s\nwant it to start:\n%s", out, want)
	}
	if !strings.Contains(out, "// csvdata.table! currency_name (line 7)") {
		t.Errorf("nomi expand printed no second expansion:\n%s", out)
	}
	if out, err := runNomi(t, t.TempDir(), "expand"); err == nil || !strings.Contains(out, "usage: nomi expand <file>") {
		t.Errorf("nomi expand with no file: %v\n%s", err, out)
	}
}

// `nomi expand` prints a type a declaration with a type name declares
// (cafe's `choices.one_of! Size`).
func TestExpandCommand_PrintsADeclaredType(t *testing.T) {
	out, err := runNomi(t, t.TempDir(), "expand", filepath.Join(macroFixture, "cafe", "sizes.nomi"))
	if err != nil {
		t.Fatalf("nomi expand: %v\n%s", err, out)
	}
	want := "// choices.one_of! Size (line 4), expanded by the macro one_of of module choices\n" +
		"/// How big a coffee is.\n" +
		"pub enum Size {\n" +
		"    Small\n" +
		"    Medium\n" +
		"    Large\n" +
		"}\n"
	if out != want {
		t.Errorf("nomi expand printed:\n%s\nwant:\n%s", out, want)
	}
}

// `nomi tool <module> <name>` runs the module's tools/<name>.nomi with every
// effect a program has, in the current directory.
func TestToolCommand_RunsAModulesTool(t *testing.T) {
	rates, err := filepath.Abs(filepath.Join(macroFixture, "rates"))
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
