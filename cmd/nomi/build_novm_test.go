package main

// `nomi build --no-vm` and `nomi build-vm`: a compiled program (.nomic), run
// by a nomi-vm that does not embed it, by path or framed on stdin
// (vmrunner/imagefile.go, vmrunner/bare.go), and by `nomi run`.

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/vmrunner"
)

var imagePrograms = map[string]string{
	"echo.nomi": `import std/io

struct App {
  context: Context
}

fn boot(startup: Startup) -> App {
  io.print("args: ${startup.args}")
  #App{context: Context.root()}
}

fn main() {
  case io.read_line() {
    Ok(line) -> io.print("read: ${line}")
    Err(e) -> io.print("no line: ${e}")
  }
}
`,
	"trap.nomi": `import std/io

fn main() {
  io.print("dividing")
  zero = String.length("")
  io.print("${100 / zero}")
}
`,
}

// wasiImageScript runs a wasip1 runner under Node's node:wasi: with the root
// preopened and the module's path as argv[0], or, after --no-fs, with no
// preopen at all, as a browser's WASI shim runs it.
const wasiImageScript = `import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { WASI } from "node:wasi";

const path = resolve(process.argv[2]);
let rest = process.argv.slice(3);
const noFs = rest[0] === "--no-fs";
if (noFs) rest = rest.slice(1);
const wasi = new WASI({
  version: "preview1",
  args: noFs ? ["nomi-vm", ...rest] : [path, ...rest],
  env: noFs ? {} : { PWD: process.cwd() },
  preopens: noFs ? {} : { "/": "/" },
  returnOnExit: true,
});
const module = await WebAssembly.compile(await readFile(path));
const instance = await WebAssembly.instantiate(module, wasi.getImportObject());
process.exitCode = wasi.start(instance);
`

// TestBuildNoVM_TheRunnerRunsItAsNomiRunDoes builds each program's image and
// a bare runner, and runs the image by path, framed on stdin, and with `nomi
// run`: stdout, stderr and exit status must equal `nomi run`'s of the source
// over the same stdin and arguments. With node on PATH, a wasip1 runner runs
// it under node:wasi both ways.
func TestBuildNoVM_TheRunnerRunsItAsNomiRunDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds runners; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	for name, src := range imagePrograms {
		mustWrite(t, filepath.Join(dir, name), src)
	}
	runner := filepath.Join(t.TempDir(), "nomi-vm")
	if b := runProcess(t, env, dir, "", nomiBin, "build-vm", "-o", runner); b.exit != 0 {
		t.Fatalf("nomi build-vm: exit %d\n%s", b.exit, b.transcript())
	}
	node, nodeErr := exec.LookPath("node")
	wasm := ""
	script := filepath.Join(t.TempDir(), "wasi.mjs")
	if nodeErr == nil && nodeMajor(t, node) >= 20 {
		wasm = filepath.Join(t.TempDir(), "nomi-vm.wasm")
		if b := runProcess(t, env, dir, "", nomiBin, "build-vm", "--target", "wasip1/wasm", "-o", wasm); b.exit != 0 {
			t.Fatalf("nomi build-vm --target wasip1/wasm: exit %d\n%s", b.exit, b.transcript())
		}
		mustWrite(t, script, wasiImageScript)
	} else {
		t.Log("no node 20 or later on PATH; not running the wasip1 runner")
	}
	args := []string{"first", "two words"}
	const input = "a line\n"
	for name := range imagePrograms {
		base := strings.TrimSuffix(name, ".nomi")
		// The default output is the entry's name with .nomic, in the
		// working directory.
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--no-vm", name); b.exit != 0 {
			t.Fatalf("nomi build --no-vm %s: exit %d\n%s", name, b.exit, b.transcript())
		}
		imagePath := filepath.Join(dir, base+".nomic")
		data, err := os.ReadFile(imagePath)
		if err != nil {
			t.Fatal(err)
		}
		if !vmrunner.IsImageFile(data) {
			t.Fatalf("%s does not open with %q", imagePath, vmrunner.ImageFileMagic)
		}
		want := runProcess(t, env, dir, input, nomiBin, append([]string{"run", name}, args...)...)
		runs := map[string]observedRun{
			"by path":  runProcess(t, env, dir, input, runner, append([]string{imagePath}, args...)...),
			"on stdin": runProcess(t, env, dir, string(data)+input, runner, append([]string{"-"}, args...)...),
			"nomi run": runProcess(t, env, dir, input, nomiBin, append([]string{"run", imagePath}, args...)...),
		}
		if wasm != "" {
			runs["wasip1 by path"] = runProcess(t, env, dir, input, node, append([]string{"--no-warnings", script, wasm, imagePath}, args...)...)
			runs["wasip1 on stdin"] = runProcess(t, env, dir, string(data)+input, node, append([]string{"--no-warnings", script, wasm, "--no-fs", "-"}, args...)...)
		}
		for how, got := range runs {
			if got != want {
				t.Errorf("%s %s differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- %s (exit %d) ---\n%s",
					name, how, want.exit, want.transcript(), how, got.exit, got.transcript())
			}
		}
		switch name {
		case "echo.nomi":
			if want.exit != 0 || want.stdout != "args: [first, two words]\nread: a line\n" {
				t.Fatalf("echo ran as %q, exit %d; this test compares the wrong thing", want.transcript(), want.exit)
			}
		case "trap.nomi":
			if want.exit != 1 || !strings.Contains(want.stderr, "division by zero") {
				t.Fatalf("trap ran as %q, exit %d; this test compares the wrong thing", want.transcript(), want.exit)
			}
		}
	}

	// Given no path and no -, the runner reads the image from stdin and the
	// program gets no arguments.
	data, err := os.ReadFile(filepath.Join(dir, "echo.nomic"))
	if err != nil {
		t.Fatal(err)
	}
	if got := runProcess(t, env, dir, string(data)+"piped\n", runner); got.exit != 0 || got.stdout != "args: []\nread: piped\n" {
		t.Fatalf("a bare runner over a framed stdin: exit %d\n%s", got.exit, got.transcript())
	}
	// A runner nothing was appended to and nothing was handed says how to
	// hand it a program.
	if got := runProcess(t, env, dir, "", runner); got.exit != 1 || !strings.Contains(got.stderr, "usage: nomi-vm <program.nomic>") {
		t.Fatalf("a bare runner over an empty stdin: exit %d\n%s", got.exit, got.transcript())
	}
	if got := runProcess(t, env, dir, "", runner, "--version"); got.exit != 0 ||
		!strings.Contains(got.stdout, fmt.Sprintf("(program format %d)", ir.FormatVersion)) {
		t.Fatalf("nomi-vm --version: exit %d\n%s", got.exit, got.transcript())
	}

	// An image in another format is refused, naming both versions.
	other := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(other[8:12], ir.FormatVersion+1)
	otherPath := filepath.Join(t.TempDir(), "other.nomic")
	if err := os.WriteFile(otherPath, other, 0o644); err != nil {
		t.Fatal(err)
	}
	wantMismatch := fmt.Sprintf("in program format %d, and this nomi-vm (nomi ", ir.FormatVersion+1)
	for _, got := range []observedRun{
		runProcess(t, env, dir, "", runner, otherPath),
		runProcess(t, env, dir, string(other), runner),
		runProcess(t, env, dir, "", nomiBin, "run", otherPath),
	} {
		if got.exit != 1 || got.stdout != "" || !strings.Contains(got.stderr, wantMismatch) ||
			!strings.Contains(got.stderr, fmt.Sprintf("reads format %d", ir.FormatVersion)) {
			t.Fatalf("an image in format %d: exit %d\n%s", ir.FormatVersion+1, got.exit, got.transcript())
		}
	}
}

