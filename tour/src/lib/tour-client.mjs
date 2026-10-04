// Client enhancer for the Starlight tour: finds runnable Nomi code blocks
// (Astro's syntax highlighting is off) and turns each into a live, editable,
// run-on-type example using the same analyzer and grammar as Zed:
//   syntax     web-tree-sitter (grammar + queries/highlights.scm) on this thread
//   semantics  the analyzer's tokens, from the Nomi wasm (async overlay)
//   run        debounced, in a Web Worker so a runaway program can't freeze the page
// Served statically from /nomi/ (no bundling); web-tree-sitter + the wasm load
// lazily when the first example scrolls into view.
import { render } from "./highlight.mjs";

const ASSET = (f) => new URL(f, import.meta.url).href;
const DEBOUNCE_MS = 300;
const FORMAT_DEBOUNCE_MS = 1500;
// wasm cold-load allowance. It covers the fetch and compile AND the worker's
// VM warm-up (vmhost.Warm), which lowers the whole standard library once:
// measured at about a minute under Node on a loaded machine, so the budget is
// generous rather than tight.
const LOAD_BUDGET_MS = 120000;
// execution allowance once running. A VM run lowers the block before running
// it, measured at about 1.5 s under Node on a loaded machine.
const RUN_BUDGET_MS = 6000;
const EDITOR_RENDER = Object.freeze({ showTrailingWhitespace: true });

// --- syntax engine (main thread), initialized once, lazily ---
let tsPromise = null;
function syntax() {
  if (!tsPromise) {
    tsPromise = (async () => {
      const { Parser, Language, Query } = await import(ASSET("web-tree-sitter.js"));
      await Parser.init({ locateFile: () => ASSET("web-tree-sitter.wasm") });
      const bytes = new Uint8Array(await (await fetch(ASSET("tree-sitter-nomi.wasm"))).arrayBuffer());
      const lang = await Language.load(bytes);
      const parser = new Parser();
      parser.setLanguage(lang);
      const query = new Query(lang, await (await fetch(ASSET("highlights.scm"))).text());

      // Vendored markdown pipeline for hover tooltips: marked parses
      // (CommonMark-compliant), DOMPurify sanitizes the resulting HTML
      // before innerHTML insertion. Hooked renderer routes Nomi code fences
      // through the tree-sitter highlighter so
      // signatures still render with the same colors Zed uses.
      const { marked } = await import(ASSET("marked.esm.js"));
      const DOMPurify = (await import(ASSET("purify.es.mjs"))).default;
      marked.use({
        renderer: {
          code(text, infostring) {
            const name = (infostring || "").trim().split(/\s+/)[0];
            if (!name || name === "nomi" || name === "nomi-run" || name === "nomi-test") {
              const caps = query.captures(parser.parse(text).rootNode);
              return `<pre class="nomi-tour">${render(caps, [], text)}</pre>`;
            }
            // Non-nomi fence — escape and emit a plain block.
            const esc = text.replace(/[&<>]/g, (c) => c === "&" ? "&amp;" : c === "<" ? "&lt;" : "&gt;");
            return `<pre><code>${esc}</code></pre>`;
          },
        },
      });
      return { parser, query, marked, DOMPurify };
    })();
  }
  return tsPromise;
}

// --- execution worker (shared per page), created lazily, killed on timeout ---
let worker = null;
const pending = new Map(); // reqId -> { msg, cbs, loadTimer, runTimer }
const formatCbs = new Map(); // reqId -> { cb, msg }
let reqSeq = 0;
let formatSeq = 0;

function ensureWorker() {
  if (worker) return;
  worker = new Worker(ASSET("run-worker.js"));
  worker.onmessage = (e) => {
    const m = e.data;
    if (m.type === "ready") return;
    if (m.type === "exited") {
      // A program stopped the Go runtime in this worker (deadlock, the 1 GiB
      // memory cap, the engine's stack). Its own reply has already arrived;
      // replace the worker and re-send whatever else was waiting on it.
      restartWorker();
      return;
    }
    if (m.type === "error") {
      // The worker could not load the Nomi wasm at all. Without this
      // branch the request decays into the load-budget timer and surfaces as
      // "execution timed out", which hides an asset/build failure behind a
      // message about the program under edit.
      failAll("the Nomi wasm failed to load: " + m.error);
      return;
    }
    if (m.op === "format") {
      const f = formatCbs.get(m.id);
      if (f) {
        formatCbs.delete(m.id);
        f.cb(m.output, m.error);
      }
      return;
    }
    if (m.op === "hover") {
      if (m.id === hoverReq) hoverCb?.(m.markdown);
      return;
    }
    const p = pending.get(m.id);
    if (!p) return;
    if (m.kind === "sem") {
      clearTimeout(p.loadTimer);
      p.runTimer = setTimeout(() => fail(m.id), RUN_BUDGET_MS);
      p.cbs.onSem?.(m.semtokens);
    } else {
      clearTimeout(p.loadTimer);
      clearTimeout(p.runTimer);
      pending.delete(m.id);
      p.cbs.onRun?.(m.output, m.error);
    }
  };
  // A worker-level script error (a missing or broken `/nomi/` asset, so
  // `importScripts` or the wasm fetch throws before any message is sent) is
  // otherwise invisible: every in-flight request would time out instead.
  worker.onerror = (e) => {
    const where = e?.filename ? " (" + e.filename + ":" + (e.lineno ?? 0) + ")" : "";
    failAll("worker error: " + (e?.message || "unknown") + where);
  };
}

