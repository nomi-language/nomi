package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi check --format json` writes JSON Lines: a record per diagnostic
// with its span as lines, byte columns and byte offsets, its hints and its
// fix as text edits, a record per file, and a summary. The golden file is
// the output with the temporary directory written `<dir>`; regenerate it
// with NOMI_REGENERATE_EXPECTATIONS=1.
func TestCheckCommand_JSONLines(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import helper\n\nfn main() {\n    xs = [1, 2]\n    dbg xs.len()\n    dbg helper.twice(nil)\n}\n")
	mustWrite(t, filepath.Join(dir, "helper.nomi"), "pub fn twice(n: Int) -> Int {\n    n * 2\n}\n")
	out, err := runNomi(t, t.TempDir(), "check", "--format", "json", dir)
	if err == nil {
		t.Fatalf("nomi check passed a broken program:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
	}
	got := strings.ReplaceAll(out, dir, "<dir>")
	golden := filepath.Join("cmd", "nomi", "testdata", "check_json.golden") // tests run at the repository root
	if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

// Each fix's edits, applied to the file at their byte offsets, give a
// program with no error left at that place.
func TestCheckCommand_JSONFixesApply(t *testing.T) {
	dir := t.TempDir()
	src := "fn main() {\n    xs = [1, 2]\n    dbg xs.len()\n    v: Vector<Int> = [1]\n    dbg v\n}\n"
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, src)
	out, _ := runNomi(t, t.TempDir(), "check", "--format", "json", path)
	type edit struct {
		StartByte int    `json:"start_byte"`
		EndByte   int    `json:"end_byte"`
		Text      string `json:"text"`
	}
	var edits []edit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec struct {
			Type string
			Fix  *struct{ Edits []edit }
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if rec.Fix != nil {
			edits = append(edits, rec.Fix.Edits...)
		}
	}
	if len(edits) < 2 {
		t.Fatalf("got %d edits, want the method call's and the vector literal's:\n%s", len(edits), out)
	}
	fixed := []byte(src)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		fixed = append(append(append([]byte(nil), fixed[:e.StartByte]...), e.Text...), fixed[e.EndByte:]...)
	}
	mustWrite(t, path, string(fixed))
	if out, err := runNomi(t, t.TempDir(), "check", path); err != nil {
		t.Fatalf("the fixed program does not check:\n%s\n%s", fixed, out)
	}
}

// A file with several syntax errors reports each of them in one run: one
// per declaration and one per statement in a body.
func TestCheckCommand_ReportsEverySyntaxError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, "fn main() {\n    let a = 1\n    b := 2\n    dbg a\n}\n\nfn other() -> Int {\n    x = 1 +\n}\n")
	out, err := runNomi(t, t.TempDir(), "check", "--format", "short", path)
	if err == nil {
		t.Fatalf("nomi check passed:\n%s", out)
	}
	for _, want := range []string{"main.nomi:2:5: Nomi has no `let`", "main.nomi:3:7: Nomi binds a name with `=`", "main.nomi:9:1:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
