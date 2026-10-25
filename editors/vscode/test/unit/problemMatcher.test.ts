// The nomi-test problem matcher the extension's `nomi test` tasks use: a
// failed test in `nomi test`'s text report becomes a problem at its line.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";

// The tests run from out/test/unit, three levels below the extension.
const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "..", "..", "package.json"), "utf8"));
const matcher = manifest.contributes.problemMatchers.find((m: { name: string }) => m.name === "nomi-test");

interface Problem {
  file: string;
  line: number;
  message: string;
}

// problems applies the matcher's two-line pattern as VS Code does: a line
// matching the first pattern, then the next line matching the second.
function problems(report: string): Problem[] {
  const [first, second] = matcher.pattern;
  const head = new RegExp(first.regexp);
  const body = new RegExp(second.regexp);
  const lines = report.split("\n");
  const out: Problem[] = [];
  for (let i = 0; i + 1 < lines.length; i++) {
    const h = head.exec(lines[i]);
    const b = h && body.exec(lines[i + 1]);
    if (h && b) {
      out.push({ file: h[first.file], line: Number(b[second.line]), message: b[second.message] });
    }
  }
  return out;
}

test("the extension contributes the nomi-test matcher", () => {
  assert.ok(matcher, "no problem matcher named nomi-test");
  assert.equal(matcher.owner, "nomi");
  assert.equal(matcher.pattern.length, 2);
});

test("a test stopped by its time limit is a problem at the line it was running", () => {
  const report = [
    "FAIL loop_test.nomi :: spins",
    "  line 2: did not finish within 10s, so it was stopped",
    "  it was running:",
    "    loop_test.nomi:2 in spin",
    "    loop_test.nomi:6 in spins",
    "  help: `nomi test --timeout <duration>` sets how long a case may run (10s unless given), and `--timeout 0` removes the limit",
    "ok loop_test.nomi :: passes",
    "test result: FAILED. 1 passed, 1 failed",
  ].join("\n");
  assert.deepEqual(problems(report), [
    { file: "loop_test.nomi", line: 2, message: "did not finish within 10s, so it was stopped" },
  ]);
});

test("a failed assertion and a trap are problems; passes and blocked tests are not", () => {
  const report = [
    "ok a_test.nomi :: passes",
    "FAIL a_test.nomi :: fails",
    "  line 16: assertion failed",
    "    assert Iter.count(xs) == 4",
    "FAIL sub/b_test.nomi :: group / traps",
    "  line 21: division by zero",
    "BLOCKED a_test.nomi :: counts a_test.nomi:9:20: this call to `skip_odd` is not supported yet",
    "test result: FAILED. 1 passed, 2 failed, 1 blocked",
  ].join("\n");
  assert.deepEqual(problems(report), [
    { file: "a_test.nomi", line: 16, message: "assertion failed" },
    { file: "sub/b_test.nomi", line: 21, message: "division by zero" },
  ]);
});