function killWorker() {
  if (worker) {
    worker.onerror = null;
    worker.terminate();
    worker = null;
  }
  for (const p of pending.values()) {
    clearTimeout(p.loadTimer);
    clearTimeout(p.runTimer);
  }
  pending.clear();
  formatCbs.clear();
}

// failAll reports one worker-wide failure to everything in flight, so a broken
// worker never masquerades as a slow program. Each pending run gets `reason`;
// pending formatter and hover requests are released so the editor does not sit
// in its formatting state forever waiting on a worker that is gone.
function failAll(reason) {
  const runs = [...pending.values()];
  const formats = [...formatCbs.values()];
  const hover = hoverCb;
  killWorker();
  hoverCb = null;
  hoverReq = null;
  for (const p of runs) p.cbs.onFail?.(reason);
  for (const f of formats) f.cb("", reason);
  hover?.("");
}

// restartWorker replaces the worker and re-sends every run and format request
// still waiting on it, so one block that has to be killed (a timeout) or that
// stopped the Go runtime does not silently drop the other blocks' requests
// queued behind it. A pending hover is released; the next mouse move asks again.
function restartWorker() {
  const runs = [...pending.values()];
  const formats = [...formatCbs.entries()];
  const hover = hoverCb;
  killWorker();
  hoverCb = null;
  hoverReq = null;
  hover?.("");
  for (const p of runs) post(p.msg, p.cbs);
  for (const [id, f] of formats) postFormat(id, f);
}

function fail(id) {
  const p = pending.get(id);
  if (!p) return;
  pending.delete(id);
  restartWorker(); // hard-kill the runaway; the replacement reloads the wasm
  p.cbs.onFail?.("execution timed out");
}

function post(msg, cbs) {
  ensureWorker();
  const loadTimer = setTimeout(() => fail(msg.id), LOAD_BUDGET_MS);
  pending.set(msg.id, { msg, cbs, loadTimer, runTimer: null });
  worker.postMessage(msg);
}

function dispatch(id, source, cbs) {
  post({ id, source }, cbs);
}

function dispatchStdlibTest(id, moduleName, context, source, cbs) {
  post({ id, op: "stdlibTest", module: moduleName, context, source }, cbs);
}

function postFormat(id, f) {
  ensureWorker();
  formatCbs.set(id, f);
  worker.postMessage(f.msg);
}

function requestFormat(source, cb, op = "format") {
  const id = "f" + ++formatSeq;
  postFormat(id, { cb, msg: { id, op, source } });
}

// --- hover (LSP hover via the worker; shared, latest-wins) ---
let hoverSeq = 0;
let hoverReq = null;
let hoverCb = null;
function requestHover(source, line, col, cb) {
  ensureWorker();
  hoverReq = "h" + ++hoverSeq;
  hoverCb = cb;
  worker.postMessage({ id: hoverReq, op: "hover", source, line, col });
}

// Mirrors the Go-side SplitMultiFile regex: a `// FILE: <name>.<ext>`
// directive on its own line. Returns the parsed [{ name, body }] when the
// source has any markers, else null (single-file path).
const FILE_MARKER = /^\s*\/\/\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$/;
function splitFiles(source) {
  const lines = source.split("\n");
  let hasMarker = false;
  const files = [];
  let current = null;
  const push = (f) => files.push({ name: f.name, body: f.body.replace(/\n+$/, "") });
  for (const line of lines) {
    const m = FILE_MARKER.exec(line);
    if (m) {
      if (current) push(current);
      current = { name: m[1], body: "" };
      hasMarker = true;
    } else if (current) {
      current.body += (current.body ? "\n" : "") + line;
    }
  }
  if (current) push(current);
  return hasMarker ? files : null;
}

// Stitch a per-tab file list back into the source string the wasm
// runtime expects — same FILE-marker convention SplitMultiFile parses.
function stitchFiles(files) {
  return files.map((f) => `// FILE: ${f.name}\n${f.body}`).join("\n\n");
}

// Help tooltip for the live-indicator dot — same look as the LSP hover
// (it reuses showTooltip / hideTooltip). The dot itself is the pulsing
// accent indicator in the widget's top-right corner.
const LIVE_HELP_HTML =
  "<p><strong>Interactive code.</strong> Edits run automatically as you type.</p>" +
  "<p>Hover any identifier in the code for its signature and docs.</p>";

