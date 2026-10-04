// Web Worker that runs Nomi programs off the main thread, so a runaway program
// (e.g. `loop {}`) in an editable tour example never freezes the page — the main
// thread enforces a timeout by terminate()-ing this worker.
//
// Protocol (per request id):
//   in:  { id, source }                      // run
//        { id, op: "format", source }           // formatter
//        { id, op: "formatTestBody", source }   // formatter for test bodies
//        { id, op: "hover", source, line, col } // LSP hover (1-based pos)
//        { id, op: "stdlibTest", module, context, source } // run stdlib reference test
//   out: { id, kind: "sem", semtokens }      // analyzer tokens — computed first,
//        { id, kind: "run", output, error }  // so a hanging run still highlights
//        { id, op: "format", output, error }  // formatter reply
//        { id, op: "hover", markdown }        // hover reply
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
  // A message queued behind the one that stopped the runtime gets no answer
  // here; the page re-sends it to the replacement worker.
  if (dead) return;
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
    const r = callGo(() => self.nomiHover(msg.source, msg.line, msg.col));
    postMessage({ id: msg.id, op: "hover", markdown: (!r.failure && r.value) || "" });
    return;
  }

  // Run. Semantic tokens first: analysis always terminates, so even a program
  // that then hangs in nomiRun gets full (analyzer-refined) highlighting.
  let semtokens = [];
  const sem = callGo(() => self.nomiSemTokens(msg.source));
  if (!sem.failure) {
    try {
      semtokens = JSON.parse(sem.value || "[]");
    } catch (_) {}
  }
  postMessage({ id: msg.id, kind: "sem", semtokens });

  const r = sem.failure
    ? sem
    : callGo(() =>
        msg.op === "stdlibTest"
          ? self.nomiRunStdlibTest(msg.module, msg.source, msg.context || "")
          : self.nomiRun(msg.source),
      );
  const res = r.failure ? { output: "", error: r.failure } : r.value;
  postMessage({ id: msg.id, kind: "run", output: res.output, error: res.error });
}
