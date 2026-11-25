package main

// The reactor's nomi.host_call import: a WASI host answers a program's own
// `host fn`s, in JSON, for the keys nomi_load_with's "host_calls" lists.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostCallsApp = `import
    std::json.{FromJson, ToJson}
    helpers::util
end

struct Point
    x: Int
    y: Int
end

derive ToJson for Point
derive FromJson for Point

host fn add(a: Int, b: Int) -> Int

host fn greet(name: String) -> String

host fn mirror(p: Point) -> Point

host fn note(text: String)

export fn sum(a: Int, b: Int) -> Int
    add(a, b) + 1
end

export fn hello(name: String) -> String
    "${greet(name)} / ${util.shout(name)}"
end

export fn flip(x: Int, y: Int) -> Int
    p = mirror(Point{x:, y:})
    p.x * 10 + p.y
end

export fn told(text: String) -> String
    note(text)
    "noted"
end
`

const hostCallsUtil = `pub host fn shout(text: String) -> String
`

const hostCallsRefused = `host fn apply(n: Int, f: (Int) -> Int) -> Int

export fn twice(n: Int) -> Int
    apply(n, fn x -> x * 2)
end
`

const hostCallsResult = `host fn lookup(id: Int) -> Result<String, String>

export fn find(id: Int) -> String
    case lookup(id)
        Ok(name) -> name
        Err(why) -> "not found: ${why}"
    end
end

export fn found(id: Int) -> Result<String, String>
    lookup(id)
end
`

const hostCallsPlain = `export fn double(n: Int) -> Int
    n * 2
end
`

// TestBuildCommand_ReactorAnswersHostFunctionsThroughItsImport builds the
// reactor with no program, and programs whose `host fn`s a host answers, as
// .nomic files and one built into a reactor, and drives them under
// node:wasi with the JavaScript host tooling.md shows: host functions over
// Ints, a String, a struct, a Unit result and a sibling file's key; a host
// that throws, which fails the call with its message, after which the
// program takes more calls; a `host fn` the host does not list, which fails
// the load naming its key; one whose parameter is a function value, refused
// at the load as having no JSON form; one whose result is a Result, whose
// host answers either arm inside the envelope, and an export returning it; a
// host list ("host") that refuses one of the crossings; a program with no
// `host fn` under a host that supplies the three-line stub; the program
// built into a reactor; and nomi-wasi.js's hosts option.
func TestBuildCommand_ReactorAnswersHostFunctionsThroughItsImport(t *testing.T) {
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
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[package]\nname = \"app\"\n")
	mustWrite(t, filepath.Join(dir, "app.nomi"), hostCallsApp)
	mustWrite(t, filepath.Join(dir, "helpers", "util.nomi"), hostCallsUtil)
	mustWrite(t, filepath.Join(dir, "refused.nomi"), hostCallsRefused)
	mustWrite(t, filepath.Join(dir, "result.nomi"), hostCallsResult)
	mustWrite(t, filepath.Join(dir, "plain.nomi"), hostCallsPlain)
	nomi("build-vm", "--reactor", "-o", filepath.Join("vm", "nomi-vm-reactor.wasm"))
	nomi("build", "--no-vm", "app.nomi")
	nomi("build", "--no-vm", "refused.nomi")
	nomi("build", "--no-vm", "result.nomi")
	nomi("build", "--no-vm", "plain.nomi")
	nomi("build", "--target", "wasip1/wasm", "-o", "app.wasm", "app.nomi")

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+ with node:wasi; not running the reactor")
	}
	script := filepath.Join(t.TempDir(), "hostcalls.mjs")
	mustWrite(t, script, hostCallsNodeScript)
	got := runNode(t, os.Environ(), dir, "", node, "--no-warnings", script,
		filepath.Join("vm", "nomi-vm-reactor.wasm"), "app.nomic", "refused.nomic", "result.nomic", "plain.nomic", "app.wasm")
	if got.exit != 0 {
		t.Fatalf("driving the reactor: exit %d\n%s", got.exit, got.transcript())
	}
	const signatures = `{"ok":["sum(a: Int, b: Int) -> Int","hello(name: String) -> String","flip(x: Int, y: Int) -> Int","told(text: String) -> String"]}`
	const noForm = "; a host that answers in JSON receives the arguments and answers the result as JSON values, " +
		"so a function value, an opaque host type or a stream crosses only to a Go host (vmhost.WithHosts)"
	want := []string{
		"load: " + signatures,
		`sum: {"ok":6}`,
		`hello: {"ok":"hello, ada / ADA"}`,
		`flip: {"ok":21}`,
		`told: {"ok":"noted"}`,
		`told boom: error:line 36: the host refused boom`,
		`sum after the failure: {"ok":10}`,
		"unlisted: error:no host table of this runner answers greet, a `host fn` the program declares; " +
			`list its key in nomi_load_with's "host_calls"`,
		"none listed: error:no host table of this runner answers add, a `host fn` the program declares; " +
			`list its key in nomi_load_with's "host_calls"`,
		`a function value: error:nomi_load_with's "host_calls" lists "apply", and parameter ` + "`f` of host function `apply` " +
			"cannot cross as JSON: `(Int) -> Int` does not implement `ToJson` (`host fn apply(n: Int, f: (Int) -> Int) -> Int`)" + noForm,
		`a Result: {"ok":["find(id: Int) -> String","found(id: Int) -> Result<String, String>"]}`,
		`find Ok: {"ok":"ada"}`,
		`find Err: {"ok":"not found: no user 2"}`,
		`found Err: {"ok":{"Err":"no user 2"}}`,
		"held: error:app.nomi:16: this program has the effect host:greet, and the host allows only " +
			"host:add, host:mirror, host:note, host:helpers/util.shout",
		"held, all allowed: " + signatures,
		`stub: {"ok":["double(n: Int) -> Int"]}`,
		`stub double: {"ok":8}`,
		"built in: " + signatures,
		`built in sum: {"ok":4}`,
		`nomi-wasi hosts: "hi, bo / BO"`,
		`nomi-wasi none: no host table of this runner answers add, a ` + "`host fn`" +
			` the program declares; list its key in nomi_load_with's "host_calls"`,
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
				!strings.HasPrefix(result["error"], text) {
				t.Fatalf("line %d is %s\nwant {\"error\": ...} starting %q (%v)", i+1, lines[i], text, err)
			}
			continue
		}
		if answer != wantAnswer {
			t.Fatalf("line %d is %q, want %q", i+1, lines[i], w)
		}
	}
}

