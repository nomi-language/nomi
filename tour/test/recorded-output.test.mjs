// A ```nomi-run block runs nothing when its page loads: its output panel
// shows the output its expect block records, and the reader's first edit
// runs it. A manual block's panel says the output is recorded, and only its
// Run button runs it. A block with no record (```nomi-run no-run) runs on
// load.
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
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import rehypeShiki from "../src/lib/rehype-shiki.mjs";
import { TOUR, findChrome, openPage, serve } from "./chrome.mjs";

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

test("the build carries each block's expect block into the page", async () => {
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
  assert.equal(recordedSpan(plain.pre), "x < y", "a block that is not manual carries its record too");

  const none = runBlock("", [{ type: "element", tagName: "p", properties: {}, children: [] }]);
  await transform(none.tree);
  assert.equal(recordedSpan(none.pre), "", "a block with no expect block prints nothing");

  const noRun = runBlock("no-run", [{ type: "element", tagName: "p", properties: {}, children: [] }]);
  await transform(noRun.tree);
  assert.equal(recordedSpan(noRun.pre), null, "a no-run block has no record and runs on load");
});

// The page: a single-file manual block, a multi-file manual error block
// that stops while it runs, a block that is not manual, one with no record
// (a no-run block) and an error block that does not compile, as
// rehype-shiki.mjs writes them, and a run worker faked to record each
// message and answer it at once: a source holding `compile-me` fails to
// compile and one holding `stop-me` stops after printing. While
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
          if (m.source.includes("compile-me")) {
            send({ id: m.id, kind: "run", output: "", error: "main.nomi:4:1: bad\\nmain.nomi:4:1: help: fix it", stage: "compile" });
          } else if (m.source.includes("stop-me")) {
            send({ id: m.id, kind: "run", output: "partial\\n", error: "line 9: division by zero", stage: "run" });
          } else {
            send({ id: m.id, kind: "run", output: "ran " + m.source.length + "\\n", error: "" });
          }
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
<pre class="nomi-tour"><code class="language-nomi-run">fn main() {
    io.print("three")
}
</code><span hidden data-nomi-recorded>three</span></pre>
<pre class="nomi-tour"><code class="language-nomi-run">fn main() {}
</code></pre>
<pre class="nomi-tour" data-nomi-expect="error"><code class="language-nomi-run">fn main() {
    x = 1
}
</code><span hidden data-nomi-recorded>main.nomi:2:5: unused binding x
main.nomi:2:5: help: write _ = 1</span></pre>
</body></html>`;

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

test("a block shows its recorded output on load and runs when edited, a manual one on Run", async (t) => {
  const chrome = findChrome();
  if (!chrome) return t.skip("no Chrome; NOMI_CHROME names its binary");
  if (!existsSync(join(TOUR, "public/nomi/web-tree-sitter.js"))) {
    return t.skip("public/nomi/ is not staged; run scripts/build-tour-wasm.sh");
  }
  const server = await serve(PAGE);
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-chrome-"));
  let page = null;
  try {
    page = await openPage(chrome, `http://127.0.0.1:${server.address().port}/`, profile);
    const loaded = await page.evaluate(`(async () => {${IN_PAGE}
      await waitFor("the five blocks", () => widgets().length === 5 && runs().length === 1 && sems().length === 4);
      // Time for a stray run on load to arrive.
      await new Promise((r) => setTimeout(r, 500));
      return {
        runs: runs().map((m) => m.source),
        sems: sems().map((m) => m.files || null),
        panels: widgets().map(panel),
      };
    })()`);
    assert.deepEqual(loaded.runs, ["fn main() {}"], "only the block with no record runs on load");
    assert.deepEqual(loaded.sems, [null, ["main.nomi"], null, null], "each recorded block asks for its tokens alone");
    assert.deepEqual(loaded.panels[0], {
      out: "recorded <one>\nsecond line\n",
      title: "Output",
      note: "Recorded output. Press Run to run it.",
    });
    assert.deepEqual(loaded.panels[1], {
      out: "two\nStopped: boom",
      title: "Output",
      note: "Recorded output. Press Run to run it.",
    });
    assert.deepEqual(loaded.panels[2], { out: "three\n", title: "Output", note: null });
    assert.deepEqual(loaded.panels[3], { out: "ran 12\n", title: "Output", note: null });
    assert.deepEqual(loaded.panels[4], {
      out: "main.nomi:2:5: unused binding x\nmain.nomi:2:5: help: write _ = 1",
      title: "Didn't compile",
      note: null,
    });
    const styled = await page.evaluate(`(async () => {${IN_PAGE}
      const of = (w, sel) => [...widgets()[w].querySelectorAll(".nomi-out " + sel)].map((s) => s.textContent);
      return {
        stop: of(1, ".nomi-stop"),
        label: of(1, ".nomi-stop-label"),
        errors: of(4, ".nomi-err"),
        asides: of(4, ".nomi-diag-aside"),
      };
    })()`);
    assert.deepEqual(styled, {
      stop: ["Stopped: boom"],
      label: ["Stopped"],
      errors: ["main.nomi:2:5: unused binding x"],
      asides: ["main.nomi:2:5: help: write _ = 1"],
    }, "a recorded stop closes the output on a line of its own, and a compile error's help is set apart");

    const live = await page.evaluate(`(async () => {${IN_PAGE}
      const w = widgets()[3];
      const ta = w.querySelector("textarea");
      const edit = async (text, done) => {
        ta.value = text;
        ta.dispatchEvent(new Event("input"));
        await waitFor("the edit's run", () => done(panel(w)));
        return { ...panel(w), stop: [...w.querySelectorAll(".nomi-stop")].map((s) => s.textContent) };
      };
      return {
        compile: await edit("// compile-me", (p) => p.title !== "Output"),
        stop: await edit("// stop-me", (p) => p.out.startsWith("partial")),
        clean: await edit("fn main() {}", (p) => p.out.startsWith("ran")),
      };
    })()`);
    assert.deepEqual(live, {
      compile: { out: "main.nomi:4:1: bad\nmain.nomi:4:1: help: fix it", title: "Didn't compile", note: null, stop: [] },
      stop: { out: "partial\nStopped at line 9: division by zero", title: "Output", note: null, stop: ["Stopped at line 9: division by zero"] },
      clean: { out: "ran 12\n", title: "Output", note: null, stop: [] },
    }, "a live run's panel tells a compile error from a run that stopped");

    const edited = await page.evaluate(`(async () => {${IN_PAGE}
      const ta = widgets()[2].querySelector("textarea");
      ta.value = ta.value + " ";
      ta.dispatchEvent(new Event("input"));
      await waitFor("the edit's run", () => panel(widgets()[2]).out.startsWith("ran"));
      return { runs: runs().map((m) => m.source), panel: panel(widgets()[2]) };
    })()`);
    assert.deepEqual(edited.runs.slice(-1), ['fn main() {\n    io.print("three")\n} ']);
    assert.deepEqual(edited.panel, { out: "ran 36\n", title: "Output", note: null });

    const ran = await page.evaluate(`(async () => {${IN_PAGE}
      widgets()[0].querySelector(".nomi-run-button").click();
      await waitFor("the run's output", () => panel(widgets()[0]).out.startsWith("ran"));
      return { runs: runs().map((m) => m.source), panel: panel(widgets()[0]) };
    })()`);
    assert.deepEqual(ran.runs.slice(-1), ['fn main() {\n    io.print("one")\n}']);
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
  const server = await serve(PAGE);
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-chrome-"));
  let page = null;
  try {
    page = await openPage(chrome, `http://127.0.0.1:${server.address().port}/`, profile);
    await page.evaluate(`(async () => {${IN_PAGE}
      await waitFor("the first run's output", () => widgets().length === 5 && panel(widgets()[3]).out === "ran 12\\n");
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
      out: "recorded <one>\nsecond line\n",
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
      const w = widgets()[3];
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
