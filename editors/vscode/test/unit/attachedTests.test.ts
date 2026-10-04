import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";
import { Group, attachedTestGroups, dimSpans, groupsUnder, splitLines } from "../../src/attachedTests";

const repo = path.resolve(__dirname, "../../../../..");

// Lines are 0-based, as VS Code's Position.line is.
const lines = splitLines(
  [
    /*  0 */ "/// Doubles n.",
    /*  1 */ "//! assert double(2) == 4",
    /*  2 */ "//!assert double(0) == 0",
    /*  3 */ "//",
    /*  4 */ "fn double(n: Int): Int { n * 2 }",
    /*  5 */ "",
    /*  6 */ "//! assert answer() == 42",
    /*  7 */ "",
    /*  8 */ "//! assert answer() != 0",
    /*  9 */ "fn answer(): Int { 42 }",
    /* 10 */ "",
    /* 11 */ "impl Thing {",
    /* 12 */ "    //! assert Thing.one() == 1",
    /* 13 */ "    //!",
    /* 14 */ "    fn one(): Int { 1 }",
    /* 15 */ "}",
    /* 16 */ 'x = "//! not at the start"',
  ].join("\r\n"),
);

const groups = attachedTestGroups(lines);
const firsts = (gs: Group[]) => gs.map((g) => g.first);
const spans = (revealed: Group[] = []) => dimSpans(lines, groups, revealed).map((s) => `${s.line}:${s.start}-${s.end}`);

test("consecutive //! lines form one group; any other line, blank or a bare //, ends it", () => {
  assert.deepEqual(groups, [
    { first: 1, last: 2 },
    { first: 6, last: 6 },
    { first: 8, last: 8 },
    { first: 12, last: 13 },
  ]);
});

test("everything after each //! marker is dimmed, the marker is not, and an empty prompt has nothing to dim", () => {
  assert.deepEqual(spans(), ["1:3-25", "2:3-24", "6:3-25", "8:3-24", "12:7-31"]);
});

test("the group under the cursor is revealed, and only that group", () => {
  const at = (line: number) => firsts(groupsUnder(groups, [[line, line]]));
  assert.deepEqual(at(1), [1]);
  assert.deepEqual(at(2), [1]);
  assert.deepEqual(at(3), []);
  assert.deepEqual(at(4), []);
  assert.deepEqual(at(6), [6]);
  assert.deepEqual(at(7), []);
  assert.deepEqual(at(13), [12]);
  assert.deepEqual(spans(groupsUnder(groups, [[2, 2]])), ["6:3-25", "8:3-24", "12:7-31"]);
});

test("a selection reveals every group it touches, and each cursor of a multi-cursor reveals its own", () => {
  assert.deepEqual(firsts(groupsUnder(groups, [[2, 6]])), [1, 6]);
  assert.deepEqual(firsts(groupsUnder(groups, [[0, 0], [8, 8], [12, 12]])), [8, 12]);
  assert.deepEqual(spans(groupsUnder(groups, [[0, 15]])), []);
});

test("a corpus file with attached tests: std/strings.nomi", () => {
  const text = fs.readFileSync(path.join(repo, "std/strings.nomi"), "utf8");
  const src = splitLines(text);
  const found = attachedTestGroups(src);
  assert.ok(found.length > 10, `only ${found.length} groups`);
  for (const g of found) {
    for (let line = g.first; line <= g.last; line++) {
      assert.match(src[line], /^\s*\/\/!/);
    }
    assert.doesNotMatch(src[g.last + 1] ?? "", /^\s*\/\/!/);
  }
});