function labelStaticGoBlocks(root = document) {
  for (const pre of root.querySelectorAll("pre.nomi-tour:not(.nomi-code-go):not(.nomi-code-nomi)")) {
    if (pre.querySelector(":scope > span.line")) pre.classList.add("nomi-code-go");
  }
}

function attachLiveDot(widget) {
  const controls = document.createElement("div");
  controls.className = "nomi-live-controls";
  const dot = document.createElement("span");
  dot.className = "nomi-live";
  dot.setAttribute("role", "note");
  dot.setAttribute("aria-label", "Interactive code: edits run automatically; hover any identifier for its signature.");
  dot.tabIndex = 0;
  const show = () => showAnchoredTooltip(LIVE_HELP_HTML, dot);
  dot.addEventListener("mouseenter", show);
  dot.addEventListener("mouseleave", scheduleHideTooltip);
  dot.addEventListener("focus", show);
  dot.addEventListener("blur", hideTooltip);
  const formatter = document.createElement("span");
  formatter.className = "nomi-formatting";
  formatter.setAttribute("role", "status");
  formatter.setAttribute("aria-live", "polite");
  formatter.textContent = "format";
  controls.append(formatter, dot);
  widget.appendChild(controls);
  let formattingTimer;
  return {
    setFormatting(active) {
      clearTimeout(formattingTimer);
      widget.classList.toggle("is-formatting", active);
    },
    flashFormatting() {
      clearTimeout(formattingTimer);
      widget.classList.add("is-formatting");
      formattingTimer = setTimeout(() => {
        widget.classList.remove("is-formatting");
      }, 1000);
    },
  };
}

function mapOffsetThroughFormat(before, after, offset) {
  let prefix = 0;
  const maxPrefix = Math.min(before.length, after.length, offset);
  while (prefix < maxPrefix && before[prefix] === after[prefix]) prefix++;

  let suffix = 0;
  const beforeTail = before.length - offset;
  const maxSuffix = Math.min(before.length - prefix, after.length - prefix, beforeTail);
  while (
    suffix < maxSuffix &&
    before[before.length - 1 - suffix] === after[after.length - 1 - suffix]
  ) {
    suffix++;
  }

  if (offset <= prefix) return offset;
  if (offset >= before.length - suffix) return after.length - (before.length - offset);
  return prefix + Math.max(0, after.length - prefix - suffix);
}

function applyFormattedSource(ta, source, formatted) {
  // `nomi fmt` emits a trailing newline, as a file formatter should. An
  // editor's buffer is not a file: writing that back verbatim leaves a blank
  // line under the program's last `}`, visible as a gap above the output
  // panel. Normalized HERE rather than at either call site because both of
  // them (single-file on input, multi-file on the tab sweep) write through
  // this function, and because stripping before the equality check below is
  // what makes a format whose ONLY change was that newline a correct no-op.
  if (formatted) formatted = formatted.replace(/\n+$/, "");
  if (!formatted || formatted === source || ta.value !== source) return false;
  if (ta.selectionStart !== ta.selectionEnd) return false;

  const scrollTop = ta.scrollTop;
  const scrollLeft = ta.scrollLeft;
  const cursor = mapOffsetThroughFormat(source, formatted, ta.selectionStart);

  ta.value = formatted;
  ta.selectionStart = ta.selectionEnd = cursor;
  ta.scrollTop = scrollTop;
  ta.scrollLeft = scrollLeft;
  return true;
}

function cursorFollowsWhitespace(ta) {
  if (ta.selectionStart !== ta.selectionEnd || ta.selectionStart <= 0) return false;
  const prev = ta.value[ta.selectionStart - 1];
  return prev === " " || prev === "\t";
}

function autoFormatSkipReason(ta, composing) {
  if (composing) return "composing";
  if (ta.selectionStart !== ta.selectionEnd) return "selection";
  if (cursorFollowsWhitespace(ta)) return "cursor";
  return "";
}

// showAnchoredTooltip pins the shared `.nomi-hover` tooltip to a specific
// element (right-aligned with it, just below) instead of following the
// mouse. Used for the live-indicator dot where the tooltip should sit in
// a predictable place rather than wherever the cursor entered the dot.
function showAnchoredTooltip(html, anchor) {
  if (!tipEl) {
    tipEl = document.createElement("div");
    tipEl.className = "nomi-hover";
    tipEl.addEventListener("mouseenter", () => clearTimeout(hideTimer));
    tipEl.addEventListener("mouseleave", hideTooltip);
    document.body.appendChild(tipEl);
  }
  clearTimeout(hideTimer);
  tipEl.innerHTML = html;
  tipEl.style.display = "block";
  // Read tipEl's size after it's painted, then anchor right-aligned and
  // just below the anchor element. Flip above if it would clip below.
  const r = anchor.getBoundingClientRect();
  const tr = tipEl.getBoundingClientRect();
  let left = r.right - tr.width;
  let top = r.bottom + 6;
  if (left < 8) left = 8;
  if (top + tr.height > window.innerHeight - 8) top = r.top - tr.height - 6;
  tipEl.style.left = left + "px";
  tipEl.style.top = top + "px";
}

