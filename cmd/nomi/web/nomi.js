// nomi.js loads and runs a Nomi program built by `nomi build --web`.
//
//   import { run } from "./nomi.js";
//   const { exitCode, fatal, files } = await run("./app.wasm", {
//     args: [],                 // Startup.args
//     env: {},                  // Startup.env; NOMI_COLOR=never unless set here
//     stdout: (text) => {},     // text as written; default console.log per line
//     stderr: (text) => {},     // default console.error per line
//     stdin: "all of it\n",     // or async () => string | null; default: end of input
//     timeoutMs: 10_000,        // stop the program after this long (exit 124)
//     signal,                   // an AbortSignal that stops it (exit 130)
//     worker: true,             // false runs on the page's own thread
//     locales: "./locales/",    // where Locale.load fetches <tag>.nomil; default beside app.wasm
//     files: { "data/prices.csv": csvText },  // the files std/fs sees: string or Uint8Array
//     setup: "./setup.mjs",     // a module whose default export runs, awaited, before the program
//   });
//   // files: every file in the program's file system when it ended, by
//   // path, as Uint8Array
//
// The program's file system is in memory. It starts with the `files` the
// run is given, by path (relative to the program's working directory,
// /work), and run answers every file in it when the program ends.
//
// A program with exported functions (`export fn`) is loaded once and called
// any number of times:
//
//   import { load } from "./nomi.js";
//   const nomi = await load("./app.wasm", { stdout, stderr, args, env });
//   let game = await nomi.call("new_game", "pipeline");  // values cross as JSON
//   game = await nomi.call("guess", game, "e");
//   await nomi.exports();     // [{ name, params: [{ name, type }], result, signature }]
//   nomi.close();             // stops its worker
//
// load starts the program in its own worker, runs its main first when it
// has one (its output goes to stdout like run's), and resolves when the
// functions can be called. Calls run one at a time, in the order they are
// made. A call that fails (a trap, an argument its parameter's FromJson
// refuses) rejects with an Error whose message is the Nomi diagnostic, and
// the program stays loaded. nomi.d.ts beside this file types each call.
//
// `nomi build --web` writes this file beside app.wasm as one ES module: Go's
// wasm_exec.js from the toolchain that built app.wasm, unchanged, as the body
// of installGo above, then this code. The same module is the page's loader
// and the module worker each run executes in: run starts
// `new Worker(new URL(import.meta.url), { type: "module" })`, and the copy
// that finds itself in a worker serves the page. installGo runs only where a
// program runs (in the worker, or on the page for `worker: false`), so the
// page's globals stay its own.
//
// Each run gets a fresh instance in its own worker, so a runaway program can
// be stopped and the page stays responsive. A module is fetched and compiled
// once per URL and shared by every later run.
//
// Imported by a page with an element carrying `data-nomi-program="app.wasm"`,
// it mounts a terminal on that element and runs the program: this is what
// the index.html beside it does, with no inline script.
//
// setup (run's and load's) is for what a program's Go bindings need from
// JavaScript before it starts: the module is imported where the program
// runs (its worker, or the page for `worker: false`) and its default export
// awaited, so a binding that reaches a library through globalThis finds it
// there. The sqlite driver's browser build is one: its setup module loads
// SQLite's WebAssembly build,
//
//   import sqlite3InitModule from "./sqlite3.mjs";
//   export default async () => { globalThis.sqlite3 = await sqlite3InitModule(); };
//
// A setup that throws ends the run before the program starts, as a fatal
// naming the module.
//
// A program that lists its locales carries only `[i18n].web_embedded`'s in
// app.wasm; its Locale.load fetches each other one's locales/<tag>.nomil
// through globalThis.nomiLocale, which runProgram sets (vmrunner/locales_js.go).
//
// Protocol between the page and a worker:
//   in:  { type: "start", module, image, url, locales, args, env, stdinKind, stdinText, files, mode? }
//        { type: "line", line }          // answer to "read": a string, or null for end of input
//        { type: "call", id, name, args } // mode "call" only: args is the JSON text of the argument array
//   out: { type: "output", fd, text }    // fd 1 is stdout, fd 2 stderr
//        { type: "read" }                // the program reads stdin and wants a line
//        { type: "exit", code, fatal?, files? } // the program ended; files when it returned them
//        { type: "ready", exports }      // mode "call": main ran and calls can start
//        { type: "failed", message }     // mode "call": the program could not start (main failed)
//        { type: "result", id, value }   // a call's result as JSON text
//        { type: "result", id, error }   // a call failed: the Nomi diagnostic
//
// In mode "call" the worker sets globalThis.nomiServe before the program
// runs; the runner calls it with the exported functions and a call function
// once main has run (vmrunner/calls_js.go), and keeps running.
//
// wasm_exec.js reaches `fs` and the other host objects as free names, which
// resolve to globalThis's because this module declares none of them at top
// level. Keep it that way: a top-level `fs`, `process`, `path`, `crypto` or
// `Go` here would shadow the global inside installGo.