// hostCallsNodeScript drives the reactor under node:wasi. argv: the
// reactor, then app.nomic, refused.nomic, result.nomic and plain.nomic, then
// app.wasm, a
// reactor with app built in. Its start is the JavaScript host tooling.md
// shows, over a table of host functions. It writes one `label: answer`
// line per step.
const hostCallsNodeScript = `import { readFile } from "node:fs/promises";
import { writeSync } from "node:fs";
import { WASI } from "node:wasi";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const [reactorPath, appPath, refusedPath, resultPath, plainPath, builtInPath] = process.argv.slice(2);
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const reactor = await WebAssembly.compile(await readFile(reactorPath));
const builtIn = await WebAssembly.compile(await readFile(builtInPath));

const table = {
  add: (a, b) => a + b,
  greet: (name) => "hello, " + name,
  "helpers/util.shout": (text) => text.toUpperCase(),
  mirror: (p) => ({ x: p.y, y: p.x }),
  note: (text) => { if (text === "boom") throw new Error("the host refused " + text); },
};

async function start(module, hosts) {
  const wasi = new WASI({ version: "preview1", args: [], env: {}, stdout: 2, returnOnExit: true });
  let x; // the instance's exports, once it is instantiated
  const read = (ptr, len) => decoder.decode(new Uint8Array(x.memory.buffer, ptr >>> 0, len));
  const nomi = {
    host_call(keyPtr, keyLen, argsPtr, argsLen) {
      let answer;
      try {
        const args = JSON.parse(read(argsPtr, argsLen));
        answer = { ok: hosts[read(keyPtr, keyLen)](...args) ?? null };
      } catch (e) {
        answer = { error: e.message };
      }
      const bytes = encoder.encode(JSON.stringify(answer));
      const ptr = x.nomi_alloc(bytes.length) >>> 0;
      new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
      return BigInt(ptr) << 32n | BigInt(bytes.length);
    },
  };
  const instance = await WebAssembly.instantiate(module, { ...wasi.getImportObject(), nomi });
  x = instance.exports;
  wasi.initialize(instance);
  return x;
}

// stub is a host with no host functions: its import answers 0.
async function stub(module) {
  const wasi = new WASI({ version: "preview1", args: [], env: {}, stdout: 2, returnOnExit: true });
  const nomi = { host_call: () => 0n };
  const instance = await WebAssembly.instantiate(module, { ...wasi.getImportObject(), nomi });
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
  return JSON.stringify(JSON.parse(text));
}

function loadWith(x, bytes, opts) {
  const [ptr, len] = bytes.length === 0 ? [0, 0] : put(x, bytes);
  const [optsPtr, optsLen] = put(x, encoder.encode(JSON.stringify(opts)));
  return answer(x, x.nomi_load_with(ptr, len, optsPtr, optsLen));
}

function call(x, name, args) {
  const [namePtr, nameLen] = put(x, encoder.encode(name));
  const [argsPtr, argsLen] = put(x, encoder.encode(JSON.stringify(args)));
  return answer(x, x.nomi_call(namePtr, nameLen, argsPtr, argsLen));
}

const say = (label, text) => writeSync(1, label + ": " + text + "\n");
const app = await readFile(appPath);
const keys = Object.keys(table);

const a = await start(reactor, table);
say("load", loadWith(a, app, { host_calls: keys }));
say("sum", call(a, "sum", [2, 3]));
say("hello", call(a, "hello", ["ada"]));
say("flip", call(a, "flip", [1, 2]));
say("told", call(a, "told", ["fine"]));
say("told boom", call(a, "told", ["boom"]));
say("sum after the failure", call(a, "sum", [4, 5]));

say("unlisted", loadWith(await start(reactor, table), app, { host_calls: ["add", "mirror", "note", "helpers/util.shout"] }));
say("none listed", loadWith(await start(reactor, table), app, {}));
const refused = await readFile(refusedPath);
say("a function value", loadWith(await start(reactor, {}), refused, { host_calls: ["apply"] }));
const lookup = (id) => (id === 1 ? { Ok: "ada" } : { Err: "no user " + id });
const r = await start(reactor, { lookup });
say("a Result", loadWith(r, await readFile(resultPath), { host_calls: ["lookup"] }));
say("find Ok", call(r, "find", [1]));
say("find Err", call(r, "find", [2]));
say("found Err", call(r, "found", [2]));
say("held", loadWith(await start(reactor, table), app, { host_calls: keys, host: ["add", "mirror", "note", "helpers/util.shout"] }));
say("held, all allowed", loadWith(await start(reactor, table), app, { host_calls: keys, std: [], host: keys }));

const s = await stub(reactor);
say("stub", loadWith(s, await readFile(plainPath), {}));
say("stub double", call(s, "double", [4]));

const b = await start(builtIn, table);
say("built in", loadWith(b, new Uint8Array(), { host_calls: keys }));
say("built in sum", call(b, "sum", [1, 2]));

// nomi-wasi.js, beside the reactor, answers through its hosts option.
const { loadRuntime } = await import(pathToFileURL(resolve(reactorPath, "..", "nomi-wasi.js")).href);
const vm = await loadRuntime(reactorPath);
const quiet = { onStdout: () => {}, onStderr: () => {} };
const viaWasi = await vm.load(appPath, { ...quiet, hosts: { ...table, greet: (name) => "hi, " + name } });
say("nomi-wasi hosts", JSON.stringify(await viaWasi.call("hello", "bo")));
try {
  await vm.load(appPath, quiet);
  say("nomi-wasi none", "resolved");
} catch (e) {
  say("nomi-wasi none", e.message);
}
`
