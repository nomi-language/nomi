package main

// nomi-wasi.js, the loader `nomi build-vm --reactor` writes beside the
// reactor, driven in Node with no node:wasi: it implements the WASI
// imports itself.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wasiGame = `import std/json.{ToJson, FromJson}

struct Game {
    word: String
    guesses: List<String>
    state: State
}

enum State {
    Playing
    Won
}

derive ToJson for Game
derive FromJson for Game
derive ToJson for State
derive FromJson for State

export fn new_game(word: String) -> Game {
    Game{word, guesses: [], state: State.Playing}
}

export fn guess(game: Game, letter: String) -> Game {
    {..game, guesses: [letter, ..game.guesses]}
}

export fn win(game: Game) -> Game {
    {..game, state: State.Won}
}

export fn divide(a: Int, b: Int) -> Int {
    a / b
}
`

const wasiGreet = `import std/io

fn main() {
    Ok(name) = io.read_line() else { return }
    io.print("Hello, ${String.trim(name)}!")
}
`

const wasiTrap = `import std/io

fn main() {
    io.print("before")
    n = 10 / String.length("")
    io.print("after ${n}")
}
`

// wasiSleep's tasks sleep 50 and 20 ms under concurrent, so the run takes at
// least 50 ms.
const wasiSleep = `import std/io
import std/tasks.Task
import std/timer
import std/duration.Duration

fn main() {
    total = concurrent {
        a = Task.spawn(fn -> {
            timer.sleep(Duration.milliseconds(50))
            1
        })
        b = Task.spawn(fn -> {
            timer.sleep(Duration.milliseconds(20))
            2
        })
        Task.await(a) + Task.await(b)
    }
    io.print("total ${total}")
}
`

// TestBuildCommand_ReactorWritesTheWasiLoader builds the reactor with no
// program into a folder of its own and checks nomi-wasi.js and
// nomi-wasi.d.ts land beside it, then imports that nomi-wasi.js in Node and
// loads, calls and runs .nomic programs through it: struct and enum round
// trips, a failed call followed by a working one, main over stdin, a trap's
// status and stderr, two programs loaded from one runtime keeping their own
// state, tasks sleeping under concurrent, and the load errors for bytes
// that are not a program and for another program format.
func TestBuildCommand_ReactorWritesTheWasiLoader(t *testing.T) {
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
	for name, src := range map[string]string{
		"game.nomi": wasiGame, "greet.nomi": wasiGreet, "trap.nomi": wasiTrap,
		"sleep.nomi": wasiSleep, "counter.nomi": loadCounter, "words.nomi": loadWords,
	} {
		mustWrite(t, filepath.Join(dir, name), src)
		nomi("build", "--no-vm", name)
	}
	// vm does not exist yet: --reactor makes the folder its three files go in.
	nomi("build-vm", "--reactor", "-o", filepath.Join("vm", "nomi-vm-reactor.wasm"))
	for _, name := range []string{"nomi-wasi.js", "nomi-wasi.d.ts"} {
		got, err := os.ReadFile(filepath.Join(dir, "vm", name))
		if err != nil {
			t.Fatalf("--reactor did not write %s beside the reactor: %v", name, err)
		}
		want := map[string][]byte{"nomi-wasi.js": wasiLoaderJS, "nomi-wasi.d.ts": wasiLoaderDTS}[name]
		if string(got) != string(want) {
			t.Fatalf("%s is not the embedded copy", name)
		}
	}

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+; not running nomi-wasi.js")
	}
	script := filepath.Join(dir, "vm", "drive.mjs")
	mustWrite(t, script, wasiLoaderScript)
	got := runProcess(t, nil, dir, "", node, "--no-warnings", script)
	if got.exit != 0 {
		t.Fatalf("driving nomi-wasi.js: exit %d\n%s", got.exit, got.transcript())
	}
	want := []string{
		`game exports: ["new_game(word: String) -> Game","guess(game: Game, letter: String) -> Game","win(game: Game) -> Game","divide(a: Int, b: Int) -> Int"]`,
		`new_game: {"word":"pipeline","guesses":[],"state":"Playing"}`,
		`guess: {"word":"pipeline","guesses":["i","e"],"state":"Playing"}`,
		`win: {"word":"pipeline","guesses":["i","e"],"state":"Won"}`,
		`divide by zero rejects: line 32: division by zero`,
		`divide after the failure: 3`,
		`an unknown function rejects: no exported function ` + "`nope`" + `; the exported functions are divide, guess, new_game, win`,
		`closed: nomi-wasi: the program is closed, so it cannot go on calling ` + "`divide`",
		`counter a: "Hello, Ada!"`,
		`counter b: "Hello, Bo!"`,
		`counter a again: "Hello, Cy!"`,
		`counter a output: "counter booted\n"`,
		`counter b output: "counter booted\n"`,
		`words: ["shout(text: String) -> String"] "HI"`,
		`words has no add: no exported function ` + "`add`" + `; the exported functions are shout`,
		`greet: {"status":0,"output":"Hello, Ada!\n","stdout":"Hello, Ada!\n","stderr":""}`,
		`greet streamed: 0 ["Hello, Bo!\n"]`,
		`trap: {"status":1,"output":"before\nline 5: division by zero\n","stdout":"before\n","stderr":"line 5: division by zero\n"}`,
		`words main: 0 "words main ran\n"`,
		`counter main: 1 "this program has no ` + "`fn main`" + `; it is called through its exported functions\n"`,
		`sleep: {"status":0,"output":"total 3\n","stdout":"total 3\n","stderr":""} waited: true`,
		`not a program: the loaded program: not a compiled Nomi program (.nomic)`,
		`another format: true`,
		`streamed runtime: ["shout(text: String) -> String"]`,
		`untyped runtime: ["shout(text: String) -> String"]`,
		`a page, not a module: nomi-wasi: the response is not a WebAssembly module (served as text/html; serve .wasm files as application/wasm)`,
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Fatalf("line %d: want\n%s\n\ngot:\n%s", i+1, w, got.transcript())
		}
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), got.transcript())
	}
}

// wasiLoaderScript runs beside the generated nomi-wasi.js, in vm/ under the
// folder of .nomic files, and writes one `label: answer` line per check.
const wasiLoaderScript = `import { loadRuntime } from "./nomi-wasi.js";
import { readFile } from "node:fs/promises";

