package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildCommand_RefusesTodos: `nomi check` and `nomi run` accept a program
// with `todo`s, and `nomi build` refuses it, listing every `todo` in the
// program's files whether the run reaches it or not, and writes nothing.
func TestBuildCommand_RefusesTodos(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	// The real path, so the working directory and the paths agree and the
	// reports name files relative to it.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import {
    std/io
    header
}

fn main() {
    io.print("start")
    if False {
        _ = header.parse("x")
    }
}
`)
	mustWrite(t, filepath.Join(dir, "header.nomi"), `pub fn parse(text: String): Int {
    todo "parse the header"
}

pub fn size(): Int {
    todo
}
`)

	if c := runProcess(t, env, dir, "", nomiBin, "check", entry); c.exit != 0 {
		t.Fatalf("nomi check rejects todos: exit %d\n%s", c.exit, c.transcript())
	}
	if r := runProcess(t, env, dir, "", nomiBin, "run", entry); r.exit != 0 || r.stdout != "start\n" {
		t.Fatalf("nomi run with an unreached todo: exit %d\n%s", r.exit, r.transcript())
	}

	bin := filepath.Join(t.TempDir(), "app")
	b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin)
	want := "nomi build: 2 todos remain:\n" +
		"  header.nomi:2:5 todo \"parse the header\"\n" +
		"  header.nomi:6:5 todo\n"
	if b.exit == 0 || b.stderr != want || b.stdout != "" {
		t.Fatalf("nomi build: exit %d\n%s\nwant stderr\n%s", b.exit, b.transcript(), want)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("a refused build left something at -o (%v)", err)
	}
}

// TestRunCommand_ReachingATodoReportsIt: reaching a `todo` stops `nomi run`
// with rt's text, relative to the working directory, and a failing status.
func TestRunCommand_ReachingATodoReportsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	// The real path, so the working directory and the paths agree and the
	// reports name files relative to it.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, "import std/io\n\nfn main() {\n    io.print(\"start\")\n    todo \"finish main\"\n}\n")
	r := runProcess(t, env, dir, "", nomiBin, "run", entry)
	if r.exit == 0 || r.stdout != "start\n" || r.stderr != "todo reached at main.nomi:5: finish main\n" {
		t.Fatalf("exit %d\n%s", r.exit, r.transcript())
	}
}
