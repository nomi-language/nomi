package main

// `nomi build --target wasip1/wasm` of a program with an `export fn`: a WASI
// reactor (cmd/nomi-vm-reactor) a runner drives through nomi_alloc,
// nomi_call, nomi_free and nomi_main. A program with none stays a command
// module with `_start`.

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const reactorProgram = `import std/io
import std/json.{ToJson, FromJson}

struct Point {
    x: Int
    y: Int
}

derive ToJson for Point
derive FromJson for Point

enum Shape {
    Circle {radius: Int}
    Dot
}

derive ToJson for Shape
derive FromJson for Shape

export fn moved(p: Point, dx: Int) -> Point {
    #{..p, x: p.x + dx}
}

export fn grow(s: Shape) -> Shape {
    case s {
        #Shape.Circle{radius} -> #Shape.Circle{radius: radius * 2}
        Shape.Dot -> Shape.Dot
    }
}

export fn divide(a: Int, b: Int) -> Int {
    a / b
}

export fn shout(text: String) {
    io.print(String.to_upper(text))
}
`

const reactorWithMain = `import std/io

fn main() {
    io.print("main ran")
}

export fn twice(n: Int) -> Int {
    n * 2
}
`

const commandOnly = `import std/io

fn main() {
    io.print("command")
}
`

// wasmExports lists the names a module's export section exports.
func wasmExports(t *testing.T, data []byte) []string {
	t.Helper()
	sections, err := wasmSections(data)
	if err != nil {
		t.Fatalf("not a valid module: %v", err)
	}
	var names []string
	for _, s := range sections {
		if s.id != 7 {
			continue
		}
		b := data[s.start:s.end]
		count, n := binary.Uvarint(b)
		b = b[n:]
		for range count {
			nameLen, n := binary.Uvarint(b)
			b = b[n:]
			names = append(names, string(b[:nameLen]))
			b = b[nameLen+1:] // the name, then the export's kind
			_, n = binary.Uvarint(b)
			b = b[n:]
		}
	}
	return names
}

// TestBuildCommand_Wasip1ReactorServesExportedFunctions builds a program
// with exported functions for wasip1, requires the reactor's exports and no
// `_start`, and drives it under node:wasi as a runner would: `_initialize`,
// the name and arguments written into nomi_alloc buffers, nomi_call's i64
// unpacked, the result read and freed. A trap and an unknown name answer
// `{"error": ...}` and the next call succeeds. nomi_main runs a program's
// main over WASI stdout. A program with no `export fn` is still a command
// module.
func TestBuildCommand_Wasip1ReactorServesExportedFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds the wasip1 runners; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	build := func(name, src string) string {
		t.Helper()
		entry := filepath.Join(dir, name+".nomi")
		mustWrite(t, entry, src)
		mod := filepath.Join(dir, name+".wasm")
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--target", "wasip1/wasm", entry, "-o", mod); b.exit != 0 {
			t.Fatalf("nomi build --target wasip1/wasm %s: exit %d\n%s", name, b.exit, b.transcript())
		}
		return mod
	}
	reactor := build("shapes", reactorProgram)
	withMain := build("withmain", reactorWithMain)
	command := build("command", commandOnly)

	for _, mod := range []string{reactor, withMain} {
		data, err := os.ReadFile(mod)
		if err != nil {
			t.Fatal(err)
		}
		exports := wasmExports(t, data)
		for _, want := range []string{"_initialize", "nomi_alloc", "nomi_call", "nomi_free", "nomi_main", "memory"} {
			if !slices.Contains(exports, want) {
				t.Fatalf("%s exports %v, want %s", filepath.Base(mod), exports, want)
			}
		}
		if slices.Contains(exports, "_start") {
			t.Fatalf("the reactor %s exports _start: %v", filepath.Base(mod), exports)
		}
	}
	data, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if exports := wasmExports(t, data); !slices.Contains(exports, "_start") || slices.Contains(exports, "nomi_call") ||
		slices.Contains(exports, "_initialize") {
		t.Fatalf("a program with no export fn built a module exporting %v, want a command module", exports)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH; not running the modules")
	}
	if major := nodeMajor(t, node); major < 20 {
		t.Skipf("node %d has no node:wasi preview1; not running the modules", major)
	}
	script := filepath.Join(t.TempDir(), "reactor.mjs")
	mustWrite(t, script, reactorNodeScript)
	drive := func(mod, mode string, calls ...[2]string) observedRun {
		t.Helper()
		args := []string{"--no-warnings", script, mod, mode}
		for _, c := range calls {
			line, _ := json.Marshal(c)
			args = append(args, string(line))
		}
		return runNode(t, env, dir, "", node, args...)
	}

	got := drive(reactor, "call",
		[2]string{"moved", `[{"x": 1, "y": 2}, 3]`},
		[2]string{"grow", `[{"Circle": {"radius": 4}}]`},
		[2]string{"grow", `["Dot"]`},
		[2]string{"divide", `[1, 0]`},
		[2]string{"divide", `[7, 2]`},
		[2]string{"nope", `[]`},
		[2]string{"shout", `["hi"]`},
		[2]string{"moved", `[{"x": 0, "y": 0}, -1]`},
	)
	if got.exit != 0 || got.stderr != "" {
		t.Fatalf("driving the reactor: exit %d\n%s", got.exit, got.transcript())
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	want := []string{
		`{"ok":{"x":4,"y":2}}`,
		`{"ok":{"Circle":{"radius":8}}}`,
		`{"ok":"Dot"}`,
		"error:division by zero",
		`{"ok":3}`,
		"error:no exported function `nope`; the exported functions are divide, grow, moved, shout",
		"HI",
		`{"ok":null}`,
		`{"ok":{"x":-1,"y":0}}`,
	}
	if len(lines) != len(want) {
		t.Fatalf("the reactor answered %d lines, want %d:\n%s", len(lines), len(want), got.stdout)
	}
	for i, w := range want {
		line := lines[i]
		if text, isErr := strings.CutPrefix(w, "error:"); isErr {
			var answer map[string]string
			if err := json.Unmarshal([]byte(line), &answer); err != nil || len(answer) != 1 ||
				!strings.Contains(answer["error"], text) {
				t.Fatalf("line %d is %s, want {\"error\": ...} containing %q (%v)", i+1, line, text, err)
			}
			continue
		}
		if line != w {
			t.Fatalf("line %d is %s, want %s\n%s", i+1, line, w, got.stdout)
		}
	}

	if main := drive(withMain, "main"); main.exit != 0 || main.stdout != "main ran\n" || main.stderr != "" {
		t.Fatalf("nomi_main: exit %d\n%s", main.exit, main.transcript())
	}
	twice := drive(withMain, "call", [2]string{"twice", "[21]"})
	if twice.exit != 0 || twice.stdout != "{\"ok\":42}\n" {
		t.Fatalf("a reactor with main does not run main before a call: exit %d\n%s", twice.exit, twice.transcript())
	}
	noMain := drive(reactor, "main")
	if noMain.exit != 1 || !strings.Contains(noMain.stderr, "this program has no `fn main`") {
		t.Fatalf("nomi_main of a program with no main: exit %d\n%s", noMain.exit, noMain.transcript())
	}
}

