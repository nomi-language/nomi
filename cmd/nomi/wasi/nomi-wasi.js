// nomi-wasi.js runs compiled Nomi programs (.nomic) in the WASI reactor
// `nomi build-vm --reactor` writes beside it, or the program built into a
// reactor `nomi build --target wasip1/wasm` writes for a program with an
// `export fn`, in a browser or in Node, with no other dependency.
//
//   import { loadRuntime } from "./nomi-wasi.js";
//   const vm = await loadRuntime("./nomi-vm-reactor.wasm");
//
//   const game = await vm.load("./game.nomic");
//   game.exports;                     // ["new_game(word: String) -> Game", ...]
//   let state = await game.call("new_game", "pipeline");
//   state = await game.call("guess", state, "e");
//   game.close();
//
//   const { status, output, stdout, stderr } =
//     await vm.run("./greet.nomic", { stdin: "Ada\n", args: [], env: {} });
//
//   const app = await loadRuntime("./game.wasm");   // nomi build --target wasip1/wasm
//   const own = await app.load();                   // the program built into it
//
// loadRuntime compiles the reactor once. Each load and run instantiates it
// afresh, since a reactor holds one program for its life, and hands it the
// .nomic through nomi_load. With no .nomic, nomi_load is handed nothing,
// which boots the program built into the module and answers its exported
// functions; a reactor with none built in refuses. Values cross as JSON: a call's arguments are
// JSON-encoded, its `{"ok": value}` result is decoded, and its
// `{"error": "..."}` result rejects with an Error carrying the Nomi
// diagnostic. The program stays usable after a failed call.
//
// A source is a URL string, URL, Response, ArrayBuffer or typed array (and
// for loadRuntime, a WebAssembly.Module). Where there is no fetch for it
// (Node and a file: URL, or a string that is not an absolute URL) it is
// read as a file, a relative path from the working directory.
//
// The WASI preview1 imports are implemented here: arguments, environment,
// clocks, random bytes, stdin from the `stdin` option, stdout and stderr to
// the output sinks, and a sleep for poll_oneoff. There are no files: no
// directory is preopened, so path_open answers EBADF. An import this file
// does not know answers ENOSYS, so a newer Go that imports more still
// instantiates.
//
// Everything runs on the calling thread, since a WASI call is synchronous.
// A sleeping Nomi task (timer.sleep, a timeout) blocks the thread for its
// duration: with Atomics.wait where the thread may block (Node, a Worker,
// a cross-origin-isolated page), and by spinning on the clock elsewhere. On
// a page, run programs that sleep or run long in a Worker.

const ESUCCESS = 0, EBADF = 8, EINVAL = 28, ENOSYS = 52, ESPIPE = 70;

const encoder = new TextEncoder();

/**
 * @typedef {string | URL | Response | ArrayBuffer | ArrayBufferView} Source
 * @typedef {{
 *   args?: string[], env?: Record<string, string>,
 *   stdin?: string | Uint8Array,
 *   onStdout?: (text: string) => void, onStderr?: (text: string) => void,
 * }} Options
 */

/** proc_exit's status, thrown out of the module and caught here. */
class Exit {
  /** @param {number} code */
  constructor(code) { this.code = code; }
}

const isNode = typeof process === "object" && !!process.versions?.node;

/**
 * Answers source's bytes, or a Response to read them from.
 * @param {Source} source
 * @returns {Promise<Response | Uint8Array>}
 */
async function open(source) {
  if (typeof Response !== "undefined" && source instanceof Response) return source;
  if (source instanceof ArrayBuffer) return new Uint8Array(source);
  if (ArrayBuffer.isView(source)) return new Uint8Array(source.buffer, source.byteOffset, source.byteLength);
  if (typeof source !== "string" && !(source instanceof URL)) {
    throw new TypeError("nomi-wasi: a source is a URL, a Response or bytes");
  }
  let url = null;
  try { url = new URL(source); } catch { /* relative */ }
  if (isNode && (url === null || url.protocol === "file:")) {
    const { readFile } = await import("node:fs/promises");
    return new Uint8Array(await readFile(url ?? source));
  }
  return fetch(source);
}

/**
 * Answers source's bytes.
 * @param {Source} source
 */
