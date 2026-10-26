package main

import (
	"path/filepath"
	"testing"
)

// A `main` or an entry boot that returns `Err(#Exit{code, message})` with
// std/process's Exit exits with the code and prints the message alone, with
// no `error: `, after the shutdown every run ends with.

var exitPrograms = []struct {
	name, src      string
	stdout, stderr string
	exit           int
}{
	{"main's Exit sets the status", `import {
  std/io
  std/process.Exit
}

fn main() -> Result<Unit, Exit> {
  io.print("before")
  io.eprint("warning")
  Err(#Exit{code: 3, message: "usage: count <file>"})
}
`, "before\n", "warning\nusage: count <file>\n", 3},
	{"an empty message prints nothing", `import std/process.Exit

fn main() -> Result<Unit, Exit> {
  Err(#Exit{code: 42})
}
`, "", "", 42},
	{"code 0 succeeds and still prints the message", `import std/process.Exit

fn main() -> Result<Unit, Exit> {
  Err(#Exit{code: 0, message: "nothing to do"})
}
`, "", "nothing to do\n", 0},
	{"a code past 255 is a trap", `import std/process.Exit

fn main() -> Result<Unit, Exit> {
  Err(#Exit{code: 256, message: "x"})
}
`, "", "exit code 256 is outside 0 to 255\n", 1},
	{"boot's Exit sets the status after its cleanup", `import {
  std/io
  std/process.Exit
}

struct App {
  port: Int
}

fn boot() -> Result<App, Exit> {
  defer io.print("cleanup")
  Err(#Exit{code: 5, message: "no port"})
}

fn main() {
  io.print("main ran on ${App.port}")
}
`, "cleanup\n", "no port\n", 5},
	{"a program's own Exit is an ordinary error", `struct Exit {
  code: Int
}

fn main() -> Result<Unit, Exit> {
  Err(#Exit{code: 9})
}
`, "", "error: #Exit{code: 9}\n", 1},
}

func TestRunCommand_ExitSetsTheStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	for _, tc := range exitPrograms {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, tc.src)
			got := runProcess(t, env, dir, "", nomiBin, "run", entry)
			if got.stdout != tc.stdout || got.stderr != tc.stderr || got.exit != tc.exit {
				t.Fatalf("nomi run: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
					got.exit, got.stdout, got.stderr, tc.exit, tc.stdout, tc.stderr)
			}
		})
	}
}

// TestBuildCommand_ExitSetsTheBinarysStatus: a built executable, and a
// .nomic file run by `nomi run`, exit as `nomi run` does.
func TestBuildCommand_ExitSetsTheBinarysStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	env := buildEnv(t.TempDir())
	for _, tc := range exitPrograms {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, tc.src)
			bin := exe(filepath.Join(t.TempDir(), "app"))
			if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			got := runProcess(t, env, dir, "", bin)
			if got.stdout != tc.stdout || got.stderr != tc.stderr || got.exit != tc.exit {
				t.Fatalf("built binary: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
					got.exit, got.stdout, got.stderr, tc.exit, tc.stdout, tc.stderr)
			}
			nomic := filepath.Join(t.TempDir(), "app.nomic")
			if b := runProcess(t, env, dir, "", nomiBin, "build", "--no-vm", entry, "-o", nomic); b.exit != 0 {
				t.Fatalf("nomi build --no-vm: exit %d\n%s", b.exit, b.transcript())
			}
			got = runProcess(t, env, dir, "", nomiBin, "run", nomic)
			if got.stdout != tc.stdout || got.stderr != tc.stderr || got.exit != tc.exit {
				t.Fatalf("nomi run app.nomic: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
					got.exit, got.stdout, got.stderr, tc.exit, tc.stdout, tc.stderr)
			}
		})
	}
}
