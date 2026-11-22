// Post-build SSR highlighting: rewrite Nomi code blocks in
// the built dist/ HTML to highlighted markup, so the page arrives highlighted
// (no flash before the client enhances). Runs in plain Node — the same engine as
// the client (web-tree-sitter grammar + queries/highlights.scm + the analyzer's
// nomiSemTokens + highlight.mjs) — so it sidesteps the Vite/wasm SSR bundling
// that blocked doing this inside Astro. Invoked by `npm run build`.
//
//   node prehighlight.mjs [dist-dir]
//
// - language-nomi (static): emit the final <pre class="nomi-tour"> directly,
//   inside the chip wrapper the client's highlightStatic gives it.
// - language-nomi-run / language-nomi-test (runnable): keep <pre><code> (the
//   client still finds + turns it into the editor) but highlight inside and add
//   the .nomi-tour class so it's styled pre-enhancement.
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { webcrypto } from "node:crypto";
import { gunzipSync } from "node:zlib";
import { fileURLToPath } from "node:url";
import { Parser, Language, Query } from "./public/nomi/web-tree-sitter.js";
import { fileLanguage, fileSections, render, renderData, renderFiles } from "./public/nomi/highlight.mjs";

if (!globalThis.crypto) globalThis.crypto = webcrypto;
const asset = (f) => fileURLToPath(new URL(`./public/nomi/${f}`, import.meta.url));

// Semantic tokens (interpreter wasm).
await import(new URL("./public/nomi/wasm_exec.js", import.meta.url).href);
// The in-memory file system the page's worker gives Go, where a block that
// names modules is written to be analyzed and lowered, so its tokens, and
// the marks on what it reaches outside, are the ones its editor shows.
await import(new URL("./public/nomi/go-memfs.js", import.meta.url).href);
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(
  gunzipSync(readFileSync(asset("nomi.wasm.gz"))), // staged gzipped; see scripts/build-tour-wasm.sh
  go.importObject,
);
go.run(instance);

// Syntax (tree-sitter grammar + queries).
await Parser.init({ locateFile: () => asset("web-tree-sitter.wasm") });
const lang = await Language.load(new Uint8Array(readFileSync(asset("tree-sitter-nomi.wasm"))));
const parser = new Parser();
parser.setLanguage(lang);
const query = new Query(lang, readFileSync(asset("highlights.scm"), "utf8"));

function decode(s) {
  return s
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#x([0-9a-fA-F]+);/g, (_, h) => String.fromCodePoint(parseInt(h, 16))) // e.g. Astro's &#x3C; for <
    .replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(parseInt(d, 10)))
    .replace(/&amp;/g, "&"); // last, so &amp;lt; -> &lt; not <
}
// highlight renders a block. A multi-file one is rendered a file at a time,
// each in its own language as its editor tab shows it (renderFiles): a Nomi
// file with the semantic tokens the editor gets for its tab, from the
// analysis of the whole block; a nomi.toml or JSON file in its colours.
function highlight(code) {
  const sections = fileSections(code);
  if (!sections) {
    const captures = query.captures(parser.parse(code).rootNode);
    const sem = JSON.parse(globalThis.nomiSemTokens(code) || "[]");
    return render(captures, sem, code);
  }
  const nomiFiles = sections.files.filter((f) => fileLanguage(f.name) === "nomi").map((f) => f.name);
  const sem = JSON.parse(globalThis.nomiSemTokensIn(code, ...nomiFiles) || "{}");
  return renderFiles(sections, (name, lang, text) => {
    if (lang === "nomi") return render(query.captures(parser.parse(text).rootNode), sem[name] ?? [], text);
    return renderData(lang, text);
  });
}

// A block's recorded output follows its <code> as a hidden span, which is
// kept as it is.
const BLOCK = /<pre([^>]*)><code class="language-(nomi(?:-run|-test)?)"([^>]*)>([\s\S]*?)<\/code>((?:<span hidden[\s\S]*?)?)<\/pre>/g;
function processHtml(html) {
  const highlighted = html.replace(BLOCK, (_m, preAttrs, lang, codeAttrs, body, hidden) => {
    const hl = highlight(decode(body).replace(/\n$/, ""));
    return lang === "nomi"
      ? `<div class="nomi-chip nomi-chip-nomi"><pre class="nomi-tour nomi-code-nomi">${hl}</pre></div>`
      : `<pre class="nomi-tour"${preAttrs}><code class="language-${lang}"${codeAttrs}>${hl}</code>${hidden}</pre>`;
  });
  return highlighted.replace(
    /<pre class="nomi-tour"([^>]*)><span class="line">/g,
    '<pre class="nomi-tour nomi-code-go"$1><span class="line">',
  );
}

function walk(dir) {
  for (const e of readdirSync(dir)) {
    const p = `${dir}/${e}`;
    if (statSync(p).isDirectory()) walk(p);
    else if (p.endsWith(".html")) {
      const html = readFileSync(p, "utf8");
      const out = processHtml(html);
      if (out !== html) writeFileSync(p, out);
    }
  }
}

const distDir = fileURLToPath(new URL(process.argv[2] || "dist", import.meta.url));
walk(distDir);
console.log("prehighlight: done");
process.exit(0);
