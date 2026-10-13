// nomi-web.js loads and runs a Nomi program built by `nomi build --web`.
//
//   import { run } from "./nomi-web.js";
//   const { exitCode, fatal } = await run("./app.wasm", {
//     args: [],                 // Startup.args
//     env: {},                  // Startup.env; NOMI_COLOR=never unless set here
//     stdout: (text) => {},     // text as written; default console.log per line
//     stderr: (text) => {},     // default console.error per line
//     stdin: "all of it\n",     // or async () => string | null; default: end of input
//     timeoutMs: 10_000,        // stop the program after this long (exit 124)
//     signal,                   // an AbortSignal that stops it (exit 130)
//     worker: true,             // false runs on the page's own thread
//   });
//
// Each run gets a fresh instance in its own worker (nomi-worker.js), so a
// runaway program can be stopped and the page stays responsive. A module is
// fetched and compiled once per URL and shared by every later run.
//
// Imported by a page with an element carrying `data-nomi-program="app.wasm"`,
// it mounts a terminal on that element and runs the program: this is what
// the index.html beside it does, with no inline script.

const SECTION = "nomi.image";

// compiled holds one promise of { module, image } per absolute URL.
const compiled = new Map();

function load(href) {
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
 * Runs the Nomi program at url and resolves with { exitCode, fatal? } when
 * it ends, times out or is aborted. Rejects only when the module cannot be
 * loaded or an option is malformed.
 */
export async function run(url, options = {}) {
  const { args = [], env = {}, stdout, stderr, stdin, timeoutMs, signal, worker = true } = options;
  if (!Array.isArray(args)) throw new TypeError("run: args must be an array of strings");
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
  const { module, image } = await load(href);
  if (signal?.aborted) return { ...ABORTED };

  const init = {
    type: "start",
    module,
    image: image.slice(),
    args: args.map(String),
    env: Object.fromEntries(Object.entries({ NOMI_COLOR: "never", ...env }).map(([k, v]) => [k, String(v)])),
    stdinKind: typeof stdin === "string" ? "text" : typeof stdin === "function" ? "callback" : "none",
    stdinText: typeof stdin === "string" ? stdin : "",
  };
  const io = {
    out: sink(stdout, (line) => console.log(line)),
    err: sink(stderr, (line) => console.error(line)),
    readLine: async () => {
      try {
        const line = await stdin();
        return line === null || line === undefined ? null : String(line);
      } catch (err) {
        console.error("nomi-web: the stdin function threw; ending input:", err);
        return null;
      }
    },
  };
  return worker ? runInWorker(init, io, timeoutMs, signal) : runOnThisThread(init, io, timeoutMs, signal);
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

function runInWorker(init, io, timeoutMs, signal) {
  return new Promise((resolve) => {
    const worker = new Worker(new URL("./nomi-worker.js", import.meta.url), { type: "module" });
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
        finish(msg.fatal ? { exitCode: msg.code, fatal: msg.fatal } : { exitCode: msg.code });
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
  const start = mainThreadQueue.then(async () => {
    const { runProgram } = await import(new URL("./nomi-worker.js", import.meta.url).href);
    return new Promise((resolve) => {
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
        else if (msg.type === "exit") finish(msg.fatal ? { exitCode: msg.code, fatal: msg.fatal } : { exitCode: msg.code });
      };
      const requestLine = () => (done ? Promise.resolve(null) : io.readLine());
      runProgram(init, post, requestLine).catch((err) =>
        finish({ exitCode: 2, fatal: "the program stopped the Nomi runtime: " + String(err) }),
      );
    });
  });
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

if (typeof document !== "undefined") {
  const mountAll = () => document.querySelectorAll("[data-nomi-program]").forEach((el) => mount(el));
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mountAll, { once: true });
  else mountAll();
}
