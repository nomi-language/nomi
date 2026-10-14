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
   * JSON-decoded, so an Int past 2^53 loses precision. A failed call
   * rejects with an Error whose message is the Nomi diagnostic, and the
   * program stays usable.
   */
  call<T = unknown>(name: string, ...args: unknown[]): Promise<T>;
  /** Drops the program's instance; later calls reject. */
  close(): void;
}
