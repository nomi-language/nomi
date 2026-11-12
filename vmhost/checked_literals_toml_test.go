package vmhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A Toml literal is checked when the program is checked: a slot that does
// not stand for a whole key or value is a compile error at its `${`, and
// text that is not TOML is one at the character the TOML reader stopped at.
func TestCheckedLiteral_TomlRefusesMisplacedSlotsAndBadText(t *testing.T) {
	for _, c := range []struct {
		line, message string
		col           int
	}{
		{`    _ = Toml"user = \"${v}\""`, "typed literal Toml\"user = \\\"${v}\\\"\" is invalid: a slot cannot go inside a TOML string; write it where the whole value goes, as in `key = ${value}`", 23},
		{`    _ = Toml"user = 'a${v}'"`, "typed literal Toml\"user = 'a${v}'\" is invalid: a slot cannot go inside a TOML string; write it where the whole value goes, as in `key = ${value}`", 23},
		{`    _ = Toml"a = 1 # ${v}"`, "typed literal Toml\"a = 1 # ${v}\" is invalid: a slot cannot go inside a TOML comment", 22},
		{`    _ = Toml"port = 80${v}"`, "typed literal Toml\"port = 80${v}\" is invalid: a slot must stand for a whole TOML key or value", 23},
		{`    _ = Toml"[a${v}]"`, "typed literal Toml\"[a${v}]\" is invalid: a slot must stand for a whole TOML key or value", 16},
		{`    _ = Toml"a = = 1"`, "typed literal Toml\"a = = 1\" is invalid: expected value but found '=' instead", 18},
		{`    _ = Toml"a = 1\na = ${v}"`, "typed literal Toml\"a = 1\\na = ${v}\" is invalid: Key 'a' has already been defined.", 21},
	} {
		src := "import\n    std::io\n    std::toml.Toml\nend\n\nfn main()\n    v = \"x\"\n    io.print(v)\n" + c.line + "\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.line, ds[0].Message, c.message)
		}
		if ds[0].Line != 9 || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want 9:%d", c.line, ds[0].Line, ds[0].Col, c.col)
		}
	}
}

// A program loaded from a file on disk places a checked literal's error at
// the character it names, as an in-memory one does.
func TestCheckedLiteral_ErrorPlacedInAFileOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	src := "import std::toml.Toml\n\nfn main()\n    _ = Toml\"a = = 1\"\nend\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	ds, ok := err.(Diagnostics)
	if !ok || len(ds) != 1 {
		t.Fatalf("Load: %v; want one diagnostic", err)
	}
	if ds[0].Line != 4 || ds[0].Col != 18 {
		t.Errorf("at %d:%d, want 4:18, the second `=`", ds[0].Line, ds[0].Col)
	}
}
