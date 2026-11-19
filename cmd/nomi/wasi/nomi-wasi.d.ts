// Types for nomi-wasi.js, which `nomi build-vm --reactor` writes beside
// the WASI reactor, and `nomi build --target wasip1/wasm` beside the
// reactor it writes for a program with an `export fn`.

/** Where bytes come from: a URL (a file path in Node), a Response, or the bytes. */
export type Source = string | URL | Response | ArrayBuffer | ArrayBufferView;

export interface Options {
  /** The program's arguments (Startup.args). */
  args?: string[];
  /** The program's environment (Startup.env). */
  env?: Record<string, string>;
  /** All of the program's standard input; it reads end of input after it. */
  stdin?: string | Uint8Array;
  /** Receives stdout as it is written, in place of capturing it. */
  onStdout?: (text: string) => void;
  /** Receives stderr as it is written, in place of capturing it. */
  onStderr?: (text: string) => void;
  /**
   * The std modules the program may reach outside itself through, such as
   * ["std::io"]. An empty list allows nothing; without it these calls are
   * not held. A program that reaches more is refused at the load, before it
   * runs.
   */
  std?: string[];
  /**
   * The keys of the host functions linked into the reactor that the
   * program may call, such as ["lookup"]. With std given and host not, it
   * may call none; host alone holds only host functions.
   */
  host?: string[];
  /**
   * Milliseconds the boot, and each later call and run of main, may run
   * before it is stopped. A stopped call rejects, and the program stays
   * loaded.
   */
  timeoutMs?: number;
}

export interface RunResult {
  /** 0, or 1 when main failed (the reason is on stderr). */
  status: number;
  /** stdout and stderr in the order they were written. */
  output: string;
  stdout: string;
  stderr: string;
}

/** Compiles the reactor once; each load and run instantiates it afresh. */
export function loadRuntime(source: Source | WebAssembly.Module): Promise<Runtime>;

export class Runtime {
  readonly module: WebAssembly.Module;
  /**
   * Loads a .nomic program into its own instance, or with no source the
   * program built into the module. Rejects with the load error, such as a
   * program built for another program format, a .nomic handed to a module
   * with a program built in, or no source for one without. Output goes to
   * the callbacks, or to console.log and console.error by line.
   */
  load(source?: Source, options?: Options): Promise<Program>;
  /**
   * Loads a .nomic program into its own instance, or with no source the
   * program built into the module, and runs its `fn main`.
   */
  run(source?: Source, options?: Options): Promise<RunResult>;
}

export class Program {
  /** Each exported function's signature, such as "add(a: Int, b: Int): Int". */
  readonly exports: string[];
  /**
   * Calls an exported function. Arguments are JSON-encoded and the result
   * JSON-decoded. An Int is exact at any size: one past
   * Number.MAX_SAFE_INTEGER arrives as a BigInt, and a BigInt argument
   * crosses as the Int it holds. A failed call rejects with an Error whose
   * message is the Nomi diagnostic, and the program stays usable.
   */
  call<T = unknown>(name: string, ...args: unknown[]): Promise<T>;
  /** Drops the program's instance; later calls reject. */
  close(): void;
}
