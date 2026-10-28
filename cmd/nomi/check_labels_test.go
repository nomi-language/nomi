package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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
