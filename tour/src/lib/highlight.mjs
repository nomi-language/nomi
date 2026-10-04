// Shared Nomi syntax highlighter: turns tree-sitter highlight captures into
// HTML <span>s. Platform-agnostic (no web-tree-sitter import) — the caller
// parses + queries (in Node for the static tour, in the browser for the
// editable tour examples) and passes the captures here. The grammar +
// queries/highlights.scm are the source of truth for the tour and Helix; Zed
// keeps a reverse-priority query copy for its own highlighter.

// Maps a tree-sitter capture name to a token CSS class. Dotted names fall back
// to their prefix (e.g. "function.call" -> "function"); unmapped names render
// as default foreground.
const CLASS = {
  keyword: "tok-kw",
  string: "tok-str",
  number: "tok-num",
  boolean: "tok-num",
  comment: "tok-comment",
  type: "tok-type",
  enum: "tok-type",
  constructor: "tok-fn",
  function: "tok-fn",
  "module.path_separator": "tok-type",
  module: "tok-ns",
  namespace: "tok-ns",
  "punctuation.special": "tok-special",
  property: "tok-prop",
  "variable.other.member": "tok-prop",
  "variable.parameter": "tok-param",
  // variable, operator, punctuation.* -> default foreground.
};

export function classForCapture(name) {
  let n = name;
  while (n) {
    if (CLASS[n]) return CLASS[n];
    const dot = n.lastIndexOf(".");
    if (dot < 0) break;
    n = n.slice(0, dot);
  }
  return "";
}

function escapeHtml(s) {
  return s.replace(/[&<>]/g, (c) => (c === "&" ? "&amp;" : c === "<" ? "&lt;" : "&gt;"));
}

function trailingWhitespaceChars(code) {
  const trailing = new Array(code.length).fill(false);
  let lineEnd = 0;
  while (lineEnd <= code.length) {
    const nextNewline = code.indexOf("\n", lineEnd);
    const end = nextNewline < 0 ? code.length : nextNewline;
    let i = end - 1;
    while (i >= lineEnd && (code[i] === " " || code[i] === "\t")) {
      trailing[i] = true;
      i--;
    }
    if (nextNewline < 0) break;
    lineEnd = nextNewline + 1;
  }
  return trailing;
}

function renderChar(ch, isTrailingWhitespace) {
  if (!isTrailingWhitespace) return escapeHtml(ch);
  if (ch === "\t") return '<span class="nomi-trailing-space">→</span>';
  return '<span class="nomi-trailing-space">·</span>';
}

// semClass maps an analyzer semantic-token type to a token CSS class.
const SEM_CLASS = {
  function: "tok-fn",
  struct: "tok-type",
  enum: "tok-type",
  interface: "tok-type",
  type: "tok-type",
  enumMember: "tok-variant",
  parameter: "tok-param",
  variable: "tok-var",
  property: "tok-prop",
  module: "tok-ns",
  namespace: "tok-ns",
  comment: "tok-comment",
};

function lineStarts(src) {
  const offs = [0, 0]; // index 0 unused; index L = byte offset of line L start
  for (let i = 0; i < src.length; i++) if (src[i] === "\n") offs.push(i + 1);
  return offs;
}

function applyRegexLiteralOverlay(code, cls) {
  const tag = "Regex`";
  let search = 0;
  while (search < code.length) {
    const tagStart = code.indexOf(tag, search);
    if (tagStart < 0) break;
    const before = tagStart === 0 ? "" : code[tagStart - 1];
    if (/[A-Za-z0-9_]/.test(before)) {
      search = tagStart + tag.length;
      continue;
    }
    const bodyStart = tagStart + tag.length;
    const bodyEnd = code.indexOf("`", bodyStart);
    if (bodyEnd < 0) break;

    let i = bodyStart;
    while (i < bodyEnd) {
      const ch = code[i];
      if (ch === "\\") {
        cls[i] = "tok-special";
        if (i + 1 < bodyEnd) cls[i + 1] = "tok-special";
        i += 2;
        continue;
      }
      if (ch === "[") {
        cls[i] = "tok-special";
        i++;
        while (i < bodyEnd) {
          if (code[i] === "\\") {
            cls[i] = "tok-special";
            if (i + 1 < bodyEnd) cls[i + 1] = "tok-special";
            i += 2;
            continue;
          }
          cls[i] = code[i] === "]" ? "tok-special" : "tok-type";
          if (code[i] === "]") {
            i++;
            break;
          }
          i++;
        }
        continue;
      }
      if (ch === "{") {
        cls[i] = "tok-special";
        i++;
        while (i < bodyEnd && code[i] !== "}") {
          cls[i] = /[0-9]/.test(code[i]) ? "tok-num" : "tok-special";
          i++;
        }
        if (i < bodyEnd) {
          cls[i] = "tok-special";
          i++;
        }
        continue;
      }
      if ("^$.*+?|()".includes(ch)) cls[i] = "tok-special";
      i++;
    }
    search = bodyEnd + 1;
  }
}

// render produces highlighted HTML by layering, exactly as an editor does:
// tree-sitter `captures` form the base (keywords/strings/numbers/punctuation),
// then the analyzer's `semTokens` (each {line, col, len, type}, 1-based)
// override identifiers (so a module reads as a module, a parameter as a
// parameter, etc.) — the same syntax-plus-semantic-tokens model Zed uses.
export function render(captures, semTokens, code, options = {}) {
  const n = code.length;
  const cls = new Array(n).fill("");
  const size = new Array(n).fill(Infinity);
  for (const cap of captures) {
    // A capture may map to "" (default foreground) — it still claims its range,
    // so a more-specific default capture (e.g. an `operator` or
    // `punctuation.special` inside a string interpolation) overrides a broader
    // coloured one (the enclosing `string`), instead of inheriting its colour.
    const c = classForCapture(cap.name);
    const s = cap.node.startIndex;
    const e = cap.node.endIndex;
    const sz = e - s;
    for (let i = s; i < e && i < n; i++) {
      if (sz < size[i]) {
        cls[i] = c;
        size[i] = sz;
      }
    }
  }
  applyRegexLiteralOverlay(code, cls);
  // Semantic overlay: analyzer tokens unconditionally override the base for
  // their (identifier) ranges.
  if (semTokens && semTokens.length) {
    const starts = lineStarts(code);
    for (const t of semTokens) {
      const c = SEM_CLASS[t.type];
      if (c === undefined) continue;
      const base = starts[t.line];
      if (base === undefined) continue;
      const s = base + t.col - 1;
      for (let i = s; i < s + t.len && i < n; i++) cls[i] = c;
    }
  }
  const trailing = options.showTrailingWhitespace ? trailingWhitespaceChars(code) : null;
  let out = "";
  let i = 0;
  while (i < n) {
    const c = cls[i];
    let j = i;
    while (j < n && cls[j] === c && (!trailing || trailing[j] === trailing[i])) j++;
    const chunk = trailing
      ? code.slice(i, j).split("").map((ch, idx) => renderChar(ch, trailing[i + idx])).join("")
      : escapeHtml(code.slice(i, j));
    out += c ? `<span class="${c}">${chunk}</span>` : chunk;
    i = j;
  }
  return out;
}
