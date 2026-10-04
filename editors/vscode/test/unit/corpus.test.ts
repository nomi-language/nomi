// Tokenizes every .nomi file under tests/ and std/ and checks that no
// string, comment, attached test or interpolation leaks: each top-level
// declaration starts with the grammar at its root, and so does the end of the
// file. A leak there would recolor the rest of the file in VS Code. No token
// in these files may be marked invalid (a bad string escape) either.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";
import { extensionRoot, loadGrammar, tokenize } from "./tokenize";
import * as vsctm from "vscode-textmate";

const repo = path.resolve(extensionRoot, "../..");
const topLevel = /^(pub |fn |struct |enum |interface |impl |derive |type |typealias |import |once |host |test |tests |opaque )/;

function nomiFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...nomiFiles(full));
    } else if (entry.name.endsWith(".nomi")) {
      out.push(full);
    }
  }
  return out;
}

test("no literal or comment leaks, and nothing is marked invalid, in tests/ and std/", async () => {
  const g = await loadGrammar();
  const rootDepth = g.tokenizeLine("", vsctm.INITIAL).ruleStack.depth;
  const files = ["tests", "std"].flatMap((d) => nomiFiles(path.join(repo, d)));
  assert.ok(files.length > 100, `found only ${files.length} files`);

  const leaks: string[] = [];
  for (const file of files) {
    const lines = await tokenize(fs.readFileSync(file, "utf8"));
    lines.forEach((line, i) => {
      if (i > 0 && topLevel.test(line.text) && line.before.depth !== rootDepth) {
        leaks.push(`${path.relative(repo, file)}:${i + 1}: ${line.text}`);
      }
      if (line.tokens.some((t) => t.scopes.some((s) => s.startsWith("invalid.")))) {
        leaks.push(`${path.relative(repo, file)}:${i + 1}: marked invalid: ${line.text}`);
      }
    });
    const last = lines[lines.length - 1];
    const end = g.tokenizeLine(last.text, last.before).ruleStack;
    if (end.depth !== rootDepth) {
      leaks.push(`${path.relative(repo, file)}: the file ends inside a literal or comment`);
    }
  }
  assert.deepEqual(leaks, []);
});