const SECTION = "nomi.image";

// ---- running a program (in a worker, or on the page for worker: false) ----

// Go's wasm_exec.js sends every write to fd 1 and 2 through
// globalThis.fs.writeSync (the runtime's own writes) or globalThis.fs.write
// (os.Stdout and os.Stderr), and reads fd 0 through globalThis.fs.read, which
// answers through a callback, so a read can wait for the page. The `fs`
// installed here answers those and refuses everything else, as wasm_exec.js's
// own shim does; the runner refuses file access before it gets here
// (internal/stdio/file_browser.go).

const enosys = () => {
  const err = new Error("not implemented");
  err.code = "ENOSYS";
  return err;
};

// The run this thread is serving, or null. A worker serves one run; the
// page's own thread serves one at a time.
let current = null;

function installFS() {
  if (globalThis.fs && globalThis.fs.nomi) return;
  const refuse = (...args) => args[args.length - 1](enosys());
  globalThis.fs = {
    nomi: true,
    constants: { O_WRONLY: -1, O_RDWR: -1, O_CREAT: -1, O_TRUNC: -1, O_APPEND: -1, O_EXCL: -1, O_DIRECTORY: -1 },
    writeSync(fd, buf) {
      if (current && (fd === 1 || fd === 2)) current.write(fd, buf);
      return buf.length;
    },
    write(fd, buf, offset, length, position, callback) {
      if (offset !== 0 || length !== buf.length || position !== null) {
        callback(enosys());
        return;
      }
      callback(null, this.writeSync(fd, buf));
    },
    read(fd, buffer, offset, length, position, callback) {
      if (fd !== 0 || position !== null || !current) {
        callback(enosys());
        return;
      }
      current.read(buffer, offset, length, callback);
    },
    fsync(fd, callback) {
      callback(null);
    },
    chmod: refuse, chown: refuse, close: refuse, fchmod: refuse, fchown: refuse, fstat: refuse,
    ftruncate: refuse, lchown: refuse, link: refuse, lstat: refuse, mkdir: refuse, open: refuse,
    readdir: refuse, readlink: refuse, rename: refuse, rmdir: refuse, stat: refuse, symlink: refuse,
    truncate: refuse, unlink: refuse, utimes: refuse,
  };
}

// fatalLines picks Go's own reason out of what a stopped runtime wrote to
// stderr: its "fatal error:" line, or "runtime: out of memory".
function fatalLines(stderrHead) {
  return [/^runtime: out of memory.*$/m, /^fatal error: .*$/m]
    .map((re) => re.exec(stderrHead)?.[0])
    .filter(Boolean)
    .join("\n");
}

