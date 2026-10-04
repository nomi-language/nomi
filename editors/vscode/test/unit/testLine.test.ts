import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";
import { enclosingTestLine } from "../../src/testLine";

const repo = path.resolve(__dirname, "../../../../..");

// Lines are 1-based in the fixtures' comments and in the results; the cursor
// argument is 0-based, as VS Code's Position.line is.
const at = (text: string, line1: number) => enclosingTestLine(text, line1 - 1);

const source = [
  /*  1 */ "/// Doubles n.",
  /*  2 */ "//! assert double(2) == 4",
  /*  3 */ "//! assert double(0) == 0",
  /*  4 */ "fn double(n: Int): Int { n * 2 }",
  /*  5 */ "",
  /*  6 */ 'test "braces in literals do not count" {',
  /*  7 */ '    s = "}"',
  /*  8 */ '    t = "${ #{1: "}"} }"',
  /*  9 */ "    r = `}}`",
  /* 10 */ "    c = '}'",
  /* 11 */ "    // }",
  /* 12 */ '    u = """',
  /* 13 */ "        }",
  /* 14 */ '        """',
  /* 15 */ "    assert True",
  /* 16 */ "}",
  /* 17 */ "",
  /* 18 */ 'tests "group" {',
  /* 19 */ "    setup { 1 }",
  /* 20 */ "",
  /* 21 */ '    test "inner", n {',
  /* 22 */ "        assert n == 1",
  /* 23 */ "    }",
  /* 24 */ "}",
].join("\n");

test("an attached test resolves to the first //! line of its block", () => {
  assert.equal(at(source, 2), 2);
  assert.equal(at(source, 3), 2);
});

test("a declaration's own line is not a test", () => {
  assert.equal(at(source, 1), undefined);
  assert.equal(at(source, 4), undefined);
  assert.equal(at(source, 5), undefined);
});

test("braces inside strings, raw strings, codepoints and comments are skipped", () => {
  for (let line = 6; line <= 16; line++) {
    assert.equal(at(source, line), 6, `line ${line}`);
  }
  assert.equal(at(source, 17), undefined);
});

test("a test inside a group wins over the group", () => {
  assert.equal(at(source, 18), 18);
  assert.equal(at(source, 19), 18);
  assert.equal(at(source, 20), 18);
  assert.equal(at(source, 21), 21);
  assert.equal(at(source, 22), 21);
  assert.equal(at(source, 23), 21);
  assert.equal(at(source, 24), 18);
});

test("CRLF line ends give the same lines", () => {
  const crlf = source.replace(/\n/g, "\r\n");
  assert.equal(at(crlf, 9), 6);
  assert.equal(at(crlf, 22), 21);
  assert.equal(at(crlf, 17), undefined);
});

test("a corpus file: tests/16-concurrency/supervisors/supervisors_test.nomi", () => {
  const text = fs.readFileSync(path.join(repo, "tests/16-concurrency/supervisors/supervisors_test.nomi"), "utf8");
  const lines = text.split("\n");
  const header = (prefix: string) => lines.findIndex((l) => l.startsWith(prefix)) + 1;
  const group = header('tests "named task groups"');
  const first = header('    test "work outlives the call that started it"');
  assert.ok(group > 0 && first > group);
  assert.equal(at(text, group + 1), group);
  assert.equal(at(text, first), first);
  assert.equal(at(text, first + 2), first);
});
