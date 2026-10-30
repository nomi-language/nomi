// Client enhancer for the Starlight tour: finds runnable Nomi code blocks
// (Astro's syntax highlighting is off) and turns each into a live, editable,
// run-on-type example using the same analyzer and grammar as Zed:
//   syntax     web-tree-sitter (grammar + queries/highlights.scm) on this thread
//   semantics  the analyzer's tokens, from the Nomi wasm (async overlay)
//   run        debounced, in a Web Worker so a runaway program can't freeze the page;
//              a ```nomi-run manual block runs only on its Run button, and shows
//              its recorded output (its expect block) until then
// Served statically from /nomi/ (no bundling); web-tree-sitter + the wasm load
// lazily when the first example scrolls into view.
import { render, renderJson, renderToml } from "./highlight.mjs";

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
// TestTourWasmAnswersTheTourBlocks fails a block whose run under Node takes
// more than half of it, since a browser runs the wasm slower than Node.
const RUN_BUDGET_MS = 6000;
// execution allowance for a ```nomi-run manual block, whose run is expensive
// and which runs only when the reader asks. The same test holds a manual
// block to half of it.
const MANUAL_RUN_BUDGET_MS = 60000;
// A block that runs as the reader edits shows its run's status only when the
// run takes longer than this, so a fast run does not flicker.
const STATUS_DELAY_MS = 300;
const LOADING_STATUS = "Loading Nomi…";
const RUNNING_STATUS = "Running…";
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
const pending = new Map(); // reqId -> { msg, cbs, budget, loadTimer, runTimer }
const formatCbs = new Map(); // reqId -> { cb, msg }
const semCbs = new Map(); // reqId -> { cb, msg }
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
    if (m.op === "sem") {
      const f = semCbs.get(m.id);
      if (f) {
        semCbs.delete(m.id);
        f.cb(m.semtokens);
      }
      return;
    }
    const p = pending.get(m.id);
    if (!p) return;
    if (m.kind === "start") {
      // The worker has the wasm (and any module the run imports) and starts
      // the run.
      p.cbs.onStart?.();
      return;
    }
    if (m.kind === "sem") {
      clearTimeout(p.loadTimer);
      p.runTimer = setTimeout(() => fail(m.id), p.budget);
      p.cbs.onSem?.(m.semtokens);
    } else {
      clearTimeout(p.loadTimer);
      clearTimeout(p.runTimer);
      pending.delete(m.id);
      p.cbs.onRun?.(m.output, m.error, m.written);
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
  semCbs.clear();
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
  const sems = [...semCbs.entries()];
  const hover = hoverCb;
  killWorker();
  hoverCb = null;
  hoverReq = null;
  hover?.("");
  // The replacement loads the wasm again before it starts any of them.
  for (const p of runs) {
    p.cbs.onLoading?.();
    post(p.msg, p.cbs, p.budget);
  }
  for (const [id, f] of formats) postFormat(id, f);
  for (const [id, f] of sems) postSem(id, f);
}

function fail(id) {
  const p = pending.get(id);
  if (!p) return;
  pending.delete(id);
  restartWorker(); // hard-kill the runaway; the replacement reloads the wasm
  p.cbs.onFail?.("execution timed out");
}

function post(msg, cbs, budget = RUN_BUDGET_MS) {
  ensureWorker();
  const loadTimer = setTimeout(() => fail(msg.id), LOAD_BUDGET_MS);
  pending.set(msg.id, { msg, cbs, budget, loadTimer, runTimer: null });
  worker.postMessage(msg);
}

// files names a multi-file source's Nomi tabs, whose semantic tokens the
// worker answers per tab ({ file: tokens }). Tools run in the block's
// project before the run (a ```nomi-run tool:<module>/<name> block), and
// the run's answer carries the files they wrote. A manual block's run gets
// the longer budget.
function dispatch(id, source, cbs, files, tools = [], manual = false) {
  const msg = files ? { id, source, files } : { id, source };
  if (tools.length) msg.tools = tools;
  post(msg, cbs, manual ? MANUAL_RUN_BUDGET_MS : RUN_BUDGET_MS);
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

// requestSem asks for a source's semantic tokens without running it (a
// ```nomi-run manual block before its first run); files names a multi-file
// source's Nomi tabs, as for a run.
let semSeq = 0;
function postSem(id, f) {
  ensureWorker();
  semCbs.set(id, f);
  worker.postMessage(f.msg);
}

function requestSem(source, files, cb) {
  const id = "s" + ++semSeq;
  const msg = files ? { id, op: "sem", source, files } : { id, op: "sem", source };
  postSem(id, { cb, msg });
}

// --- hover (LSP hover via the worker; shared, latest-wins) ---
let hoverSeq = 0;
let hoverReq = null;
let hoverCb = null;
// With file, source is a multi-file source and (line, col) is a position in
// that tab's own text; the worker analyzes the whole program.
function requestHover(source, line, col, cb, file) {
  ensureWorker();
  hoverReq = "h" + ++hoverSeq;
  hoverCb = cb;
  const msg = { id: hoverReq, op: "hover", source, line, col };
  if (file) msg.file = file;
  worker.postMessage(msg);
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

// fileLanguage names the language a multi-file example's tab holds, by its
// file name: "nomi" for a Nomi source, "toml" for a manifest or message
// catalog, and "text" for a data file the program reads (`notes.txt`),
// which the run puts in the program's in-memory file system. "go" is a Go
// file the example binds, which the tour links ahead of time, so its tab is
// read-only (rehype-shiki.mjs highlights it at build time).
function fileLanguage(name) {
  if (name.endsWith(".nomi")) return "nomi";
  if (name.endsWith(".go")) return "go";
  if (name.endsWith(".json")) return "json";
  return name.endsWith(".toml") ? "toml" : "text";
}

// tabLabel writes a tab's file name into btn, a file under a directory
// (`csvdata/csvdata.nomi`, a module of its own in the block) with
// the directory muted, so the file's own name reads first.
function tabLabel(btn, name) {
  const slash = name.lastIndexOf("/");
  if (slash < 0) {
    btn.textContent = name;
    return;
  }
  const dir = document.createElement("span");
  dir.className = "nomi-tab-dir";
  dir.textContent = name.slice(0, slash + 1);
  btn.append(dir, document.createTextNode(name.slice(slash + 1)));
}

// A setup file says how the project builds rather than what it does; a
// file tree lists it muted and the block never opens on it.
const SETUP_FILES = new Set(["go.mod", "go.sum", "nomi.toml"]);
function isSetupFile(name) {
  return SETUP_FILES.has(name.slice(name.lastIndexOf("/") + 1));
}

// projectDir is the directory a block runs in, from the block's root, as
// vmhost.LoadMarked picks it: "" for a block whose root is its project,
// else, for a block of sibling projects (no go.mod at its root, and one in
// two or more top-level directories), the top-level directory of its first
// file, with a trailing slash ("rates/").
function projectDir(files) {
  if (files.some((f) => f.name === "go.mod")) return "";
  const modules = new Set(
    files.filter((f) => /^[^/]+\/go\.mod$/.test(f.name)).map((f) => f.name.split("/")[0]),
  );
  if (modules.size < 2 || !files[0].name.includes("/")) return "";
  return files[0].name.split("/")[0] + "/";
}

// usesTree says whether a multi-file block shows a file tree beside its
// editor rather than a tab strip above it: when it has more than four
// files, or any file under a directory.
function usesTree(files) {
  return files.length > 4 || files.some((f) => f.name.includes("/"));
}

// A file in a live example is editable only when an edit can change what
// the example prints. readOnlyReason answers why a file is not, the
// tooltip its lock carries, or "" for an editable file: a Go file (the
// tour compiles it ahead of time), go.mod and go.sum, a nomi.toml whose
// only table is [module], and a data file the block's fence marks with a
// `read-only:<glob>` word (globs, from data-nomi-read-only), whose
// contents the program never reads. vmhost's TestTourDoctests holds a
// marked file to that: a run that reads one fails, and so does a run that
// never reads a data file left editable.
function readOnlyReason(file, lang, globs) {
  const base = file.name.slice(file.name.lastIndexOf("/") + 1);
  if (lang === "go") return "The tour compiles this Go ahead of time";
  if (base === "go.mod" || base === "go.sum") return "Managed by nomi add and nomi remove";
  if (base === "nomi.toml" && onlyModuleTable(file.body)) {
    return "Only names the module; an edit can't change the output";
  }
  if (globs.some((glob) => globMatch(glob, file.name))) return "Only its name matters to this example";
  return "";
}

// onlyModuleTable reports whether a nomi.toml's only table header is
// [module]: comments, blank lines and the module's keys may surround it.
function onlyModuleTable(text) {
  return text.split("\n").every((line) => {
    const t = line.replace(/#.*$/, "").trim();
    return !t.startsWith("[") || t === "[module]";
  });
}

// globMatch is Go's path.Match, which the doctest matches a fence's
// `read-only:` glob with, over a file's path as its `// FILE:` line writes
// it: `*` is any run of characters but `/`, `?` one of them, `[...]` a
// class (`[^...]` its complement, `a-z` a range), and `\` escapes. A
// malformed glob matches nothing; the doctest rejects it.
function globMatch(glob, name) {
  let re = "^";
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i];
    if (c === "*") re += "[^/]*";
    else if (c === "?") re += "[^/]";
    else if (c === "\\") {
      if (++i >= glob.length) return false;
      re += escapeRegExp(glob[i]);
    } else if (c === "[") {
      const end = glob.indexOf("]", i + 1);
      if (end <= i + 1) return false;
      let body = glob.slice(i + 1, end);
      let negate = false;
      if (body.startsWith("^")) {
        negate = true;
        body = body.slice(1);
        if (body === "") return false;
      }
      re += (negate ? "[^" : "[") + body.replace(/[\]\\^]/g, "\\$&") + "]";
      i = end;
    } else re += escapeRegExp(c);
  }
  try {
    return new RegExp(re + "$").test(name);
  } catch {
    return false;
  }
}

