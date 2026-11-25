// Headless Chrome for the tests that load tour-client.mjs into a page:
// findChrome finds its binary (NOMI_CHROME names it), serve answers the
// test's page at / and the client's files under /nomi/, and openPage drives
// a page over the DevTools protocol.
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

export const TOUR = fileURLToPath(new URL("..", import.meta.url));

export function findChrome() {
  const candidates = [
    process.env.NOMI_CHROME,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ];
  return candidates.find((p) => p && existsSync(p)) || null;
}

const TYPES = { ".mjs": "text/javascript", ".js": "text/javascript", ".wasm": "application/wasm", ".scm": "text/plain" };

// serve answers page at / and the client's files under /nomi/.
export function serve(page) {
  const server = createServer((req, res) => {
    const path = new URL(req.url, "http://localhost").pathname;
    if (path === "/") {
      res.writeHead(200, { "content-type": "text/html" });
      res.end(page);
      return;
    }
    const name = path.replace(/^\/nomi\//, "");
    // The client as the source tree holds it, the rest as staged.
    let file = null;
    if (name === "tour-client.mjs" || name === "highlight.mjs") file = join(TOUR, "src/lib", name);
    else if (path.startsWith("/nomi/") && !name.includes("..")) file = join(TOUR, "public/nomi", name);
    if (!file || !existsSync(file)) {
      res.writeHead(404);
      res.end();
      return;
    }
    const ext = name.slice(name.lastIndexOf("."));
    res.writeHead(200, { "content-type": TYPES[ext] || "application/octet-stream" });
    res.end(readFileSync(file));
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

// openPage starts headless Chrome on url and answers evaluate(expression),
// which awaits the expression's promise over the DevTools protocol.
export async function openPage(chrome, url, profile) {
  const proc = spawn(chrome, [
    "--headless=new",
    "--disable-gpu",
    "--no-first-run",
    "--no-default-browser-check",
    `--user-data-dir=${profile}`,
    "--remote-debugging-port=0",
    "--window-size=1200,2400",
    url,
  ]);
  const exited = new Promise((resolve) => proc.on("exit", resolve));
  const close = () => {
    proc.kill();
    return exited;
  };
  try {
    const wsURL = await new Promise((resolve, reject) => {
      let err = "";
      const timer = setTimeout(() => reject(new Error("Chrome did not start:\n" + err)), 20000);
      proc.stderr.on("data", (d) => {
        err += d;
        const m = /DevTools listening on (ws:\/\/\S+)/.exec(err);
        if (m) {
          clearTimeout(timer);
          resolve(m[1]);
        }
      });
      proc.on("exit", () => reject(new Error("Chrome exited:\n" + err)));
    });
    const port = new URL(wsURL).port;
    let target = null;
    for (let i = 0; i < 50 && !target; i++) {
      const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      target = targets.find((t) => t.type === "page" && t.url.startsWith(url));
      if (!target) await new Promise((r) => setTimeout(r, 100));
    }
    if (!target) throw new Error("Chrome opened no page at " + url);
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      ws.onopen = resolve;
      ws.onerror = reject;
    });
    let seq = 0;
    const waiting = new Map();
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      const done = waiting.get(m.id);
      if (done) {
        waiting.delete(m.id);
        done(m);
      }
    };
    const evaluate = (expression) =>
      new Promise((resolve, reject) => {
        const id = ++seq;
        waiting.set(id, (m) => {
          const thrown = m.result?.exceptionDetails;
          if (m.error) reject(new Error(m.error.message));
          else if (thrown) reject(new Error(thrown.exception?.description || thrown.text));
          else resolve(m.result.result.value);
        });
        ws.send(JSON.stringify({ id, method: "Runtime.evaluate", params: { expression, awaitPromise: true, returnByValue: true } }));
      });
    // Wait out the navigation to url, which replaces the page's first
    // execution context.
    for (let i = 0; ; i++) {
      const state = await evaluate(`location.href + " " + document.readyState`).catch(() => "");
      if (state === url + " complete") break;
      if (i === 200) throw new Error("Chrome did not load " + url + ": " + state);
      await new Promise((r) => setTimeout(r, 50));
    }
    return {
      evaluate,
      close() {
        ws.close();
        return close();
      },
    };
  } catch (err) {
    await close();
    throw err;
  }
}
