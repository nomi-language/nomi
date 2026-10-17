// The debugger contribution and the configurations the extension hands
// `nomi dap`.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";
import {
  debugMainConfiguration,
  debugTestConfiguration,
  declaresMain,
  initialConfigurations,
  resolveConfiguration,
} from "../../src/debugConfig";

// The tests run from out/test/unit, three levels below the extension.
const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "..", "..", "package.json"), "utf8"));
const debuggerContribution = manifest.contributes.debuggers.find((d: { type: string }) => d.type === "nomi");

test("the extension contributes a nomi debugger and breakpoints in .nomi files", () => {
  assert.ok(debuggerContribution, "no debugger of type nomi");
  assert.deepEqual(debuggerContribution.languages, ["nomi"]);
  assert.ok(
    manifest.contributes.breakpoints.some((b: { language: string }) => b.language === "nomi"),
    "breakpoints are not enabled for nomi",
  );
  assert.ok(manifest.activationEvents.includes("onDebugResolve:nomi"));
});

test("launch.json's initial configurations are the ones the extension provides", () => {
  assert.deepEqual(debuggerContribution.initialConfigurations, initialConfigurations());
});

test("every attribute a configuration sets is one launch.json documents", () => {
  const documented = new Set(Object.keys(debuggerContribution.configurationAttributes.launch.properties));
  const configs = [
    ...initialConfigurations(),
    debugMainConfiguration("/p/main.nomi", "/p"),
    debugTestConfiguration("/p/main_test.nomi", 12, "/p"),
  ];
  for (const config of configs) {
    for (const key of Object.keys(config)) {
      if (["type", "request", "name"].includes(key)) {
        continue;
      }
      assert.ok(documented.has(key), `${config.name} sets ${key}, which launch.json does not document`);
    }
  }
  const modes = debuggerContribution.configurationAttributes.launch.properties.mode.enum;
  assert.deepEqual(modes, ["run", "test"]);
});

test("a code lens's debug configuration names its file, mode and test line", () => {
  assert.deepEqual(debugTestConfiguration("/p/a/main_test.nomi", 7, "/p"), {
    type: "nomi",
    request: "launch",
    name: "Debug test at main_test.nomi:7",
    mode: "test",
    program: "/p/a/main_test.nomi",
    cwd: "/p",
    line: 7,
  });
  assert.deepEqual(debugMainConfiguration("/p/main.nomi", "/p"), {
    type: "nomi",
    request: "launch",
    name: "Debug main.nomi",
    mode: "run",
    program: "/p/main.nomi",
    cwd: "/p",
  });
  assert.equal(debugTestConfiguration("/p/main_test.nomi").line, undefined);
});

test("F5 with no launch.json debugs the active file: its main, or its tests", () => {
  const program = { file: "/p/app.nomi", source: "import std/io\n\nfn main() {\n    io.print(\"hi\")\n}\n" };
  assert.equal(resolveConfiguration({}, program, "/p")?.mode, "run");
  const tests = { file: "/p/math.nomi", source: "fn add(a: Int, b: Int) -> Int { a + b }\n\ntest \"adds\" {\n}\n" };
  assert.equal(resolveConfiguration({}, tests, "/p")?.mode, "test");
  const testFile = { file: "/p/app_test.nomi", source: "fn main() {\n}\n" };
  assert.equal(resolveConfiguration({}, testFile, "/p")?.mode, "test");
  assert.equal(resolveConfiguration({}, undefined, "/p"), undefined);
});

test("a configuration with no program debugs the active file", () => {
  const active = { file: "/p/app.nomi", source: "fn main() {\n}\n" };
  const resolved = resolveConfiguration({ type: "nomi", request: "launch", name: "x", mode: "test" }, active, "/p");
  assert.equal(resolved?.program, "/p/app.nomi");
  assert.equal(resolved?.mode, "test");
  assert.equal(resolved?.cwd, "/p");
  const named = resolveConfiguration({ type: "nomi", request: "launch", name: "x", program: "/q/b.nomi", cwd: "/q" }, active, "/p");
  assert.equal(named?.program, "/q/b.nomi");
  assert.equal(named?.cwd, "/q");
});

test("declaresMain finds a top-level fn main only", () => {
  assert.ok(declaresMain("fn main() {\n}\n"));
  assert.ok(declaresMain("pub fn main() -> Unit {\n}\n"));
  assert.ok(!declaresMain("fn mainly() {\n}\n"));
  assert.ok(!declaresMain("    fn main() {\n    }\n"));
});
