// A ```nomi-run manual block runs nothing when its page loads: its output
// panel shows the output its expect block records, with a note saying so,
// and only its Run button runs it.
//
// The first test holds rehype-shiki.mjs to carrying the expect block into
// the page. The second loads tour-client.mjs into headless Chrome over the
// DevTools protocol, with a fake run worker that records what the page
// asks of it. It needs the staged assets in public/nomi/
// (scripts/build-tour-wasm.sh) and Chrome (NOMI_CHROME names its binary),
// and skips without them.
//
//   npm --prefix tour test
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import rehypeShiki from "../src/lib/rehype-shiki.mjs";

const TOUR = fileURLToPath(new URL("..", import.meta.url));

function runBlock(meta, after) {
  const pre = {
    type: "element",
    tagName: "pre",
    properties: {},
    children: [
      {
        type: "element",
        tagName: "code",
        properties: { className: ["language-nomi-run"] },
        data: { meta },
        children: [{ type: "text", value: 'fn main() {\n    io.print("x < y")\n}\n' }],
      },
    ],
  };
  return { tree: { type: "root", children: [pre, { type: "text", value: "\n" }, ...after] }, pre };
}

function recordedSpan(pre) {
  const span = pre.children.find((c) => c.tagName === "span" && "dataNomiRecorded" in (c.properties || {}));
  return span ? span.children.map((c) => c.value).join("") : null;
}

test("the build carries a manual block's expect block into the page", async () => {
  const transform = rehypeShiki();
  const viaComment = runBlock("manual", [{ type: "comment", value: " expect\nx < y\n--- & more\n" }]);
  await transform(viaComment.tree);
  assert.equal(viaComment.pre.properties.dataNomiManual, "1");
  assert.equal(recordedSpan(viaComment.pre), "x < y\n--- & more");

  const viaRaw = runBlock("manual error", [{ type: "raw", value: "<!-- expect\nx < y\n-->" }]);
  await transform(viaRaw.tree);
  assert.equal(recordedSpan(viaRaw.pre), "x < y");

  const plain = runBlock("", [{ type: "comment", value: " expect\nx < y\n" }]);
  await transform(plain.tree);
  assert.equal(recordedSpan(plain.pre), null, "a block that is not manual runs on load and carries nothing");

  const none = runBlock("manual", [{ type: "element", tagName: "p", properties: {}, children: [] }]);
  await transform(none.tree);
  assert.equal(recordedSpan(none.pre), null);
});

