// Loads syntaxes/nomi.tmLanguage.json with the same TextMate engine VS Code
// uses (vscode-textmate over vscode-oniguruma), for the corpus test and for
// `node out/test/unit/tokenize.js <file>`, which prints each token's scopes.
import * as fs from "node:fs";
import * as path from "node:path";
import * as oniguruma from "vscode-oniguruma";
import * as vsctm from "vscode-textmate";

export const extensionRoot = path.resolve(__dirname, "../../..");

let grammar: Promise<vsctm.IGrammar> | undefined;

export function loadGrammar(): Promise<vsctm.IGrammar> {
  grammar ??= (async () => {
    const wasm = fs.readFileSync(require.resolve("vscode-oniguruma/release/onig.wasm"));
    await oniguruma.loadWASM(wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength) as ArrayBuffer);
    const registry = new vsctm.Registry({
      onigLib: Promise.resolve({
        createOnigScanner: (patterns: string[]) => new oniguruma.OnigScanner(patterns),
        createOnigString: (s: string) => new oniguruma.OnigString(s),
      }),
      loadGrammar: async (scopeName: string) => {
        if (scopeName !== "source.nomi") {
          return null;
        }
        const file = path.join(extensionRoot, "syntaxes", "nomi.tmLanguage.json");
        return vsctm.parseRawGrammar(fs.readFileSync(file, "utf8"), file);
      },
    });
    const g = await registry.loadGrammar("source.nomi");
    if (!g) {
      throw new Error("source.nomi did not load");
    }
    return g;
  })();
  return grammar;
}

export interface Line {
  text: string;
  /** The rule stack in effect at the start of this line. */
  before: vsctm.StateStack;
  tokens: vsctm.IToken[];
}

export async function tokenize(text: string): Promise<Line[]> {
  const g = await loadGrammar();
  let state = vsctm.INITIAL;
  const out: Line[] = [];
  for (const line of text.split(/\r?\n/)) {
    const r = g.tokenizeLine(line, state);
    out.push({ text: line, before: state, tokens: r.tokens });
    state = r.ruleStack;
  }
  return out;
}

if (require.main === module) {
  void (async () => {
    const lines = await tokenize(fs.readFileSync(process.argv[2], "utf8"));
    lines.forEach((l, i) => {
      console.log(`${i + 1}: ${l.text}`);
      for (const t of l.tokens) {
        const scopes = t.scopes.filter((s) => s !== "source.nomi").join(" ");
        console.log(`    ${JSON.stringify(l.text.slice(t.startIndex, t.endIndex))} ${scopes}`);
      }
    });
  })();
}
