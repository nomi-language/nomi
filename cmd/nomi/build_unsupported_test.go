package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Every target has files: a js/wasm module's are in memory, so `nomi build
// --target js/wasm` builds a program that reaches std/fs, and `--web` builds
// it with a note that the page's `run` gives it its files. The refusal of a
// class a target lacks is held by vmhost's TestRefuse_NamesEachCallSite
// until a target lacks one (processes).

const filesProgram = `import {
    std/fs
    std/io
}

fn save(text: String) -> Result<Unit, io.Error> {
    fs.write_text("notes.txt", text)
}

fn main() -> Result<Unit, io.Error> {
    text = try fs.read_text("notes.txt")
    try save(text + "!")
    io.print("saved")
    Ok(Unit)
}
`

func TestBuildCommand_JsWasmBuildsAProgramThatReachesFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	entry := filepath.Join(dir, "notes.nomi")
	mustWrite(t, entry, filesProgram)
	out := filepath.Join(t.TempDir(), "notes.wasm")
	if got := runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "js/wasm", "-o", out); got.exit != 0 || got.stderr != "" {
		t.Fatalf("nomi build --target js/wasm: exit %d\n%s", got.exit, got.transcript())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the build wrote no module: %v", err)
	}

	web := filepath.Join(t.TempDir(), "notes-web")
	if got := runProcess(t, env, dir, "", nomiBin, "build", entry, "--web", "-o", web); got.exit != 0 ||
		got.stderr != vmhost.WebFilesNote+"\n" {
		t.Fatalf("nomi build --web: exit %d, stderr:\n%s\nwant the note:\n%s", got.exit, got.stderr, vmhost.WebFilesNote)
	}

	if got := runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "wasip1/wasm", "-o",
		filepath.Join(t.TempDir(), "notes.wasm")); got.exit != 0 {
		t.Fatalf("nomi build --target wasip1/wasm: exit %d\n%s", got.exit, got.transcript())
	}
}

func TestBuildCommand_AllowUnsupportedTakesAClass(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	entry := filepath.Join(dir, "notes.nomi")
	mustWrite(t, entry, filesProgram)
	out := filepath.Join(t.TempDir(), "notes.wasm")
	got := runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "js/wasm", "--allow-unsupported", "files", "-o", out)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("nomi build --allow-unsupported files: exit %d\n%s", got.exit, got.transcript())
	}
	got = runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "js/wasm", "--allow-unsupported", "sockets")
	if got.exit != 1 || !strings.Contains(got.stderr, `--allow-unsupported takes a capability class (files); got "sockets"`) {
		t.Fatalf("an unknown class: exit %d\n%s", got.exit, got.transcript())
	}
}
