// A semantic token marked `effect` (cmd/nomi-wasm's semToken, from
// hoverdoc.MarkReaching) keeps its colour and gets tok-effect, which the
// page draws bold; the page's static rendering (prehighlight.mjs) and its
// editors (tour-client.mjs) both draw through render.
//
//   npm --prefix tour test
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/lib/highlight.mjs";

test("a token that has effects keeps its class and is drawn bold", () => {
  const code = "fs.read_text(path)";
  const html = render([], [
    { line: 1, col: 1, len: 2, type: "namespace" },
    { line: 1, col: 4, len: 9, type: "function", effect: true },
    { line: 1, col: 14, len: 4, type: "parameter" },
  ], code);
  assert.equal(html, '<span class="tok-ns">fs</span>.<span class="tok-fn tok-effect">read_text</span>(<span class="tok-param">path</span>)');
});

test("the keyword of a dbg keeps the keyword class and is drawn bold", () => {
  const code = "dbg n";
  const html = render([], [
    { line: 1, col: 1, len: 3, type: "keyword", effect: true },
    { line: 1, col: 5, len: 1, type: "parameter" },
  ], code);
  assert.equal(html, '<span class="tok-kw tok-effect">dbg</span> <span class="tok-param">n</span>');
});