// TestBuildCommand_Wasip1ReactorRefusesGoBindings: a project with Go
// bindings and an `export fn` would be a reactor, which links no project's
// bindings, so its wasip1 build is refused and writes nothing.
func TestBuildCommand_Wasip1ReactorRefusesGoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds an FFI wrapper; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "strings" as strings

fn upper(s: String) -> String go strings.ToUpper

fn main() {
    io.print(upper("hi"))
}

export fn shout(s: String) -> String {
    upper(s)
}
`)
	out := filepath.Join(dir, "main.wasm")
	got := runProcess(t, buildEnv(t.TempDir()), dir, "", nomiBin, "build", "--target", "wasip1/wasm", entry, "-o", out)
	if got.exit == 0 || !strings.Contains(got.stderr, "a reactor cannot link a project's Go bindings yet") {
		t.Fatalf("exit %d:\n%s", got.exit, got.transcript())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused build wrote %s (%v)", out, err)
	}
}

// reactorNodeScript drives a reactor under node:wasi. argv: the module,
// then "main" to call nomi_main and exit with its status, or "call" and one
// JSON [name, argsJSON] per call, each answer written as a line. It runs
// the module as a host with no file system does: no preopens, no
// environment, and an argv[0] naming no file, so the reactor reads its
// program from its own memory.
const reactorNodeScript = `import { readFile } from "node:fs/promises";
import { writeSync } from "node:fs";
import { resolve } from "node:path";
import { WASI } from "node:wasi";

const path = resolve(process.argv[2]);
const wasi = new WASI({
  version: "preview1",
  args: ["/no/such/dir/app.wasm"],
  env: {},
  preopens: {},
  returnOnExit: true,
});
const module = await WebAssembly.compile(await readFile(path));
const instance = await WebAssembly.instantiate(module, wasi.getImportObject());
wasi.initialize(instance);
const x = instance.exports;
const encoder = new TextEncoder();
const decoder = new TextDecoder();

function put(text) {
  const bytes = encoder.encode(text);
  const ptr = x.nomi_alloc(bytes.length) >>> 0;
  new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
  return [ptr, bytes.length];
}

function call(name, args) {
  const [namePtr, nameLen] = put(name);
  const [argsPtr, argsLen] = put(args);
  const packed = BigInt.asUintN(64, x.nomi_call(namePtr, nameLen, argsPtr, argsLen));
  x.nomi_free(namePtr, nameLen);
  x.nomi_free(argsPtr, argsLen);
  const ptr = Number(packed >> 32n);
  const len = Number(packed & 0xffffffffn);
  const text = decoder.decode(new Uint8Array(x.memory.buffer, ptr, len).slice());
  x.nomi_free(ptr, len);
  return text;
}

if (process.argv[3] === "main") {
  process.exitCode = x.nomi_main();
} else {
  for (const line of process.argv.slice(4)) {
    const [name, args] = JSON.parse(line);
    writeSync(1, call(name, args) + "\n");
  }
}
`