function escapeRegExp(c) {
  return c.replace(/[.*+?^${}()|[\]\\/-]/g, "\\$&");
}

const LOCK_ICON =
  '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">' +
  '<rect x="3" y="7" width="10" height="7.5" rx="1.5" fill="currentColor"/>' +
  '<path d="M5.25 7V5a2.75 2.75 0 0 1 5.5 0v2" fill="none" stroke="currentColor" stroke-width="1.5"/></svg>';

// readOnlyMark is the muted lock a read-only file's tab, tree entry and
// path carry, so a file that can't be edited says so before it is
// clicked; its tooltip says why.
function readOnlyMark(reason) {
  const ro = document.createElement("span");
  ro.className = "nomi-ro";
  ro.setAttribute("role", "img");
  ro.setAttribute("aria-label", "Read-only");
  ro.title = reason;
  ro.innerHTML = LOCK_ICON;
  return ro;
}

// tabsNav lays a block's files out as a tab strip in its bar: one tab per
// file, arrows move along it. select(i, focus) shows file i's pane.
function tabsNav(widget, bar, panes, first, select, controls = null) {
  const tabBar = document.createElement("div");
  tabBar.className = "nomi-tabs";
  tabBar.setAttribute("role", "tablist");
  bar.append(tabBar);
  // Each tab sits in a slot, so the open tab's move arrows (controls) can sit
  // beside it.
  const slots = [];
  const btns = panes.map((p, i) => {
    const slot = document.createElement("span");
    slot.className = "nomi-tab-slot";
    slot.setAttribute("role", "presentation");
    slots.push(slot);
    const btn = document.createElement("button");
    btn.className = "nomi-tab";
    btn.type = "button";
    btn.setAttribute("role", "tab");
    tabLabel(btn, p.file.name);
    if (p.readOnly) {
      btn.title = p.readOnly;
      btn.append(readOnlyMark(p.readOnly));
    }
    p.pane.setAttribute("role", "tabpanel");
    slot.append(btn);
    tabBar.appendChild(slot);
    btn.addEventListener("click", () => {
      activate(i);
      select(i, true);
    });
    return btn;
  });
  const activate = (i) => {
    btns.forEach((btn, j) => {
      const active = i === j;
      btn.classList.toggle("is-active", active);
      btn.setAttribute("aria-selected", String(active));
      btn.tabIndex = active ? 0 : -1;
    });
    if (controls) slots[i].append(controls);
  };
  // hide leaves out of the strip the file the split's second pane shows.
  const hide = (gone) => {
    slots.forEach((slot, k) => {
      slot.hidden = gone.includes(k);
    });
  };
  activate(first);
  tabBar.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
    const at = btns.indexOf(document.activeElement);
    if (at < 0) return;
    ev.preventDefault();
    let next = at;
    do {
      next = (next + (ev.key === "ArrowLeft" ? -1 : 1) + btns.length) % btns.length;
    } while (slots[next].hidden && next !== at);
    activate(next);
    select(next, false);
    btns[next].focus();
  });
  return { activate, hide };
}

// treeNav lays a block's files out as a tree in a column beside the editor:
// each directory a folder, open at first, setup files muted after the
// others. The bar names the open file. On a narrow screen the tree is a
// menu that the bar's file button opens.
let navSeq = 0;
function treeNav(widget, bar, panes, first, select, controls = null) {
  const nav = document.createElement("nav");
  nav.className = "nomi-tree";
  nav.id = "nomi-tree-" + ++navSeq;
  nav.setAttribute("aria-label", "Files");

  const menu = document.createElement("button");
  menu.type = "button";
  menu.className = "nomi-filemenu";
  menu.setAttribute("aria-expanded", "false");
  menu.setAttribute("aria-controls", nav.id);
  const path = document.createElement("span");
  path.className = "nomi-path";
  bar.append(menu, path);
  // The open file's move arrows sit after its name.
  if (controls) bar.append(controls);

  // Folders, from the files' paths, in the block's order.
  const root = { dirs: new Map(), files: [] };
  panes.forEach((p, i) => {
    const parts = p.file.name.split("/");
    let node = root;
    for (const dir of parts.slice(0, -1)) {
      if (!node.dirs.has(dir)) node.dirs.set(dir, { dirs: new Map(), files: [] });
      node = node.dirs.get(dir);
    }
    node.files.push(i);
  });
  const fileBtns = [];
  const build = (node, depth) => {
    const ul = document.createElement("ul");
    for (let [name, child] of node.dirs) {
      // A directory holding only one directory reads as one folder
      // (`a/b/` when `a/` holds only `b/`).
      while (child.files.length === 0 && child.dirs.size === 1) {
        const [[inner, grandchild]] = child.dirs;
        name += "/" + inner;
        child = grandchild;
      }
      const li = document.createElement("li");
      const folder = document.createElement("button");
      folder.type = "button";
      folder.className = "nomi-tree-folder";
      folder.setAttribute("aria-expanded", "true");
      folder.style.setProperty("--depth", depth);
      folder.textContent = name + "/";
      const sub = build(child, depth + 1);
      folder.addEventListener("click", () => {
        const open = folder.getAttribute("aria-expanded") !== "true";
        folder.setAttribute("aria-expanded", String(open));
        sub.hidden = !open;
      });
      li.append(folder, sub);
      ul.append(li);
    }
    const order = [...node.files.filter((i) => !isSetupFile(panes[i].file.name)),
      ...node.files.filter((i) => isSetupFile(panes[i].file.name))];
    for (const i of order) {
      const p = panes[i];
      const li = document.createElement("li");
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "nomi-tree-file" +
        (isSetupFile(p.file.name) ? " is-setup" : "") +
        (p.readOnly ? " is-readonly" : "");
      btn.style.setProperty("--depth", depth);
      btn.textContent = p.file.name.slice(p.file.name.lastIndexOf("/") + 1);
      if (p.readOnly) {
        btn.title = p.readOnly;
        btn.append(readOnlyMark(p.readOnly));
      }
      btn.addEventListener("click", () => {
        activate(i);
        setOpen(false);
        select(i, true);
      });
      fileBtns[i] = btn;
      li.append(btn);
      ul.append(li);
    }
    return ul;
  };
  nav.append(build(root, 0));

  const activate = (i) => {
    fileBtns.forEach((btn, j) => {
      btn.classList.toggle("is-active", i === j);
      if (i === j) btn.setAttribute("aria-current", "true");
      else btn.removeAttribute("aria-current");
    });
    const p = panes[i];
    for (const el of [path, menu]) {
      el.textContent = "";
      tabLabel(el, p.file.name);
      if (p.readOnly) el.append(readOnlyMark(p.readOnly));
    }
    menu.setAttribute("aria-label", `File: ${p.file.name}. Choose another file`);
  };
  activate(first);

  // The narrow-screen menu: the tree drops down under the bar.
  const onOutside = (ev) => {
    if (!nav.contains(ev.target) && !menu.contains(ev.target)) setOpen(false);
  };
  const setOpen = (open) => {
    if (open === widget.classList.contains("is-tree-open")) return;
    widget.classList.toggle("is-tree-open", open);
    menu.setAttribute("aria-expanded", String(open));
    if (open) {
      const wr = widget.getBoundingClientRect();
      nav.style.top = bar.getBoundingClientRect().bottom - wr.top - widget.clientTop + "px";
      document.addEventListener("pointerdown", onOutside, true);
      (nav.querySelector(".nomi-tree-file.is-active") || nav.querySelector("button"))?.focus();
    } else {
      document.removeEventListener("pointerdown", onOutside, true);
    }
  };
  menu.addEventListener("click", () => setOpen(!widget.classList.contains("is-tree-open")));
  widget.closeFileMenu = () => {
    if (!widget.classList.contains("is-tree-open")) return false;
    setOpen(false);
    menu.focus();
    return true;
  };

  nav.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && widget.closeFileMenu()) {
      ev.preventDefault();
      ev.stopPropagation();
      return;
    }
    const btn = ev.target.closest("button");
    if (!btn) return;
    if (btn.classList.contains("nomi-tree-folder")) {
      const open = btn.getAttribute("aria-expanded") === "true";
      if ((ev.key === "ArrowLeft" && open) || (ev.key === "ArrowRight" && !open)) {
        ev.preventDefault();
        btn.click();
        return;
      }
    }
    if (ev.key !== "ArrowDown" && ev.key !== "ArrowUp" && ev.key !== "Home" && ev.key !== "End") return;
    const shown = [...nav.querySelectorAll("button")].filter((b) => b.getClientRects().length > 0);
    const at = shown.indexOf(btn);
    if (at < 0) return;
    ev.preventDefault();
    const next = ev.key === "Home" ? 0
      : ev.key === "End" ? shown.length - 1
      : Math.min(shown.length - 1, Math.max(0, at + (ev.key === "ArrowDown" ? 1 : -1)));
    shown[next].focus();
  });
  widget.prepend(nav);
  return { activate };
}