// --- per-block wiring ---
async function enhance(pre) {
  if (pre.dataset.enhanced) return;
  pre.dataset.enhanced = "1";

  const { parser, query, marked, DOMPurify } = await syntax();
  const codeEl = pre.querySelector("code");
  const source = codeEl.textContent.replace(/\n+$/, "");
  const isStdlibTest = codeEl.classList.contains("language-nomi-test");
  const stdlibModule = pre.dataset.nomiStdlibModule || "";
  const stdlibContext = pre.dataset.nomiStdlibContext || "";

  const files = isStdlibTest ? null : splitFiles(source);
  if (files && files.length > 1) {
    return enhanceMultiFile(pre, files, parser, query);
  }

  const widget = document.createElement("div");
  widget.className = "nomi-interactive" + (isStdlibTest ? " nomi-stdlib-test" : "");
  widget.innerHTML =
    '<div class="nomi-editor">' +
    '<pre class="nomi-tour nomi-hl" aria-hidden="true"></pre>' +
    '<textarea class="nomi-src" rows="1" spellcheck="false" autocapitalize="off" autocorrect="off"></textarea>' +
    "</div>" +
    '<pre class="nomi-out" aria-live="polite"></pre>';
  pre.replaceWith(widget);
  const live = attachLiveDot(widget);

  const hl = widget.querySelector(".nomi-hl");
  const ta = widget.querySelector(".nomi-src");
  const out = widget.querySelector(".nomi-out");
  ta.value = source;

  const block = { hl, ta, out, captures: null, code: "", req: 0 };

  const paint = () => {
    block.code = ta.value;
    block.captures = query.captures(parser.parse(block.code).rootNode);
    hl.innerHTML = render(block.captures, [], block.code, EDITOR_RENDER); // syntactic now; semantic arrives async
  };
  let formatDebounce;
  let formatToken = 0;
  let cleanFormatSource = null;
  let blockedFormatSource = null;
  let composing = false;
  const scheduleAutoFormat = (source) => {
    clearTimeout(formatDebounce);
    const reason = autoFormatSkipReason(ta, composing);
    if (reason || ta.value !== source) {
      if (reason === "cursor" && ta.value === source) blockedFormatSource = source;
      return;
    }
    blockedFormatSource = null;
    formatDebounce = setTimeout(() => {
      const latestReason = autoFormatSkipReason(ta, composing);
      if (latestReason || ta.value !== source) {
        if (latestReason === "cursor" && ta.value === source) blockedFormatSource = source;
        return;
      }
      blockedFormatSource = null;
      const token = ++formatToken;
      requestFormat(source, (formatted, error) => {
        if (token !== formatToken) return;
        if (error || ta.value !== source) return;
        if (!applyFormattedSource(ta, source, formatted)) return;
        live.flashFormatting();
        paint();
        run();
      }, isStdlibTest ? "formatTestBody" : "format");
    }, FORMAT_DEBOUNCE_MS);
  };
  const maybeScheduleAutoFormat = () => {
    if (blockedFormatSource === ta.value && cleanFormatSource === ta.value) {
      scheduleAutoFormat(blockedFormatSource);
    }
  };
  const run = () => {
    const id = ++reqSeq;
    block.req = id;
    const code = block.code;
    const captures = block.captures;
    const runner = isStdlibTest
      ? (callbacks) => dispatchStdlibTest(id, stdlibModule, stdlibContext, code, callbacks)
      : (callbacks) => dispatch(id, code, callbacks);
    runner({
      onSem: (sem) => {
        if (block.req === id) hl.innerHTML = render(captures, sem, code, EDITOR_RENDER);
      },
      onRun: (output, error) => {
        if (block.req !== id) return;
        showOutput(out, output, error, parser, query);
        cleanFormatSource = error ? null : code;
        if (error) live.setFormatting(false);
        else scheduleAutoFormat(code);
      },
      onFail: (reason) => {
        if (block.req !== id) return;
        showError(out, reason);
        cleanFormatSource = null;
        live.setFormatting(false);
      },
    });
  };

  let debounce;
  ta.addEventListener("input", () => {
    hideTooltip();
    clearTimeout(formatDebounce);
    cleanFormatSource = null;
    blockedFormatSource = null;
    formatToken++;
    live.setFormatting(false);
    paint();
    clearTimeout(debounce);
    debounce = setTimeout(run, DEBOUNCE_MS);
  });
  ta.addEventListener("compositionstart", () => {
    composing = true;
    clearTimeout(formatDebounce);
    blockedFormatSource = null;
    live.setFormatting(false);
  });
  ta.addEventListener("compositionend", () => {
    composing = false;
    maybeScheduleAutoFormat();
  });
  ta.addEventListener("keyup", maybeScheduleAutoFormat);
  ta.addEventListener("mouseup", maybeScheduleAutoFormat);
  ta.addEventListener("select", maybeScheduleAutoFormat);
  ta.addEventListener("focus", maybeScheduleAutoFormat);
  document.addEventListener("selectionchange", () => {
    if (document.activeElement === ta) maybeScheduleAutoFormat();
  });
  ta.addEventListener("keydown", (ev) => {
    if (ev.key !== "Tab") return;
    ev.preventDefault();
    const s = ta.selectionStart, e = ta.selectionEnd;
    ta.value = ta.value.slice(0, s) + "    " + ta.value.slice(e);
    ta.selectionStart = ta.selectionEnd = s + 4;
    ta.dispatchEvent(new Event("input"));
  });

  // Hover: map the mouse to a (line, col) on the monospace grid, ask the worker
  // for LSP hover, and show it in a tooltip — the same content Zed shows.
  const cs = getComputedStyle(ta);
  const padL = parseFloat(cs.paddingLeft) || 0;
  const padT = parseFloat(cs.paddingTop) || 0;
  const lineH = parseFloat(cs.lineHeight) || 20;
  const charW = measureCharWidth(cs);
  let hoverDebounce;
  let lastCell = "";
  ta.addEventListener("mousemove", (ev) => {
    const rect = ta.getBoundingClientRect();
    const x = ev.clientX - rect.left - padL + ta.scrollLeft;
    const y = ev.clientY - rect.top - padT + ta.scrollTop;
    if (x < 0 || y < 0) {
      hideTooltip();
      return;
    }
    const line = Math.floor(y / lineH) + 1;
    const col = Math.floor(x / charW) + 1;
    const cell = line + ":" + col;
    if (cell === lastCell) return; // same grid cell — nothing new to ask
    lastCell = cell;
    const cx = ev.clientX, cy = ev.clientY;
    clearTimeout(hoverDebounce);
    hoverDebounce = setTimeout(() => {
      requestHover(ta.value, line, col, (md) => {
        if (md) showTooltip(renderHoverMarkdown(md, marked, DOMPurify), cx, cy);
        else hideTooltip();
      });
    }, 120);
  });
  ta.addEventListener("mouseleave", () => {
    clearTimeout(hoverDebounce);
    scheduleHideTooltip();
  });
  ta.addEventListener("scroll", hideTooltip);

  paint();
  run();
}

