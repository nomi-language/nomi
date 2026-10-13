// The inside of browser.mjs's Worker under Node's
// worker_threads: it gives the module worker the globals a dedicated worker
// has (self an instance of WorkerGlobalScope, postMessage, onmessage,
// addEventListener and no document), then imports it.
import { parentPort, workerData } from "node:worker_threads";

globalThis.self = globalThis;
globalThis.WorkerGlobalScope = class WorkerGlobalScope {
  static [Symbol.hasInstance](value) {
    return value === globalThis;
  }
};
globalThis.postMessage = (msg) => parentPort.postMessage(msg);
globalThis.addEventListener = () => {};
parentPort.on("message", (data) => globalThis.onmessage?.({ data }));
await import(workerData.url);