const say = (label, text) => console.log(label + ": " + text);
const show = (v) => JSON.stringify(v);
const rejection = async (p) => { try { await p; return "resolved"; } catch (e) { return e.message; } };
const here = new URL("./", import.meta.url);
const file = (name) => new URL("../" + name, here);

// The runtime from a file: URL; programs from a file: URL, a path relative
// to the working directory, and bytes.
const vm = await loadRuntime(new URL("nomi-vm-reactor.wasm", here));

const game = await vm.load(file("game.nomic"));
say("game exports", show(game.exports));
let state = await game.call("new_game", "pipeline");
say("new_game", show(state));
state = await game.call("guess", state, "e");
state = await game.call("guess", state, "i");
say("guess", show(state));
say("win", show(await game.call("win", state)));
say("divide by zero rejects", await rejection(game.call("divide", 1, 0)));
say("divide after the failure", show(await game.call("divide", 9, 3)));
say("an unknown function rejects", await rejection(game.call("nope")));
game.close();
say("closed", await rejection(game.call("divide", 9, 3)));

const outputs = { a: "", b: "" };
const a = await vm.load("counter.nomic", { onStdout: (text) => { outputs.a += text; } });
const b = await vm.load(await readFile(file("counter.nomic")), { onStdout: (text) => { outputs.b += text; } });
say("counter a", show(await a.call("greet", "Ada")));
say("counter b", show(await b.call("greet", "Bo")));
say("counter a again", show(await a.call("greet", "Cy")));
say("counter a output", show(outputs.a));
say("counter b output", show(outputs.b));
const words = await vm.load(file("words.nomic"));
say("words", show(words.exports) + " " + show(await words.call("shout", "hi")));
say("words has no add", await rejection(words.call("add", 1, 2)));

say("greet", show(await vm.run(file("greet.nomic"), { stdin: "Ada\n" })));
const streamed = [];
const greeted = await vm.run(file("greet.nomic"), { stdin: new TextEncoder().encode("Bo\n"), onStdout: (t) => streamed.push(t) });
say("greet streamed", greeted.status + " " + show(streamed));
say("trap", show(await vm.run(file("trap.nomic"))));
let ran = await vm.run(file("words.nomic"));
say("words main", ran.status + " " + show(ran.stdout));
ran = await vm.run(file("counter.nomic"));
say("counter main", ran.status + " " + show(ran.stderr));

const start = performance.now();
const slept = await vm.run(file("sleep.nomic"));
const took = performance.now() - start;
say("sleep", show(slept) + " waited: " + (took >= 50));

say("not a program", await rejection(vm.load(new TextEncoder().encode("not a program"))));
const other = Uint8Array.from(await readFile(file("greet.nomic")));
new DataView(other.buffer).setUint32(8, 0xffffffff, true);
say("another format", (await rejection(vm.load(other))).includes("in program format 4294967295"));