// enhanceMultiFile renders a block split by `// FILE: <name>` markers as a
// tabbed editor: one tab per file, a shared output panel below, and a
// single Run dispatch that stitches all panes back together with the
// FILE markers (matching the convention runtime.SplitMultiFile parses).
//
// Each tab carries its own textarea + tree-sitter highlight layer, so
// edits in one tab don't disturb others' state. Tab switching shows
// exactly one pane at a time; keyboard arrows move focus along the bar.
// Hover is intentionally skipped in multi-file mode for v1 — wiring the
// LSP hover through a per-pane offset into the stitched program is
// future work; static highlighting still works per pane.
async function enhanceMultiFile(pre, files, parser, query) {
  const widget = document.createElement("div");
  widget.className = "nomi-interactive nomi-multifile";

  const tabBar = document.createElement("div");
  tabBar.className = "nomi-tabs";
  tabBar.setAttribute("role", "tablist");

  const editors = document.createElement("div");
  editors.className = "nomi-editors";

  const out = document.createElement("pre");
  out.className = "nomi-out";
  out.setAttribute("aria-live", "polite");

  widget.append(tabBar, editors, out);
  pre.replaceWith(widget);
  const live = attachLiveDot(widget);

  const panes = files.map((file, i) => {
    const btn = document.createElement("button");
    btn.className = "nomi-tab" + (i === 0 ? " is-active" : "");
    btn.type = "button";
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-selected", i === 0 ? "true" : "false");
    btn.tabIndex = i === 0 ? 0 : -1;
    btn.textContent = file.name;
    tabBar.appendChild(btn);

    const pane = document.createElement("div");
    pane.className = "nomi-editor";
    pane.setAttribute("role", "tabpanel");
    if (i !== 0) pane.hidden = true;
    pane.innerHTML =
      '<pre class="nomi-tour nomi-hl" aria-hidden="true"></pre>' +
      '<textarea class="nomi-src" rows="1" spellcheck="false" autocapitalize="off" autocorrect="off"></textarea>';
    editors.appendChild(pane);

    const hl = pane.querySelector(".nomi-hl");
    const ta = pane.querySelector(".nomi-src");
    ta.value = file.body;

    return { file, btn, pane, hl, ta, captures: null, code: file.body };
  });

  const block = { req: 0 };
  let formatDebounce;
  let formatToken = 0;
  let cleanFormatSnapshots = null;
  let blockedFormatSnapshots = null;

  const paint = (p) => {
    p.code = p.ta.value;
    p.captures = query.captures(parser.parse(p.code).rootNode);
    p.hl.innerHTML = render(p.captures, [], p.code, EDITOR_RENDER);
  };

  const activate = (i) => {
    panes.forEach((p, j) => {
      const active = i === j;
      p.btn.classList.toggle("is-active", active);
      p.btn.setAttribute("aria-selected", String(active));
      p.btn.tabIndex = active ? 0 : -1;
      p.pane.hidden = !active;
    });
  };

  panes.forEach((p, i) => {
    p.btn.addEventListener("click", () => {
      activate(i);
      p.ta.focus();
    });
  });

  tabBar.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
    const activeIdx = panes.findIndex((p) => p.btn === document.activeElement);
    if (activeIdx < 0) return;
    ev.preventDefault();
    const step = ev.key === "ArrowLeft" ? -1 : 1;
    const nextIdx = (activeIdx + step + panes.length) % panes.length;
    activate(nextIdx);
    panes[nextIdx].btn.focus();
  });

  const scheduleAutoFormat = (snapshots) => {
    clearTimeout(formatDebounce);
    const blockedByCursor = snapshots.some((s) => (
      s.p.ta.value === s.body && autoFormatSkipReason(s.p.ta, s.p.composing) === "cursor"
    ));
    if (snapshots.some((s) => s.p.ta.value !== s.body || autoFormatSkipReason(s.p.ta, s.p.composing))) {
      if (blockedByCursor && snapshots.every((s) => s.p.ta.value === s.body)) {
        blockedFormatSnapshots = snapshots;
      }
      return;
    }
    blockedFormatSnapshots = null;
    formatDebounce = setTimeout(() => {
      const latestBlockedByCursor = snapshots.some((s) => (
        s.p.ta.value === s.body && autoFormatSkipReason(s.p.ta, s.p.composing) === "cursor"
      ));
      if (snapshots.some((s) => s.p.ta.value !== s.body || autoFormatSkipReason(s.p.ta, s.p.composing))) {
        if (latestBlockedByCursor && snapshots.every((s) => s.p.ta.value === s.body)) {
          blockedFormatSnapshots = snapshots;
        }
        return;
      }
      blockedFormatSnapshots = null;
      let remaining = snapshots.length;
      let changed = false;
      const token = ++formatToken;
      for (const s of snapshots) {
        requestFormat(s.body, (formatted, error) => {
          if (token === formatToken && !error && s.p.ta.value === s.body && !s.p.composing) {
            if (applyFormattedSource(s.p.ta, s.body, formatted)) {
              paint(s.p);
              changed = true;
            }
          }
          remaining--;
          if (remaining === 0) {
            if (token === formatToken && changed) {
              live.flashFormatting();
              run();
            }
          }
        });
      }
    }, FORMAT_DEBOUNCE_MS);
  };
  const maybeScheduleAutoFormat = () => {
    if (!blockedFormatSnapshots || !cleanFormatSnapshots) return;
    if (
      blockedFormatSnapshots.every((s) => s.p.ta.value === s.body) &&
      cleanFormatSnapshots.every((s) => s.p.ta.value === s.body)
    ) {
      scheduleAutoFormat(blockedFormatSnapshots);
    }
  };

  const run = () => {
    const id = ++reqSeq;
    block.req = id;
    const snapshots = panes.map((p) => ({ p, body: p.ta.value }));
    const stitched = stitchFiles(snapshots.map((s) => ({ name: s.p.file.name, body: s.body })));
    dispatch(id, stitched, {
      onSem: () => {
        // Semantic-token overlay maps to a position in the stitched
        // program; mapping back to each pane's region is future work.
        // Syntactic highlight (per pane) still works.
      },
      onRun: (output, error) => {
        if (block.req !== id) return;
        showOutput(out, output, error, parser, query);
        cleanFormatSnapshots = error ? null : snapshots;
        if (error) live.setFormatting(false);
        else scheduleAutoFormat(snapshots);
      },
      onFail: (reason) => {
        if (block.req !== id) return;
        showError(out, reason);
        cleanFormatSnapshots = null;
        live.setFormatting(false);
      },
    });
  };

  let debounce;
  panes.forEach((p) => {
    p.composing = false;
    paint(p);
    p.ta.addEventListener("input", () => {
      clearTimeout(formatDebounce);
      cleanFormatSnapshots = null;
      blockedFormatSnapshots = null;
      formatToken++;
      live.setFormatting(false);
      paint(p);
      clearTimeout(debounce);
      debounce = setTimeout(run, DEBOUNCE_MS);
    });
    p.ta.addEventListener("compositionstart", () => {
      p.composing = true;
      clearTimeout(formatDebounce);
      blockedFormatSnapshots = null;
      live.setFormatting(false);
    });
    p.ta.addEventListener("compositionend", () => {
      p.composing = false;
      maybeScheduleAutoFormat();
    });
    p.ta.addEventListener("keyup", maybeScheduleAutoFormat);
    p.ta.addEventListener("mouseup", maybeScheduleAutoFormat);
    p.ta.addEventListener("select", maybeScheduleAutoFormat);
    p.ta.addEventListener("focus", maybeScheduleAutoFormat);
    document.addEventListener("selectionchange", () => {
      if (document.activeElement === p.ta) maybeScheduleAutoFormat();
    });
    p.ta.addEventListener("keydown", (ev) => {
      if (ev.key !== "Tab") return;
      ev.preventDefault();
      const s = p.ta.selectionStart, e = p.ta.selectionEnd;
      p.ta.value = p.ta.value.slice(0, s) + "    " + p.ta.value.slice(e);
      p.ta.selectionStart = p.ta.selectionEnd = s + 4;
      p.ta.dispatchEvent(new Event("input"));
    });
  });

  run();
}

