// Runs a js/wasm Go test binary under Node with SQLite's official
// WebAssembly build loaded, as a page loads it for the browser driver
// (sqlite_js.go): Go's own wasm_exec_node.js, after
// `globalThis.sqlite3 = await sqlite3InitModule()`.
//
//	SQLITE_WASM_DIR=<the jswasm folder of sqlite-wasm-*.zip> \
//	  GOOS=js GOARCH=wasm go test -exec "node $PWD/testdata/wasm_exec_sqlite.cjs" .
//
// sqlite-wasm-3540100.zip is at https://sqlite.org/2026/sqlite-wasm-3540100.zip.
"use strict";

const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { pathToFileURL } = require("url");

const dir = process.env.SQLITE_WASM_DIR;
if (!dir) {
  console.error("wasm_exec_sqlite: set SQLITE_WASM_DIR to the jswasm folder of SQLite's WebAssembly build");
  process.exit(2);
}

(async () => {
  const { default: sqlite3InitModule } = await import(pathToFileURL(path.join(dir, "sqlite3.mjs")).href);
  const bytes = fs.readFileSync(path.join(dir, "sqlite3.wasm"));
  // Its OPFS file systems need a browser worker, and the tour leaves them
  // out too (run-worker.js).
  globalThis.sqlite3ApiConfig = {
    disable: { vfs: { opfs: true, "opfs-vfs": true, "opfs-wl": true, "opfs-sahpool": true } },
  };
  const warn = console.warn;
  console.warn = () => {};
  globalThis.sqlite3 = await sqlite3InitModule({
    print() {},
    printErr() {},
    instantiateWasm(imports, done) {
      WebAssembly.instantiate(bytes, imports).then((r) => done(r.instance, r.module));
      return {};
    },
  });
  console.warn = warn;

  const goroot = execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
  // Go's js/wasm runtime takes at most 12 KB of arguments and environment,
  // and wasm_exec_node.js passes all of the process's: keep what a test
  // reads.
  for (const key of Object.keys(process.env)) {
    if (!/^(HOME|PATH|TMPDIR|GO\w*)$/.test(key)) delete process.env[key];
  }
  // wasm_exec_node.js reads the binary from argv[2], where go test's -exec
  // puts it after this script.
  require(path.join(goroot, "lib", "wasm", "wasm_exec_node.js"));
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