async function bytesOf(source) {
  const got = await open(source);
  if (got instanceof Uint8Array) return got;
  if (!got.ok) throw new Error(`nomi-wasi: fetching ${got.url || "the response"}: ${got.status} ${got.statusText}`);
  return new Uint8Array(await got.arrayBuffer());
}

/**
 * Compiles the reactor once and answers a runtime that loads and runs
 * programs in it.
 * @param {Source | WebAssembly.Module} source
 */
export async function loadRuntime(source) {
  return new Runtime(await compile(source));
}

/** @param {Source | WebAssembly.Module} source */
async function compile(source) {
  if (source instanceof WebAssembly.Module) return source;
  const got = await open(source);
  if (got instanceof Uint8Array) return WebAssembly.compile(got);
  if (!got.ok) throw new Error(`nomi-wasi: fetching ${got.url || "the response"}: ${got.status} ${got.statusText}`);
  const type = (got.headers.get("content-type") ?? "").split(";")[0].trim();
  if (type === "application/wasm" && WebAssembly.compileStreaming) return WebAssembly.compileStreaming(got);
  const bytes = await got.arrayBuffer();
  try {
    return await WebAssembly.compile(bytes);
  } catch (err) {
    throw new Error(`nomi-wasi: ${got.url || "the response"} is not a WebAssembly module (served as ${type || "no content type"}; ` +
      `serve .wasm files as application/wasm): ${err instanceof Error ? err.message : err}`);
  }
}

export class Runtime {
  /** @param {WebAssembly.Module} module the compiled reactor */
  constructor(module) {
    this.module = module;
  }

  /**
   * Loads a program into a fresh instance and answers it once its
   * exported functions can be called. With no source it is the program
   * built into the module. Its output goes to onStdout and onStderr, by
   * default console.log and console.error a line at a time.
   * @param {Source} [source] a .nomic file
   * @param {Options} [options]
   */
  async load(source, options = {}) {
    const bytes = source === undefined ? null : await bytesOf(source);
    const out = lineSink(console.log), err = lineSink(console.error);
    const host = await Host.start(this.module, options, options.onStdout ?? out.write, options.onStderr ?? err.write);
    try {
      const exports = host.load(bytes);
      return new Program(host, exports, () => { out.flush(); err.flush(); });
    } catch (e) {
      out.flush(); err.flush();
      throw e;
    }
  }

  /**
   * Loads a program into a fresh instance, runs its `fn main`, and answers
   * its exit status and output. `output` is stdout and stderr in the order
   * they were written. A stream with its own callback is not captured.
   * With no source it is the program built into the module.
   * @param {Source} [source] a .nomic file
   * @param {Options} [options]
   */
  async run(source, options = {}) {
    const bytes = source === undefined ? null : await bytesOf(source);
    let output = "", stdout = "", stderr = "";
    const host = await Host.start(this.module, options,
      options.onStdout ?? ((text) => { output += text; stdout += text; }),
      options.onStderr ?? ((text) => { output += text; stderr += text; }));
    host.load(bytes);
    const status = host.main();
    return { status, output, stdout, stderr };
  }
}

export class Program {
  /**
   * @param {Host} host
   * @param {string[]} exports
   * @param {() => void} flush
   */
  constructor(host, exports, flush) {
    this.host = host;
    /** The exported functions' signatures, such as "add(a: Int, b: Int): Int". */
    this.exports = exports;
    this.flush = flush;
  }

  /**
   * Calls an exported function with JSON-encodable arguments and answers
   * its JSON-decoded result, or rejects with the Nomi diagnostic.
   * @param {string} name
   * @param {...unknown} args
   */
  async call(name, ...args) {
    try {
      return this.host.call(name, JSON.stringify(args));
    } finally {
      this.flush();
    }
  }

  /** Drops the program's instance; later calls reject. */
  close() {
    this.host.close();
    this.flush();
  }
}

/**
 * A sink that hands write a line at a time, for console.log.
 * @param {(line: string) => void} write
 */
function lineSink(write) {
  let pending = "";
  return {
    /** @param {string} text */
    write(text) {
      const lines = (pending + text).split("\n");
      pending = lines.pop() ?? "";
      for (const line of lines) write(line);
    },
    flush() {
      if (pending !== "") write(pending);
      pending = "";
    },
  };
}