function showOutput(out, output, error, parser, query) {
  out.textContent = "";
  if (output) appendRunText(out, output, "", parser, query);
  if (error) {
    if (output) out.appendChild(document.createTextNode("\n"));
    appendRunText(out, error, "nomi-err", parser, query);
  }
  if (!output && !error) out.textContent = "(no output)";
}

function appendRunText(out, text, fallbackClass, parser, query) {
  const parts = text.split(/(\n)/);
  let inDbgBlock = false;
  for (const part of parts) {
    if (part === "") continue;
    if (part === "\n") {
      out.appendChild(document.createTextNode(part));
      continue;
    }
    const dbgState = appendHighlightedDbgLine(out, part, parser, query);
    if (dbgState) {
      inDbgBlock = dbgState === "block";
      continue;
    }
    if (inDbgBlock && appendHighlightedDbgContinuationLine(out, part, parser, query)) continue;
    if (!/^\s/.test(part)) inDbgBlock = false;
    if (appendHighlightedRunLine(out, part, parser, query)) continue;
    const cls = classForRunLine(part, fallbackClass);
    if (!cls) {
      out.appendChild(document.createTextNode(part));
      continue;
    }
    const s = document.createElement("span");
    s.className = cls;
    s.textContent = part;
    out.appendChild(s);
  }
}

