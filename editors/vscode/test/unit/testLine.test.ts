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
  /*  4 */ "fn double(n: Int) -> Int",
  /*  5 */ "    n * 2",
  /*  6 */ "end",
  /*  7 */ 'test "a string\'s lines do not end it"',
  /*  8 */ '    s = "end"',
  /*  9 */ "    // end",
  /* 10 */ '    u = """',
  /* 11 */ "end",
  /* 12 */ '"""',
  /* 13 */ "    r = `",
  /* 14 */ "end`",
  /* 15 */ "    assert True",
  /* 16 */ "end",
  /* 17 */ "",
  /* 18 */ 'group "group"',
  /* 19 */ "    setup 1",
  /* 20 */ "",
  /* 21 */ '    test "inner", n',
  /* 22 */ "        assert n == 1",
  /* 23 */ "    end",
  /* 24 */ "end",
].join("\n");

test("an attached test resolves to the first //! line of its block", () => {
  assert.equal(at(source, 2), 2);
  assert.equal(at(source, 3), 2);
});

test("a declaration's own line is not a test", () => {
  assert.equal(at(source, 1), undefined);
  assert.equal(at(source, 4), undefined);
  assert.equal(at(source, 5), undefined);
  assert.equal(at(source, 6), undefined);
});

test("lines inside strings, raw strings and comments do not end a test", () => {
  for (let line = 7; line <= 16; line++) {
    assert.equal(at(source, line), 7, `line ${line}`);
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

test("a test in a group's subtests block runs for every row; the block's line runs the group", () => {
  const rows = [
    /* 1 */ 'group "stores"',
    /* 2 */ '    subtests ["memory", "sqlite"], store',
    /* 3 */ "        setup store",
    /* 4 */ "",
    /* 5 */ '        test "reads the row", s',
    /* 6 */ "            assert s == store",
    /* 7 */ "        end",
    /* 8 */ "    end",
    /* 9 */ "end",
  ].join("\n");
  assert.equal(at(rows, 2), 1);
  assert.equal(at(rows, 3), 1);
  assert.equal(at(rows, 5), 5);
  assert.equal(at(rows, 6), 5);
  assert.equal(at(rows, 8), 1);
});

test("CRLF line ends give the same lines", () => {
  const crlf = source.replace(/\n/g, "\r\n");
  assert.equal(at(crlf, 9), 7);
  assert.equal(at(crlf, 22), 21);
  assert.equal(at(crlf, 17), undefined);
});

test("a corpus file: tests/16-concurrency/supervisors/supervisors_test.nomi", () => {
  const text = fs.readFileSync(path.join(repo, "tests/16-concurrency/supervisors/supervisors_test.nomi"), "utf8");
  const lines = text.split("\n");
  const header = (prefix: string) => lines.findIndex((l) => l.startsWith(prefix)) + 1;
  const group = header('group "named task groups"');
  const first = header('    test "work outlives the call that started it"');
  assert.ok(group > 0 && first > group);
  assert.equal(at(text, group + 1), group);
  assert.equal(at(text, first), first);
  assert.equal(at(text, first + 2), first);
});