// runProgram runs init's program to its end and posts its output and exit
// through post. requestLine answers the next stdin line (null at end of
// input) when init.stdinKind is "callback".
async function runProgram(init, post, requestLine) {
  // The fs comes first, so wasm_exec.js keeps it rather than installing its
  // own.
  installFS();
  if (typeof globalThis.Go !== "function") installGo();
  const go = new globalThis.Go();
  go.argv = ["app.wasm", ...init.args];
  go.env = init.env;
  let exitCode = 0;
  go.exit = (code) => {
    exitCode = code;
  };

  const encoder = new TextEncoder();
  const decoders = { 1: new TextDecoder("utf-8"), 2: new TextDecoder("utf-8") };
  let stderrHead = "";
  let pending = init.stdinKind === "text" ? encoder.encode(init.stdinText) : new Uint8Array(0);
  let ended = init.stdinKind !== "callback";
  let failure = null;
  // Why the setup module failed, when it did: the program never started.
  let setupFailure = "";

  current = {
    write(fd, buf) {
      // A write may end inside a UTF-8 sequence; the decoder keeps the tail.
      const text = decoders[fd].decode(buf, { stream: true });
      if (fd === 2 && stderrHead.length < 4096) stderrHead += text;
      if (text) post({ type: "output", fd, text });
    },
    read(buffer, offset, length, callback) {
      const serve = () => {
        const n = Math.min(length, pending.length);
        buffer.set(pending.subarray(0, n), offset);
        pending = pending.subarray(n);
        callback(null, n);
      };
      if (pending.length > 0 || ended) {
        serve();
        return;
      }
      requestLine().then(
        (line) => {
          if (go.exited) return;
          if (line === null || line === undefined) ended = true;
          else pending = encoder.encode(String(line) + "\n");
          try {
            serve();
          } catch (err) {
            failure = err;
            finish();
          }
        },
        () => {
          ended = true;
          if (!go.exited) serve();
        },
      );
    },
  };

  let finished = false;
  let resolveDone;
  const done = new Promise((resolve) => {
    resolveDone = resolve;
  });
  const finish = () => {
    if (finished) return;
    finished = true;
    current = null;
    for (const fd of [1, 2]) {
      const tail = decoders[fd].decode();
      if (tail) post({ type: "output", fd, text: tail });
    }
    let fatal = "";
    if (setupFailure) {
      exitCode = 2;
      fatal = setupFailure;
    } else if (failure !== null) {
      // A JS exception out of the Go runtime (a RangeError when recursion
      // outruns the engine's stack) leaves Go mid-function.
      exitCode = 2;
      fatal = "the program stopped the Nomi runtime: " + String(failure);
    } else if (exitCode !== 0) {
      fatal = fatalLines(stderrHead);
    }
    const exit = { type: "exit", code: exitCode };
    if (fatal) exit.fatal = fatal;
    // The runner leaves the program's files here as it exits
    // (vmrunner/files_js.go).
    if (globalThis.nomiFilesOut) exit.files = globalThis.nomiFilesOut;
    post(exit);
    resolveDone();
  };

  globalThis.nomiImage = init.image;
  // The files the program's file system starts with, which the runner reads
  // before main.
  globalThis.nomiFiles = init.files ?? {};
  globalThis.nomiFilesOut = undefined;
  // Locale.load's fetch of a locale file, answering its text.
  const localeFolder = new URL(init.locales ?? "locales/", init.url);
  globalThis.nomiLocale = async (tag) => {
    const href = new URL(`${tag}.nomil`, localeFolder).href;
    const response = await fetch(href);
    if (!response.ok) throw new Error(`fetching ${href}: HTTP ${response.status}`);
    return await response.text();
  };
  try {
    if (init.setup) {
      try {
        const setup = await import(init.setup);
        if (typeof setup.default === "function") await setup.default();
      } catch (err) {
        setupFailure = `the setup module ${init.setup} failed: ${String(err?.message ?? err)}`;
        finish();
        return done;
      }
    }
    const instance = await WebAssembly.instantiate(init.module, go.importObject);
    go.run(instance).then(finish, (err) => {
      failure = err;
      finish();
    });
  } catch (err) {
    failure = err;
    finish();
  }
  return done;
}

// serveThePage answers the page's messages when this module is a run's
// worker.
function serveThePage() {
  let lineWaiter = null;
  const post = (msg) => globalThis.postMessage(msg);
  const fail = (err) => {
    post({ type: "exit", code: 2, fatal: "the program stopped the Nomi runtime: " + String(err?.message ?? err) });
  };
  // A JS exception out of a Go timer callback surfaces here, not in
  // runProgram.
  globalThis.addEventListener?.("error", (e) => {
    e.preventDefault?.();
    fail(e.error ?? e.message);
  });
  globalThis.addEventListener?.("unhandledrejection", (e) => {
    e.preventDefault?.();
    fail(e.reason);
  });
  // Mode "call": the program's call function, and the chain that runs the
  // page's calls one at a time in the order they came.
  let callProgram = null;
  let calls = Promise.resolve();
  globalThis.onmessage = (e) => {
    const msg = e.data;
    if (msg.type === "start") {
      if (msg.mode === "call") {
        globalThis.nomiServe = (served) => {
          if (served.error !== undefined) {
            post({ type: "failed", message: String(served.error) });
            return;
          }
          callProgram = served.call;
          post({ type: "ready", exports: JSON.parse(served.exports) });
        };
      }
      const requestLine = () =>
        new Promise((resolve) => {
          lineWaiter = resolve;
          post({ type: "read" });
        });
      runProgram(msg, post, requestLine).catch(fail);
    } else if (msg.type === "line" && lineWaiter) {
      const resolve = lineWaiter;
      lineWaiter = null;
      resolve(msg.line);
    } else if (msg.type === "call") {
      const { id, name, args } = msg;
      calls = calls
        .then(() => {
          if (!callProgram) throw new Error("the program is not ready for calls");
          return callProgram(name, args);
        })
        .then(
          (value) => post({ type: "result", id, value }),
          (err) => post({ type: "result", id, error: String(err?.message ?? err) }),
        );
    }
  };
}