function appendHighlightedRunLine(out, line, parser, query) {
  const value = line.match(/^(\s{6,}=\s)(.+)$/);
  if (value) {
    out.appendChild(document.createTextNode(value[1]));
    appendNomiInline(out, value[2], parser, query);
    return true;
  }

  const code = line.match(/^(\s{4,})(.+)$/);
  if (!code) return false;
  if (/^(where\b|defined as:|pipeline values:|values:|actual:)/.test(code[2])) return false;

  out.appendChild(document.createTextNode(code[1]));
  appendNomiInline(out, code[2], parser, query);
  return true;
}

function appendHighlightedDbgLine(out, line, parser, query) {
  const prefix = line.match(/^(dbg) (line \d+:)( ?)/);
  if (!prefix) return "";

  const label = document.createElement("span");
  label.className = "nomi-dbg-label";
  label.textContent = prefix[1];
  out.appendChild(label);
  out.appendChild(document.createTextNode(" "));

  const loc = document.createElement("span");
  loc.className = "nomi-dbg-loc";
  loc.textContent = prefix[2];
  out.appendChild(loc);
  out.appendChild(document.createTextNode(prefix[3]));

  const rest = line.slice(prefix[0].length);
  const valueSep = rest.lastIndexOf(" = ");
  if (valueSep < 0) {
    appendNomiInline(out, rest, parser, query);
    return rest === "" ? "block" : "line";
  }

  appendNomiInline(out, rest.slice(0, valueSep), parser, query);
  out.appendChild(document.createTextNode(" = "));
  appendNomiInline(out, rest.slice(valueSep + 3), parser, query);
  return "line";
}

function appendHighlightedDbgContinuationLine(out, line, parser, query) {
  const value = line.match(/^(\s{2}=\s)(.+)$/);
  if (value) {
    out.appendChild(document.createTextNode(value[1]));
    appendNomiInline(out, value[2], parser, query);
    return true;
  }

  const code = line.match(/^(\s{2})(.+)$/);
  if (!code) return false;
  out.appendChild(document.createTextNode(code[1]));
  appendNomiInline(out, code[2], parser, query);
  return true;
}

