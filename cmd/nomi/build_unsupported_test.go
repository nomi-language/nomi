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
// it with a note that the page's `run` gives it its files. No WebAssembly
// module can start a process, so a build for one refuses a program that
// does, unless --allow-unsupported processes. The report's text is held by
// vmhost's TestCheckTarget_*.

const filesProgram = `import {
    std/fs
    std/io
}

fn save(text: String) -> Result<Unit, io.Error> {
    fs.write_text("notes.txt", text)
}

fn main() -> Result<Unit, io.Error> {
    text = fs.read_text("notes.txt")?
    save(text + "!")?
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
	if got.exit != 1 || !strings.Contains(got.stderr, `--allow-unsupported takes a capability class (files, processes, network, listen); got "sockets"`) {
		t.Fatalf("an unknown class: exit %d\n%s", got.exit, got.transcript())
	}
}

func TestBuildCommand_WebAssemblyRefusesAProgramThatStartsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a wasip1 runner; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	entry := filepath.Join(dir, "make.nomi")
	mustWrite(t, entry, `import {
    std/io
    std/process.{self, Command}
}

fn main() -> Result<Unit, io.Error> {
    _ = process.run(Command{program: "make"})?
    Ok(Unit)
}
`)
	out := filepath.Join(t.TempDir(), "make.wasm")
	got := runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "wasip1/wasm", "-o", out)
	if got.exit != 1 || !strings.Contains(got.stderr, "this program starts programs, which a wasip1/wasm module cannot do") {
		t.Fatalf("nomi build --target wasip1/wasm: exit %d\n%s", got.exit, got.transcript())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a refused build wrote a module")
	}
	got = runProcess(t, env, dir, "", nomiBin, "build", entry, "--target", "wasip1/wasm", "--allow-unsupported", "processes", "-o", out)
	if got.exit != 0 {
		t.Fatalf("nomi build --allow-unsupported processes: exit %d\n%s", got.exit, got.transcript())
	}
}
