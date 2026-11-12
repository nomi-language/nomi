// Finds the test under the cursor for `nomi test <file> --line N`, which runs
// the test whose declaration starts on line N: a `test`, a `group`, or an
// attached `//!` test. Zed and Neovim find it with tree-sitter
// (editors/zed/languages/nomi/runnables.scm, editors/nvim/lua/nomi/runner.lua);
// VS Code has no tree-sitter, so this reads the layout `nomi fmt` writes: a
// test's body is indented below its header, and the `end` that closes it
// stands at the header's indentation. Lines inside a multi-line string do
// not count.

const testHeader = /^\s*(?:test\s+"|group(?:\s+"|\s*(?:\/\/.*)?$))/;
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

  const inString = multilineStringLines(lines);
  for (let header = cursorLine; header >= 0; header--) {
    if (inString[header] || !testHeader.test(lines[header])) {
      continue;
    }
    if (closingLine(lines, inString, header) >= cursorLine) {
      return header + 1;
    }
  }
  return undefined;
}

// The 0-based line of the `end` that closes the body below `header`: the
// first later line, outside a multi-line string, that is not blank and is
// indented no deeper than the header. When that line is not an `end` at the
// header's indentation the body was never closed, and it ends the line
// before.
function closingLine(lines: string[], inString: boolean[], header: number): number {
  const indent = indentOf(lines[header]);
  for (let i = header + 1; i < lines.length; i++) {
    if (inString[i] || lines[i].trim() === "") {
      continue;
    }
    const n = indentOf(lines[i]);
    if (n > indent) {
      continue;
    }
    return n === indent && /^end\b/.test(lines[i].trim()) ? i : i - 1;
  }
  return lines.length - 1;
}

function indentOf(line: string): number {
  return line.length - line.trimStart().length;
}

// Which lines start inside a triple-quoted or multi-line raw string, which
// a body's indentation does not govern.
function multilineStringLines(lines: string[]): boolean[] {
  const out: boolean[] = [];
  let open: '"""' | "`" | undefined;
  for (const line of lines) {
    out.push(open !== undefined);
    let i = 0;
    while (i < line.length) {
      if (open === '"""') {
        const end = line.indexOf('"""', i);
        if (end < 0) {
          break;
        }
        open = undefined;
        i = end + 3;
        continue;
      }
      if (open === "`") {
        const end = line.indexOf("`", i);
        if (end < 0) {
          break;
        }
        open = undefined;
        i = end + 1;
        continue;
      }
      if (line.startsWith("//", i)) {
        break;
      }
      if (line.startsWith('"""', i)) {
        open = '"""';
        i += 3;
        continue;
      }
      if (line[i] === '"') {
        i = stringEnd(line, i);
        continue;
      }
      if (line[i] === "'") {
        const m = /^'(?:\\(?:[nt\\']|u\{[0-9a-fA-F]+\})|[^'\\])'/.exec(line.slice(i, i + 16));
        i += m ? m[0].length : 1;
        continue;
      }
      if (line[i] === "`") {
        open = "`";
        i++;
        continue;
      }
      i++;
    }
  }
  return out;
}

// The offset just past the string literal opened at `open` on its line.
function stringEnd(line: string, open: number): number {
  let i = open + 1;
  while (i < line.length && line[i] !== '"') {
    i += line[i] === "\\" ? 2 : 1;
  }
  return i + 1;
}