// ---- the page's side ----

// compiled holds one promise of { module, image } per absolute URL.
const compiled = new Map();

function fetchProgram(href) {
  let entry = compiled.get(href);
  if (!entry) {
    entry = (async () => {
      const response = await fetch(href);
      if (!response.ok) throw new Error(`fetching ${href}: HTTP ${response.status}`);
      const type = (response.headers.get("content-type") || "").split(";")[0].trim();
      // compileStreaming refuses a response that is not application/wasm,
      // which a plain static server may not send.
      const module =
        type === "application/wasm" && WebAssembly.compileStreaming
          ? await WebAssembly.compileStreaming(response)
          : await WebAssembly.compile(await response.arrayBuffer());
      const [section] = WebAssembly.Module.customSections(module, SECTION);
      if (!section) {
        throw new Error(`${href} carries no Nomi program (no "${SECTION}" section); build it with \`nomi build --web\``);
      }
      return { module, image: new Uint8Array(section) };
    })();
    compiled.set(href, entry);
    entry.catch(() => compiled.delete(href));
  }
  return entry;
}

// sink hands text to callback as written, or line by line to fallback.
// An Int crosses as a JSON number of any length, since an Int is exact at
// any size. decodeValue answers one a JavaScript number holds exactly, up to
// Number.MAX_SAFE_INTEGER in magnitude, as a number, and a larger one as a
// BigInt, read from the number's own digits. An engine without JSON.parse's
// source text gives a number, as JSON.parse always has.
function decodeValue(text) {
  return JSON.parse(text, (_key, value, context) =>
    typeof value === "number" && !Number.isSafeInteger(value) && context !== undefined &&
    /^-?\d+$/.test(context.source)
      ? BigInt(context.source)
      : value);
}

// encodeValues is the JSON text of a call's arguments, a BigInt written as
// its digits.
function encodeValues(values) {
  return JSON.stringify(values, (_key, value) => {
    if (typeof value !== "bigint") return value;
    if (typeof JSON.rawJSON !== "function") {
      throw new TypeError("call: a BigInt argument needs JSON.rawJSON, which this JavaScript engine lacks");
    }
    return JSON.rawJSON(value.toString());
  });
}

function sink(callback, fallback) {
  if (typeof callback === "function") return { write: (text) => callback(text), flush() {} };
  let buffered = "";
  return {
    write(text) {
      buffered += text;
      const nl = buffered.lastIndexOf("\n");
      if (nl < 0) return;
      for (const line of buffered.slice(0, nl).split("\n")) fallback(line);
      buffered = buffered.slice(nl + 1);
    },
    flush() {
      if (buffered) fallback(buffered);
      buffered = "";
    },
  };
}

const ABORTED = { exitCode: 130, fatal: "the run was aborted" };
const timedOut = (ms) => ({ exitCode: 124, fatal: `the program ran for ${ms} ms and was stopped` });

/**
 * Runs the Nomi program at url and resolves with { exitCode, fatal?, files? }
 * when it ends, times out or is aborted: files is every file in its
 * in-memory file system when it ended, absent when it was stopped. Rejects only when the module cannot be
 * loaded or an option is malformed.
 */
