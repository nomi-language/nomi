package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdBindingProgram binds three Go standard library packages with no wrapper
// package of its own: a string function, one returning (T, error), and a
// float function.
const stdBindingProgram = `import std/io

gopkg "strings" as strings
gopkg "strconv" as strconv
gopkg "math" as math

fn upper(s: String): String go strings.ToUpper
fn itoa(n: Int): String go strconv.Itoa
fn atoi(s: String): Result<Int, String> go strconv.Atoi
fn sqrt(x: Float): Float go math.Sqrt

fn main() {
    io.print(upper("hi"))
    io.print(itoa(42))
    io.print(atoi("17"))
    io.print(atoi("x"))
    io.print(sqrt(2.0))
}
`

const stdBindingWant = "HI\n42\nOk(17)\nErr(strconv.Atoi: parsing \"x\": invalid syntax)\n1.4142135623730951\n"

// A binding to a Go standard library package runs with no wrapper package and,
// in a directory with no go.mod, with no go.mod at all: `nomi check`, `nomi
// run`, an extensionless `#!` script and `nomi build` all reach it.
func TestFFI_StdLibraryBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	for _, withGoMod := range []bool{false, true} {
		name := "no go.mod"
		if withGoMod {
			name = "go.mod"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if withGoMod {
				mustWrite(t, filepath.Join(dir, "go.mod"), "module stdbind\n\ngo 1.26.3\n")
			} else if goModAbove(dir) {
				t.Skip("a go.mod above the temp directory")
			}
			env := buildEnv(t.TempDir())
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, stdBindingProgram)
			script := filepath.Join(dir, "bin", "hi")
			mustWrite(t, script, "#!/usr/bin/env nomi\n"+stdBindingProgram)

			if r := runProcess(t, env, t.TempDir(), "", nomiBin, "check", entry); r.exit != 0 {
				t.Fatalf("nomi check: exit %d\n%s", r.exit, r.transcript())
			}
			for _, args := range [][]string{{"run", entry}, {"run", script}, {script}} {
				if r := runProcess(t, env, t.TempDir(), "", nomiBin, args...); r.exit != 0 || r.stdout != stdBindingWant {
					t.Errorf("nomi %s: exit %d, stdout %q, want %q\nstderr:\n%s",
						strings.Join(args, " "), r.exit, r.stdout, stdBindingWant, r.stderr)
				}
			}
			built := filepath.Join(t.TempDir(), "hi")
			if b := runProcess(t, env, t.TempDir(), "", nomiBin, "build", entry, "-o", built); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			if r := runProcess(t, env, t.TempDir(), "", built); r.exit != 0 || r.stdout != stdBindingWant {
				t.Errorf("the built program: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, stdBindingWant, r.stderr)
			}
		})
	}
}

// A binding to a function the standard library package does not declare is
// refused before the program runs, naming the binding, the package and the
// function; so is a `gopkg` naming a package internal to the standard library,
// by the checker at the import path.
func TestFFI_StdLibraryBindingErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	missing := filepath.Join(dir, "missing", "main.nomi")
	mustWrite(t, missing, `import std/io

gopkg "strings" as strings

fn shout(s: String): String go strings.Shout

fn main() {
    io.print(shout("hi"))
}
`)
	want := `main.nomi:5:40: Go package "strings" has no top-level function "Shout" for Nomi binding "shout"`
	for _, cmd := range []string{"check", "run"} {
		r := runProcess(t, env, t.TempDir(), "", nomiBin, cmd, missing)
		if r.exit == 0 || !strings.Contains(r.stderr, want) {
			t.Errorf("nomi %s: exit %d, want a refusal containing %q\n%s", cmd, r.exit, want, r.transcript())
		}
		if strings.Contains(r.stdout, "HI") {
			t.Errorf("nomi %s ran the program:\n%s", cmd, r.transcript())
		}
	}

	internal := filepath.Join(dir, "internal", "main.nomi")
	mustWrite(t, internal, `gopkg "internal/abi" as abi

fn main() {}
`)
	r := runProcess(t, env, t.TempDir(), "", nomiBin, "check", internal)
	if want := `gopkg "internal/abi": internal to the Go standard library, which no other module may import`; r.exit == 0 || !strings.Contains(r.stderr, want) {
		t.Errorf("nomi check: exit %d, want a refusal containing %q\n%s", r.exit, want, r.transcript())
	}
}

// goModAbove reports whether a go.mod sits in dir or a directory above it.
func goModAbove(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
