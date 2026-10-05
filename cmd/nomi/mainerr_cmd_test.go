package main

import (
	"path/filepath"
	"testing"
)

// A `fn main` that returns `Err(e)` fails the run: `error: ` and e's text on
// stderr, exit 1. The text is e's Display rendering when its type implements
// Display, and its Debug rendering otherwise. `Ok`, and a main returning any
// other type, exit 0.

var mainErrPrograms = []struct {
	name, src      string
	stdout, stderr string
	exit           int
}{
	{"string error prints without quotes", `import std/io

fn main(): Result<Unit, String> {
  io.print("before")
  Err("config file not found")
}
`, "before\n", "error: config file not found\n", 1},
	{"an error type with Display prints through it", `struct Missing {
  path: String
  line: Int
}

impl Display for Missing {
  fn to_string(m: Missing): String {
    "${m.path} is missing (line ${m.line})"
  }
}

fn main(): Result<Unit, Missing> {
  Err(Missing{path: "app.toml", line: 3})
}
`, "", "error: app.toml is missing (line 3)\n", 1},
	{"an error type without Display prints its Debug text", `struct Missing {
  path: String
  line: Int
}

fn main(): Result<Unit, Missing> {
  Err(Missing{path: "app.toml", line: 3})
}
`, "", "error: Missing{path: \"app.toml\", line: 3}\n", 1},
	{"a try in main leaves with the error", `fn port(text: String): Result<Int, String> {
  if text == "" {
    Err("no port given")
  } else {
    Ok(8080)
  }
}

fn main(): Result<Unit, String> {
  _ = try port("")
  Ok(Unit)
}
`, "", "error: no port given\n", 1},
	{"Ok exits 0", `import std/io

fn main(): Result<Unit, String> {
  io.print("fine")
  Ok(Unit)
}
`, "fine\n", "", 0},
	{"a Maybe main is not a failure", `fn main(): Maybe<Int> {
  None
}
`, "", "", 0},
}

func TestRunCommand_MainErrFailsTheRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	for _, tc := range mainErrPrograms {
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

// TestBuildCommand_MainErrFailsTheBinary: a built executable reports a main
// that returns `Err` exactly as `nomi run` does.
func TestBuildCommand_MainErrFailsTheBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	env := buildEnv(t.TempDir())
	for _, tc := range mainErrPrograms[:3] {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, tc.src)
			bin := filepath.Join(t.TempDir(), "app")
			if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			got := runProcess(t, env, dir, "", bin)
			if got.stdout != tc.stdout || got.stderr != tc.stderr || got.exit != tc.exit {
				t.Fatalf("built binary: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
					got.exit, got.stdout, got.stderr, tc.exit, tc.stdout, tc.stderr)
			}
		})
	}
}