export async function run(url, options = {}) {
  const { args = [], env = {}, stdout, stderr, stdin, timeoutMs, signal, worker = true, locales, files = {}, setup } = options;
  if (!Array.isArray(args)) throw new TypeError("run: args must be an array of strings");
  if (files === null || typeof files !== "object" || Array.isArray(files)) {
    throw new TypeError("run: files must be an object of paths to strings or Uint8Arrays");
  }
  for (const [path, data] of Object.entries(files)) {
    if (typeof data !== "string" && !(data instanceof Uint8Array)) {
      throw new TypeError(`run: files["${path}"] must be a string or a Uint8Array`);
    }
  }
  if (env === null || typeof env !== "object") throw new TypeError("run: env must be an object");
  if (stdin !== undefined && stdin !== null && typeof stdin !== "string" && typeof stdin !== "function") {
    throw new TypeError("run: stdin must be a string, an async function, or absent");
  }
  if (timeoutMs !== undefined && timeoutMs !== null && !(timeoutMs >= 0)) {
    throw new TypeError("run: timeoutMs must be a number of milliseconds");
  }
  const base = typeof document !== "undefined" ? document.baseURI : import.meta.url;
  const href = new URL(url, base).href;
  if (signal?.aborted) return { ...ABORTED };
  const { module, image } = await fetchProgram(href);
  if (signal?.aborted) return { ...ABORTED };

  const init = {
    type: "start",
    module,
    image: image.slice(),
    url: href,
    locales: locales === undefined ? undefined : new URL(locales, base).href,
    setup: setup === undefined ? undefined : new URL(setup, base).href,
    args: args.map(String),
    env: Object.fromEntries(Object.entries({ NOMI_COLOR: "never", ...env }).map(([k, v]) => [k, String(v)])),
    stdinKind: typeof stdin === "string" ? "text" : typeof stdin === "function" ? "callback" : "none",
    stdinText: typeof stdin === "string" ? stdin : "",
    files,
  };
  const io = {
    out: sink(stdout, (line) => console.log(line)),
    err: sink(stderr, (line) => console.error(line)),
    readLine: async () => {
      try {
        const line = await stdin();
        return line === null || line === undefined ? null : String(line);
      } catch (err) {
        console.error("nomi.js: the stdin function threw; ending input:", err);
        return null;
      }
    },
  };
  return worker ? runInWorker(init, io, timeoutMs, signal) : runOnThisThread(init, io, timeoutMs, signal);
}

/**
 * Starts the Nomi program at url in its own worker for calls to its
 * exported functions, runs its main first when it has one, and resolves
 * with { call, exports, close } when the functions can be called. Rejects
 * when the module cannot be loaded or the program cannot start (its main
 * fails), with the Nomi diagnostic as the Error's message.
 */
export async function load(url, options = {}) {
  const { args = [], env = {}, stdout, stderr, locales, setup } = options;
  if (!Array.isArray(args)) throw new TypeError("load: args must be an array of strings");
  if (env === null || typeof env !== "object") throw new TypeError("load: env must be an object");
  const base = typeof document !== "undefined" ? document.baseURI : import.meta.url;
  const href = new URL(url, base).href;
  const { module, image } = await fetchProgram(href);
  const init = {
    type: "start",
    mode: "call",
    module,
    image: image.slice(),
    url: href,
    locales: locales === undefined ? undefined : new URL(locales, base).href,
    setup: setup === undefined ? undefined : new URL(setup, base).href,
    args: args.map(String),
    env: Object.fromEntries(Object.entries({ NOMI_COLOR: "never", ...env }).map(([k, v]) => [k, String(v)])),
    stdinKind: "none",
    stdinText: "",
  };
  const out = sink(stdout, (line) => console.log(line));
  const err = sink(stderr, (line) => console.error(line));
  const worker = new Worker(new URL(import.meta.url), { type: "module" });
  return new Promise((resolve, reject) => {
    const pending = new Map();
    let nextId = 1;
    let ready = false;
    let exported = [];
    // Why the program stopped, once it has: every later call rejects with it.
    let stopped = null;
    const stop = (message) => {
      if (stopped !== null) return;
      stopped = message;
      worker.terminate();
      out.flush();
      err.flush();
      for (const call of pending.values()) call.reject(new Error(message));
      pending.clear();
      if (!ready) reject(new Error(message));
    };
    const program = {
      call(name, ...callArgs) {
        if (stopped !== null) return Promise.reject(new Error(stopped));
        if (typeof name !== "string") return Promise.reject(new TypeError("call: the function name must be a string"));
        let text;
        try {
          text = encodeValues(callArgs);
        } catch (e) {
          return Promise.reject(e);
        }
        // A Unit result crosses as nothing.
        const unit = exported.some((x) => x.name === name && x.result === "");
        const id = nextId++;
        return new Promise((res, rej) => {
          pending.set(id, { resolve: (value) => res(unit ? undefined : decodeValue(value)), reject: rej });
          worker.postMessage({ type: "call", id, name, args: text });
        });
      },
      exports() {
        return Promise.resolve(exported.map((x) => ({ ...x, params: x.params.map((p) => ({ ...p })) })));
      },
      close() {
        stop("the program was closed");
      },
    };
    worker.onmessage = (e) => {
      const msg = e.data;
      if (stopped !== null) return;
      if (msg.type === "output") {
        (msg.fd === 2 ? err : out).write(msg.text);
      } else if (msg.type === "ready") {
        ready = true;
        exported = msg.exports;
        resolve(program);
      } else if (msg.type === "failed") {
        stop(msg.message);
      } else if (msg.type === "result") {
        const call = pending.get(msg.id);
        if (!call) return;
        pending.delete(msg.id);
        if (msg.error !== undefined) call.reject(new Error(msg.error));
        else call.resolve(msg.value);
      } else if (msg.type === "exit") {
        stop(msg.fatal || `the program exited with status ${msg.code}`);
      }
    };
    worker.onerror = (e) => {
      e.preventDefault?.();
      stop("the program stopped the Nomi runtime: " + (e.message || "the worker failed"));
    };
    worker.postMessage(init);
  });
}

