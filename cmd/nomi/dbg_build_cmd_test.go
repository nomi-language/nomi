package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildCommand_RefusesDbgsAndTodosTogether: `nomi check`, `nomi run` and
// `nomi test` accept a program with `dbg`s and run them, and `nomi build`
// refuses it with one listing of every `todo` and `dbg` in the program's
// files (the entry and what it imports), reached or not, and writes nothing.
func TestBuildCommand_RefusesDbgsAndTodosTogether(t *testing.T) {
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
    _ = dbg header.size()
    if False {
        _ = header.parse("x")
    }
}
`)
	mustWrite(t, filepath.Join(dir, "header.nomi"), `pub fn parse(text: String): Int {
    todo "parse the header"
}

pub fn size(): Int {
    [1, 2] |> Iter.count() |> dbg
}
`)
	mustWrite(t, filepath.Join(dir, "header_test.nomi"), `import header

test "size" {
    assert dbg header.size() == 2
}
`)

	if c := runProcess(t, env, dir, "", nomiBin, "check", entry); c.exit != 0 {
		t.Fatalf("nomi check rejects dbg: exit %d\n%s", c.exit, c.transcript())
	}
	r := runProcess(t, env, dir, "", nomiBin, "run", entry)
	if r.exit != 0 || !strings.HasPrefix(r.stdout, "start\n") || strings.Count(r.transcript(), "dbg line") != 2 {
		t.Fatalf("nomi run does not run both dbgs: exit %d\n%s", r.exit, r.transcript())
	}
	tr := runProcess(t, env, dir, "", nomiBin, "test", filepath.Join(dir, "header_test.nomi"))
	if tr.exit != 0 || !strings.Contains(tr.transcript(), "dbg line") {
		t.Fatalf("nomi test does not run the dbg: exit %d\n%s", tr.exit, tr.transcript())
	}

	bin := filepath.Join(t.TempDir(), "app")
	b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin)
	want := "nomi build: 1 todo and 2 dbgs remain:\n" +
		"  main.nomi:8:9 dbg header.size()\n" +
		"  header.nomi:2:5 todo \"parse the header\"\n" +
		"  header.nomi:6:31 |> dbg\n"
	if b.exit == 0 || b.stderr != want || b.stdout != "" {
		t.Fatalf("nomi build: exit %d\n%s\nwant stderr\n%s", b.exit, b.transcript(), want)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("a refused build left something at -o (%v)", err)
	}
}