/** One instance of the reactor, with the WASI host it imports. */
class Host {
  static async start(/** @type {WebAssembly.Module} */ module, /** @type {Options} */ options,
    /** @type {(text: string) => void} */ stdout, /** @type {(text: string) => void} */ stderr) {
    const host = new Host();
    const args = ["nomi", ...(options.args ?? [])].map((a) => encoder.encode(a + "\0"));
    const env = Object.entries(options.env ?? {}).map(([k, v]) => encoder.encode(`${k}=${v}\0`));
    const stdin = typeof options.stdin === "string" ? encoder.encode(options.stdin) : options.stdin ?? new Uint8Array();
    const wasi = wasiImports(host, { args, env, stdin, sinks: { 1: decoding(stdout), 2: decoding(stderr) } });
    // Every function the module imports gets an answer, so a Go that
    // imports one more still instantiates: ENOSYS for any not here.
    /** @type {Record<string, Record<string, Function>>} */
    const imports = {};
    for (const { module: from, name, kind } of WebAssembly.Module.imports(module)) {
      if (kind !== "function") continue;
      imports[from] ??= {};
      imports[from][name] = (from === "wasi_snapshot_preview1" && wasi[name]) || (() => ENOSYS);
    }
    host.x = /** @type {any} */ (await WebAssembly.instantiate(module, imports)).exports;
    host.enter("starting", () => host.x._initialize());
    return host;
  }

  constructor() {
    /** @type {any} the instance's exports */
    this.x = null;
    this.closed = false;
  }

  /** The module's memory as it is now; a call may grow it. */
  view() { return new DataView(this.x.memory.buffer); }

  /**
   * Runs f in the module. A throw out of it (proc_exit, the Go runtime's
   * fatal error, a WebAssembly trap) leaves the instance unusable, so the
   * host closes it, and an exit becomes an Error saying so.
   * @template T
   * @param {string} doing what f does, for that Error
   * @param {() => T} f
   * @returns {T}
   */
  enter(doing, f) {
    if (this.closed) throw new Error(`nomi-wasi: the program is closed, so it cannot go on ${doing}`);
    try {
      return f();
    } catch (e) {
      this.close();
      throw e instanceof Exit ? new Error(`nomi-wasi: the program exited with status ${e.code} while ${doing}`) : e;
    }
  }

  /** @param {Uint8Array} bytes @returns {[number, number]} */
  put(bytes) {
    const ptr = this.x.nomi_alloc(bytes.length) >>> 0;
    new Uint8Array(this.x.memory.buffer, ptr, bytes.length).set(bytes);
    return [ptr, bytes.length];
  }

  /** Reads a packed result and frees it. @param {bigint} packed @returns {{ ok?: any, error?: string }} */
  answer(packed) {
    packed = BigInt.asUintN(64, packed);
    const ptr = Number(packed >> 32n), len = Number(packed & 0xffffffffn);
    const text = new TextDecoder().decode(new Uint8Array(this.x.memory.buffer, ptr, len));
    this.x.nomi_free(ptr, len);
    return JSON.parse(text);
  }

  /**
   * Boots the program, or with null the one built into the module, and
   * answers its exports' signatures.
   * @param {Uint8Array | null} bytes
   * @returns {string[]}
   */
  load(bytes) {
    const answer = this.enter("loading", () => {
      if (bytes === null) return this.answer(this.x.nomi_load(0, 0));
      const [ptr, len] = this.put(bytes);
      const answer = this.answer(this.x.nomi_load(ptr, len));
      this.x.nomi_free(ptr, len);
      return answer;
    });
    if (answer.error !== undefined) {
      this.close();
      throw new Error(answer.error);
    }
    return answer.ok;
  }

  /** @param {string} name @param {string} args a JSON array */
  call(name, args) {
    const answer = this.enter(`calling \`${name}\``, () => {
      const [namePtr, nameLen] = this.put(encoder.encode(name));
      const [argsPtr, argsLen] = this.put(encoder.encode(args));
      const answer = this.answer(this.x.nomi_call(namePtr, nameLen, argsPtr, argsLen));
      this.x.nomi_free(namePtr, nameLen);
      this.x.nomi_free(argsPtr, argsLen);
      return answer;
    });
    if (answer.error !== undefined) throw new Error(answer.error);
    return answer.ok;
  }

  /** Runs `fn main`, answers its exit status, and closes the instance. @returns {number} */
  main() {
    try {
      return this.x.nomi_main();
    } catch (e) {
      if (e instanceof Exit) return e.code;
      throw e;
    } finally {
      this.close();
    }
  }

