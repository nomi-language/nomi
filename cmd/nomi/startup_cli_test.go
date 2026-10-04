package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStartupCLIInputs(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.nomi")
	mustWrite(t, source, `import {
 std/io
}
struct Settings {
  context: Context
}
fn boot(startup: Startup): Settings {
 io.print(Map.get(startup.env, "NOMI_STARTUP_CLI_TEST"))
 io.print(startup.args)
 Settings{context: Context.root(), }
}
fn main() {
  Unit
}
`)
	cmd := exec.Command(nomiBin, "run", source, "first", "--literal", "two words")
	cmd.Env = append(os.Environ(), "NOMI_STARTUP_CLI_TEST=configured")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := "Some(configured)\n[first, --literal, two words]\n"
	if string(out) != want {
		t.Fatalf("startup inputs: got %q, want %q", out, want)
	}
}
