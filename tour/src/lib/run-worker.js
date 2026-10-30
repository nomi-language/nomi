// Web Worker that runs Nomi programs off the main thread, so a runaway program
// (e.g. `loop {}`) in an editable tour example never freezes the page — the main
// thread enforces a timeout by terminate()-ing this worker.
//
// Protocol (per request id):
//   in:  { id, source }                      // run
//        { id, source, files }               // run a multi-file source; files names its Nomi tabs
//        { id, op: "format", source }           // formatter
//        { id, op: "formatTestBody", source }   // formatter for test bodies
//        { id, op: "hover", source, line, col } // LSP hover (1-based pos)
//        { id, op: "hover", source, file, line, col } // hover in tab `file` of a multi-file
//                                                      // source, pos in that tab's own text
//        { id, op: "stdlibTest", module, context, source } // run stdlib reference test
//        { id, op: "sem", source }           // semantic tokens alone, no run
//        { id, op: "sem", source, files }    // the same for a multi-file source
//        { id, source, files, tools }        // run tools ([{ module, name }]) first
//   out: { id, kind: "start" }               // the run starts: the wasm and any module
//                                            // it imports are loaded
//        { id, kind: "sem", semtokens }      // analyzer tokens — computed first,
//                                            // ({ file: tokens } for a multi-file run)
//        { id, kind: "run", output, error, written } // so a hanging run still highlights;
//                                            // written: the files the tools wrote, by path
//        { id, op: "format", output, error }  // formatter reply
//        { id, op: "hover", markdown }        // hover reply
//        { id, op: "sem", semtokens }         // semantic tokens reply
//   out: { type: "ready" }                   // wasm loaded, queued work flushed
//        { type: "exited" }                  // the Go runtime stopped; replace this worker
//
// Classic worker (importScripts) loaded from this file's own directory, so the
// relative fetches resolve against /nomi/.
//
// DO NOT "FIX" THIS LINE FROM A HEADLESS-BROWSER OBSERVATION. `./wasm_exec.js`
// is correct per spec: importScripts resolves against the worker's own
// location, which is this file's URL. It has now been changed twice on
// evidence that did not survive checking, and the second attempt is why this
// paragraph exists.
//
// The omp headless browser REPLACES `Worker` with a wrapper — measured, not
// guessed: `Worker.toString()` reports `[native code]` while
// `Worker.prototype.constructor === Worker` is FALSE, and `navigator.webdriver`
// reports false under CDP. The wrapper fetches the script into a blob, so
// inside the worker `self.location.href` is `blob:http://…/<uuid>` even when
// the worker was constructed from an ordinary http URL, and the error's
// `filename` still reports the http URL. A blob is not a usable base, so under
// that browser EVERY specifier fails — bare, `./`, `/nomi/…`, and
// `new URL("wasm_exec.js", self.location.href)` alike. That is a property of
// the instrument and says nothing about a real browser.
//
// So the runnable blocks CANNOT be verified with the headless browser, and a
// `worker error:` seen there is not evidence. Verify with a real browser, or
// not at all.
importScripts("./wasm_exec.js"); // sets self.Go
// The in-memory file system Go's `os` reads and writes, where a block that
// names modules is written as its files (go-memfs.js). After wasm_exec.js,
// whose fs stub it fills in.
importScripts("./go-memfs.js");

// A visitor's program can stop the Go runtime in this worker. A deadlock ("all
// goroutines are asleep") or an allocation past the module's 1 GiB memory cap
// makes Go exit; a JS exception thrown out of a Go call (a RangeError when
// recursion outruns the engine's stack) leaves Go mid-function, which ends in
// a fatal error later. Every later call on the instance then fails, and before
// this handling it failed as `Cannot read properties of undefined`. Now the
// request that stopped the runtime is answered with Go's own fatal line, the
// worker posts `exited`, and the page replaces it and re-sends anything queued.
//
// wasm_exec.js installs its `fs` shim because nothing here defines one, and Go
// writes a fatal error to fd 2 through the shim's writeSync, message first and
// then every goroutine's trace. Keep the head of what one call writes.
let stderrHead = "";
const stderrDecoder = new TextDecoder("utf-8");
const shimWriteSync = globalThis.fs.writeSync;
globalThis.fs.writeSync = function (fd, buf) {
  if (fd === 2 && stderrHead.length < 4096) stderrHead += stderrDecoder.decode(buf);
  return shimWriteSync.call(this, fd, buf);
};

