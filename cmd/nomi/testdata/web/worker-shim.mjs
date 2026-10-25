// The inside of browser.mjs's Worker under Node's
// worker_threads: it gives the module worker the globals a dedicated worker
// has (self an instance of WorkerGlobalScope, postMessage, onmessage,
// addEventListener and no document) and a fetch that reads files, as
// browser.mjs's does (a program's Locale.load fetches its locale files from
// here), then imports it.
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { parentPort, workerData } from "node:worker_threads";

globalThis.self = globalThis;
globalThis.WorkerGlobalScope = class WorkerGlobalScope {
  static [Symbol.hasInstance](value) {
    return value === globalThis;
  }
};
globalThis.postMessage = (msg) => parentPort.postMessage(msg);
globalThis.addEventListener = () => {};
globalThis.fetch = async (url) => {
  try {
    return new Response(await readFile(fileURLToPath(new URL(url))));
  } catch {
    return new Response("not found", { status: 404 });
  }
};
parentPort.on("message", (data) => globalThis.onmessage?.({ data }));
await import(workerData.url);