// guard wires a run's timeout and abort signal to finish, which resolves
// the run once, and answers the function that unwires them.
function guard(finish, timeoutMs, signal) {
  let timer = null;
  const onAbort = () => finish({ ...ABORTED });
  signal?.addEventListener("abort", onAbort, { once: true });
  if (timeoutMs !== undefined && timeoutMs !== null) timer = setTimeout(() => finish(timedOut(timeoutMs)), timeoutMs);
  return () => {
    if (timer !== null) clearTimeout(timer);
    signal?.removeEventListener("abort", onAbort);
  };
}

// exitResult is what run resolves with for a program's exit message.
function exitResult(msg) {
  const result = { exitCode: msg.code };
  if (msg.fatal) result.fatal = msg.fatal;
  if (msg.files) result.files = msg.files;
  return result;
}

function runInWorker(init, io, timeoutMs, signal) {
  return new Promise((resolve) => {
    // This module, from its own URL: a file the host serves, so a strict
    // worker-src 'self' needs no blob: exception.
    const worker = new Worker(new URL(import.meta.url), { type: "module" });
    let done = false;
    let unguard = () => {};
    const finish = (result) => {
      if (done) return;
      done = true;
      unguard();
      worker.terminate();
      io.out.flush();
      io.err.flush();
      resolve(result);
    };
    unguard = guard(finish, timeoutMs, signal);
    worker.onmessage = (e) => {
      const msg = e.data;
      if (done) return;
      if (msg.type === "output") {
        (msg.fd === 2 ? io.err : io.out).write(msg.text);
      } else if (msg.type === "read") {
        io.readLine().then((line) => {
          if (!done) worker.postMessage({ type: "line", line });
        });
      } else if (msg.type === "exit") {
        finish(exitResult(msg));
      }
    };
    worker.onerror = (e) => {
      e.preventDefault?.();
      finish({ exitCode: 2, fatal: "the program stopped the Nomi runtime: " + (e.message || "the worker failed") });
    };
    worker.postMessage(init);
  });
}

// Main-thread runs share one `fs` and one globalThis.nomiImage, so they run
// one at a time. A timeout or abort resolves the run and silences it, but
// nothing can stop a program on the page's own thread.
let mainThreadQueue = Promise.resolve();

function runOnThisThread(init, io, timeoutMs, signal) {
  const start = mainThreadQueue.then(
    () =>
      new Promise((resolve) => {
        let done = false;
        let unguard = () => {};
        const finish = (result) => {
          if (done) return;
          done = true;
          unguard();
          io.out.flush();
          io.err.flush();
          resolve(result);
        };
        unguard = guard(finish, timeoutMs, signal);
        const post = (msg) => {
          if (done) return;
          if (msg.type === "output") (msg.fd === 2 ? io.err : io.out).write(msg.text);
          else if (msg.type === "exit") finish(exitResult(msg));
        };
        const requestLine = () => (done ? Promise.resolve(null) : io.readLine());
        runProgram(init, post, requestLine).catch((err) =>
          finish({ exitCode: 2, fatal: "the program stopped the Nomi runtime: " + String(err) }),
        );
      }),
  );
  mainThreadQueue = start.catch(() => {});
  return start;
}

