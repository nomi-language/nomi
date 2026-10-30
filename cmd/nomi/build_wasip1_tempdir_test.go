package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A wasip1 module reaches only the directories its runtime preopens. With
// none, `fs.with_temp_dir` answers Unsupported, naming why; with `/tmp`
// preopened it makes its directory there and removes it after.
func TestBuildCommand_Wasip1TempDirNeedsAPreopenedDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a wasip1 runner; -short")
	}
	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+ with node:wasi; not running the module")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	entry := filepath.Join(dir, "scratch.nomi")
	mustWrite(t, entry, `import {
    std/fs
    std/io
    std/path
}

fn main() {
    io.print(fs.temp_dir())
    made = fs.with_temp_dir(fn dir -> {
        fs.write_text(path.join(dir, "x.txt"), "x")?
        fs.read_text(path.join(dir, "x.txt"))
    })
    io.print(made)
}
`)
	mod := filepath.Join(t.TempDir(), "scratch.wasm")
	if b := runProcess(t, env, dir, "", nomiBin, "build", "--target", "wasip1/wasm", entry, "-o", mod); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}

	script := filepath.Join(t.TempDir(), "wasi.mjs")
	mustWrite(t, script, wasiNodeScript)
	bare := runNode(t, env, dir, "", node, "--no-warnings", script, mod)
	want := "/tmp\nErr(fs.with_temp_dir is not supported on wasip1/wasm with no preopened directory (TMPDIR, /tmp or the working directory))\n"
	if bare.exit != 0 || bare.stdout != want {
		t.Fatalf("with no preopens: exit %d\n%s\nwant stdout:\n%s", bare.exit, bare.transcript(), want)
	}

	host := t.TempDir()
	withTmp := strings.Replace(wasiNodeScript, "preopens: {},", "preopens: { \"/tmp\": process.env.NOMI_TEST_TMP },", 1)
	if withTmp == wasiNodeScript {
		t.Fatal("wasiNodeScript no longer sets preopens as this test expects")
	}
	script = filepath.Join(t.TempDir(), "wasi-tmp.mjs")
	mustWrite(t, script, withTmp)
	preopened := runNode(t, append(env, "NOMI_TEST_TMP="+host), dir, "", node, "--no-warnings", script, mod)
	if preopened.exit != 0 || preopened.stdout != "/tmp\nOk(x)\n" {
		t.Fatalf("with /tmp preopened: exit %d\n%s", preopened.exit, preopened.transcript())
	}
	left, err := os.ReadDir(host)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("the temporary directory was not removed: %v", left)
	}
}