  close() {
    this.closed = true;
    this.x = /** @type {any} */ (null);
  }
}

/** Hands text to write as UTF-8 arrives, across writes that split a character. @param {(text: string) => void} write */
function decoding(write) {
  const decoder = new TextDecoder();
  /** @param {Uint8Array} bytes */
  return (bytes) => { const text = decoder.decode(bytes, { stream: true }); if (text) write(text); };
}

/** Reads now from a clock, in nanoseconds. @param {number} id */
function clockNs(id) {
  const ms = id === 0 ? performance.timeOrigin + performance.now() : performance.now();
  return BigInt(Math.round(ms * 1e6));
}

/** @type {Int32Array | null | undefined} */
let waitCell;

/** Blocks the thread for ms milliseconds. @param {number} ms */
function block(ms) {
  if (waitCell === undefined) {
    try {
      waitCell = new Int32Array(new SharedArrayBuffer(4));
      Atomics.wait(waitCell, 0, 0, 0);
    } catch {
      waitCell = null;
    }
  }
  if (waitCell) {
    Atomics.wait(waitCell, 0, 0, ms);
    return;
  }
  const end = performance.now() + ms;
  while (performance.now() < end) { /* spin: this thread may not block */ }
}

/**
 * The wasi_snapshot_preview1 functions, over host's memory.
 * @param {Host} host
 * @param {{ args: Uint8Array[], env: Uint8Array[], stdin: Uint8Array,
 *           sinks: Record<number, (bytes: Uint8Array) => void> }} io
 */
