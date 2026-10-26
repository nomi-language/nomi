import { codeToHast } from "shiki";

function textContent(node) {
  if (!node) return "";
  if (node.type === "text") return node.value || "";
  if (!node.children) return "";
  return node.children.map(textContent).join("");
}

function hasClass(node, cls) {
  const props = node?.properties || {};
  const classes = props.className || props.class || [];
  return Array.isArray(classes) ? classes.includes(cls) : String(classes).split(/\s+/).includes(cls);
}

function addClass(node, cls) {
  const props = node.properties || {};
  const classes = props.className || props.class || [];
  const list = Array.isArray(classes) ? classes : String(classes).split(/\s+/).filter(Boolean);
  if (!list.includes(cls)) list.push(cls);
  node.properties = { ...props, className: list.join(" ") };
}

function setCodeLabel(pre, label) {
  addClass(pre, `nomi-code-${label.toLowerCase()}`);
}

// Static blocks Shiki highlights at build time, by fence language: the
// shiki grammar and the chip class (`nomi-code-<chip>`) each one gets.
const SHIKI_LANGS = {
  go: { lang: "go", chip: "go" },
  js: { lang: "javascript", chip: "js" },
  javascript: { lang: "javascript", chip: "js" },
  toml: { lang: "toml", chip: "toml" },
  sql: { lang: "sql", chip: "sql" },
  json: { lang: "json", chip: "json" },
  csv: { lang: "csv", chip: "csv" },
};

async function highlight(code, { lang, chip }) {
  const root = await codeToHast(code, {
    lang,
    themes: { light: "one-light", dark: "one-dark-pro" },
  });
  const pre = root.children?.find((node) => node.type === "element" && node.tagName === "pre");
  const codeNode = pre?.children?.find((node) => node.type === "element" && node.tagName === "code");
  return {
    type: "element",
    tagName: "div",
    properties: { className: `nomi-chip nomi-chip-${chip}` },
    children: [
      {
        type: "element",
        tagName: "pre",
        properties: { className: `nomi-tour nomi-code-${chip}` },
        children: codeNode?.children || [],
      },
    ],
  };
}

// A ```console block is a terminal transcript or the compiler's output. It
// gets no chip, since output is not a language; each line is classed the way
// `nomi` colours it in a terminal: the `$` prompt, `error:` and `warning:`
// labels, `help:` and `note:` labels, the `-->` location, the source gutter,
// the underline under the source, and `FAIL`/`ok` result lines.
function span(cls, value) {
  return { type: "element", tagName: "span", properties: { className: cls }, children: [{ type: "text", value }] };
}