let ready = false;
let dead = false;
const queue = [];

// The wasm is staged gzipped (nomi.wasm.gz): Cloudflare Pages refuses files
// over 25 MiB and the uncompressed module is about 27 MiB. It is decompressed
// here, not by the host. The gzip magic is checked first so that a host which
// does add `Content-Encoding: gzip` (the browser then hands over the already
// decompressed module) still works.
async function fetchNomiWasm() {
  const r = await fetch("./nomi.wasm.gz");
  if (!r.ok) throw new Error(`fetch nomi.wasm.gz: HTTP ${r.status}`);
  const buf = await r.arrayBuffer();
  const head = new Uint8Array(buf, 0, Math.min(2, buf.byteLength));
  if (head[0] !== 0x1f || head[1] !== 0x8b) return buf;
  const stream = new Blob([buf]).stream().pipeThrough(new DecompressionStream("gzip"));
  return new Response(stream).arrayBuffer();
}

const go = new Go();
fetchNomiWasm()
  .then((buf) => WebAssembly.instantiate(buf, go.importObject))
  .then(({ instance }) => {
    go.run(instance); // registers self.nomiRun + self.nomiSemTokens, then parks
    ready = true;
    postMessage({ type: "ready" });
    for (const msg of queue.splice(0)) handle(msg);
  })
  .catch((err) => postMessage({ type: "error", error: String(err) }));

onmessage = (e) => {
  if (!ready) {
    queue.push(e.data);
    return;
  }
  handle(e.data);
};

// SQLite's official WebAssembly build (sqlite.org's sqlite-wasm, staged in
// ./sqlite/ by scripts/build-tour-wasm.sh), which the browser build of the
// sqlite driver calls (internal/tourgo: a block that `import sqlite`s runs
// the driver module's Go side, which reaches SQLite through
// globalThis.sqlite3). It is about 1.3 MB, so it is loaded only when a run
// needs it, once per worker, and before the run's semantic tokens are
// posted, which starts the page's run budget: loading is not the program's
// time. Its OPFS file systems are left out: a block's database lives in the
// worker's memory and writes nothing to the visitor's storage.
let sqlitePromise = null;
function loadSqlite() {
  sqlitePromise ??= (async () => {
    self.sqlite3ApiConfig = {
      disable: { vfs: { opfs: true, "opfs-vfs": true, "opfs-wl": true, "opfs-sahpool": true } },
    };
    importScripts("./sqlite/sqlite3.js"); // sets self.sqlite3InitModule
    self.sqlite3 = await self.sqlite3InitModule({
      print() {},
      printErr() {},
      locateFile: (file) => "./sqlite/" + file,
    });
  })().catch((err) => {
    // The driver answers a Connection error that says the build is not
    // loaded; the reason is here.
    console.error("loading SQLite's WebAssembly build:", err);
  });
  return sqlitePromise;
}

// needs answers the loads a run of msg waits for: the modules it imports
// that need more than the Nomi wasm (nomiTourModules).
function needs(msg) {
  if (msg.op === "format" || msg.op === "formatTestBody" || msg.op === "hover" || msg.op === "sem" || msg.op === "stdlibTest") return null;
  let modules = [];
  try {
    modules = JSON.parse(self.nomiTourModules(msg.source) || "[]");
  } catch (_) {}
  return modules.includes("sqlite") && !sqlitePromise ? loadSqlite() : null;
}

// waiting holds the messages that arrived while a load needs() started was
// under way, in order, the one that started it first.
let waiting = null;

// callGo runs one exported Go function and returns { value }, or { failure }
// when the call stopped the runtime or threw out of it, marking this instance
// dead.
function callGo(fn) {
  let value;
  let thrown = null;
  stderrHead = "";
  try {
    value = fn();
  } catch (err) {
    thrown = err;
  }
  if (!go.exited && thrown === null) return { value };
  dead = true;
  if (!go.exited) return { failure: "the program stopped the Nomi runtime: " + String(thrown) };
  const lines = [/^runtime: out of memory.*$/m, /^fatal error: .*$/m]
    .map((re) => re.exec(stderrHead)?.[0])
    .filter(Boolean);
  return {
    failure: "the program stopped the Nomi runtime:\n" + (lines.join("\n") || "the Go runtime exited"),
  };
}

