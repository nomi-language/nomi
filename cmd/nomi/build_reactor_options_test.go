package main

// The reactor's options: nomi_load_with's "std", "host" and "timeout_ms", and
// nomi_call_with's "timeout_ms", driven under node:wasi and through
// nomi-wasi.js.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const optionsGreeter = `import std::io

export fn hello(name: String) -> String
    io.print("hello, ${name}")
    name
end
`

const optionsEndless = `import std::duration.Duration
import std::tasks.Task
import std::timer

fn spin(n: Int) -> Int
    if n < 0 then
        n
    else
        spin(n + 1)
    end
end

fn main()
    _ = spin(0)
end

export fn forever() -> Int
    spin(0)
end

export fn sleepy() -> Int
    timer.sleep(Duration.milliseconds(3600000))
    1
end

export fn busy() -> Int
    concurrent
        a = Task.spawn(fn -> spin(0))
        b = Task.spawn(fn -> spin(0))
        Task.await(a) + Task.await(b)
    end
end

export fn add(a: Int, b: Int) -> Int
    a + b
end
`

// TestBuildCommand_ReactorLoadsAndCallsWithOptions builds the reactor with
// no program and two programs as .nomic files and drives the reactor's
// option exports under node:wasi: a load held by "std" that admits the
// program and one that refuses it, naming the call; "host" alone, which
// holds only host functions; bad options, each an
// error naming what is wrong; a call that never ends stopped by
// nomi_call_with's timeout_ms, and by nomi_load_with's as every later
// call's default, each followed by a call that runs, which shows the
// program stays loaded; and main stopped the same way. Then nomi-wasi.js
// passes std and timeoutMs through its load options. Each stop is
// asserted by its answer, not its timing.
func TestBuildCommand_ReactorLoadsAndCallsWithOptions(t *testing.T) {
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
	mustWrite(t, filepath.Join(dir, "greeter.nomi"), optionsGreeter)
	mustWrite(t, filepath.Join(dir, "endless.nomi"), optionsEndless)
	nomi("build-vm", "--reactor", "-o", filepath.Join("vm", "nomi-vm-reactor.wasm"))
	nomi("build", "--no-vm", "greeter.nomi")
	nomi("build", "--no-vm", "endless.nomi")

	node := wasiNode(t)
	if node == "" {
		t.Skip("no node 20+ with node:wasi; not running the reactor")
	}
	script := filepath.Join(t.TempDir(), "options.mjs")
	mustWrite(t, script, optionsNodeScript)
	got := runNode(t, os.Environ(), dir, "", node, "--no-warnings", script, filepath.Join("vm", "nomi-vm-reactor.wasm"), "greeter.nomic", "endless.nomic")
	if got.exit != 0 {
		t.Fatalf("driving the reactor: exit %d\n%s", got.exit, got.transcript())
	}
	want := []string{
		`allowed: {"ok":["hello(name: String) -> String"]}`,
		`allowed hello: {"ok":"Ada"}`,
		"refused: error:greeter.nomi:4: this program reaches std::io, and the host allows nothing outside the program\n" +
			"greeter.nomi:4: help: reached from `hello`\n" +
			"greeter.nomi:4: help: add \"std::io\" to the names the host passes to nomi_load_with's \"std\", or remove the call",
		`refused, then allowed: {"ok":["hello(name: String) -> String"]}`,
		`host alone: {"ok":["hello(name: String) -> String"]}`,
		`allow: error:nomi_load_with has no option "allow"; its options are "std", "host" and "timeout_ms"`,
		`not an object: error:nomi_load_with: the options must be one JSON object, such as {"std": ["std::io"], "host": ["lookup"], "timeout_ms": 500}`,
		`std not an array: error:nomi_load_with: "std" must be an array of std names, such as ["std::io"]; [] allows nothing`,
		`a go name: error:nomi_load_with's "std": "go:example.com/pkg" names a Go package; a compiled program records no Go import path`,
		`a misformed name: error:nomi_load_with's "std": "files" is not a std module`,
		`a misspelled name: error:nomi_load_with's "std" names std::fss, which is not a std module (did you mean "std::fs"?)`,
		`a host name in std: error:nomi_load_with: "std" holds std names, and host:lookup is a host function; list its key, "lookup", in "host"`,
		`a host key no table answers: error:nomi_load_with's "host" names host:lookup, which no table passed with vmrunner.WithHosts answers`,
		`timeout zero: error:nomi_load_with: "timeout_ms" must be a whole number of milliseconds above 0, such as 500, not 0`,
		`load endless: {"ok":["forever() -> Int","sleepy() -> Int","busy() -> Int","add(a: Int, b: Int) -> Int"]}`,
		`a call's std: error:nomi_call_with has no option "std"; its options are "timeout_ms"`,
		`a call's timeout not a number: error:nomi_call_with: "timeout_ms" must be a whole number of milliseconds above 0, such as 500, not "soon"`,
		"forever with timeout_ms: error:the call to `forever` was stopped after 200 ms, its timeout_ms",
		`add after the stop: {"ok":5}`,
		"sleepy with timeout_ms: error:the call to `sleepy` was stopped after 200 ms, its timeout_ms",
		"busy with timeout_ms: error:the call to `busy` was stopped after 200 ms, its timeout_ms",
		`add after the stops: {"ok":7}`,
		`load with timeout_ms: {"ok":["forever() -> Int","sleepy() -> Int","busy() -> Int","add(a: Int, b: Int) -> Int"]}`,
		"forever under the load's timeout_ms: error:the call to `forever` was stopped after 200 ms, its timeout_ms",
		`add under the load's timeout_ms: {"ok":3}`,
		"main under the load's timeout_ms: 1",
		`add after main: {"ok":9}`,
		`nomi-wasi std refused: greeter.nomi:4: this program reaches std::io, and the host allows only std::fs`,
		`nomi-wasi std admitted: "Bo"`,
		// nomi-wasi.js hands timeoutMs on as the reactor's timeout_ms.
		"nomi-wasi timeoutMs: the call to `forever` was stopped after 200 ms, its timeout_ms",
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
		if !strings.HasPrefix(answer, wantAnswer) {
			t.Fatalf("line %d is %q, want %q", i+1, lines[i], w)
		}
	}
	if !strings.Contains(got.stderr, "main was stopped after 200 ms, its timeout_ms") {
		t.Fatalf("nomi_main did not say it was stopped:\n%s", got.stderr)
	}
}