// --- the large view: one shared <dialog> the widget is moved into ---
let modal = null;
function modalDialog() {
  if (modal) return modal;
  const dialog = document.createElement("dialog");
  dialog.className = "nomi-modal";
  dialog.setAttribute("aria-labelledby", "nomi-modal-title");
  const frame = document.createElement("div");
  frame.className = "nomi-modal-frame";
  const head = document.createElement("div");
  head.className = "nomi-modal-head";
  const title = document.createElement("h2");
  title.className = "nomi-modal-title";
  title.id = "nomi-modal-title";
  const close = document.createElement("button");
  close.type = "button";
  close.className = "nomi-modal-close";
  close.setAttribute("aria-label", "Close");
  close.title = "Close (Esc)";
  close.innerHTML = CLOSE_ICON;
  head.append(title, close);
  const body = document.createElement("div");
  body.className = "nomi-modal-body";
  frame.append(head, body);
  dialog.append(frame);
  document.body.append(dialog);
  modal = { dialog, title, body, current: null };

  close.addEventListener("click", () => dialog.close());
  // A click on the backdrop lands on the dialog itself, since the frame
  // fills it. A drag that starts in the editor and ends outside does not
  // count.
  let downOnBackdrop = false;
  dialog.addEventListener("pointerdown", (ev) => {
    downOnBackdrop = ev.target === dialog;
  });
  dialog.addEventListener("click", (ev) => {
    if (ev.target === dialog && downOnBackdrop) dialog.close();
    downOnBackdrop = false;
  });
  // Esc closes an open file menu first, then the view.
  // An editor's first Esc readies Tab to leave it instead (tabKeys).
  dialog.addEventListener("cancel", (ev) => {
    if (escapeClaimed || modal.current?.widget.closeFileMenu?.()) ev.preventDefault();
  });
  dialog.addEventListener("close", closeModal);
  return modal;
}

// scrollState records where each scrolled element in root is scrolled to;
// moving an element out of the document and back resets its scroll.
function scrollState(root) {
  const state = [];
  for (const el of [root, ...root.querySelectorAll("*")]) {
    if (el.scrollTop || el.scrollLeft) state.push([el, el.scrollTop, el.scrollLeft]);
  }
  return () => {
    for (const [el, top, left] of state) {
      el.scrollTop = top;
      el.scrollLeft = left;
    }
  };
}

// blockTitle names a block in the large view by the heading of the section
// it is in.
function blockTitle(el) {
  for (; el && el !== document.body; el = el.parentElement) {
    for (let s = el.previousElementSibling; s; s = s.previousElementSibling) {
      const h = s.matches("h1, h2, h3, h4") ? s : [...s.querySelectorAll("h1, h2, h3, h4")].pop();
      if (h) return h.textContent.trim();
    }
  }
  return document.title;
}

// openModal moves the widget, not a copy of it, into the large view, so its
// edits, its run and its output carry over, and closeModal puts it back.
function openModal(widget, opener) {
  const m = modalDialog();
  if (m.current) return;
  hideTooltip();
  widget.closeFileMenu?.();
  const restoreScroll = scrollState(widget);
  const focused = widget.contains(document.activeElement) ? document.activeElement : null;
  const placeholder = document.createElement("div");
  placeholder.className = "nomi-modal-placeholder";
  placeholder.style.height = widget.offsetHeight + "px";
  widget.replaceWith(placeholder);
  m.title.textContent = blockTitle(placeholder);
  m.body.append(widget);
  widget.classList.add("is-modal");
  // The live dot and the formatting chip describe the whole example, so the
  // large view, whose editor may be split, shows them in the output's
  // header; closing it puts them back.
  const live = widget.querySelector(".nomi-live-controls");
  const head = widget.querySelector(".nomi-out-head");
  let liveHome = null;
  if (live && head) {
    liveHome = { parent: live.parentElement, next: live.nextSibling };
    head.append(live);
  }
  m.current = { widget, placeholder, opener, restoreScroll, live, liveHome };
  document.documentElement.classList.add("nomi-modal-open");
  m.dialog.showModal();
  // The bars measure the panes, so they go in once the dialog is shown.
  addSplitters(widget);
  widget.onModalOpen?.();
  restoreScroll();
  const editor = focused?.matches("textarea")
    ? focused
    : widget.querySelector(".nomi-editor:not([hidden]) textarea");
  editor?.focus({ preventScroll: true });
}

function closeModal() {
  const m = modal;
  if (!m?.current) return;
  // The page's view of the widget comes back scrolled as it was left.
  const { widget, placeholder, opener, restoreScroll, live, liveHome } = m.current;
  if (liveHome) liveHome.parent.insertBefore(live, liveHome.next);
  hideTooltip();
  widget.closeFileMenu?.();
  m.current = null;
  widget.onModalClose?.();
  widget.classList.remove("is-modal");
  removeSplitters(widget);
  placeholder.replaceWith(widget);
  document.documentElement.classList.remove("nomi-modal-open");
  restoreScroll();
  opener.focus({ preventScroll: true });
}

// --- resizing the large view's panes ---

// The widths a reader chose for the file tree and the output, in pixels,
// kept for the rest of the visit.
const SPLIT_KEYS = { tree: "nomi-split-tree", out: "nomi-split-out" };
const SPLIT_PROPS = { tree: "--nomi-tree-w", out: "--nomi-out-w" };
function savedWidth(which) {
  try {
    const n = Number(sessionStorage.getItem(SPLIT_KEYS[which]));
    return n > 0 ? n : null;
  } catch {
    return null;
  }
}
function saveWidth(which, px) {
  try {
    sessionStorage.setItem(SPLIT_KEYS[which], String(Math.round(px)));
  } catch {}
}

// clampWidth keeps the tree between 8rem and 24rem, the output at least
// 12rem wide, and the editor at least 20rem wide between them.
function clampWidth(widget, which, px) {
  const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
  const total = widget.getBoundingClientRect().width;
  const tree = widget.querySelector('.nomi-split[data-split="tree"]') ? paneWidth(widget, "tree") : 0;
  const out = paneWidth(widget, "out");
  const room = total - 20 * rem - (which === "tree" ? out : tree);
  const min = which === "tree" ? 8 * rem : 12 * rem;
  const max = which === "tree" ? Math.min(24 * rem, room) : room;
  return Math.max(min, Math.min(max, px));
}
function paneWidth(widget, which) {
  const pane = widget.querySelector(which === "tree" ? ".nomi-tree" : ".nomi-out-panel");
  return pane ? pane.getBoundingClientRect().width : 0;
}
function setWidth(widget, which, px) {
  const w = clampWidth(widget, which, px);
  widget.style.setProperty(SPLIT_PROPS[which], w + "px");
  widget.querySelector(`.nomi-split[data-split="${which}"]`)?.setAttribute("aria-valuenow", String(Math.round(w)));
  return w;
}

