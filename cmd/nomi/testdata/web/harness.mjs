// Drives `nomi build --web`'s nomi.js under Node for
// TestBuildWeb_RunsUnderNode (cmd/nomi/build_web_test.go).
//
// browser.mjs gives Node a browser's Worker and a fetch that reads files.
// Everything else (compiling the module, posting it to the worker, Go's
// wasm_exec.js, the fs shim, stdin, exit codes, timeouts) is the code the
// browser runs.
//
// usage: node harness.mjs '<json>' where json is
//   { runs: [{ folder, contentType?, stdin?, stdinLines?, args?, env?,
//              timeoutMs?, abortAfterMs?, worker? }] }
// and it prints one JSON array of { stdout, stderr, exitCode, fatal,
// events, fetches }.
import { pathToFileURL } from "node:url";
import { fetches, setContentType } from "./browser.mjs";

const spec = JSON.parse(process.argv[2]);
const results = [];
for (const r of spec.runs) {
  const folder = pathToFileURL(r.folder.endsWith("/") ? r.folder : r.folder + "/");
  const { run } = await import(new URL("nomi.js", folder).href);
  setContentType(r.contentType ?? "application/wasm");
  const events = [];
  let stdout = "";
  let stderr = "";
  const options = {
    args: r.args,
    env: r.env,
    stdout: (text) => {
      stdout += text;
      events.push("stdout:" + text);
    },
    stderr: (text) => {
      stderr += text;
      events.push("stderr:" + text);
    },
    timeoutMs: r.timeoutMs,
    worker: r.worker,
  };
  if (typeof r.stdin === "string") options.stdin = r.stdin;
  if (Array.isArray(r.stdinLines)) {
    const lines = [...r.stdinLines];
    options.stdin = async () => {
      events.push("read");
      await new Promise((resolve) => setTimeout(resolve, 5));
      return lines.length ? lines.shift() : null;
    };
  }
  if (r.abortAfterMs !== undefined) {
    const controller = new AbortController();
    setTimeout(() => controller.abort(), r.abortAfterMs);
    options.signal = controller.signal;
  }
  const result = await run("app.wasm", options).then(
    (res) => res,
    (err) => ({ exitCode: -1, fatal: "run rejected: " + String(err?.message ?? err) }),
  );
  results.push({
    stdout,
    stderr,
    exitCode: result.exitCode,
    fatal: result.fatal ?? "",
    events,
    // nomi.js resolves app.wasm against its own import.meta.url, which
    // Node gives with symlinks resolved (/private/var for /var on macOS).
    fetches: [...fetches]
      .filter(([href]) => href.endsWith(new URL("app.wasm", folder).pathname.split("/").slice(-2).join("/")))
      .reduce((sum, [, n]) => sum + n, 0),
  });
}
process.stdout.write(JSON.stringify(results));
// A main-thread run that timed out keeps its timers; nothing else should
// keep Node alive.
process.exit(0);