function appendNomiInline(out, code, parser, query) {
  const s = document.createElement("span");
  s.className = "nomi-inline-code";
  try {
    const captures = query.captures(parser.parse(code).rootNode);
    s.innerHTML = render(captures, [], code);
  } catch (_) {
    s.textContent = code;
  }
  out.appendChild(s);
}

function classForRunLine(line, fallbackClass) {
  if (/^FAIL\b/.test(line) || /^test result: FAILED\b/.test(line)) return "nomi-test-fail";
  if (/^ok\b/.test(line) || /^test result: ok\b/.test(line)) return "nomi-test-ok";
  if (/^  line \d+/.test(line)) return "nomi-test-detail";
  if (/^\s{4,}(where\b|defined as:|pipeline values:|values:|actual:)/.test(line)) return "nomi-test-detail";
  if (/^    /.test(line)) return "nomi-test-code";
  return fallbackClass;
}

function showError(out, msg) {
  out.textContent = "";
  const s = document.createElement("span");
  s.className = "nomi-err";
  s.textContent = msg;
  out.appendChild(s);
}

// --- hover tooltip ---
function measureCharWidth(cs) {
  const m = document.createElement("span");
  m.style.cssText = "position:absolute;visibility:hidden;white-space:pre;top:-9999px;left:-9999px";
  m.style.fontFamily = cs.fontFamily;
  m.style.fontSize = cs.fontSize;
  m.style.fontWeight = cs.fontWeight;
  m.style.letterSpacing = cs.letterSpacing;
  m.textContent = "0".repeat(40);
  document.body.appendChild(m);
  const w = m.getBoundingClientRect().width / 40;
  m.remove();
  return w || 8;
}

let tipEl = null;
let hideTimer = null;
function showTooltip(html, x, y) {
  if (!tipEl) {
    tipEl = document.createElement("div");
    tipEl.className = "nomi-hover";
    // Hovering the tooltip itself cancels a pending hide so the user can reach
    // it (e.g. to scroll horizontally on a long signature); leaving the tooltip
    // dismisses it. Editor `mousemove` may re-show it over the same token.
    tipEl.addEventListener("mouseenter", () => clearTimeout(hideTimer));
    tipEl.addEventListener("mouseleave", hideTooltip);
    document.body.appendChild(tipEl);
  }
  clearTimeout(hideTimer);
  tipEl.innerHTML = html;
  tipEl.style.display = "block";
  const pad = 14;
  const r = tipEl.getBoundingClientRect();
  let left = x + pad;
  let top = y + pad;
  if (left + r.width > window.innerWidth - 8) left = x - r.width - pad;
  if (top + r.height > window.innerHeight - 8) top = y - r.height - pad;
  tipEl.style.left = Math.max(8, left) + "px";
  tipEl.style.top = Math.max(8, top) + "px";
}
function scheduleHideTooltip() {
  clearTimeout(hideTimer);
  hideTimer = setTimeout(hideTooltip, 150);
}
function hideTooltip() {
  clearTimeout(hideTimer);
  if (tipEl) tipEl.style.display = "none";
}

// CommonMark via marked + DOMPurify. The marked renderer is configured
// in `syntax()` to route Nomi fences through the tree-sitter highlighter;
// everything else goes through marked's
// default. DOMPurify.sanitize is the last hop before innerHTML insertion
// so a stray <script> in any future user-authored doc comment can't
// reach the DOM.
function renderHoverMarkdown(md, marked, DOMPurify) {
  return DOMPurify.sanitize(marked.parse(md));
}

// highlightStatic renders a plain ```nomi block as a highlighted,
// non-interactive code block — syntactic layer only (no worker/semtokens).
async function highlightStatic(pre) {
  if (pre.dataset.enhanced) return;
  pre.dataset.enhanced = "1";
  const { parser, query } = await syntax();
  const code = pre.querySelector("code").textContent.replace(/\n+$/, "");
  const out = document.createElement("pre");
  out.className = "nomi-tour nomi-code-nomi";
  out.innerHTML = render(query.captures(parser.parse(code).rootNode), [], code);
  pre.replaceWith(out);
}

// --- lazy activation: enhance/highlight each block as it nears the viewport ---
labelStaticGoBlocks();

const io = new IntersectionObserver(
  (entries) => {
    for (const en of entries) {
      if (!en.isIntersecting) continue;
      io.unobserve(en.target);
      const code = en.target.querySelector("code");
      if (
        code &&
        (code.classList.contains("language-nomi-run") || code.classList.contains("language-nomi-test"))
      ) enhance(en.target); // runnable
      else highlightStatic(en.target); // language-nomi: static snippet/signature
    }
  },
  { rootMargin: "200px" },
);
document
  .querySelectorAll("pre > code.language-nomi, pre > code.language-nomi-run, pre > code.language-nomi-test")
  .forEach((code) => io.observe(code.parentElement));
