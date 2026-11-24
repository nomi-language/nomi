// nomi-node.mjs, written by `nomi build --target js/wasm` and `nomi build-vm
// --target js/wasm` beside the module and wasm_exec.js: it runs a js/wasm
// module under Node 22 or later, with nothing else installed.
//
//   node nomi-node.mjs <module.wasm> [args...]
//
// The module gets the words after its path as its arguments, this process's
// environment, standard input and output, and Node's file system, through
// which it reads its program from its own file. Its exit status is the
// process's.
//
// wasm_exec.js writes go.argv and go.env into 8 KiB of the module's memory
// and refuses to start past that, which a shell's environment can pass. So
// the arguments and the environment go to the module as globalThis.nomiArgs
// and globalThis.nomiEnv, which the runtime reads as it starts
// (rt/jsprocess), whatever their size. go.argv holds only the module's path,
// and go.env only the variables the Go runtime reads before that, when they
// are set.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";

if (process.argv.length < 3) {
  console.error("usage: node nomi-node.mjs <module.wasm> [args...]");
  process.exit(1);
}

// wasm_exec.js reads these globals when it runs, and installs stubs that
// fail every call for the ones it finds missing.
globalThis.fs = fs;
globalThis.path = path;
await import(new URL("./wasm_exec.js", import.meta.url).href);

const go = new globalThis.Go();
const env = { TMPDIR: os.tmpdir(), ...process.env };
globalThis.nomiArgs = process.argv.slice(3);
globalThis.nomiEnv = env;
go.argv = [process.argv[2]];
go.env = Object.fromEntries(
  ["GODEBUG", "GOGC", "GOMEMLIMIT", "TMPDIR"].filter((name) => name in env).map((name) => [name, env[name]]),
);
// The module's exit sets the status and lets Node end on its own, which it
// does at once when nothing is pending. process.exit is the fallback for a
// process something still holds open (a read of standard input), a moment
// later: calling it first can hang for good, since it stops V8's platform
// threads without tearing the isolate down, and a background compile
// waiting on a garbage collection then never returns (seen on Node 24).
go.exit = (code) => {
  process.exitCode = code;
  setTimeout(() => process.exit(code), 100).unref();
};
const { instance } = await WebAssembly.instantiate(fs.readFileSync(go.argv[0]), go.importObject);
process.on("exit", (code) => {
  // Node exits when nothing is pending. A module still running then is
  // waiting on itself: resuming it once makes it report the deadlock.
  if (code === 0 && !go.exited) {
    go._pendingEvent = { id: 0 };
    go._resume();
  }
});
await go.run(instance);
