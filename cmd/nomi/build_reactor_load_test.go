package main

// `nomi build-vm --reactor`: the wasip1 reactor with no program, which a
// runner compiles once and instantiates per script, handing each instance
// one .nomic file through nomi_load.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmrunner"
)

const loadCounter = `import std/io

once greeting = make_greeting()

fn make_greeting(): String {
    io.print("counter booted")
    "Hello"
}

export fn add(a: Int, b: Int): Int {
    a + b
}

export fn greet(name: String): String {
    "${greeting}, ${name}!"
}
`

const loadWords = `import std/io

fn main() {
    io.print("words main ran")
}

export fn shout(text: String): String {
    String.to_upper(text)
}
`

// wasmMemoryMinimum is the initial size in bytes the module's memory
// section declares.
func wasmMemoryMinimum(t *testing.T, data []byte) uint64 {
	t.Helper()
	sections, err := wasmSections(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sections {
		if s.id != 5 {
			continue
		}
		body := data[s.start:s.end]
		_, n := binary.Uvarint(body)
		pages, _ := binary.Uvarint(body[n+1:])
		return pages * 64 << 10
	}
	t.Fatal("the module declares no memory")
	return 0
}

// TestBuildCommand_ReactorLoadsOneProgramPerInstance builds the reactor
// with no program (`nomi build-vm --reactor`) and two programs as .nomic
// files, compiles the reactor once under node:wasi and instantiates it
// twice with no preopens, no environment and no arguments, loads a
// different program into each, and calls both. It checks the error results
// for a call and a main before any load, an empty load (which asks for a
// program built in, and this reactor has none), a corrupt payload, a payload in
// another program format, a second load, and a load into a reactor `nomi
// build` embedded a program in, and that an empty load into that one answers
// its exported functions. The reactor `--reactor` writes is the
// one `nomi build` embeds programs in, with an empty image area, and its
// declared initial memory stays under 12 MiB. With no --target it writes the
// same bytes as with --target wasip1/wasm.
func TestBuildCommand_ReactorLoadsOneProgramPerInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds the wasip1 reactor; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	nomi := func(args ...string) {
		t.Helper()
		if got := runProcess(t, env, dir, "", nomiBin, args...); got.exit != 0 {
			t.Fatalf("nomi %s: exit %d\n%s", strings.Join(args, " "), got.exit, got.transcript())
		}
	}
	mustWrite(t, filepath.Join(dir, "counter.nomi"), loadCounter)
	mustWrite(t, filepath.Join(dir, "words.nomi"), loadWords)
	mustWrite(t, filepath.Join(dir, "lib.nomi"), "export fn twice(n: Int): Int {\n    n * 2\n}\n")
	nomi("build-vm", "--reactor", "--target", "wasip1/wasm", "-o", "reactor.wasm")
	nomi("build-vm", "--reactor", "-o", "vm-reactor.wasm")
	nomi("build", "--no-vm", "counter.nomi")
	nomi("build", "--no-vm", "words.nomi")
	nomi("build", "--target", "wasip1/wasm", "lib.nomi")

	reactor, err := os.ReadFile(filepath.Join(dir, "reactor.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if vmReactor, err := os.ReadFile(filepath.Join(dir, "vm-reactor.wasm")); err != nil || !bytes.Equal(vmReactor, reactor) {
		t.Fatalf("build-vm --reactor with no --target did not write the reactor it writes with --target wasip1/wasm (%v)", err)
	}
	exports := wasmExports(t, reactor)
	for _, want := range []string{"_initialize", "memory", "nomi_alloc", "nomi_call", "nomi_free", "nomi_load", "nomi_main"} {
		if !slices.Contains(exports, want) {
			t.Fatalf("the reactor exports %v, want %s", exports, want)
		}
	}
	if slices.Contains(exports, "_start") {
		t.Fatalf("the reactor exports _start: %v", exports)
	}
	if _, err := vmrunner.ReadWasmImage(bytes.NewReader(reactor), int64(len(reactor))); !errors.Is(err, vmrunner.ErrNoImage) {
		t.Fatalf("the reactor --reactor wrote carries a program: %v", err)
	}
	if _, err := vmrunner.EmbedWasmImage(reactor, []byte("x")); err != nil {
		t.Fatalf("the reactor --reactor wrote is not the one nomi build embeds programs in: %v", err)
	}
	if got := wasmMemoryMinimum(t, reactor); got > 12<<20 {
		t.Fatalf("the reactor declares %d bytes of initial memory, over 12 MiB", got)
	}

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+ with node:wasi; not running the reactor")
	}
	script := filepath.Join(t.TempDir(), "load.mjs")
	mustWrite(t, script, loadNodeScript)
	got := runProcess(t, nil, dir, "", node, "--no-warnings", script, "reactor.wasm", "lib.wasm", "counter.nomic", "words.nomic")
	if got.exit != 0 {
		t.Fatalf("driving the reactors: exit %d\n%s", got.exit, got.transcript())
	}
	want := []string{
		"a call before load: error:this reactor has no program yet: write a compiled program (.nomic) into memory from nomi_alloc and pass it to nomi_load first",
		"main before load: 1",
		"empty load: error:this reactor has no program built in: pass it a compiled program (.nomic)",
		"not a .nomic: error:the loaded program: not a compiled Nomi program (.nomic)",
		"truncated: error:the loaded program is truncated",
		"another format: error:in program format 4294967295",
		`load counter into a: {"ok":["add(a: Int, b: Int): Int","greet(name: String): String"]}`,
		`load words into b: {"ok":["shout(text: String): String"]}`,
		`a add: {"ok":5}`,
		`b shout: {"ok":"HI"}`,
		`a greet: {"ok":"Hello, Ada!"}`,
		`a greet: {"ok":"Hello, Bo!"}`,
		"a shout: error:no exported function `shout`; the exported functions are add, greet",
		"b add: error:no exported function `add`; the exported functions are shout",
		"b main: 0",
		"a main: 1",
		"second load into a: error:this reactor already loaded a program, and a reactor runs one program for its life. To run another, instantiate the module again",
		`a add after the second load: {"ok":9}`,
		"load into an embedding reactor: error:this reactor carries the program `nomi build` embedded in it, so it loads no other. Load programs into a reactor `nomi build-vm --reactor` writes, which carries none",
		`empty load into an embedding reactor: {"ok":["twice(n: Int): Int"]}`,
		`embedded twice: {"ok":42}`,
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), got.transcript())
	}
	for i, w := range want {
		label, answer, _ := strings.Cut(lines[i], ": ")
		wantLabel, wantAnswer, _ := strings.Cut(w, ": ")
		if label != wantLabel {
			t.Fatalf("line %d is %q, want %q", i+1, lines[i], w)
		}
		if text, isErr := strings.CutPrefix(wantAnswer, "error:"); isErr {
			var result map[string]string
			if err := json.Unmarshal([]byte(answer), &result); err != nil || len(result) != 1 ||
				!strings.Contains(result["error"], text) {
				t.Fatalf("line %d is %s, want {\"error\": ...} containing %q (%v)", i+1, lines[i], text, err)
			}
			continue
		}
		if answer != wantAnswer {
			t.Fatalf("line %d is %q, want %q", i+1, lines[i], w)
		}
	}
	// Both instances write their output to the script's stderr. The
	// counter's `once` ran once, in the one instance that loaded it, across
	// its two greet calls; words' main ran when asked; and each failed main
	// said why.
	for text, n := range map[string]int{
		"counter booted\n": 1,
		"words main ran\n": 1,
		"this reactor has no program yet: write a compiled program (.nomic) into memory from nomi_alloc and pass it to nomi_load first\n": 1,
		"this program has no `fn main`; it is called through its exported functions\n":                                                    1,
	} {
		if c := strings.Count(got.stderr, text); c != n {
			t.Fatalf("stderr holds %q %d times, want %d:\n%s", text, c, n, got.stderr)
		}
	}
}

