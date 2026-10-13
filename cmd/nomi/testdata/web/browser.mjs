// The browser globals harness.mjs and load-harness.mjs give nomi.js under
// Node.
//
// Node has no browser Worker, so this defines one over worker_threads that
// starts worker-shim.mjs, which gives the worker's copy of nomi.js a
// dedicated worker's globals. Node's fetch cannot read file: URLs, so fetch
// is replaced by one that reads the file and answers with the content type
// setContentType chose, and counts requests in fetches.
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Worker as NodeWorker } from "node:worker_threads";

const shim = new URL("./worker-shim.mjs", import.meta.url);

globalThis.Worker = class {
  constructor(url, options) {
    if (options?.type !== "module") throw new Error("nomi.js must start a module worker");
    this.onmessage = null;
    this.onerror = null;
    this.worker = new NodeWorker(shim, { workerData: { url: String(url) } });
    this.worker.on("message", (data) => this.onmessage?.({ data }));
    this.worker.on("error", (err) => this.onerror?.({ message: String(err?.message ?? err), preventDefault() {} }));
  }
  postMessage(msg) {
    this.worker.postMessage(msg);
  }
  terminate() {
    this.worker.terminate();
  }
};

let contentType = "application/wasm";

/** setContentType sets the content type fetch answers app.wasm with. */
export function setContentType(type) {
  contentType = type;
}

/** fetches counts the requests for each URL. */
export const fetches = new Map();

globalThis.fetch = async (url) => {
  const u = new URL(url);
  fetches.set(u.href, (fetches.get(u.href) ?? 0) + 1);
  const data = await readFile(fileURLToPath(u));
  return new Response(data, { headers: { "content-type": contentType } });
};