// mount puts a terminal on el (output, an input line that is enabled while
// the program waits for a line, a status line and a Run again button) and
// runs the program el's data-nomi-program names. Styles are set from script
// so a strict style-src needs no exception.
export function mount(el, url = el.getAttribute("data-nomi-program")) {
  const doc = el.ownerDocument;
  const make = (tag, style, attrs = {}) => {
    const node = doc.createElement(tag);
    Object.assign(node.style, style);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    return node;
  };
  const mono = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
  Object.assign(el.style, {
    colorScheme: "light dark",
    fontFamily: mono,
    fontSize: "14px",
    maxWidth: "60rem",
    margin: "0 auto",
    padding: "16px",
    boxSizing: "border-box",
  });
  const output = make(
    "pre",
    {
      margin: "0",
      padding: "12px",
      minHeight: "12rem",
      maxHeight: "70vh",
      overflow: "auto",
      whiteSpace: "pre-wrap",
      overflowWrap: "anywhere",
      background: "Canvas",
      color: "CanvasText",
      border: "1px solid GrayText",
      borderRadius: "6px",
    },
    { role: "log", "aria-live": "polite", "aria-label": "Program output" },
  );
  const form = make("form", { display: "flex", gap: "8px", marginTop: "8px" });
  const input = make(
    "input",
    { flex: "1", minWidth: "0", font: "inherit", padding: "6px 8px" },
    { type: "text", "aria-label": "Program input", autocomplete: "off", placeholder: "input (Ctrl-D ends it)" },
  );
  input.disabled = true;
  const send = make("button", { font: "inherit" }, { type: "submit" });
  send.textContent = "Enter";
  send.disabled = true;
  form.append(input, send);
  const footer = make("div", { display: "flex", gap: "8px", alignItems: "center", marginTop: "8px" });
  const status = make("span", { flex: "1", opacity: "0.8" }, { role: "status" });
  const again = make("button", { font: "inherit" }, { type: "button" });
  again.textContent = "Run again";
  again.disabled = true;
  footer.append(status, again);
  el.replaceChildren(output, form, footer);

  const append = (text, isError) => {
    const atBottom = output.scrollTop + output.clientHeight >= output.scrollHeight - 4;
    const span = make("span", isError ? { color: "light-dark(#b3261e, #ff8a80)" } : {});
    span.textContent = text;
    output.append(span);
    if (atBottom) output.scrollTop = output.scrollHeight;
  };

  let answer = null;
  const ask = () =>
    new Promise((resolve) => {
      answer = resolve;
      input.disabled = false;
      send.disabled = false;
      input.focus();
    });
  const reply = (line) => {
    if (!answer) return;
    const resolve = answer;
    answer = null;
    input.value = "";
    input.disabled = true;
    send.disabled = true;
    if (line !== null) append(line + "\n", false);
    resolve(line);
  };
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    reply(input.value);
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "d" && e.ctrlKey && input.value === "") {
      e.preventDefault();
      reply(null);
    }
  });

  const start = async () => {
    again.disabled = true;
    output.replaceChildren();
    status.textContent = "running";
    try {
      const result = await run(url, {
        stdout: (text) => append(text, false),
        stderr: (text) => append(text, true),
        stdin: ask,
      });
      reply(null);
      status.textContent = `exited with status ${result.exitCode}` + (result.fatal ? `: ${result.fatal}` : "");
    } catch (err) {
      status.textContent = `could not run ${url}: ${err.message ?? err}`;
    }
    again.disabled = false;
  };
  again.addEventListener("click", start);
  start();
}

// ---- which side this copy is ----

if (typeof WorkerGlobalScope !== "undefined" && self instanceof WorkerGlobalScope) {
  serveThePage();
} else if (typeof document !== "undefined") {
  const mountAll = () => document.querySelectorAll("[data-nomi-program]").forEach((el) => mount(el));
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mountAll, { once: true });
  else mountAll();
}
