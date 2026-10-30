package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A `fn main` that returns `Err(e)` fails the run: `error: ` and e's text on
// stderr, exit 1. The text is e's Display rendering when its type implements
// Display, and its Debug rendering otherwise. `Ok(Unit)` exits 0.

var mainErrPrograms = []struct {
	name, src      string
	stdout, stderr string
	exit           int
}{
	{"string error prints without quotes", `import std/io

fn main() -> Result<Unit, String> {
  io.print("before")
  Err("config file not found")
}
`, "before\n", "error: config file not found\n", 1},
	{"an error type with Display prints through it", `struct Missing {
  path: String
  line: Int
}

impl Display for Missing {
  fn to_string(m: Missing) -> String {
    "${m.path} is missing (line ${m.line})"
  }
}

fn main() -> Result<Unit, Missing> {
  Err(#Missing{path: "app.toml", line: 3})
}
`, "", "error: app.toml is missing (line 3)\n", 1},
	{"an error type without Display prints its Debug text", `struct Missing {
  path: String
  line: Int
}

fn main() -> Result<Unit, Missing> {
  Err(#Missing{path: "app.toml", line: 3})
}
`, "", "error: #Missing{path: \"app.toml\", line: 3}\n", 1},
	{"a try in main leaves with the error", `fn port(text: String) -> Result<Int, String> {
  if text == "" {
    Err("no port given")
  } else {
    Ok(8080)
  }
}

fn main() -> Result<Unit, String> {
  _ = port("")?
  Ok(Unit)
}
`, "", "error: no port given\n", 1},
	{"Ok exits 0", `import std/io

fn main() -> Result<Unit, String> {
  io.print("fine")
  Ok(Unit)
}
`, "fine\n", "", 0},
}

// TestRunCommand_MainReturningAValueIsACheckError: `nomi run` would drop an
// `Ok` payload, so a main declared to return one fails the check, at the
// return type, with a help line that says how to show the value.
func TestRunCommand_MainReturningAValueIsACheckError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, "fn main() -> Result<String, String> {\n    Ok(\"hello\")\n}\n")
	for _, command := range []string{"check", "run"} {
		got := runProcess(t, env, dir, "", nomiBin, command, entry)
		if got.exit != 1 || got.stdout != "" {
			t.Fatalf("nomi %s: exit %d, stdout %q; want exit 1 and no output\n%s", command, got.exit, got.stdout, got.transcript())
		}
		for _, want := range []string{
			"error: `main` must return `Unit` or `Result<Unit, E>`",
			"main.nomi:1:14",
			"help: to show a value, print it with `io.print` and return `Ok(Unit)`",
		} {
			if !strings.Contains(got.stderr, want) {
				t.Fatalf("nomi %s stderr lacks %q:\n%s", command, want, got.stderr)
			}
		}
	}
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
			bin := exe(filepath.Join(t.TempDir(), "app"))
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

// A boot that returns `Err(e)` fails the run as a main returning it does:
// main does not run, the cleanup boot registered runs, most recent first,
// and the run prints `error: ` and e's text on stderr and exits 1.
var bootErrPrograms = []struct {
	name, src      string
	stdout, stderr string
}{
	{"a String error, after boot's cleanup", `import std/io

struct App {
  port: Int
}

fn port(text: String) -> Result<Int, String> {
  if text == "" {
    Err("no port given")
  } else {
    Ok(8080)
  }
}

fn boot() -> Result<App, String> {
  defer io.print("first registered, last run")
  defer io.print("last registered, first run")
  p = port("")?
  Ok(#App{port: p})
}

fn main() {
  io.print("main ran on ${App.port}")
}
`, "last registered, first run\nfirst registered, last run\n", "error: no port given\n"},
	{"an error type with Display prints through it", `struct App {
  port: Int
}

struct Missing {
  key: String
}

impl Display for Missing {
  fn to_string(m: Missing) -> String {
    "${m.key} is not set"
  }
}

fn boot() -> Result<App, Missing> {
  Err(#Missing{key: "PORT"})
}

fn main() {
  _ = App.port
}
`, "", "error: PORT is not set\n"},
	{"an error type without Display prints its Debug text", `struct App {
  port: Int
}

struct Missing {
  key: String
}

fn boot() -> Result<App, Missing> {
  Err(#Missing{key: "PORT"})
}

fn main() {
  _ = App.port
}
`, "", "error: #Missing{key: \"PORT\"}\n"},
}

// TestBootErrFailsEveryWayAProgramRuns: `nomi run`, a built executable, and
// a `.nomic` file run by `nomi run` and by a bare nomi-vm all report a boot
// that returns `Err` the same way.
func TestBootErrFailsEveryWayAProgramRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	env := buildEnv(t.TempDir())
	runner := exe(filepath.Join(t.TempDir(), "nomi-vm"))
	if b := runProcess(t, env, t.TempDir(), "", nomiBin, "build-vm", "-o", runner); b.exit != 0 {
		t.Fatalf("nomi build-vm: exit %d\n%s", b.exit, b.transcript())
	}
	for _, tc := range bootErrPrograms {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, tc.src)
			bin := exe(filepath.Join(t.TempDir(), "app"))
			if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			image := filepath.Join(t.TempDir(), "app.nomic")
			if b := runProcess(t, env, dir, "", nomiBin, "build", "--no-vm", entry, "-o", image); b.exit != 0 {
				t.Fatalf("nomi build --no-vm: exit %d\n%s", b.exit, b.transcript())
			}
			for how, got := range map[string]observedRun{
				"nomi run":          runProcess(t, env, dir, "", nomiBin, "run", entry),
				"built executable":  runProcess(t, env, dir, "", bin),
				"nomi run (.nomic)": runProcess(t, env, dir, "", nomiBin, "run", image),
				"nomi-vm (.nomic)":  runProcess(t, env, dir, "", runner, image),
			} {
				if got.stdout != tc.stdout || got.stderr != tc.stderr || got.exit != 1 {
					t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 1, stdout %q, stderr %q",
						how, got.exit, got.stdout, got.stderr, tc.stdout, tc.stderr)
				}
			}
		})
	}
}