function consoleLine(line, severity) {
  let m;
  if ((m = /^(\$)( .*?)(\s+#.*)?$/.exec(line))) {
    const nodes = [span("con-prompt", m[1]), span("con-cmd", m[2])];
    if (m[3]) nodes.push(span("con-comment", m[3]));
    return { nodes, severity };
  }
  if ((m = /^(error|warning)(\[[^\]]*\])?(:)(.*)$/.exec(line))) {
    const cls = m[1] === "error" ? "con-error" : "con-warning";
    return { nodes: [span(cls, m[1] + (m[2] || "") + m[3]), span("con-message", m[4])], severity: cls };
  }
  if ((m = /^(FAIL)(\b.*)$/.exec(line))) return { nodes: [span("con-error", m[1]), { type: "text", value: m[2] }], severity };
  if ((m = /^(ok)( .*)$/.exec(line))) return { nodes: [span("con-ok", m[1]), { type: "text", value: m[2] }], severity };
  if ((m = /^(\s*=?\s*)(help|note)(:)(.*)$/.exec(line))) {
    return { nodes: [{ type: "text", value: m[1] }, span("con-label", m[2] + m[3]), { type: "text", value: m[4] }], severity };
  }
  if ((m = /^(\s*-->\s.*)$/.exec(line))) return { nodes: [span("con-location", m[1])], severity };
  if ((m = /^(\s*\d*\s*\|)(\s*)([\^~-]+.*)?(.*)$/.exec(line))) {
    const nodes = [span("con-gutter", m[1])];
    if (m[3] && !/\d/.test(m[1])) {
      const under = /^[\^~-]+/.exec(m[3])[0];
      nodes.push({ type: "text", value: m[2] }, span(severity || "con-error", under), { type: "text", value: m[3].slice(under.length) + m[4] });
    } else {
      nodes.push({ type: "text", value: m[2] + (m[3] || "") + m[4] });
    }
    return { nodes, severity };
  }
  return { nodes: [{ type: "text", value: line }], severity };
}

function consoleBlock(code) {
  const children = [];
  let severity = null;
  code.split("\n").forEach((line, i) => {
    if (i > 0) children.push({ type: "text", value: "\n" });
    const out = consoleLine(line, severity);
    severity = out.severity;
    children.push(...out.nodes);
  });
  return { type: "element", tagName: "pre", properties: { className: "nomi-tour nomi-console" }, children };
}

// A ```tree block is a directory listing drawn with box characters. It is
// rebuilt as nested lists whose connecting lines are CSS borders: box
// characters only join at a line height of 1, which is too tight to read.
// A directory (a name ending in `/`) is bold.
function treeBlock(code) {
  const lines = code.split("\n").filter((line) => line.trim() !== "");
  const root = { name: lines[0].trim(), children: [] };
  const stack = [root];
  for (const line of lines.slice(1)) {
    const m = /^((?:[│ ] {3})*)[├└]── (.*)$/.exec(line);
    if (!m) continue;
    const depth = m[1].length / 4 + 1;
    const node = { name: m[2], children: [] };
    stack.length = depth;
    stack[depth - 1].children.push(node);
    stack[depth] = node;
  }
  const label = (name) =>
    name.endsWith("/") ? span("tree-dir", name) : { type: "text", value: name };
  const list = (nodes) => ({
    type: "element",
    tagName: "ul",
    properties: {},
    children: nodes.map((n) => ({
      type: "element",
      tagName: "li",
      properties: {},
      children: [label(n.name), ...(n.children.length ? [list(n.children)] : [])],
    })),
  });
  return {
    type: "element",
    tagName: "div",
    properties: { className: "nomi-tour nomi-filetree" },
    children: [label(root.name), list(root.children)],
  };
}

// A ```gomod block is a go.mod file: its directives are keywords, module
// paths plain, versions numbers and `//` comments muted. Shiki has no
// grammar for it, and these four kinds are all a go.mod holds.
function goModBlock(code) {
  const children = [];
  code.split("\n").forEach((line, i) => {
    if (i > 0) children.push({ type: "text", value: "\n" });
    const parts = line.split(/(\/\/.*$|\bv?\d+\.\d+(?:\.\d+)?(?:-[\w.]+)?\b|=>|^\s*(?:module|go|toolchain|require|replace|exclude|retract)\b|[()])/);
    for (const part of parts) {
      if (!part) continue;
      if (part.startsWith("//")) children.push(span("tok-comment", part));
      else if (/^\s*(module|go|toolchain|require|replace|exclude|retract)$/.test(part)) children.push(span("tok-kw", part));
      else if (/^v?\d/.test(part)) children.push(span("tok-num", part));
      else if (part === "=>" || part === "(" || part === ")") children.push(span("tree-glyph", part));
      else children.push({ type: "text", value: part });
    }
  });
  return chipped("nomi-tour nomi-code-gomod", "gomod", children);
}

// A multi-file ```nomi-run block's Go files (`// FILE: urltools.go`), which
// its editor shows read-only: the tour links that Go ahead of time
// (internal/tourgo). Shiki highlights each here, at build time, into a hidden
// span inside the block's <pre> named by the file, which tour-client.mjs
// moves into the file's tab. The text is the section as the client's
// splitFiles cuts it: its lines up to the next marker, trailing blank lines
// dropped.
const FILE_MARKER = /^\s*\/\/\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$/;

function goSections(source) {
  const sections = [];
  let current = null;
  for (const line of source.split("\n")) {
    const m = FILE_MARKER.exec(line);
    if (m) {
      current = { name: m[1], lines: [] };
      sections.push(current);
    } else if (current) {
      current.lines.push(line);
    }
  }
  return sections
    .filter((s) => s.name.endsWith(".go"))
    .map((s) => ({ name: s.name, code: s.lines.join("\n").replace(/\n+$/, "") }));
}

async function goFileSpans(source) {
  const spans = [];
  for (const { name, code } of goSections(source)) {
    const root = await codeToHast(code, {
      lang: "go",
      themes: { light: "one-light", dark: "one-dark-pro" },
    });
    const pre = root.children?.find((node) => node.type === "element" && node.tagName === "pre");
    const codeNode = pre?.children?.find((node) => node.type === "element" && node.tagName === "code");
    spans.push({
      type: "element",
      tagName: "span",
      properties: { hidden: true, dataNomiFile: name },
      children: codeNode?.children || [],
    });
  }
  return spans;
}

function chipped(preClass, chip, children) {
  return {
    type: "element",
    tagName: "div",
    properties: { className: `nomi-chip nomi-chip-${chip}` },
    children: [{ type: "element", tagName: "pre", properties: { className: preClass }, children }],
  };
}

export default function rehypeShiki() {
  return async function transform(tree) {
    const edits = [];
    const goFiles = [];

    function visit(node, parent) {
      if (!node || !node.children) return;
      for (let i = 0; i < node.children.length; i++) {
        const child = node.children[i];
        if (child?.type === "element" && child.tagName === "pre") {
          const code = child.children?.find((c) => c.type === "element" && c.tagName === "code");
          const fence = Object.keys(SHIKI_LANGS).find((name) => hasClass(code, `language-${name}`));
          if (fence) {
            edits.push({ parent: node, index: i, fence, code: textContent(code).replace(/\n+$/, "") });
          } else if (hasClass(code, "language-tree")) {
            node.children[i] = treeBlock(textContent(code).replace(/\n+$/, ""));
            continue;
          } else if (hasClass(code, "language-gomod")) {
            node.children[i] = goModBlock(textContent(code).replace(/\n+$/, ""));
            continue;
          } else if (hasClass(code, "language-console")) {
            node.children[i] = consoleBlock(textContent(code).replace(/\n+$/, ""));
            continue;
          } else if (
            hasClass(code, "language-nomi") ||
            hasClass(code, "language-nomi-run") ||
            hasClass(code, "language-nomi-test")
          ) {
            setCodeLabel(child, "Nomi");
            // ```nomi-run error: the example fails to compile on purpose.
            // The fence's words after the language arrive as the code
            // node's meta; the client reads these attributes to show the
            // "Expected error" badge, to give a ```nomi-run manual block a
            // Run button, to show a ```nomi-run expand block's expansion as
            // its output, to make the data files a `read-only:<glob>`
            // word matches read-only, and to run a `tool:<module>/<name>`
            // word's tool before each run.
            const words = String(code.data?.meta || "").split(/\s+/);
            if (hasClass(code, "language-nomi-run")) {
              const props = { ...(child.properties || {}) };
              if (words.includes("error")) props.dataNomiExpect = "error";
              if (words.includes("manual")) props.dataNomiManual = "1";
              if (words.includes("expand")) props.dataNomiExpand = "1";
              const readOnly = words.filter((w) => w.startsWith("read-only:")).map((w) => w.slice("read-only:".length));
              if (readOnly.length) props.dataNomiReadOnly = readOnly.join(" ");
              // ```nomi-run tool:<module>/<name>: the tools each run runs first.
              const tools = words.filter((w) => w.startsWith("tool:")).map((w) => w.slice("tool:".length));
              if (tools.length) props.dataNomiTools = tools.join(" ");
              child.properties = props;
            }
            if (hasClass(code, "language-nomi-run")) {
              goFiles.push({ pre: child, source: textContent(code) });
            }
          }
        }
        visit(child, node);
      }
    }

    visit(tree, null);
    for (const edit of edits) {
      edit.parent.children[edit.index] = await highlight(edit.code, SHIKI_LANGS[edit.fence]);
    }
    for (const { pre, source } of goFiles) {
      pre.children.push(...(await goFileSpans(source)));
    }
  };
}
