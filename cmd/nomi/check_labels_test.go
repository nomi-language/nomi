package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi check <dir>` labels a failure by the file that holds the errors:
// a CSV cell a macro's table read is `FAIL data/rates.csv`, not the
// entry that imports the table.
func TestCheckCommand_LabelsAMacroInputsErrorByTheInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(macroFixture)); err != nil {
		t.Fatal(err)
	}
	rates := filepath.Join(dir, "rates")
	mustWrite(t, filepath.Join(rates, "data", "rates.csv"), "code,basis_points\nusd,9200\ngbp,1.5\n")
	out, err := runNomi(t, t.TempDir(), "check", rates)
	if err == nil {
		t.Fatalf("nomi check passed a Float cell in an Int table:\n%s", out)
	}
	csv := filepath.Join(rates, "data", "rates.csv")
	for _, want := range []string{"FAIL " + csv + "\n", "case branch type mismatch: expected Maybe<Int>, got Maybe<Float>", csv + ":3:5"} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL "+filepath.Join(rates, "main.nomi")) {
		t.Errorf("nomi check labelled the entry, which holds no error:\n%s", out)
	}
}

// Two entries that import one broken file print its errors once, under
// the file; the second entry's failure says its errors are above.
func TestCheckCommand_PrintsAnErrorTwoEntriesShareOnce(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"a\", \"b\"]\n")
	mustWrite(t, filepath.Join(dir, "shared.nomi"), "pub fn n() -> Int {\n    \"not an int\"\n}\n")
	for _, entry := range []string{"a", "b"} {
		mustWrite(t, filepath.Join(dir, entry+".nomi"), "import {\n    shared\n    std/io\n}\n\nfn main() {\n    io.print(shared.n())\n}\n")
	}
	out, err := runNomi(t, t.TempDir(), "check", dir)
	if err == nil {
		t.Fatalf("nomi check passed a broken shared file:\n%s", out)
	}
	if got := strings.Count(out, "return type mismatch: expected Int, got String"); got != 1 {
		t.Errorf("the shared error is printed %d times, want once:\n%s", got, out)
	}
	for _, want := range []string{
		"FAIL " + filepath.Join(dir, "shared.nomi") + "\n",
		"FAIL " + filepath.Join(dir, "b.nomi") + ": its errors are printed above\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}
}
