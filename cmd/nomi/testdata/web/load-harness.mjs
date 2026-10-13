// Drives `nomi build --web`'s nomi.js load() under Node for TestBuildWeb
// (cmd/nomi/build_web_test.go), with browser.mjs's Worker and fetch.
//
// usage: node load-harness.mjs '<json>' where json is
//   { loads: [{ folder, calls: [[name, ...args]], closeThenCall? }] }
// and it prints one JSON array with, per load, { stdout, stderr, error,
// results, exports, afterClose }: error is load's rejection, results one
// { value } or { error } per call (a value of undefined is reported as
// { undefined: true }), exports what exports() resolved with, and
// afterClose the rejection of a call made after close().
import { pathToFileURL } from "node:url";
import "./browser.mjs";

const spec = JSON.parse(process.argv[2]);
const out = [];
for (const l of spec.loads) {
  const folder = pathToFileURL(l.folder.endsWith("/") ? l.folder : l.folder + "/");
  const { load } = await import(new URL("nomi.js", folder).href);
  let stdout = "";
  let stderr = "";
  const result = { stdout: "", stderr: "", error: "", results: [], exports: null, afterClose: "" };
  let nomi;
  try {
    nomi = await load("app.wasm", { stdout: (t) => (stdout += t), stderr: (t) => (stderr += t) });
  } catch (err) {
    result.error = String(err?.message ?? err);
  }
  if (nomi) {
    // Every call is made before any settles: they run in order.
    const settled = await Promise.allSettled(l.calls.map(([name, ...args]) => nomi.call(name, ...args)));
    for (const s of settled) {
      if (s.status === "rejected") {
        result.results.push({ error: s.reason instanceof Error ? s.reason.message : "not an Error: " + String(s.reason) });
      } else if (s.value === undefined) {
        result.results.push({ undefined: true });
      } else {
        result.results.push({ value: s.value });
      }
    }
    result.exports = await nomi.exports();
    nomi.close();
    if (l.closeThenCall) {
      await nomi.call(l.calls[0][0], ...l.calls[0].slice(1)).then(
        () => (result.afterClose = "resolved"),
        (err) => (result.afterClose = String(err?.message ?? err)),
      );
    }
  }
  result.stdout = stdout;
  result.stderr = stderr;
  out.push(result);
}
process.stdout.write(JSON.stringify(out));
process.exit(0);
