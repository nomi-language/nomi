// A `#!` first line is a comment-colored shebang; `#!` on any later line is
// not one (the compiler rejects it there). The grammar test file cannot
// cover this: its first line must be the `SYNTAX TEST` header.
import * as assert from "node:assert/strict";
import { test } from "node:test";
import { tokenize } from "./tokenize";

test("a #! first line is a shebang comment, and nowhere else", async () => {
  const lines = await tokenize("#!/usr/bin/env nomi\nfn main() {}\n#!/usr/bin/env nomi\n");
  const first = lines[0].tokens;
  assert.deepEqual(
    first.map((t) => [lines[0].text.slice(t.startIndex, t.endIndex), t.scopes.filter((s) => s !== "source.nomi")]),
    [
      ["#!", ["comment.line.shebang.nomi", "punctuation.definition.comment.nomi"]],
      ["/usr/bin/env nomi", ["comment.line.shebang.nomi"]],
    ],
  );
  assert.ok(
    lines[1].tokens.every((t) => !t.scopes.includes("comment.line.shebang.nomi")),
    "the shebang leaked into line 2",
  );
  assert.ok(
    lines[2].tokens.every((t) => !t.scopes.includes("comment.line.shebang.nomi")),
    "a #! on line 3 was highlighted as a shebang",
  );
});