// An executable with its program appended runs it as before: its arguments
// are the program's, even one spelled as the bare runner's flag.
func TestBuildNoVM_AnEmbeddedProgramStillWins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "echo.nomi"), imagePrograms["echo.nomi"])
	bin := filepath.Join(t.TempDir(), "echo")
	if b := runProcess(t, env, dir, "", nomiBin, "build", "echo.nomi", "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}
	got := runProcess(t, env, dir, "typed\n", bin, "--version", "-")
	if got.exit != 0 || got.stdout != "args: [--version, -]\nread: typed\n" {
		t.Fatalf("exit %d\n%s", got.exit, got.transcript())
	}
}

func TestBuildNoVM_Refusals(t *testing.T) {
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "echo.nomi"), imagePrograms["echo.nomi"])
	project := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "go_bindings", "main.nomi")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"build", "--no-vm", project, "-o", "ffi.nomic"}, "nomi build --no-vm: this project has Go bindings"},
		{[]string{"build", "--no-vm", "--target", "linux/amd64", "echo.nomi"}, "a compiled program runs on every target's nomi-vm, so it takes no --target"},
		{[]string{"build", "--no-vm", "--web", "echo.nomi"}, "--no-vm and --web are different outputs"},
		{[]string{"build", "--compiler", "echo.nomi"}, "nomi build: unknown flag \"--compiler\""},
		{[]string{"build", "--vm"}, "nomi build: unknown flag \"--vm\""},
		{[]string{"build", "echo.nomi", "--vm"}, "nomi build: unknown flag \"--vm\""},
		{[]string{"build", "--reactor"}, "nomi build: unknown flag \"--reactor\""},
		{[]string{"build", "echo.nomi", "--reactor"}, "nomi build: unknown flag \"--reactor\""},
		{[]string{"build"}, buildUsage},
		{[]string{"build-vm", "echo.nomi"}, "nomi build-vm: the VM carries no program, so it takes no file (echo.nomi). " +
			"nomi build echo.nomi writes the program with the VM"},
		{[]string{"build-vm", "--web"}, "nomi build-vm: unknown flag \"--web\""},
		{[]string{"build-vm", "--no-vm"}, "nomi build-vm: unknown flag \"--no-vm\""},
		{[]string{"build-vm", "echo.nomi", "--reactor"}, "nomi build-vm: the reactor carries no program, so it takes no file (echo.nomi). " +
			"A program with an `export fn` already builds a reactor: nomi build echo.nomi --target wasip1/wasm"},
		{[]string{"build-vm", "--reactor", "--no-vm"}, "nomi build-vm: unknown flag \"--no-vm\""},
		{[]string{"build-vm", "--reactor", "--web"}, "nomi build-vm: unknown flag \"--web\""},
		{[]string{"build-vm", "--reactor", "--target", "js/wasm"}, "nomi build-vm --reactor: the reactor is a WASI module, so it builds for wasip1/wasm, not js/wasm"},
		{[]string{"build-vm", "--reactor", "--target", "linux/amd64"}, "so it builds for wasip1/wasm, not linux/amd64"},
		{[]string{"build-vm", "--target", "linux"}, "nomi build-vm: --target takes <goos>/<goarch>"},
		{[]string{"build-vm", "-o"}, "nomi build-vm: -o needs a value\n" + buildVMUsage},
	} {
		got := runProcess(t, env, dir, "", nomiBin, c.args...)
		if got.exit == 0 || !strings.Contains(got.stderr, c.want) {
			t.Fatalf("nomi %v: exit %d, want %q:\n%s", c.args, got.exit, c.want, got.transcript())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a refused build wrote into %s: %d entries", dir, len(entries))
	}
}
