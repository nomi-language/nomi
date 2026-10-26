package main

// `nomi build --target wasip1/wasm` of a project with Go bindings and an
// `export fn`: the project's own runner, linked -buildmode=c-shared as a
// WASI reactor (ffirun.ProjectRunner's reactor variant), with nomi-wasi.js
// beside it.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reactorFFIMain binds a function of the project's own Go package (hash.go)
// and one of Go's standard library, and exports functions that call them.
const reactorFFIMain = `import {
    std/io
    hash
}

gopkg "strings"

fn upper(s: String) -> String go strings.ToUpper

fn main() {
    io.print(upper("main ran"))
}

export fn shout(text: String) -> String {
    upper(text)
}

export fn digest(text: String) -> String {
    text
    |> String.to_bytes()
    |> hash.sha256()
    |> hash.to_hex()
}
`

const reactorFFIHashNomi = `gopkg "reactorgo"

pub fn sha256(data: Bytes) -> Bytes go reactorgo.SHA256

pub fn to_hex(data: Bytes) -> String go reactorgo.HexEncode
`

const reactorFFIHashGo = `package reactorgo

import (
	"crypto/sha256"
	"encoding/hex"
)

func SHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}
`

// reactorFFIScript loads the module built in next to nomi-wasi.js, lists
// its exports, calls them, and runs its main.
const reactorFFIScript = `import { loadRuntime } from "./nomi-wasi.js";

const app = await loadRuntime("./main.wasm");
const own = await app.load();
console.log("exports: " + JSON.stringify(own.exports));
console.log("shout: " + JSON.stringify(await own.call("shout", "hi")));
console.log("digest: " + JSON.stringify(await own.call("digest", "nomi")));
try {
  await own.call("nope");
} catch (e) {
  console.log("unknown rejects");
}
console.log("shout again: " + JSON.stringify(await own.call("shout", "again")));
own.close();
const { status, stdout } = await app.run();
console.log("main: " + status + " " + JSON.stringify(stdout));
`

// TestBuildCommand_Wasip1ReactorLinksGoBindings builds a project whose
// entry binds Go (the project's own package and Go's strings) and exports
// functions for wasip1. The module is a reactor, with the call ABI's
// exports and no `_start`, nomi-wasi.js and nomi-wasi.d.ts land beside it,
// and in Node nomi-wasi.js loads the program built into it, calls the
// exports, whose bodies cross into the bound Go, and runs main.
func TestBuildCommand_Wasip1ReactorLinksGoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a wasip1 reactor with Go bindings; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	mustWrite(t, filepath.Join(dir, "go.mod"), "module reactorgo\n\n"+repoGoLine(t)+"\n")
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"reactorgo\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "hash.go"), reactorFFIHashGo)
	mustWrite(t, filepath.Join(dir, "hash.nomi"), reactorFFIHashNomi)
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, reactorFFIMain)
	out := filepath.Join(dir, "main.wasm")
	if got := runProcess(t, webFFIEnv(t), dir, "", nomiBin, "build", "--target", "wasip1/wasm", entry, "-o", out); got.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", got.exit, got.transcript())
	}
	module, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	exports := wasmExports(t, module)
	for _, want := range []string{"_initialize", "memory", "nomi_alloc", "nomi_call", "nomi_free", "nomi_load", "nomi_main"} {
		if !slices.Contains(exports, want) {
			t.Fatalf("the module exports %v, want %s", exports, want)
		}
	}
	if slices.Contains(exports, "_start") {
		t.Fatalf("the module exports _start, so it is a command module: %v", exports)
	}
	for name, want := range map[string][]byte{"nomi-wasi.js": wasiLoaderJS, "nomi-wasi.d.ts": wasiLoaderDTS} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("the build did not write the embedded %s beside the module (%v)", name, err)
		}
	}

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+; not running nomi-wasi.js")
	}
	script := filepath.Join(dir, "drive.mjs")
	mustWrite(t, script, reactorFFIScript)
	got := runNode(t, os.Environ(), dir, "", node, "--no-warnings", script)
	if got.exit != 0 {
		t.Fatalf("driving the reactor: exit %d\n%s", got.exit, got.transcript())
	}
	want := strings.Join([]string{
		`exports: ["shout(text: String) -> String","digest(text: String) -> String"]`,
		`shout: "HI"`,
		`digest: "` + "0f937e60148316557dbe0dce3e862e77d4d8157bfea24ae4b4c1fa367297c2d9" + `"`,
		`unknown rejects`,
		`shout again: "AGAIN"`,
		`main: 0 "MAIN RAN\n"`,
	}, "\n") + "\n"
	if got.stdout != want {
		t.Fatalf("nomi-wasi.js over the reactor printed\n%s\nwant\n%s\nstderr:\n%s", got.stdout, want, got.stderr)
	}
}
