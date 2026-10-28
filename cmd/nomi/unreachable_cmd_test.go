package main

import (
	"path/filepath"
	"testing"
)

// TestUnreachable_BuildAcceptsItAndReachingItTraps: `nomi build` accepts a
// program with an `unreachable`, which is finished code, unlike a `todo`.
// Reaching one stops `nomi run` and the built program alike with
// `line N: unreachable: <reason>` and status 1.
func TestUnreachable_BuildAcceptsItAndReachingItTraps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import std/io

fn position(n: Int, ns: List<Int>) -> Int {
    Some(i) = ns |> Iter.find_index(&(&1 == n)) else {
        unreachable "every number is listed"
    }
    i
}

fn main() {
    io.print("${position(2, [1, 2])}")
    io.print("${position(3, [1, 2])}")
}
`)
	const wantOut, wantErr = "1\n", "line 5: unreachable: every number is listed\n"
	if r := runProcess(t, env, dir, "", nomiBin, "run", entry); r.exit != 1 || r.stdout != wantOut || r.stderr != wantErr {
		t.Fatalf("nomi run: exit %d\n%s", r.exit, r.transcript())
	}
	bin := exe(filepath.Join(t.TempDir(), "app"))
	if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build refuses an unreachable: exit %d\n%s", b.exit, b.transcript())
	}
	if r := runProcess(t, env, dir, "", bin); r.exit != 1 || r.stdout != wantOut || r.stderr != wantErr {
		t.Fatalf("built program: exit %d\n%s", r.exit, r.transcript())
	}
}