// addSplitters puts a bar between the tree and the editor (when the block
// has a tree) and between the editor and the output.
function addSplitters(widget) {
  const kinds = widget.classList.contains("nomi-has-tree") ? ["tree", "out"] : ["out"];
  for (const which of kinds) {
    const bar = document.createElement("div");
    bar.className = "nomi-split";
    bar.dataset.split = which;
    bar.tabIndex = 0;
    bar.setAttribute("role", "separator");
    bar.setAttribute("aria-orientation", "vertical");
    bar.setAttribute("aria-label", which === "tree" ? "Resize the file list" : "Resize the output");
    widget.append(bar);
    bar.addEventListener("pointerdown", (ev) => {
      ev.preventDefault();
      bar.setPointerCapture(ev.pointerId);
      bar.classList.add("is-dragging");
      const move = (e) => {
        const rect = widget.getBoundingClientRect();
        setWidth(widget, which, which === "tree" ? e.clientX - rect.left : rect.right - e.clientX);
      };
      const up = (e) => {
        bar.releasePointerCapture(e.pointerId);
        bar.classList.remove("is-dragging");
        bar.removeEventListener("pointermove", move);
        bar.removeEventListener("pointerup", up);
        bar.removeEventListener("pointercancel", up);
        saveWidth(which, paneWidth(widget, which));
      };
      bar.addEventListener("pointermove", move);
      bar.addEventListener("pointerup", up);
      bar.addEventListener("pointercancel", up);
    });
    // Arrow keys move the bar 16px, 64px with Shift; Left always moves it left.
    bar.addEventListener("keydown", (ev) => {
      if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
      ev.preventDefault();
      const step = (ev.shiftKey ? 64 : 16) * (ev.key === "ArrowLeft" ? -1 : 1);
      const now = paneWidth(widget, which);
      saveWidth(which, setWidth(widget, which, which === "tree" ? now + step : now - step));
    });
  }
  for (const which of kinds) {
    const saved = savedWidth(which);
    if (saved) setWidth(widget, which, saved);
    else widget.querySelector(`.nomi-split[data-split="${which}"]`).setAttribute("aria-valuenow", String(Math.round(paneWidth(widget, which))));
  }
}

function removeSplitters(widget) {
  for (const bar of widget.querySelectorAll(".nomi-split")) bar.remove();
  for (const prop of Object.values(SPLIT_PROPS)) widget.style.removeProperty(prop);
}

// --- splitting the large view's editor ---