function handle(msg) {
  if (waiting) {
    waiting.push(msg);
    return;
  }
  // A message queued behind the one that stopped the runtime gets no answer
  // here; the page re-sends it to the replacement worker.
  if (dead) return;
  const load = needs(msg);
  if (load) {
    waiting = [msg];
    load.then(() => {
      const msgs = waiting;
      waiting = null;
      for (const m of msgs) handle(m);
    });
    return;
  }
  handleOne(msg);
  if (dead) postMessage({ type: "exited" });
}

function handleOne(msg) {
  if (msg.op === "format" || msg.op === "formatTestBody") {
    const r = callGo(() =>
      msg.op === "format" ? self.nomiFormat(msg.source) : self.nomiFormatTestBody(msg.source),
    );
    const res = r.failure ? { output: "", error: r.failure } : r.value;
    postMessage({ id: msg.id, op: "format", output: res.output, error: res.error });
    return;
  }

  if (msg.op === "hover") {
    const r = callGo(() =>
      msg.file
        ? self.nomiHoverIn(msg.source, msg.file, msg.line, msg.col)
        : self.nomiHover(msg.source, msg.line, msg.col),
    );
    postMessage({ id: msg.id, op: "hover", markdown: (!r.failure && r.value) || "" });
    return;
  }

  // A ```nomi-run manual block asks for its tokens before its first run,
  // which only the reader starts.
  if (msg.op === "sem") {
    const r = callGo(() =>
      msg.files ? self.nomiSemTokensIn(msg.source, ...msg.files) : self.nomiSemTokens(msg.source),
    );
    let semtokens = msg.files ? {} : [];
    if (!r.failure) {
      try {
        semtokens = JSON.parse(r.value || (msg.files ? "{}" : "[]"));
      } catch (_) {}
    }
    postMessage({ id: msg.id, op: "sem", semtokens });
    return;
  }

  // The run starts here, after the loads it waited for: the page's status
  // says "Loading Nomi" before this and "Running" after.
  postMessage({ id: msg.id, kind: "start" });

  // A ```nomi-run tool:<module>/<name> block's tools run first, each on the
  // source the one before left, as a reader runs `nomi tool` before `nomi
  // run`: the files they write replace the block's (posted back as
  // `written`, for the page's tabs), and what they print comes before the
  // run's output. A tool that fails is the run's failure.
  let source = msg.source;
  let printed = "";
  const written = {};
  for (const t of msg.tools || []) {
    const r = callGo(() => self.nomiTool(source, t.module, t.name));
    const res = r.failure ? { output: "", error: r.failure } : r.value;
    printed += res.output;
    if (res.error) {
      postMessage({ id: msg.id, kind: "sem", semtokens: msg.files ? {} : [] });
      postMessage({ id: msg.id, kind: "run", output: printed, error: res.error, written });
      return;
    }
    source = res.source;
    Object.assign(written, res.files);
  }

  // Run. Semantic tokens first: analysis always terminates, so even a program
  // that then hangs in nomiRun gets full (analyzer-refined) highlighting.
  // A multi-file source's tokens are each tab's, from one analysis of the
  // whole program, which the hovers that follow reuse.
  let semtokens = msg.files ? {} : [];
  const sem = callGo(() =>
    msg.files ? self.nomiSemTokensIn(source, ...msg.files) : self.nomiSemTokens(source),
  );
  if (!sem.failure) {
    try {
      semtokens = JSON.parse(sem.value || (msg.files ? "{}" : "[]"));
    } catch (_) {}
  }
  postMessage({ id: msg.id, kind: "sem", semtokens });

  const r = sem.failure
    ? sem
    : callGo(() =>
        msg.op === "stdlibTest"
          ? self.nomiRunStdlibTest(msg.module, msg.source, msg.context || "")
          : self.nomiRun(source),
      );
  const res = r.failure ? { output: "", error: r.failure } : r.value;
  postMessage({ id: msg.id, kind: "run", output: printed + res.output, error: res.error, written });
}
