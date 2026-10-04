// Finds the test under the cursor for `nomi test <file> --line N`, which runs
// the test whose declaration starts on line N: a `test`, a `tests` group, or an
// attached `//!` test. Zed and Neovim find it with tree-sitter
// (editors/zed/languages/nomi/runnables.scm, editors/nvim/lua/nomi/runner.lua);
// VS Code has no tree-sitter, so this scans the text, skipping comments and
// string literals when it matches braces.

const testHeader = /^\s*tests?\s+"/;
const attachedTest = /^\s*\/\/!/;

/**
 * The 1-based first line of the innermost test enclosing `cursorLine`
 * (0-based), or undefined when the cursor is not inside one.
 */
export function enclosingTestLine(text: string, cursorLine: number): number | undefined {
  const lines = text.split(/\r?\n/);
  if (cursorLine < 0 || cursorLine >= lines.length) {
    return undefined;
  }

  if (attachedTest.test(lines[cursorLine])) {
    let first = cursorLine;
    while (first > 0 && attachedTest.test(lines[first - 1])) {
      first--;
    }
    return first + 1;
  }

  const lineStarts: number[] = [];
  let offset = 0;
  for (const line of lines) {
    lineStarts.push(offset);
    offset += line.length + 1;
  }
  // Offsets below assume "\n" line ends; normalize so they hold for CRLF too.
  const source = lines.join("\n");

  for (let header = cursorLine; header >= 0; header--) {
    if (!testHeader.test(lines[header])) {
      continue;
    }
    const open = scan(source, lineStarts[header], "open");
    if (open === undefined) {
      continue;
    }
    const close = scan(source, open, "close");
    const closeLine = close === undefined ? lines.length - 1 : lineOf(lineStarts, close);
    if (closeLine >= cursorLine) {
      return header + 1;
    }
  }
  return undefined;
}

function lineOf(lineStarts: number[], offset: number): number {
  let lo = 0;
  let hi = lineStarts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (lineStarts[mid] <= offset) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  return lo;
}

type Frame =
  | { kind: "code"; depth: number }
  | { kind: "string" }
  | { kind: "triple" }
  | { kind: "raw" };

/**
 * Scans Nomi source from `start`. In "open" mode it returns the offset of the
 * first `{` outside comments and literals; in "close" mode `start` is a `{` and
 * it returns the offset of its matching `}`. Interpolations (`${...}`) inside
 * strings are scanned as code.
 */
function scan(src: string, start: number, mode: "open" | "close"): number | undefined {
  const stack: Frame[] = [{ kind: "code", depth: 0 }];
  let i = start;
  while (i < src.length) {
    const top = stack[stack.length - 1];
    const c = src[i];
    switch (top.kind) {
      case "code":
        if (src.startsWith("//", i)) {
          const eol = src.indexOf("\n", i);
          i = eol < 0 ? src.length : eol;
          continue;
        }
        if (src.startsWith('"""', i)) {
          stack.push({ kind: "triple" });
          i += 3;
          continue;
        }
        if (c === '"') {
          stack.push({ kind: "string" });
        } else if (c === "`") {
          stack.push({ kind: "raw" });
        } else if (c === "'") {
          // A codepoint literal: 'a', '\n', '\'', '\u{7F}'.
          const end = codepointEnd(src, i);
          if (end !== undefined) {
            i = end;
          }
        } else if (c === "{") {
          if (mode === "open" && stack.length === 1) {
            return i;
          }
          top.depth++;
        } else if (c === "}") {
          top.depth--;
          if (top.depth <= 0) {
            if (stack.length === 1) {
              if (mode === "close") {
                return i;
              }
              top.depth = 0;
            } else {
              stack.pop();
            }
          }
        }
        i++;
        continue;
      case "string":
        if (c === "\\") {
          i += 2;
          continue;
        }
        if (src.startsWith("${", i)) {
          stack.push({ kind: "code", depth: 1 });
          i += 2;
          continue;
        }
        if (c === '"' || c === "\n") {
          stack.pop();
        }
        i++;
        continue;
      case "triple":
        if (src.startsWith("\\${", i)) {
          i += 3;
          continue;
        }
        if (src.startsWith("${", i)) {
          stack.push({ kind: "code", depth: 1 });
          i += 2;
          continue;
        }
        if (src.startsWith('"""', i)) {
          stack.pop();
          i += 3;
          continue;
        }
        i++;
        continue;
      case "raw":
        if (c === "`") {
          stack.pop();
        }
        i++;
        continue;
    }
  }
  return undefined;
}

// The offset of a codepoint literal's closing quote, given its opening quote.
function codepointEnd(src: string, open: number): number | undefined {
  const m = /^'(?:\\(?:[nt\\']|u\{[0-9a-fA-F]+\})|[^'\\\n])'/.exec(src.slice(open, open + 16));
  return m ? open + m[0].length - 1 : undefined;
}