// splitPanes lets a multi-file example show its files in two groups in the
// large view, each a pane with its own tabs. The open tab of a group carries
// arrows for the moves it can make: in a lone group of several files, right
// and down, which split the tab off into a new group on that side; in a
// split, one arrow pointing at the other group, which moves the tab there.
// A group that a move empties closes, and the example is one group again.
// (A group on the left or above would be the same split with the groups
// swapped.) A pane is the file's own editor moved between the two places,
// so its text, highlighting and listeners go with it. Leaving the large
// view and coming back keeps the split while the page is open; the inline
// editor, which grows to its code, never splits.
const MOVE_ARROWS = {
  right: ["Move {} to a pane on the right", "M6 3l5 5-5 5"],
  below: ["Move {} to a pane below", "M3 6l5 5 5-5"],
  left: ["Move {} to the pane on the left", "M10 3L5 8l5 5"],
  above: ["Move {} to the pane above", "M3 10l5-5 5 5"],
};
function moveArrows(sides, file, onMove) {
  const span = document.createElement("span");
  span.className = "nomi-move-controls";
  for (const where of sides) {
    const [label, path] = MOVE_ARROWS[where];
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "nomi-move-btn";
    btn.setAttribute("aria-label", label.replace("{}", file));
    btn.title = btn.getAttribute("aria-label");
    btn.innerHTML = `<svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true"><path d="${path}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      onMove(where);
    });
    span.append(btn);
  }
  return span;
}
function splitPanes(widget, main, bar, editors, panes, first) {
  // a is the main group's open file; the second group holds the files in
  // second, b its open one, on side ("right" or "below").
  let a = first;
  let second = [];
  let b = -1;
  let side = null;
  let wrapper = null;
  let paneA = null;
  let other = null;
  let strip = null;
  let kept = null;
  const api = { nav: null };
  // The main group's arrows go in a slot nav gives them, beside its open tab.
  const controls = document.createElement("span");
  controls.className = "nomi-move-slot";

  const inMain = (j) => !second.includes(j);
  const mainCount = () => panes.length - second.length;
  // nextIn is the file a group opens when its own leaves: the next one in
  // the block that stays in the group, preferring one that is not a setup
  // file.
  const nextIn = (from, stays) => {
    let fallback = -1;
    for (let k = 1; k < panes.length; k++) {
      const j = (from + k) % panes.length;
      if (!stays(j)) continue;
      if (!isSetupFile(panes[j].file.name)) return j;
      if (fallback < 0) fallback = j;
    }
    return fallback;
  };

  const render = () => {
    panes.forEach((p, j) => {
      const inB = side && second.includes(j);
      const home = inB ? other : editors;
      if (p.pane.parentElement !== home) home.append(p.pane);
      p.pane.hidden = !(j === a || (inB && j === b));
    });
    api.nav?.hide?.(side ? second : []);
    api.nav?.activate(a);
    const mainSides = side ? [side] : panes.length > 1 ? ["right", "below"] : [];
    controls.replaceChildren(moveArrows(mainSides, panes[a].file.name, (where) => moveFromMain(where)));
    if (strip) {
      strip.replaceChildren();
      for (const j of second) {
        const slot = document.createElement("span");
        slot.className = "nomi-tab-slot";
        slot.setAttribute("role", "presentation");
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "nomi-tab" + (j === b ? " is-active" : "");
        btn.setAttribute("role", "tab");
        btn.setAttribute("aria-selected", String(j === b));
        btn.tabIndex = j === b ? 0 : -1;
        tabLabel(btn, panes[j].file.name);
        if (panes[j].readOnly) {
          btn.title = panes[j].readOnly;
          btn.append(readOnlyMark(panes[j].readOnly));
        }
        btn.addEventListener("click", () => {
          b = j;
          render();
          panes[j].ta.focus({ preventScroll: true });
        });
        slot.append(btn);
        if (j === b) {
          slot.append(moveArrows([side === "right" ? "left" : "above"], panes[j].file.name, () => moveFromSecond()));
        }
        strip.append(slot);
      }
    }
  };

  const build = (where) => {
    side = where;
    wrapper = document.createElement("div");
    wrapper.className = `nomi-panes is-${where}`;
    const divider = document.createElement("div");
    divider.className = "nomi-pane-divider";
    divider.tabIndex = 0;
    divider.setAttribute("role", "separator");
    divider.setAttribute("aria-orientation", where === "right" ? "vertical" : "horizontal");
    divider.setAttribute("aria-label", "Resize the split");
    const box = document.createElement("div");
    box.className = "nomi-pane-b";
    strip = document.createElement("div");
    strip.className = "nomi-tabs nomi-pane-bar";
    strip.setAttribute("role", "tablist");
    other = document.createElement("div");
    other.className = "nomi-editors";
    box.append(strip, other);
    // Each group is its tabs over its editor, so the two tab strips sit on
    // one row when the groups are side by side.
    paneA = document.createElement("div");
    paneA.className = "nomi-pane-a";
    editors.replaceWith(wrapper);
    paneA.append(bar, editors);
    wrapper.append(paneA, divider, box);
    resizable(divider);
  };
  const unbuild = () => {
    if (!side) return;
    side = null;
    second = [];
    b = -1;
    // The panes go home before the wrapper leaves, so none leaves with it.
    for (const p of panes) if (p.pane.parentElement !== editors) editors.append(p.pane);
    wrapper.replaceWith(bar, editors);
    wrapper = paneA = other = strip = null;
  };

  // moveFromMain moves the main group's open file: into a new group on that
  // side, or, while split, into the second group. A main group the move
  // empties hands the split's files back as one group.
  const moveFromMain = (where) => {
    const moved = a;
    if (!side) {
      build(where);
      second = [moved];
      b = moved;
      a = nextIn(moved, (j) => j !== moved);
    } else if (mainCount() === 1) {
      unbuild();
      a = moved;
    } else {
      second = [...second, moved];
      b = moved;
      a = nextIn(moved, (j) => inMain(j) && j !== moved);
    }
    render();
    panes[moved].ta.focus({ preventScroll: true });
  };
  // moveFromSecond moves the second group's open file into the main group,
  // and closes the second group when it empties.
  const moveFromSecond = () => {
    const moved = b;
    second = second.filter((j) => j !== moved);
    if (second.length === 0) unbuild();
    else b = nextIn(moved, (j) => second.includes(j));
    a = moved;
    render();
    panes[moved].ta.focus({ preventScroll: true });
  };

  // The divider moves by dragging, or by the arrow keys in steps of 5%
  // (20% with Shift); each pane keeps at least a fifth of the room.
  // --nomi-split-a is the main group's share.
  const resizable = (divider) => {
    const across = () => side === "right";
    const set = (frac) => {
      const f = Math.max(0.2, Math.min(0.8, frac));
      wrapper.style.setProperty("--nomi-split-a", `${(f * 100).toFixed(1)}%`);
      divider.setAttribute("aria-valuenow", String(Math.round(f * 100)));
    };
    const share = () => {
      const r = wrapper.getBoundingClientRect();
      const e = paneA.getBoundingClientRect();
      return across() ? e.width / r.width : e.height / r.height;
    };
    set(0.5);
    divider.addEventListener("pointerdown", (ev) => {
      ev.preventDefault();
      divider.setPointerCapture(ev.pointerId);
      divider.classList.add("is-dragging");
      const move = (e) => {
        const r = wrapper.getBoundingClientRect();
        set(across() ? (e.clientX - r.left) / r.width : (e.clientY - r.top) / r.height);
      };
      const up = (e) => {
        divider.releasePointerCapture(e.pointerId);
        divider.classList.remove("is-dragging");
        divider.removeEventListener("pointermove", move);
        divider.removeEventListener("pointerup", up);
        divider.removeEventListener("pointercancel", up);
      };
      divider.addEventListener("pointermove", move);
      divider.addEventListener("pointerup", up);
      divider.addEventListener("pointercancel", up);
    });
    divider.addEventListener("keydown", (ev) => {
      const back = across() ? "ArrowLeft" : "ArrowUp";
      const fwd = across() ? "ArrowRight" : "ArrowDown";
      if (ev.key !== back && ev.key !== fwd) return;
      ev.preventDefault();
      set(share() + (ev.shiftKey ? 0.2 : 0.05) * (ev.key === back ? -1 : 1));
    });
  };

  // Leaving the large view puts every file back in one group and keeps the
  // split, which the large view opens with again.
  widget.onModalOpen = () => {
    if (!kept) return;
    const k = kept;
    kept = null;
    build(k.side);
    second = k.second;
    b = k.b;
    a = inMain(k.a) ? k.a : nextIn(k.a, inMain);
    render();
  };
  widget.onModalClose = () => {
    kept = side ? { side, second, b, a } : null;
    unbuild();
    render();
  };

  return Object.assign(api, {
    controls,
    // show opens file i where it sits: in the second group when it is
    // there (a file tree lists every file), else in the main group.
    show(i) {
      if (side && second.includes(i)) b = i;
      else a = i;
      render();
    },
    render,
  });
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
  "<p>Hover any identifier in the code for its signature and docs.</p>" +
  "<p>Tab indents. Press Esc, then Tab, to move focus out of the editor.</p>";
// The same tooltip for a ```nomi-run manual block's Run button: the block
// runs only when asked.
const MANUAL_HELP_HTML =
  "<p><strong>Interactive code.</strong> This example takes a moment to run, so edits run when you press <strong>Run</strong> (or Ctrl+Enter).</p>" +
  "<p>Hover any identifier in the code for its signature and docs.</p>" +
  "<p>Tab indents. Press Esc, then Tab, to move focus out of the editor.</p>";

// chipped wraps a static block's <pre> in the element that carries its
// language chip, so the chip stays put while the <pre> scrolls sideways.
function chipped(pre, chip) {
  const box = document.createElement("div");
  box.className = `nomi-chip nomi-chip-${chip}`;
  box.append(pre);
  return box;
}

// outputPanel builds the output area: a header holding its heading (and,
// for a ```nomi-run manual block, the Run button) over the <pre> a run
// writes into.
let panelSeq = 0;
function outputPanel(label) {
  const panel = document.createElement("div");
  panel.className = "nomi-out-panel";
  panel.setAttribute("role", "region");
  const head = document.createElement("div");
  head.className = "nomi-out-head";
  const title = document.createElement("span");
  title.className = "nomi-out-title";
  title.id = "nomi-out-" + ++panelSeq;
  title.textContent = label;
  panel.setAttribute("aria-labelledby", title.id);
  // A ```nomi-run manual block's note that the panel shows its recorded
  // output, not a run's.
  const note = document.createElement("span");
  note.className = "nomi-out-note";
  note.hidden = true;
  // A run's status while it is in flight: "Loading Nomi…" while the worker
  // loads, then "Running…" and the seconds since it started. The line is
  // hidden from screen readers; the live region beside it announces each
  // phase once, not each second.
  const status = document.createElement("span");
  status.className = "nomi-out-status";
  status.setAttribute("aria-hidden", "true");
  status.hidden = true;
  const statusText = document.createElement("span");
  const statusClock = document.createElement("span");
  statusClock.className = "nomi-out-clock";
  status.append(statusText, statusClock);
  const announce = document.createElement("span");
  announce.className = "nomi-sr-only";
  announce.setAttribute("role", "status");
  announce.setAttribute("aria-live", "polite");
  head.append(title, note, status, announce);
  const out = document.createElement("pre");
  out.className = "nomi-out";
  out.setAttribute("aria-live", "polite");
  panel.append(head, out);
  let current = null; // the run whose status shows
  const clearStatus = () => {
    if (!current) return;
    clearTimeout(current.timer);
    clearInterval(current.tick);
    current = null;
    status.hidden = true;
    statusText.textContent = "";
    statusClock.textContent = "";
    announce.textContent = "";
    out.classList.remove("is-dimmed");
  };
  return {
    panel,
    head,
    out,
    // beginRun shows a run's status after delay ms (at once for 0) and dims
    // the output it will replace. The answer's loading and started move it
    // between its phases, and end clears it; a later beginRun replaces it.
    beginRun(delay) {
      clearStatus();
      const run = { shown: false, startedAt: null, timer: null, tick: null };
      current = run;
      const paint = () => {
        if (!run.shown) return;
        const text = run.startedAt === null ? LOADING_STATUS : RUNNING_STATUS;
        if (statusText.textContent !== text) {
          statusText.textContent = text;
          announce.textContent = text;
        }
        const secs = run.startedAt === null ? 0 : Math.floor((Date.now() - run.startedAt) / 1000);
        statusClock.textContent = secs > 0 ? " " + secs + "s" : "";
      };
      const show = () => {
        run.shown = true;
        status.hidden = false;
        out.classList.add("is-dimmed");
        paint();
      };
      if (delay > 0) run.timer = setTimeout(show, delay);
      else show();
      return {
        loading() {
          if (current !== run) return;
          clearInterval(run.tick);
          run.startedAt = null;
          paint();
        },
        started() {
          if (current !== run) return;
          clearInterval(run.tick);
          run.startedAt = Date.now();
          run.tick = setInterval(paint, 1000);
          paint();
        },
        end() {
          if (current === run) clearStatus();
        },
      };
    },
    // setHeading names what the output shows: a ```nomi-run error block's
    // intended failure gets the "on purpose" heading in place of the label.
    setHeading(onPurpose) {
      title.textContent = onPurpose ? EXPECTED_ERROR_HEADING : label;
      title.classList.toggle("is-expected", onPurpose);
    },
    setNote(text) {
      note.textContent = text || "";
      note.hidden = !text;
    },
  };
}

// The note over a ```nomi-run manual block's output before its first run.
const RECORDED_NOTE = "Recorded output. Press Run to run it.";
const UNRECORDED_NOTE = "Press Run to run it.";

// showRecorded fills a ```nomi-run manual block's output panel before its
// first run: the output its expect block records (null when it has none),
// which the build carries in a hidden span (rehype-shiki.mjs), under the
// same heading a run of the block as written gets.
function showRecorded(panel, recorded, expectError, parser, query) {
  panel.out.textContent = "";
  panel.setHeading(expectError);
  if (recorded === null) {
    panel.setNote(UNRECORDED_NOTE);
    return;
  }
  panel.setNote(RECORDED_NOTE);
  if (recorded) appendRunText(panel.out, recorded, "", parser, query);
  else panel.out.textContent = "(no output)";
}

// recordedOf answers the recorded output a ```nomi-run manual block's <pre>
// carries, or null.
function recordedOf(pre) {
  const span = pre.querySelector("span[data-nomi-recorded]");
  return span ? span.textContent : null;
}

const EXPAND_ICON =
  '<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">' +
  '<path d="M2 6V2h4M10 2h4v4M14 10v4h-4M6 14H2v-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>';
const CLOSE_ICON =
  '<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">' +
  '<path d="M3.5 3.5l9 9M12.5 3.5l-9 9" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>';

const RUN_LABEL = "Run this example. Edits run when you press Run or Ctrl+Enter.";

// attachControls adds the widget's controls to host: the live dot (a
// ```nomi-run manual block, onRun, has the Run button in its output
// header instead), the formatting chip, for a ```nomi-run error block
// (expectError) the "Expected error" badge, and the button that opens the
// widget in the large view.
function attachControls(widget, host, output, expectError = false, onRun = null) {
  const controls = document.createElement("div");
  controls.className = "nomi-live-controls";
  let dot;
  if (onRun) {
    dot = document.createElement("button");
    dot.type = "button";
    dot.className = "nomi-run-button";
    dot.textContent = "Run";
    dot.setAttribute("aria-label", RUN_LABEL);
    // While a run is in flight the button is disabled with aria-disabled,
    // not the disabled attribute, so it keeps the focus a press gave it.
    dot.addEventListener("click", () => {
      if (dot.getAttribute("aria-disabled") !== "true") onRun();
    });
    output.head.append(dot);
  } else {
    dot = document.createElement("span");
    dot.className = "nomi-live";
    dot.setAttribute("role", "note");
    dot.setAttribute("aria-label", "Interactive code: edits run automatically; hover any identifier for its signature.");
    dot.tabIndex = 0;
  }
  const show = () => showAnchoredTooltip(onRun ? MANUAL_HELP_HTML : LIVE_HELP_HTML, dot);
  dot.addEventListener("mouseenter", show);
  dot.addEventListener("mouseleave", scheduleHideTooltip);
  dot.addEventListener("focus", show);
  dot.addEventListener("blur", hideTooltip);
  const formatter = document.createElement("span");
  formatter.className = "nomi-formatting";
  formatter.setAttribute("role", "status");
  formatter.setAttribute("aria-live", "polite");
  formatter.textContent = "format";
  controls.append(formatter);
  let expectedBadge = null;
  if (expectError) {
    expectedBadge = document.createElement("span");
    expectedBadge.className = "nomi-expected-error";
    expectedBadge.textContent = "Expected error";
    expectedBadge.title = "This example fails on purpose.";
    controls.append(expectedBadge);
  }
  if (!onRun) controls.append(dot);
  const expandBtn = document.createElement("button");
  expandBtn.type = "button";
  expandBtn.className = "nomi-expand";
  expandBtn.setAttribute("aria-label", "Open large");
  expandBtn.title = "Open large";
  expandBtn.innerHTML = EXPAND_ICON;
  expandBtn.addEventListener("click", () => openModal(widget, expandBtn));
  controls.append(expandBtn);
  host.appendChild(controls);
  let formattingTimer;
  return {
    controls,
    // setRunning marks a manual block's Run button while its run is in
    // flight, and setStale while its code has changed since the last run.
    // While running the button says so and is disabled.
    setRunning(active) {
      if (!onRun) return;
      dot.classList.toggle("is-running", active);
      dot.textContent = active ? "Running" : "Run";
      dot.setAttribute("aria-label", active ? "Running" : RUN_LABEL);
      if (active) dot.setAttribute("aria-disabled", "true");
      else dot.removeAttribute("aria-disabled");
    },
    setStale(stale) {
      if (onRun) dot.classList.toggle("is-stale", stale);
    },
    setExpectedError(shown) {
      if (expectedBadge) expectedBadge.hidden = !shown;
    },
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

// expectedErrorTracker follows a ```nomi-run error block as the reader edits
// it, and answers, for each run, whether its output opens with the "on
// purpose" heading. The heading describes the example as written, so it
// shows only while the code is the original and fails. The badge stays until
// an edit compiles, and comes back with the original code.
function expectedErrorTracker(original, live) {
  let compiled = false;
  return (code, error) => {
    const asWritten = code === original;
    if (asWritten) compiled = false;
    else if (!error) compiled = true;
    live.setExpectedError(Boolean(error) && (asWritten || !compiled));
    return Boolean(error) && asWritten;
  };
}

// A ```nomi-run error block fails on purpose, to compile or while it runs.
const EXPECTED_ERROR_HEADING = "This example fails on purpose:";

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
  tooltip();
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

// --- Tab in an editor ---
// Tab indents in an editor, so it cannot also move focus. As in CodeMirror,
// Esc and then Tab (or Shift+Tab) leaves the editor; any other key indents
// again. In the large view a second Esc closes it, as Esc does from any
// other control there. Each widget has one hint, which describes its
// editors to a screen reader and shows while one of them has focus.
const TAB_HINT = "Tab indents. Esc then Tab leaves the editor.";
const TAB_HINT_LEAVING = "Tab now moves focus out of the editor.";
const TAB_HINT_LEAVING_MODAL = "Tab now moves focus out of the editor. Esc closes the large view.";
let tabHintCount = 0;
// escapeClaimed is set while an editor's Esc keydown is dispatched, so the
// large view's cancel, which that Esc would fire, leaves the view open.
let escapeClaimed = false;

function tabHint(main) {
  // A span: the page's rule that spaces apart adjacent blocks in the
  // content leaves spans alone.
  const hint = document.createElement("span");
  hint.className = "nomi-tab-hint";
  hint.id = `nomi-tab-hint-${++tabHintCount}`;
  hint.setAttribute("aria-live", "polite");
  hint.textContent = TAB_HINT;
  main.append(hint);
  return hint;
}

// tabKeys makes Tab indent ta, unless Esc came just before it, or ta is
// read-only, when Tab moves focus as it does anywhere else.
function tabKeys(ta, hint) {
  ta.setAttribute("aria-describedby", hint.id);
  let leaving = false;
  const setLeaving = (on) => {
    if (leaving === on) return;
    leaving = on;
    hint.classList.toggle("is-leaving", on);
    hint.textContent = !on ? TAB_HINT : ta.closest(".is-modal") ? TAB_HINT_LEAVING_MODAL : TAB_HINT_LEAVING;
  };
  ta.addEventListener("blur", () => setLeaving(false));
  ta.addEventListener("keydown", (ev) => {
    if (ev.ctrlKey || ev.altKey || ev.metaKey || ev.isComposing) return;
    if (ev.key === "Shift") return;
    if (ta.readOnly) return;
    if (ev.key === "Escape" && !leaving) {
      ev.preventDefault();
      setLeaving(true);
      escapeClaimed = true;
      setTimeout(() => {
        escapeClaimed = false;
      });
      return;
    }
    if (ev.key === "Tab" && leaving) {
      setLeaving(false);
      return;
    }
    setLeaving(false);
    if (ev.key !== "Tab") return;
    ev.preventDefault();
    const s = ta.selectionStart, e = ta.selectionEnd;
    ta.value = ta.value.slice(0, s) + "    " + ta.value.slice(e);
    ta.selectionStart = ta.selectionEnd = s + 4;
    ta.dispatchEvent(new Event("input"));
  });
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

  const expectError = pre.dataset.nomiExpect === "error";
  // ```nomi-run manual: run only when the reader asks, and until then show
  // the output the block records.
  const manual = pre.dataset.nomiManual === "1";
  const recorded = manual ? recordedOf(pre) : null;

  const files = isStdlibTest ? null : splitFiles(source);
  if (files && files.length > 1) {
    // ```nomi-run read-only:<glob>: the data files only named, never read.
    const readOnlyGlobs = (pre.dataset.nomiReadOnly || "").split(" ").filter(Boolean);
    // ```nomi-run tool:<module>/<name>: `nomi tool <module> <name>` runs
    // in the block's project before each run.
    const tools = (pre.dataset.nomiTools || "")
      .split(" ")
      .filter(Boolean)
      .map((w) => ({ module: w.slice(0, w.indexOf("/")), name: w.slice(w.indexOf("/") + 1) }));
    return enhanceMultiFile(pre, files, parser, query, marked, DOMPurify, expectError, manual, recorded, readOnlyGlobs, tools);
  }

  const widget = document.createElement("div");
  widget.className = "nomi-interactive" + (isStdlibTest ? " nomi-stdlib-test" : "");
  // A single-file block has the same bar as a multi-file one: the file's
  // name on the left (a stdlib reference example, which is a test body, says
  // "Interactive Tests") and the block's controls on the right, above the code rather
  // than over it.
  widget.innerHTML =
    '<div class="nomi-main">' +
    '<div class="nomi-bar"><span class="nomi-path nomi-path-single"></span></div>' +
    '<div class="nomi-editor">' +
    '<pre class="nomi-tour nomi-hl" aria-hidden="true"></pre>' +
    '<textarea class="nomi-src" rows="1" spellcheck="false" autocapitalize="off" autocorrect="off" aria-label="Code"></textarea>' +
    "</div>" +
    "</div>";
  const out = outputPanel("Output");
  widget.append(out.panel);
  pre.replaceWith(widget);
  widget.querySelector(".nomi-path-single").textContent = isStdlibTest ? "Interactive Tests" : "main.nomi";
  const live = attachControls(widget, widget.querySelector(".nomi-bar"), out, expectError, manual ? () => run() : null);
  const headingFor = expectError ? expectedErrorTracker(source, live) : () => false;

  const hl = widget.querySelector(".nomi-hl");
  const ta = widget.querySelector(".nomi-src");
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
  // The page's text stays as the page wrote it until the reader edits the
  // block: a run on load never reformats it.
  let edited = false;
  const scheduleAutoFormat = (source) => {
    clearTimeout(formatDebounce);
    if (!edited) return;
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
    out.setNote("");
    live.setStale(false);
    live.setRunning(true);
    const status = out.beginRun(manual ? 0 : STATUS_DELAY_MS);
    const runner = isStdlibTest
      ? (callbacks) => dispatchStdlibTest(id, stdlibModule, stdlibContext, code, callbacks)
      : (callbacks) => dispatch(id, code, callbacks, undefined, [], manual);
    runner({
      onLoading: status.loading,
      onStart: status.started,
      onSem: (sem) => {
        if (block.req === id) hl.innerHTML = render(captures, sem, code, EDITOR_RENDER);
      },
      onRun: (output, error) => {
        if (block.req !== id) return;
        live.setRunning(false);
        status.end();
        showOutput(out, output, error, parser, query, headingFor(code, error));
        cleanFormatSource = error ? null : code;
        if (error) live.setFormatting(false);
        else scheduleAutoFormat(code);
      },
      onFail: (reason) => {
        if (block.req !== id) return;
        live.setRunning(false);
        status.end();
        showError(out, reason);
        cleanFormatSource = null;
        live.setFormatting(false);
      },
    });
  };

  let debounce;
  ta.addEventListener("input", () => {
    edited = true;
    hideTooltip();
    clearTimeout(formatDebounce);
    cleanFormatSource = null;
    blockedFormatSource = null;
    formatToken++;
    live.setFormatting(false);
    paint();
    clearTimeout(debounce);
    if (manual) live.setStale(true);
    else debounce = setTimeout(run, DEBOUNCE_MS);
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
    if (manual && ev.key === "Enter" && (ev.ctrlKey || ev.metaKey)) {
      ev.preventDefault();
      run();
    }
  });
  tabKeys(ta, tabHint(widget.querySelector(".nomi-main")));

  attachHover(ta, marked, DOMPurify, (line, col, cb) => requestHover(ta.value, line, col, cb));

  paint();
  if (!manual) {
    run();
    return;
  }
  // A manual block shows its recorded output and asks only for the
  // analyzer's tokens, which a run would bring.
  showRecorded(out, recorded, expectError, parser, query);
  const id = ++reqSeq;
  block.req = id;
  const { code, captures } = block;
  requestSem(code, null, (sem) => {
    if (block.req === id && sem) hl.innerHTML = render(captures, sem, code, EDITOR_RENDER);
  });
}

// attachHover wires LSP hover to an editor's textarea: map the mouse to a
// (line, col) on the monospace grid of the textarea's own text, ask
// `ask(line, col, cb)` for the markdown, and show it in a tooltip, the same
// content Zed shows. The grid is measured on first use, since a multi-file
// editor's hidden tabs have no layout until shown.
function attachHover(ta, marked, DOMPurify, ask) {
  let grid = null;
  let hoverDebounce;
  let lastCell = "";
  ta.addEventListener("mousemove", (ev) => {
    if (!grid) {
      const cs = getComputedStyle(ta);
      grid = {
        padL: parseFloat(cs.paddingLeft) || 0,
        padT: parseFloat(cs.paddingTop) || 0,
        lineH: parseFloat(cs.lineHeight) || 20,
        charW: measureCharWidth(cs),
      };
    }
    const rect = ta.getBoundingClientRect();
    const x = ev.clientX - rect.left - grid.padL + ta.scrollLeft;
    const y = ev.clientY - rect.top - grid.padT + ta.scrollTop;
    if (x < 0 || y < 0) {
      hideTooltip();
      return;
    }
    const line = Math.floor(y / grid.lineH) + 1;
    const col = Math.floor(x / grid.charW) + 1;
    const cell = line + ":" + col;
    if (cell === lastCell) return; // same grid cell — nothing new to ask
    lastCell = cell;
    const cx = ev.clientX, cy = ev.clientY;
    clearTimeout(hoverDebounce);
    hoverDebounce = setTimeout(() => {
      ask(line, col, (md) => {
        if (md) showTooltip(renderHoverMarkdown(md, marked, DOMPurify), cx, cy);
        else hideTooltip();
      });
    }, 120);
  });
  ta.addEventListener("mouseleave", () => {
    clearTimeout(hoverDebounce);
    lastCell = "";
    scheduleHideTooltip();
  });
  ta.addEventListener("scroll", hideTooltip);
}

// enhanceMultiFile renders a block split by `// FILE: <name>` markers as a
// editor with one pane per file, picked from a tab strip (tabsNav) or, for a
// larger block, a file tree (treeNav, usesTree), a shared output panel
// below, and a single Run dispatch that stitches all panes back together
// with the FILE markers (matching the convention runtime.SplitMultiFile
// parses).
//
// Each file carries its own textarea + tree-sitter highlight layer, so
// edits in one file don't disturb others' state, and exactly one pane
// shows at a time. Each Nomi tab gets the analyzer's semantic tokens and LSP hover from one
// analysis of the whole program (every tab, nomi.toml and the catalogs, as
// the run builds it), so a name one tab imports from another resolves. A
// tab's text is its file's text, so a position in the tab is a position in
// that file and needs no offset. TOML tabs get neither.
async function enhanceMultiFile(pre, files, parser, query, marked, DOMPurify, expectError, manual, recorded, readOnlyGlobs, tools = []) {
  const tree = usesTree(files);
  const widget = document.createElement("div");
  widget.className = "nomi-interactive nomi-multifile" + (tree ? " nomi-has-tree" : "");

  const main = document.createElement("div");
  main.className = "nomi-main";
  const bar = document.createElement("div");
  bar.className = "nomi-bar";
  const editors = document.createElement("div");
  editors.className = "nomi-editors";
  main.append(bar, editors);
  const hint = tabHint(main);
  const out = outputPanel("Output");
  widget.append(main, out.panel);

  // The Go files' highlighting, which the build put in the block (one hidden
  // span per file, rehype-shiki.mjs).
  const goHighlight = {};
  for (const span of pre.querySelectorAll("span[data-nomi-file]")) {
    goHighlight[span.dataset.nomiFile] = span.innerHTML;
  }

  // The block opens on its first file, the one the prose is about, unless
  // that is a setup file (go.mod, nomi.toml), which is never opened first.
  // A block of sibling projects opens on its project's first such file.
  const dir = projectDir(files);
  let first = files.findIndex((f) => !isSetupFile(f.name) && f.name.startsWith(dir));
  if (first < 0) first = 0;

  const panes = files.map((file, i) => {
    const lang = fileLanguage(file.name);
    const pane = document.createElement("div");
    pane.className = "nomi-editor";
    if (i !== first) pane.hidden = true;
    pane.innerHTML =
      '<pre class="nomi-tour nomi-hl" aria-hidden="true"></pre>' +
      '<textarea class="nomi-src" rows="1" spellcheck="false" autocapitalize="off" autocorrect="off"></textarea>';
    editors.appendChild(pane);

    const hl = pane.querySelector(".nomi-hl");
    const ta = pane.querySelector(".nomi-src");
    ta.value = file.body;
    // Setting the value leaves the caret at the end, and focusing the pane
    // later would scroll the textarea and the page to it.
    ta.setSelectionRange(0, 0);
    ta.setAttribute("aria-label", file.name);
    const readOnly = readOnlyReason(file, lang, readOnlyGlobs);
    if (readOnly) {
      ta.readOnly = true;
      pane.classList.add("is-readonly");
    }

    return { file, lang, readOnly, pane, hl, ta, captures: null, code: file.body };
  });

  const split = splitPanes(widget, main, bar, editors, panes, first);
  const nav = (tree ? treeNav : tabsNav)(widget, bar, panes, first, (i, focus) => {
    split.show(i);
    if (focus) panes[i].ta.focus({ preventScroll: true });
  }, panes.length > 1 ? split.controls : null);
  split.nav = nav;
  split.render();

  pre.replaceWith(widget);
  const live = attachControls(widget, bar, out, expectError, manual ? () => run() : null);
  const headingFor = expectError ? expectedErrorTracker(stitchFiles(files), live) : () => false;
  // Only the Nomi files go through the formatter: `nomi fmt` would read a
  // nomi.toml or a data file as Nomi statements and space them apart.
  const nomiPanes = panes.filter((p) => p.lang === "nomi");

  const block = { req: 0 };
  let formatDebounce;
  let formatToken = 0;
  let cleanFormatSnapshots = null;
  let blockedFormatSnapshots = null;
  // As in a single-file editor, the files keep the page's text until the
  // reader edits one of them.
  let edited = false;

  const paint = (p) => {
    p.code = p.ta.value;
    if (p.lang === "toml") {
      p.hl.innerHTML = renderToml(p.code, EDITOR_RENDER);
      return;
    }
    if (p.lang === "json") {
      p.hl.innerHTML = renderJson(p.code, EDITOR_RENDER);
      return;
    }
    if (p.lang === "text") {
      p.hl.innerHTML = render([], [], p.code, EDITOR_RENDER);
      return;
    }
    if (p.lang === "go") {
      p.hl.innerHTML = goHighlight[p.file.name] ?? render([], [], p.code, EDITOR_RENDER);
      return;
    }
    p.captures = query.captures(parser.parse(p.code).rootNode);
    p.hl.innerHTML = render(p.captures, [], p.code, EDITOR_RENDER);
  };

  const scheduleAutoFormat = (snapshots) => {
    clearTimeout(formatDebounce);
    if (!edited) return;
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
  // The tabs as the run reads them; hover sends the same text, so a hover
  // after a run reuses the worker's analysis of it.
  const stitchSource = () => stitchFiles(panes.map((p) => ({ name: p.file.name, body: p.ta.value })));
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
    const snapshots = nomiPanes.map((p) => ({ p, body: p.ta.value }));
    const stitched = stitchSource();
    const painted = nomiPanes.map((p) => ({ p, code: p.code, captures: p.captures }));
    out.setNote("");
    live.setStale(false);
    live.setRunning(true);
    const status = out.beginRun(manual ? 0 : STATUS_DELAY_MS);
    dispatch(id, stitched, {
      onLoading: status.loading,
      onStart: status.started,
      onSem: (sem) => {
        if (block.req !== id) return;
        for (const { p, code, captures } of painted) {
          if (p.code === code) p.hl.innerHTML = render(captures, sem?.[p.file.name] || [], code, EDITOR_RENDER);
        }
      },
      onRun: (output, error, written) => {
        if (block.req !== id) return;
        live.setRunning(false);
        status.end();
        // The files the block's tools wrote (the files `generate`
        // wrote) replace their tabs' text, as the tool replaces them
        // on disk.
        for (const p of panes) {
          const text = written?.[p.file.name];
          if (text !== undefined && text !== p.ta.value) {
            p.ta.value = text;
            paint(p);
          }
        }
        showOutput(out, output, error, parser, query, headingFor(stitched, error));
        cleanFormatSnapshots = error ? null : snapshots;
        if (error) live.setFormatting(false);
        else scheduleAutoFormat(snapshots);
      },
      onFail: (reason) => {
        if (block.req !== id) return;
        live.setRunning(false);
        status.end();
        showError(out, reason);
        cleanFormatSnapshots = null;
        live.setFormatting(false);
      },
    }, nomiPanes.map((p) => p.file.name), tools, manual);
  };

  let debounce;
  panes.forEach((p) => {
    p.composing = false;
    paint(p);
    if (p.lang === "nomi") {
      attachHover(p.ta, marked, DOMPurify, (line, col, cb) =>
        requestHover(stitchSource(), line, col, cb, p.file.name),
      );
    }
    p.ta.addEventListener("input", () => {
      edited = true;
      hideTooltip();
      clearTimeout(formatDebounce);
      cleanFormatSnapshots = null;
      blockedFormatSnapshots = null;
      formatToken++;
      live.setFormatting(false);
      paint(p);
      clearTimeout(debounce);
      if (manual) live.setStale(true);
      else debounce = setTimeout(run, DEBOUNCE_MS);
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
      if (manual && ev.key === "Enter" && (ev.ctrlKey || ev.metaKey)) {
        ev.preventDefault();
        run();
      }
    });
    tabKeys(p.ta, hint);
  });

  if (!manual) {
    run();
    return;
  }
  // A manual block shows its recorded output, over the files as the block
  // holds them (a tool's generated files as committed), and asks only for
  // the analyzer's tokens, which a run would bring.
  showRecorded(out, recorded, expectError, parser, query);
  const id = ++reqSeq;
  block.req = id;
  const painted = nomiPanes.map((p) => ({ p, code: p.code, captures: p.captures }));
  requestSem(stitchSource(), nomiPanes.map((p) => p.file.name), (sem) => {
    if (block.req !== id || !sem) return;
    for (const { p, code, captures } of painted) {
      if (p.code === code) p.hl.innerHTML = render(captures, sem[p.file.name] || [], code, EDITOR_RENDER);
    }
  });
}

// showOutput writes a run's output and error into the output panel, and its
// heading: with heading, the error is a ```nomi-run error block's intended
// one, and the panel's header says so. The error starts on the line after the
// output, with no blank line between, as a terminal shows a failure after
// what the program printed; internal/doctest's Shown is this text, and a
// block that stops on purpose holds it in its expect block.
function showOutput(panel, output, error, parser, query, heading = false) {
  const out = panel.out;
  panel.setHeading(heading);
  out.textContent = "";
  if (output) appendRunText(out, output, "", parser, query);
  if (error) {
    if (output && !output.endsWith("\n")) out.appendChild(document.createTextNode("\n"));
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
  // A line diff's header and rows are not source: classForRunLine colours them.
  if (DIFF_HEADER.test(line) || DIFF_ROW.test(line)) return false;

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

// A failed assertion's line diff: the header naming the two sides, and each
// row's optional source line number, its `-`, `+` or blank marker, and `│`.
const DIFF_HEADER = /^\s{4,}.*\(-\) and .*\(\+\) differ$/;
const DIFF_ROW = /^\s{4,}(?:\d+\s+)?([-+ ]) │/;

function classForRunLine(line, fallbackClass) {
  if (/^FAIL\b/.test(line) || /^test result: FAILED\b/.test(line)) return "nomi-test-fail";
  if (/^ok\b/.test(line) || /^test result: ok\b/.test(line)) return "nomi-test-ok";
  if (/^  line \d+/.test(line)) return "nomi-test-detail";
  if (DIFF_HEADER.test(line)) return "nomi-test-detail";
  const row = line.match(DIFF_ROW);
  if (row) return row[1] === "-" ? "nomi-test-fail" : row[1] === "+" ? "nomi-test-ok" : fallbackClass;
  if (/^\s{4,}(where\b|defined as:|pipeline values:|values:|actual:)/.test(line)) return "nomi-test-detail";
  if (/^    /.test(line)) return "nomi-test-code";
  return fallbackClass;
}

function showError(panel, msg) {
  const out = panel.out;
  panel.setHeading(false);
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
// tooltip answers the shared tooltip element, inside the open large view
// while there is one: a modal <dialog> sits in the top layer, above
// anything in the page's body whatever its z-index.
function tooltip() {
  if (!tipEl) {
    tipEl = document.createElement("div");
    tipEl.className = "nomi-hover";
    // Hovering the tooltip itself cancels a pending hide so the user can reach
    // it (e.g. to scroll horizontally on a long signature); leaving the tooltip
    // dismisses it. Editor `mousemove` may re-show it over the same token.
    tipEl.addEventListener("mouseenter", () => clearTimeout(hideTimer));
    tipEl.addEventListener("mouseleave", hideTooltip);
  }
  const host = modal?.current ? modal.dialog : document.body;
  if (tipEl.parentNode !== host) host.appendChild(tipEl);
  return tipEl;
}

function showTooltip(html, x, y) {
  tooltip();
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
  pre.replaceWith(chipped(out, "nomi"));
}

// --- lazy activation: enhance/highlight each block as it nears the viewport ---

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

// --- the Examples intro's filter (generated by cmd/nomi-docgen/examples.go) ---
// The page ships the selects hidden and the whole table, so without this
// script a reader sees every example. Each row carries data-subject and
// data-chapters; a select picks one value per kind, and a row shows when it
// matches both. The choice lives in the URL (?subject=, ?chapter=), so an
// example page's subject link opens the table filtered to it.
function filterExamples(controls, table) {
  const rows = [...table.querySelectorAll("tbody tr")];
  const selects = [...controls.querySelectorAll("select[data-filter]")];
  const count = controls.querySelector("[data-examples-count]");
  const known = (kind, value) =>
    selects.some((s) => s.dataset.filter === kind && [...s.options].some((o) => o.value === value));
  const params = new URLSearchParams(location.search);
  const chosen = {};
  for (const kind of ["subject", "chapter"]) {
    const value = params.get(kind) || "";
    chosen[kind] = known(kind, value) ? value : "";
  }
  const apply = () => {
    for (const s of selects) s.value = chosen[s.dataset.filter];
    let shown = 0;
    for (const row of rows) {
      const match =
        (!chosen.subject || row.dataset.subject === chosen.subject) &&
        (!chosen.chapter || row.dataset.chapters.split(" ").includes(chosen.chapter));
      row.hidden = !match;
      if (match) shown++;
    }
    count.textContent = shown === rows.length ? `Showing all ${rows.length} examples.` : `Showing ${shown} of ${rows.length} examples.`;
  };
  controls.addEventListener("change", (e) => {
    const s = e.target.closest("select[data-filter]");
    if (!s) return;
    chosen[s.dataset.filter] = s.value;
    const url = new URL(location.href);
    for (const kind of ["subject", "chapter"]) {
      if (chosen[kind]) url.searchParams.set(kind, chosen[kind]);
      else url.searchParams.delete(kind);
    }
    history.replaceState(history.state, "", url);
    apply();
  });
  apply();
  controls.hidden = false;
}
{
  const controls = document.querySelector("[data-examples-filter]");
  const table = document.querySelector("table[data-examples]");
  if (controls && table) filterExamples(controls, table);
}
