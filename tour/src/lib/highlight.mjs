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
  const offs = [0, 0]; // index 0 unused; index L = string index of line L start
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
// then the analyzer's `semTokens` (each {line, col, len, type}, 1-based, col
// and len in UTF-16 code units, which is how `code` indexes)
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
  return emit(code, cls, options);
}

// emit writes code as HTML with each run of characters of one class in a
// <span> of that class, and with trailing whitespace marked when
// options.showTrailingWhitespace is set. It writes every character of code
// once, so an editor's highlight layer keeps the textarea's lines.
function emit(code, cls, options) {
  const n = code.length;
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

// renderToml highlights a TOML file (a multi-file example's `nomi.toml` or
// message catalog) in the colours the build-time Shiki pass gives a static
// ```toml block: table names, keys, strings, numbers, booleans and dates,
// and comments, with punctuation in the default colour. It is a small
// scanner, not a validating parser: text it does not recognize stays the
// default colour.
// renderJson colours a JSON tab: object keys, strings, and numbers and the
// literals true, false and null. Text that is not JSON keeps the colours of
// whatever parts of it read as JSON.
export function renderJson(code, options = {}) {
  const n = code.length;
  const cls = new Array(n).fill("");
  const mark = (s, e, c) => { for (let k = s; k < e && k < n; k++) cls[k] = c; };
  let i = 0;
  while (i < n) {
    const ch = code[i];
    if (ch === '"') {
      let j = i + 1;
      while (j < n && code[j] !== '"' && code[j] !== "\n") j += code[j] === "\\" ? 2 : 1;
      const e = Math.min(n, j + 1);
      let k = e;
      while (k < n && (code[k] === " " || code[k] === "\t" || code[k] === "\r" || code[k] === "\n")) k++;
      mark(i, e, code[k] === ":" ? "json-key" : "json-str");
      i = e;
      continue;
    }
    const word = /^(-?\d+(\.\d+)?([eE][+-]?\d+)?|true|false|null)\b/.exec(code.slice(i, i + 64));
    if (word && !/[A-Za-z0-9_]/.test(code[i - 1] ?? "")) {
      mark(i, i + word[0].length, "json-num");
      i += word[0].length;
      continue;
    }
    i++;
  }
  return emit(code, cls, options);
}

export function renderToml(code, options = {}) {
  const n = code.length;
  const cls = new Array(n).fill("");
  const mark = (s, e, c) => { for (let k = s; k < e && k < n; k++) cls[k] = c; };
  const BARE = /[A-Za-z0-9_\-]/;
  const VALUE = /[A-Za-z0-9_\-.:+]/;
  // A string from i (on its opening quote) answers its end, past the
  // closing quote, or the end of the line or text for one left open.
  const stringEnd = (i) => {
    const q = code[i];
    if (code.startsWith(q.repeat(3), i)) {
      const close = code.indexOf(q.repeat(3), i + 3);
      if (close < 0) return n;
      let e = close + 3;
      while (e < n && code[e] === q && e - close < 5) e++; // up to two quotes before the closing three
      return e;
    }
    let j = i + 1;
    while (j < n && code[j] !== "\n") {
      if (q === '"' && code[j] === "\\") { j += 2; continue; }
      if (code[j] === q) return j + 1;
      j++;
    }
    return j;
  };
  const nesting = []; // "{" and "[" of the inline tables and arrays open around i
  let keyPos = true; // a key may start here
  let lineStart = true; // only whitespace since the last line break
  let i = 0;
  while (i < n) {
    const ch = code[i];
    if (ch === "\n") {
      if (nesting.length === 0) keyPos = true;
      lineStart = true;
      i++;
      continue;
    }
    if (ch === " " || ch === "\t" || ch === "\r") { i++; continue; }
    if (ch === "#") {
      let e = code.indexOf("\n", i);
      if (e < 0) e = n;
      mark(i, e, "toml-comment");
      i = e;
      continue;
    }
    if (lineStart && nesting.length === 0 && ch === "[") {
      // A table header: `[name]`, `[[name]]`, `[a."b c".d]`.
      lineStart = false;
      let j = i;
      while (j < n && code[j] === "[") j++;
      while (j < n && code[j] !== "]" && code[j] !== "\n" && code[j] !== "#") {
        if (code[j] === '"' || code[j] === "'") {
          const e = stringEnd(j);
          mark(j, e, "toml-table");
          j = e;
        } else {
          if (BARE.test(code[j])) cls[j] = "toml-table";
          j++;
        }
      }
      while (j < n && code[j] === "]") j++;
      i = j;
      keyPos = false;
      continue;
    }
    lineStart = false;
    if (ch === '"' || ch === "'") {
      const e = stringEnd(i);
      mark(i, e, keyPos ? "toml-key" : "toml-str");
      i = e;
      continue;
    }
    if (keyPos && BARE.test(ch)) {
      let e = i;
      while (e < n && (BARE.test(code[e]) || code[e] === ".")) e++;
      mark(i, e, "toml-key");
      i = e;
      continue;
    }
    if (!keyPos && VALUE.test(ch)) {
      let e = i;
      while (e < n && VALUE.test(code[e])) e++;
      const word = code.slice(i, e);
      if (/^(true|false|[+-]?(inf|nan)|[+-]?[0-9][0-9A-Za-z_.:+\-]*)$/.test(word)) mark(i, e, "toml-num");
      i = e;
      continue;
    }
    switch (ch) {
      case "=":
        keyPos = false;
        break;
      case "{":
        nesting.push("{");
        keyPos = true;
        break;
      case "[":
        nesting.push("[");
        break;
      case "}":
      case "]":
        nesting.pop();
        break;
      case ",":
        keyPos = nesting[nesting.length - 1] === "{";
        break;
    }
    i++;
  }
  return emit(code, cls, options);
}