// loadNodeScript drives the reactor under node:wasi. argv: the reactor with
// no program, a reactor with an embedded program, and two .nomic files. It
// compiles the first once and instantiates it twice, each with no
// preopens, no environment and no arguments, and writes one `label:
// answer` line per step. The programs' own output goes to stderr (fd 2 for
// both), so stdout holds only the answers.
const loadNodeScript = `import { readFile } from "node:fs/promises";
import { writeSync } from "node:fs";
import { WASI } from "node:wasi";

const [reactorPath, embeddedPath, counterPath, wordsPath] = process.argv.slice(2);
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const reactor = await WebAssembly.compile(await readFile(reactorPath));

async function start(module) {
  const wasi = new WASI({ version: "preview1", args: [], env: {}, stdout: 2, returnOnExit: true });
  const instance = await WebAssembly.instantiate(module, wasi.getImportObject());
  wasi.initialize(instance);
  return instance.exports;
}

function put(x, bytes) {
  const ptr = x.nomi_alloc(bytes.length) >>> 0;
  new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
  return [ptr, bytes.length];
}

function answer(x, packed) {
  packed = BigInt.asUintN(64, packed);
  const ptr = Number(packed >> 32n), len = Number(packed & 0xffffffffn);
  const text = decoder.decode(new Uint8Array(x.memory.buffer, ptr, len).slice());
  x.nomi_free(ptr, len);
  return text;
}

function load(x, bytes) {
  const [ptr, len] = put(x, bytes);
  const result = answer(x, x.nomi_load(ptr, len));
  x.nomi_free(ptr, len);
  return result;
}

function call(x, name, args) {
  const [namePtr, nameLen] = put(x, encoder.encode(name));
  const [argsPtr, argsLen] = put(x, encoder.encode(JSON.stringify(args)));
  const result = answer(x, x.nomi_call(namePtr, nameLen, argsPtr, argsLen));
  x.nomi_free(namePtr, nameLen);
  x.nomi_free(argsPtr, argsLen);
  return result;
}

const say = (label, text) => writeSync(1, label + ": " + text + "\n");
const counter = await readFile(counterPath);
const words = await readFile(wordsPath);

const a = await start(reactor);
const b = await start(reactor);
say("a call before load", call(a, "add", [1, 2]));
say("main before load", String(a.nomi_main()));
say("empty load", load(a, new Uint8Array()));
say("not a .nomic", load(a, encoder.encode("not a program")));
say("truncated", load(a, counter.subarray(0, counter.length - 10)));
const other = Uint8Array.from(counter);
new DataView(other.buffer).setUint32(8, 0xffffffff, true);
say("another format", load(a, other));
say("load counter into a", load(a, counter));
say("load words into b", load(b, words));
say("a add", call(a, "add", [2, 3]));
say("b shout", call(b, "shout", ["hi"]));
say("a greet", call(a, "greet", ["Ada"]));
say("a greet", call(a, "greet", ["Bo"]));
say("a shout", call(a, "shout", ["hi"]));
say("b add", call(b, "add", [1, 1]));
say("b main", String(b.nomi_main()));
say("a main", String(a.nomi_main()));
say("second load into a", load(a, words));
say("a add after the second load", call(a, "add", [4, 5]));

const embedded = await start(await WebAssembly.compile(await readFile(embeddedPath)));
say("load into an embedding reactor", load(embedded, counter));
say("empty load into an embedding reactor", load(embedded, new Uint8Array()));
say("embedded twice", call(embedded, "twice", [21]));
`