// optionsNodeScript drives the reactor under node:wasi. argv: the reactor,
// then greeter.nomic and endless.nomic. It writes one `label: answer` line
// per step, the answer's JSON on one line.
const optionsNodeScript = `import { readFile } from "node:fs/promises";
import { writeSync } from "node:fs";
import { WASI } from "node:wasi";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const [reactorPath, greeterPath, endlessPath] = process.argv.slice(2);
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const reactor = await WebAssembly.compile(await readFile(reactorPath));

async function start() {
  const wasi = new WASI({ version: "preview1", args: [], env: {}, stdout: 2, returnOnExit: true });
  const instance = await WebAssembly.instantiate(reactor, wasi.getImportObject());
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
  const [ptr, len] = put(x, bytes);
  const [optsPtr, optsLen] = put(x, encoder.encode(opts));
  const result = answer(x, x.nomi_load_with(ptr, len, optsPtr, optsLen));
  x.nomi_free(ptr, len);
  x.nomi_free(optsPtr, optsLen);
  return result;
}

function callWith(x, name, args, opts) {
  const [namePtr, nameLen] = put(x, encoder.encode(name));
  const [argsPtr, argsLen] = put(x, encoder.encode(JSON.stringify(args)));
  let result;
  if (opts === undefined) {
    result = answer(x, x.nomi_call(namePtr, nameLen, argsPtr, argsLen));
  } else {
    const [optsPtr, optsLen] = put(x, encoder.encode(opts));
    result = answer(x, x.nomi_call_with(namePtr, nameLen, argsPtr, argsLen, optsPtr, optsLen));
    x.nomi_free(optsPtr, optsLen);
  }
  x.nomi_free(namePtr, nameLen);
  x.nomi_free(argsPtr, argsLen);
  return result;
}

const say = (label, text) => writeSync(1, label + ": " + text + "\n");
const greeter = await readFile(greeterPath);
const endless = await readFile(endlessPath);

const a = await start();
say("allowed", loadWith(a, greeter, JSON.stringify({ std: ["std::io"] })));
say("allowed hello", callWith(a, "hello", ["Ada"]));

const b = await start();
say("refused", loadWith(b, greeter, JSON.stringify({ std: [] })));
say("refused, then allowed", loadWith(b, greeter, JSON.stringify({ std: ["std::io"] })));

const h = await start();
say("host alone", loadWith(h, greeter, JSON.stringify({ host: [] })));

const c = await start();
say("allow", loadWith(c, greeter, JSON.stringify({ allow: [] })));
say("not an object", loadWith(c, greeter, "[]"));
say("std not an array", loadWith(c, greeter, JSON.stringify({ std: "std::io" })));
say("a go name", loadWith(c, greeter, JSON.stringify({ std: ["go:example.com/pkg"] })));
say("a misformed name", loadWith(c, greeter, JSON.stringify({ std: ["files"] })));
say("a misspelled name", loadWith(c, greeter, JSON.stringify({ std: ["std::fss"] })));
say("a host name in std", loadWith(c, greeter, JSON.stringify({ std: ["host:lookup"] })));
say("a host key no table answers", loadWith(c, greeter, JSON.stringify({ std: ["std::io"], host: ["lookup"] })));
say("timeout zero", loadWith(c, greeter, JSON.stringify({ timeout_ms: 0 })));
say("load endless", loadWith(c, endless, "{}"));
say("a call's std", callWith(c, "add", [1, 1], JSON.stringify({ std: [] })));
say("a call's timeout not a number", callWith(c, "add", [1, 1], JSON.stringify({ timeout_ms: "soon" })));
say("forever with timeout_ms", callWith(c, "forever", [], JSON.stringify({ timeout_ms: 200 })));
say("add after the stop", callWith(c, "add", [2, 3]));
say("sleepy with timeout_ms", callWith(c, "sleepy", [], JSON.stringify({ timeout_ms: 200 })));
say("busy with timeout_ms", callWith(c, "busy", [], JSON.stringify({ timeout_ms: 200 })));
say("add after the stops", callWith(c, "add", [3, 4]));

const d = await start();
say("load with timeout_ms", loadWith(d, endless, JSON.stringify({ timeout_ms: 200 })));
say("forever under the load's timeout_ms", callWith(d, "forever", []));
say("add under the load's timeout_ms", callWith(d, "add", [1, 2]));
say("main under the load's timeout_ms", String(d.nomi_main()));
say("add after main", callWith(d, "add", [4, 5]));

// nomi-wasi.js, beside the reactor, passes std and timeoutMs on.
const { loadRuntime } = await import(pathToFileURL(resolve(reactorPath, "..", "nomi-wasi.js")).href);
const vm = await loadRuntime(reactorPath);
const quiet = { onStdout: () => {}, onStderr: () => {} };
try {
  await vm.load(greeterPath, { ...quiet, std: ["std::fs"] });
  say("nomi-wasi std refused", "resolved");
} catch (e) {
  say("nomi-wasi std refused", e.message.split("\n")[0]);
}
const admitted = await vm.load(greeterPath, { ...quiet, std: ["std::io"] });
say("nomi-wasi std admitted", JSON.stringify(await admitted.call("hello", "Bo")));
const bounded = await vm.load(endlessPath, { ...quiet, timeoutMs: 200 });
try {
  await bounded.call("forever");
  say("nomi-wasi timeoutMs", "resolved");
} catch (e) {
  say("nomi-wasi timeoutMs", e.message);
}
`
