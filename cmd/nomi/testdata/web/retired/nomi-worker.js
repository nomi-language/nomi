// nomi-worker.js runs one Nomi program built by `nomi build --web`. It is
// written by `nomi build --web` beside app.wasm, wasm_exec.js and
// nomi-web.js, and nomi-web.js starts it as a module worker, one per run.
//
// Protocol with nomi-web.js:
//   in:  { type: "start", module, image, args, env, stdinKind, stdinText }
//        { type: "line", line }          // answer to "read": a string, or null for end of input
//   out: { type: "output", fd, text }    // fd 1 is stdout, fd 2 stderr
//        { type: "read" }                // the program reads stdin and wants a line
//        { type: "exit", code, fatal? }  // the program ended
//
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

// The run this thread is serving, or null. A worker serves one run;
// nomi-web.js's main-thread mode serves one at a time.
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
export async function runProgram(init, post, requestLine) {
  installFS();
  if (typeof globalThis.Go !== "function") {
    await import(new URL("./wasm_exec.js", import.meta.url).href);
  }
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
    if (failure !== null) {
      // A JS exception out of the Go runtime (a RangeError when recursion
      // outruns the engine's stack) leaves Go mid-function.
      exitCode = 2;
      fatal = "the program stopped the Nomi runtime: " + String(failure);
    } else if (exitCode !== 0) {
      fatal = fatalLines(stderrHead);
    }
    post(fatal ? { type: "exit", code: exitCode, fatal } : { type: "exit", code: exitCode });
    resolveDone();
  };

  globalThis.nomiImage = init.image;
  try {
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

// In a worker (no document, a postMessage to the page), serve the page.
if (typeof document === "undefined" && typeof globalThis.postMessage === "function") {
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
  globalThis.onmessage = (e) => {
    const msg = e.data;
    if (msg.type === "start") {
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
    }
  };
}
