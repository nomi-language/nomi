// A multi-file block's files are highlighted each in its own language, in
// the page's static rendering (prehighlight.mjs, through renderFiles) as in
// its editor's tabs (tour-client.mjs, through renderData): a nomi.toml as
// TOML, and a go.mod or a data file uncoloured, never as Nomi. Both cut the
// block with fileSections, so a tab holds the text the static rendering
// colours for it.
//
//   npm --prefix tour test
import { test } from "node:test";
import assert from "node:assert/strict";
import { fileLanguage, fileSections, renderData, renderFiles, splitFiles } from "../src/lib/highlight.mjs";

const block = [
  "// FILE: app/main.nomi",
  "import std::io",
  "",
  "fn main()",
  "end",
  "",
  "// FILE: app/nomi.toml",
  "",
  "[effects.self]",
  'std = ["std::io"]',
  "",
  "[effects.deps.modules]",
  '"lib.example/prices" = []  # computes',
  "",
  "// FILE: app/go.mod",
  "module example.com/app",
  "",
  "// FILE: notes.txt",
  "Buy milk.",
].join("\n");

const unescape = (html) =>
  html.replace(/<[^>]+>/g, "").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");

test("each file is rendered in its own language, and the block's text is kept", () => {
  const seen = [];
  const html = renderFiles(fileSections(block), (name, lang, text) => {
    seen.push([name, lang, text]);
    return lang === "nomi" ? text : renderData(lang, text);
  });
  assert.equal(unescape(html), block);
  assert.deepEqual(
    seen.map(([name, lang]) => [name, lang]),
    [["app/main.nomi", "nomi"], ["app/nomi.toml", "toml"], ["app/go.mod", "text"], ["notes.txt", "text"]],
  );
  // A file is given as its tab holds it.
  assert.deepEqual(
    seen.map(([name, , text]) => ({ name, base64: false, body: text })),
    splitFiles(block),
  );
  assert.match(html, /<span class="tok-comment">\/\/ FILE: app\/nomi\.toml<\/span>/);
  assert.match(html, /\[<span class="toml-table">effects<\/span>\.<span class="toml-table">self<\/span>\]/);
  assert.match(html, /<span class="toml-key">"lib\.example\/prices"<\/span>/);
  assert.match(html, /<span class="toml-comment"># computes<\/span>/);
  assert.match(html, /\nmodule example\.com\/app\n/, "go.mod is uncoloured");
});

test("a tab's text drops the blank lines around its file", () => {
  assert.deepEqual(splitFiles("// FILE: a.toml\n\n\nx = 1\n\n// FILE: b.nomi\nfn main()\nend\n\n"), [
    { name: "a.toml", base64: false, body: "x = 1" },
    { name: "b.nomi", base64: false, body: "fn main()\nend" },
  ]);
  assert.equal(splitFiles("fn main()\nend"), null);
});

test("a file's language comes from its name", () => {
  assert.equal(fileLanguage("app/nomi.toml"), "toml");
  assert.equal(fileLanguage("messages/en.toml"), "toml");
  assert.equal(fileLanguage("go.mod"), "text");
  assert.equal(fileLanguage("data.json"), "json");
  assert.equal(fileLanguage("main.nomi"), "nomi");
});

// A `(base64)` section is a binary file: its tab holds the section's
// base64, which the run sends back as it is, and the static rendering
// keeps that text in the page, hidden, and colours nothing in it.
test("a base64 section is a binary file, kept and hidden", () => {
  const binary = "// FILE: main.nomi\nfn main()\nend\n\n// FILE: engine/x.wasm (base64)\nAGFzbQEAAAA=\n\n// FILE: notes.txt\nhi";
  assert.deepEqual(splitFiles(binary), [
    { name: "main.nomi", base64: false, body: "fn main()\nend" },
    { name: "engine/x.wasm", base64: true, body: "AGFzbQEAAAA=" },
    { name: "notes.txt", base64: false, body: "hi" },
  ]);
  const rendered = [];
  const html = renderFiles(fileSections(binary), (name, lang, text) => {
    rendered.push(name);
    return text;
  });
  assert.deepEqual(rendered, ["main.nomi", "notes.txt"]);
  assert.match(html, /<span hidden>AGFzbQEAAAA=\n<\/span>/);
  assert.equal(unescape(html), binary);
});