function findChrome() {
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

// The page: a single-file manual block, a multi-file manual error block and
// a block that is not manual, as rehype-shiki.mjs writes them, and a run
// worker faked to record each message and answer it at once. While
// window.holdRuns is set, it holds each run instead, until the page calls
// window.startHeld() (the worker has loaded and starts the run) and then
// window.finishHeld() (the run's output).
const PAGE = `<!doctype html>
<html><head><meta charset="utf-8">
<script>
window.posted = [];
window.holdRuns = false;
window.held = [];
window.startHeld = () => {
  for (const h of window.held) h.send({ id: h.m.id, kind: "start" });
};
window.finishHeld = () => {
  for (const h of window.held.splice(0)) h.finish();
};
window.Worker = class {
  postMessage(m) {
    window.posted.push(m);
    setTimeout(() => {
      const send = (data) => this.onmessage && this.onmessage({ data });
      const tokens = m.files ? {} : [];
      if (m.op === "sem") send({ id: m.id, op: "sem", semtokens: tokens });
      else if (m.op === "format" || m.op === "formatTestBody") send({ id: m.id, op: "format", output: m.source, error: "" });
      else if (m.op === "hover") send({ id: m.id, op: "hover", markdown: "" });
      else {
        const finish = () => {
          send({ id: m.id, kind: "sem", semtokens: tokens });
          send({ id: m.id, kind: "run", output: "ran " + m.source.length + "\\n", error: "" });
        };
        if (window.holdRuns) window.held.push({ m, send, finish });
        else {
          send({ id: m.id, kind: "start" });
          finish();
        }
      }
    }, 0);
  }
  terminate() {}
};
</script>
<script type="module" src="/nomi/tour-client.mjs"></script>
</head><body>
<pre class="nomi-tour" data-nomi-manual="1"><code class="language-nomi-run">fn main() {
    io.print("one")
}
</code><span hidden data-nomi-recorded>recorded &lt;one&gt;
second line</span></pre>
<pre class="nomi-tour" data-nomi-manual="1" data-nomi-expect="error"><code class="language-nomi-run">// FILE: main.nomi
fn main() {
    io.print("two")
}

// FILE: notes.txt
two
</code><span hidden data-nomi-recorded>two
error: boom</span></pre>
<pre class="nomi-tour"><code class="language-nomi-run">fn main() {}
</code></pre>
</body></html>`;

const TYPES = { ".mjs": "text/javascript", ".js": "text/javascript", ".wasm": "application/wasm", ".scm": "text/plain" };

function serve() {
  const server = createServer((req, res) => {
    const path = new URL(req.url, "http://localhost").pathname;
    if (path === "/") {
      res.writeHead(200, { "content-type": "text/html" });
      res.end(PAGE);
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
async function openPage(chrome, url, profile) {
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

// Helpers each evaluation defines in the page.
const IN_PAGE = `
const waitFor = async (what, ok) => {
  for (let i = 0; i < 300; i++) {
    if (ok()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("timed out waiting for " + what + ": " + JSON.stringify(window.posted));
};
const runs = () => window.posted.filter((m) => !m.op);
const sems = () => window.posted.filter((m) => m.op === "sem");
const widgets = () => [...document.querySelectorAll(".nomi-interactive")];
const panel = (w) => {
  const note = w.querySelector(".nomi-out-note");
  return {
    out: w.querySelector(".nomi-out").textContent,
    title: w.querySelector(".nomi-out-title").textContent,
    note: note.hidden ? null : note.textContent,
  };
};
// running answers what a widget shows about a run in flight.
const running = (w) => {
  const status = w.querySelector(".nomi-out-status");
  const button = w.querySelector(".nomi-run-button");
  return {
    status: status.hidden ? null : status.textContent,
    announced: w.querySelector(".nomi-out-head [role=status]").textContent,
    dimmed: w.querySelector(".nomi-out").classList.contains("is-dimmed"),
    button: button && { text: button.textContent, disabled: button.getAttribute("aria-disabled") === "true" },
  };
};
`;

test("a manual block shows its recorded output on load and runs only on Run", async (t) => {
  const chrome = findChrome();
  if (!chrome) return t.skip("no Chrome; NOMI_CHROME names its binary");
  if (!existsSync(join(TOUR, "public/nomi/web-tree-sitter.js"))) {
    return t.skip("public/nomi/ is not staged; run scripts/build-tour-wasm.sh");
  }
  const server = await serve();
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-chrome-"));
  let page = null;
  try {
    page = await openPage(chrome, `http://127.0.0.1:${server.address().port}/`, profile);
    const loaded = await page.evaluate(`(async () => {${IN_PAGE}
      await waitFor("the three blocks", () => widgets().length === 3 && runs().length === 1 && sems().length === 2);
      // Time for a stray run on load to arrive.
      await new Promise((r) => setTimeout(r, 500));
      return {
        runs: runs().map((m) => m.source),
        sems: sems().map((m) => m.files || null),
        panels: widgets().map(panel),
      };
    })()`);
    assert.deepEqual(loaded.runs, ["fn main() {}"], "only the block that is not manual runs on load");
    assert.deepEqual(loaded.sems, [null, ["main.nomi"]], "each manual block asks for its tokens alone");
    assert.deepEqual(loaded.panels[0], {
      out: "recorded <one>\nsecond line",
      title: "Output",
      note: "Recorded output. Press Run to run it.",
    });
    assert.deepEqual(loaded.panels[1], {
      out: "two\nerror: boom",
      title: "This example fails on purpose:",
      note: "Recorded output. Press Run to run it.",
    });
    assert.deepEqual(loaded.panels[2], { out: "ran 12\n", title: "Output", note: null });

    const ran = await page.evaluate(`(async () => {${IN_PAGE}
      widgets()[0].querySelector(".nomi-run-button").click();
      await waitFor("the run's output", () => panel(widgets()[0]).out.startsWith("ran"));
      return { runs: runs().map((m) => m.source), panel: panel(widgets()[0]) };
    })()`);
    assert.deepEqual(ran.runs, ["fn main() {}", 'fn main() {\n    io.print("one")\n}']);
    assert.deepEqual(ran.panel, { out: "ran 33\n", title: "Output", note: null });
  } finally {
    await page?.close();
    server.close();
    rmSync(profile, { recursive: true, force: true, maxRetries: 5 });
  }
});

test("a run in flight shows its status, and a fast one shows none", async (t) => {
  const chrome = findChrome();
  if (!chrome) return t.skip("no Chrome; NOMI_CHROME names its binary");
  if (!existsSync(join(TOUR, "public/nomi/web-tree-sitter.js"))) {
    return t.skip("public/nomi/ is not staged; run scripts/build-tour-wasm.sh");
  }
  const server = await serve();
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-chrome-"));
  let page = null;
  try {
    page = await openPage(chrome, `http://127.0.0.1:${server.address().port}/`, profile);
    await page.evaluate(`(async () => {${IN_PAGE}
      await waitFor("the first run's output", () => widgets().length === 3 && panel(widgets()[2]).out === "ran 12\\n");
    })()`);

    // A manual block's run, held open: loading, then running with a
    // counter, over its recorded output dimmed.
    const loading = await page.evaluate(`(async () => {${IN_PAGE}
      window.holdRuns = true;
      widgets()[0].querySelector(".nomi-run-button").click();
      await waitFor("the held run", () => window.held.length === 1);
      // A press while it runs starts nothing.
      widgets()[0].querySelector(".nomi-run-button").click();
      await new Promise((r) => setTimeout(r, 100));
      return { runs: runs().length, out: panel(widgets()[0]).out, ...running(widgets()[0]) };
    })()`);
    assert.deepEqual(loading, {
      runs: 2,
      out: "recorded <one>\nsecond line",
      status: "Loading Nomi…",
      announced: "Loading Nomi…",
      dimmed: true,
      button: { text: "Running", disabled: true },
    });

    const started = await page.evaluate(`(async () => {${IN_PAGE}
      window.startHeld();
      await waitFor("the running status", () => running(widgets()[0]).status === "Running…");
      const first = running(widgets()[0]);
      await new Promise((r) => setTimeout(r, 2200));
      return { first, later: running(widgets()[0]) };
    })()`);
    assert.deepEqual(started.first, {
      status: "Running…",
      announced: "Running…",
      dimmed: true,
      button: { text: "Running", disabled: true },
    });
    assert.match(started.later.status, /^Running… [23]s$/);
    assert.equal(started.later.announced, "Running…", "the counter is not announced");

    const done = await page.evaluate(`(async () => {${IN_PAGE}
      window.finishHeld();
      await waitFor("the run's output", () => panel(widgets()[0]).out.startsWith("ran"));
      return { out: panel(widgets()[0]).out, ...running(widgets()[0]) };
    })()`);
    assert.deepEqual(done, {
      out: "ran 33\n",
      status: null,
      announced: "",
      dimmed: false,
      button: { text: "Run", disabled: false },
    });

    // A block that runs as the reader edits: a fast run shows no status,
    // one that takes longer than the delay shows it.
    const edits = await page.evaluate(`(async () => {${IN_PAGE}
      window.holdRuns = false;
      const w = widgets()[2];
      let shown = false;
      new MutationObserver(() => {
        if (!w.querySelector(".nomi-out-status").hidden) shown = true;
      }).observe(w.querySelector(".nomi-out-head"), { subtree: true, attributes: true, childList: true, characterData: true });
      const ta = w.querySelector("textarea");
      ta.value = "fn main() {} ";
      ta.dispatchEvent(new Event("input"));
      await waitFor("the fast run's output", () => panel(w).out === "ran 13\\n");
      await new Promise((r) => setTimeout(r, 500));
      const fast = { shown, ...running(w) };

      window.holdRuns = true;
      ta.value = "fn main() {}  ";
      ta.dispatchEvent(new Event("input"));
      await waitFor("the held run", () => window.held.length === 1);
      await new Promise((r) => setTimeout(r, 150));
      const early = running(w);
      await new Promise((r) => setTimeout(r, 300));
      const slow = running(w);
      window.startHeld();
      window.finishHeld();
      await waitFor("the slow run's output", () => panel(w).out === "ran 14\\n");
      return { fast, early, slow, after: running(w) };
    })()`);
    assert.deepEqual(edits.fast, { shown: false, status: null, announced: "", dimmed: false, button: null });
    assert.deepEqual(edits.early, { status: null, announced: "", dimmed: false, button: null });
    assert.deepEqual(edits.slow, { status: "Loading Nomi…", announced: "Loading Nomi…", dimmed: true, button: null });
    assert.deepEqual(edits.after, { status: null, announced: "", dimmed: false, button: null });
  } finally {
    await page?.close();
    server.close();
    rmSync(profile, { recursive: true, force: true, maxRetries: 5 });
  }
});