// The runtime from a Response: streamed when it is application/wasm,
// compiled from its bytes otherwise, and refused naming application/wasm
// when the bytes are not a module.
const reactor = await readFile(new URL("nomi-vm-reactor.wasm", here));
const streamedVM = await loadRuntime(new Response(reactor, { headers: { "content-type": "application/wasm" } }));
say("streamed runtime", show((await streamedVM.load(file("words.nomic"))).exports));
const untyped = await loadRuntime(new Response(reactor, { headers: { "content-type": "application/octet-stream" } }));
say("untyped runtime", show((await untyped.load(new Response(await readFile(file("words.nomic"))))).exports));
// The message ends with V8's own words, which differ across versions.
say("a page, not a module", (await rejection(loadRuntime(new Response("<html>", { headers: { "content-type": "text/html" } })))).split("): ")[0] + ")");
`

// TestBuildCommand_AWasiExportBuildCarriesItsLoader builds two programs with
// an `export fn` for wasip1, one with a `main` and one without, into a
// folder, and checks nomi-wasi.js and nomi-wasi.d.ts land beside them. In
// Node it loads each module's own program with load() and run() and no
// argument: the exports and calls, main's output, and the no-main status.
// It checks the two refusals: a .nomic handed to a module with a program
// built in, and no argument to the reactor `nomi build-vm --reactor`
// writes, for load() and for run().
func TestBuildCommand_AWasiExportBuildCarriesItsLoader(t *testing.T) {
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
	mustWrite(t, filepath.Join(dir, "game.nomi"), wasiGame)
	mustWrite(t, filepath.Join(dir, "words.nomi"), loadWords)
	mustWrite(t, filepath.Join(dir, "app", "drive.mjs"), wasiEmbeddedScript)
	nomi("build", "game.nomi", "--target", "wasip1/wasm", "-o", filepath.Join("app", "game.wasm"))
	nomi("build", "words.nomi", "--target", "wasip1/wasm", "-o", filepath.Join("app", "words.wasm"))
	nomi("build", "--no-vm", "words.nomi")
	nomi("build-vm", "--reactor", "-o", filepath.Join("vm", "nomi-vm-reactor.wasm"))
	for name, want := range map[string][]byte{"nomi-wasi.js": wasiLoaderJS, "nomi-wasi.d.ts": wasiLoaderDTS} {
		got, err := os.ReadFile(filepath.Join(dir, "app", name))
		if err != nil {
			t.Fatalf("nomi build --target wasip1/wasm did not write %s beside the reactor: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s is not the embedded copy", name)
		}
	}

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+; not running nomi-wasi.js")
	}
	got := runProcess(t, nil, dir, "", node, "--no-warnings", filepath.Join(dir, "app", "drive.mjs"))
	if got.exit != 0 {
		t.Fatalf("driving nomi-wasi.js: exit %d\n%s", got.exit, got.transcript())
	}
	want := []string{
		`game exports: ["new_game(word: String) -> Game","guess(game: Game, letter: String) -> Game","win(game: Game) -> Game","divide(a: Int, b: Int) -> Int"]`,
		`guess: {"word":"pipeline","guesses":["e"],"state":"Playing"}`,
		`divide by zero rejects: line 32: division by zero`,
		`game main: 1 "this program has no ` + "`fn main`" + `; it is called through its exported functions\n"`,
		`words exports: ["shout(text: String) -> String"]`,
		`words shout: "HI"`,
		`words main: {"status":0,"output":"words main ran\n","stdout":"words main ran\n","stderr":""}`,
		`a .nomic into a built-in program: this reactor carries the program ` + "`nomi build`" + ` embedded in it, so it loads no other. Load programs into a reactor ` + "`nomi build-vm --reactor`" + ` writes, which carries none`,
		`load() on the bare reactor: this reactor has no program built in: pass it a compiled program (.nomic)`,
		`run() on the bare reactor: this reactor has no program built in: pass it a compiled program (.nomic)`,
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Fatalf("line %d: want\n%s\n\ngot:\n%s", i+1, w, got.transcript())
		}
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), got.transcript())
	}
}

// wasiEmbeddedScript runs beside the nomi-wasi.js `nomi build` wrote, in
// app/ under the folder that holds words.nomic and vm/.
const wasiEmbeddedScript = `import { loadRuntime } from "./nomi-wasi.js";

const say = (label, text) => console.log(label + ": " + text);
const show = (v) => JSON.stringify(v);
const rejection = async (p) => { try { await p; return "resolved"; } catch (e) { return e.message; } };
const here = new URL("./", import.meta.url);

const app = await loadRuntime(new URL("game.wasm", here));
const game = await app.load();
say("game exports", show(game.exports));
say("guess", show(await game.call("guess", await game.call("new_game", "pipeline"), "e")));
say("divide by zero rejects", await rejection(game.call("divide", 1, 0)));
game.close();
const ran = await app.run();
say("game main", ran.status + " " + show(ran.stderr));

const words = await loadRuntime(new URL("words.wasm", here));
const w = await words.load();
say("words exports", show(w.exports));
say("words shout", show(await w.call("shout", "hi")));
say("words main", show(await words.run()));
say("a .nomic into a built-in program", await rejection(words.load(new URL("../words.nomic", here))));

const bare = await loadRuntime(new URL("../vm/nomi-vm-reactor.wasm", here));
say("load() on the bare reactor", await rejection(bare.load()));
say("run() on the bare reactor", await rejection(bare.run()));
`
