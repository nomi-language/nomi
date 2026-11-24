// The semantic token modifier the language server marks code that has
// effects with: the extension declares it by the server's name and maps it
// to a scope the default themes draw bold.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";

// The tests run from out/test/unit, three levels below the extension.
const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "..", "..", "package.json"), "utf8"));

test("the effect modifier is declared and drawn bold", () => {
  const ids = manifest.contributes.semanticTokenModifiers.map((m: { id: string }) => m.id);
  assert.deepEqual(ids, ["effect"]);
  const nomi = manifest.contributes.semanticTokenScopes.find((s: { language: string }) => s.language === "nomi");
  assert.deepEqual(nomi.scopes["*.effect"], ["markup.bold"]);
});