function wasiImports(host, { args, env, stdin, sinks }) {
  let stdinAt = 0;
  const bytes = () => new Uint8Array(host.x.memory.buffer);
  const isStdio = (/** @type {number} */ fd) => fd >= 0 && fd <= 2;

  /** Writes strings and their pointers, as args_get and environ_get do. */
  const list = (/** @type {Uint8Array[]} */ items) => ({
    sizes(/** @type {number} */ countPtr, /** @type {number} */ sizePtr) {
      const v = host.view();
      v.setUint32(countPtr, items.length, true);
      v.setUint32(sizePtr, items.reduce((n, s) => n + s.length, 0), true);
      return ESUCCESS;
    },
    get(/** @type {number} */ ptrs, /** @type {number} */ buf) {
      const v = host.view(), mem = bytes();
      for (const [i, s] of items.entries()) {
        v.setUint32(ptrs + 4 * i, buf, true);
        mem.set(s, buf);
        buf += s.length;
      }
      return ESUCCESS;
    },
  });
  const argList = list(args), envList = list(env);

  /** The iovec buffers at iovs. @param {number} iovs @param {number} count */
  const buffers = (iovs, count) => {
    const v = host.view();
    return Array.from({ length: count }, (_, i) =>
      new Uint8Array(host.x.memory.buffer, v.getUint32(iovs + 8 * i, true), v.getUint32(iovs + 8 * i + 4, true)));
  };

  return {
    args_sizes_get: argList.sizes,
    args_get: argList.get,
    environ_sizes_get: envList.sizes,
    environ_get: envList.get,

    clock_res_get(/** @type {number} */ id, /** @type {number} */ ptr) {
      host.view().setBigUint64(ptr, 1000n, true);
      return ESUCCESS;
    },
    clock_time_get(/** @type {number} */ id, /** @type {bigint} */ _precision, /** @type {number} */ ptr) {
      host.view().setBigUint64(ptr, clockNs(id), true);
      return ESUCCESS;
    },

    random_get(/** @type {number} */ ptr, /** @type {number} */ len) {
      for (let at = 0; at < len; at += 65536) {
        crypto.getRandomValues(new Uint8Array(host.x.memory.buffer, ptr + at, Math.min(65536, len - at)));
      }
      return ESUCCESS;
    },

    fd_write(/** @type {number} */ fd, /** @type {number} */ iovs, /** @type {number} */ count, /** @type {number} */ nPtr) {
      const sink = sinks[fd];
      if (!sink) return EBADF;
      let n = 0;
      for (const buf of buffers(iovs, count)) {
        sink(buf.slice());
        n += buf.length;
      }
      host.view().setUint32(nPtr, n, true);
      return ESUCCESS;
    },
    fd_read(/** @type {number} */ fd, /** @type {number} */ iovs, /** @type {number} */ count, /** @type {number} */ nPtr) {
      if (fd !== 0) return EBADF;
      let n = 0;
      for (const buf of buffers(iovs, count)) {
        const chunk = stdin.subarray(stdinAt, stdinAt + buf.length);
        buf.set(chunk);
        stdinAt += chunk.length;
        n += chunk.length;
        if (chunk.length < buf.length) break;
      }
      host.view().setUint32(nPtr, n, true);
      return ESUCCESS;
    },
    fd_close: (/** @type {number} */ fd) => (isStdio(fd) ? ESUCCESS : EBADF),
    fd_seek: (/** @type {number} */ fd) => (isStdio(fd) ? ESPIPE : EBADF),
    fd_fdstat_get(/** @type {number} */ fd, /** @type {number} */ ptr) {
      if (!isStdio(fd)) return EBADF;
      const v = host.view();
      bytes().fill(0, ptr, ptr + 24);
      v.setUint8(ptr, 2); // character device
      v.setBigUint64(ptr + 8, 0x1fffffffn, true);
      v.setBigUint64(ptr + 16, 0x1fffffffn, true);
      return ESUCCESS;
    },
    fd_fdstat_set_flags: (/** @type {number} */ fd) => (isStdio(fd) ? ESUCCESS : EBADF),
    fd_filestat_get(/** @type {number} */ fd, /** @type {number} */ ptr) {
      if (!isStdio(fd)) return EBADF;
      bytes().fill(0, ptr, ptr + 64);
      host.view().setUint8(ptr + 16, 2); // character device
      return ESUCCESS;
    },
    // No directory is preopened, so there is no file to open or stat.
    fd_prestat_get: () => EBADF,
    fd_prestat_dir_name: () => EBADF,
    path_open: () => EBADF,
    path_filestat_get: () => EBADF,

    // Answers each subscription that is ready: a read or write on stdio at
    // once (stdin never waits; it is all here), and otherwise the clocks,
    // after blocking until the soonest is due. Go's scheduler calls it with
    // one clock when every goroutine waits on a timer.
    poll_oneoff(/** @type {number} */ inPtr, /** @type {number} */ outPtr, /** @type {number} */ n, /** @type {number} */ nPtr) {
      const v = host.view();
      const start = clockNs(1);
      /** @type {[bigint, number, number, bigint][]} userdata, type, errno, nbytes */
      const events = [];
      /** @type {[bigint, bigint][]} userdata, nanoseconds from start */
      const clocks = [];
      for (let i = 0; i < n; i++) {
        const s = inPtr + 48 * i;
        const userdata = v.getBigUint64(s, true), type = v.getUint8(s + 8);
        if (type === 0) {
          const id = v.getUint32(s + 16, true);
          let timeout = v.getBigInt64(s + 24, true);
          if (v.getUint16(s + 40, true) & 1) timeout -= clockNs(id);
          clocks.push([userdata, timeout]);
        } else if (type === 1 || type === 2) {
          const fd = v.getUint32(s + 16, true);
          const left = type === 1 && fd === 0 ? BigInt(stdin.length - stdinAt) : 0n;
          events.push([userdata, type, isStdio(fd) ? ESUCCESS : EBADF, left]);
        } else {
          return EINVAL;
        }
      }
      if (events.length === 0 && clocks.length > 0) {
        const soonest = clocks.reduce((m, [, t]) => (t < m ? t : m), clocks[0][1]);
        for (let left = soonest; left > 0n; left = soonest - (clockNs(1) - start)) block(Number(left) / 1e6);
      }
      const elapsed = clockNs(1) - start;
      for (const [userdata, timeout] of clocks) if (timeout <= elapsed) events.push([userdata, 0, ESUCCESS, 0n]);
      const mem = bytes();
      for (const [i, [userdata, type, errno, nbytes]] of events.entries()) {
        const e = outPtr + 32 * i;
        mem.fill(0, e, e + 32);
        v.setBigUint64(e, userdata, true);
        v.setUint16(e + 8, errno, true);
        v.setUint8(e + 10, type);
        v.setBigUint64(e + 16, nbytes, true);
      }
      v.setUint32(nPtr, events.length, true);
      return ESUCCESS;
    },
    sched_yield: () => ESUCCESS,
    proc_exit(/** @type {number} */ code) {
      throw new Exit(code);
    },
  };
}
