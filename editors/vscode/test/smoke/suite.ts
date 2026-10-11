// Runs inside the VS Code extension host that ./run.ts launches, on the
// scratch workspace it writes.
import * as assert from "node:assert/strict";
import * as path from "node:path";
import * as vscode from "vscode";
import type { NomiExtensionApi } from "../../src/extension";

export async function run(): Promise<void> {
  const steps: [string, () => Promise<void>][] = [
    ["a .nomi file opens as Nomi and activates the extension", activates],
    ["the commands are registered", commandsRegistered],
    ["nomi-lsp reports a type error", languageServerDiagnoses],
    ["the outline lists the file's declarations and tests", documentSymbols],
    ["Nomi: Test at Cursor runs only the test under the cursor", testAtCursor],
    ["Nomi: Test File runs every test in the file", testFile],
    ["each test's code lens runs that test", codeLenses],
    ["a Debug test lens stops at the failed assertion, and Step Back goes back", debugLens],
    ["a function breakpoint and a step-in target stop in the function", functionBreakpointAndStepInTarget],
    ["attached tests are dimmed, except the one under the cursor", attachedTestsDimmed],
  ];
  for (const [name, step] of steps) {
    try {
      await step();
      console.log(`ok   ${name}`);
    } catch (err) {
      console.log(`FAIL ${name}`);
      throw err;
    }
  }
}

function workspaceFile(name: string): vscode.Uri {
  const folder = vscode.workspace.workspaceFolders?.[0];
  assert.ok(folder, "no workspace folder is open");
  return vscode.Uri.file(path.join(folder.uri.fsPath, name));
}

async function open(name: string): Promise<vscode.TextEditor> {
  const doc = await vscode.workspace.openTextDocument(workspaceFile(name));
  return vscode.window.showTextDocument(doc);
}

async function activates(): Promise<void> {
  const editor = await open("sample_test.nomi");
  assert.equal(editor.document.languageId, "nomi");
  const ext = vscode.extensions.getExtension("nomi-language.nomi");
  assert.ok(ext, "the extension is not installed in the test host");
  await ext.activate();
  assert.ok(ext.isActive);
}

async function commandsRegistered(): Promise<void> {
  const all = await vscode.commands.getCommands(true);
  for (const id of ["nomi.runFile", "nomi.testFile", "nomi.testAtCursor", "nomi.restartLanguageServer", "nomi.runTest", "nomi.runMain",
    "nomi.debugTest", "nomi.debugMain", "nomi.debugFile", "nomi.debugTestAtCursor"]) {
    assert.ok(all.includes(id), `${id} is not registered`);
  }
}

async function languageServerDiagnoses(): Promise<void> {
  const editor = await open("bad.nomi");
  const uri = editor.document.uri;
  const deadline = Date.now() + 30_000;
  for (;;) {
    const diags = vscode.languages.getDiagnostics(uri);
    if (diags.some((d) => d.message.includes("type mismatch"))) {
      assert.equal(diags[0].range.start.line, 1);
      return;
    }
    if (Date.now() > deadline) {
      assert.fail(`no type-mismatch diagnostic after 30s; got ${JSON.stringify(diags.map((d) => d.message))}`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
}

// VS Code drops a whole documentSymbol response when any symbol's
// selectionRange lies outside its range, leaving the outline empty.
async function documentSymbols(): Promise<void> {
  const editor = await open("sample_test.nomi");
  const syms = await vscode.commands.executeCommand<vscode.DocumentSymbol[]>(
    "vscode.executeDocumentSymbolProvider",
    editor.document.uri,
  );
  assert.deepEqual(
    (syms ?? []).map((s) => `${s.name}@${s.range.start.line}-${s.range.end.line}`),
    ["double@0-0", "passes@2-4", "fails@6-8"],
  );
}

// nomi-lsp puts a lens above each test; running one runs only that test.
async function codeLenses(): Promise<void> {
  const editor = await open("sample_test.nomi");
  const lenses = await vscode.commands.executeCommand<vscode.CodeLens[]>(
    "vscode.executeCodeLensProvider",
    editor.document.uri,
  );
  assert.deepEqual(
    (lenses ?? []).map((l) => `${l.range.start.line} ${l.command?.title} ${l.command?.command}`),
    ["2 Run test nomi.runTest", "2 Debug test nomi.debugTest", "6 Run test nomi.runTest", "6 Debug test nomi.debugTest"],
  );
  const run = async (lens: vscode.CodeLens) =>
    taskExitCodeOf(() => vscode.commands.executeCommand(lens.command!.command, ...(lens.command!.arguments ?? [])));
  assert.equal(await run(lenses[0]), 0);
  assert.equal(await run(lenses[2]), 1);
}

// The Debug test lens starts `nomi dap` on that one test: it stops at the
// failed assertion. VS Code's own Step Back command (shown because the
// adapter advertises supportsStepBack) goes back to the start of the
// assertion's statement; continuing replays to the assertion again, and
// continuing once more ends the session with the test failed.
async function debugLens(): Promise<void> {
  const editor = await open("sample_test.nomi");
  const lenses = await vscode.commands.executeCommand<vscode.CodeLens[]>(
    "vscode.executeCodeLensProvider",
    editor.document.uri,
  );
  const lens = (lenses ?? []).find((l) => l.command?.command === "nomi.debugTest" && l.range.start.line === 6);
  assert.ok(lens, "no Debug test lens on the failing test");
  const seen: { stops: string[]; stepBack?: boolean; exitCode?: number } = { stops: [] };
  let session: vscode.DebugSession | undefined;
  let onStop: (() => void) | undefined;
  const nextStop = () => new Promise<void>((resolve) => (onStop = resolve));
  let stopped = nextStop();
  const tracker = vscode.debug.registerDebugAdapterTrackerFactory("nomi", {
    createDebugAdapterTracker: (s) => {
      session = s;
      return {
        onDidSendMessage: (m: {
          type: string;
          command?: string;
          event?: string;
          body?: { reason?: string; exitCode?: number; supportsStepBack?: boolean };
        }) => {
          if (m.type === "response" && m.command === "initialize") {
            seen.stepBack = m.body?.supportsStepBack;
          }
          if (m.type === "event" && m.event === "stopped") {
            seen.stops.push(m.body?.reason ?? "");
            onStop?.();
          }
          if (m.type === "event" && m.event === "exited") {
            seen.exitCode = m.body?.exitCode;
            tracker.dispose();
          }
        },
      };
    },
  });
  const ended = new Promise<void>((resolve) => {
    const sub = vscode.debug.onDidTerminateDebugSession(() => {
      sub.dispose();
      resolve();
    });
  });
  const timeout = (what: string) =>
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`no ${what} after 60s`)), 60_000));
  await vscode.commands.executeCommand(lens.command!.command, ...(lens.command!.arguments ?? []));
  await Promise.race([stopped, timeout("stop")]);
  assert.equal(seen.stepBack, true, "the adapter does not advertise supportsStepBack");
  // The command acts on the focused thread, which VS Code focuses once it
  // has fetched the stopped thread's stack.
  for (let i = 0; i < 600 && !(vscode.debug.activeStackItem instanceof vscode.DebugStackFrame); i++) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  stopped = nextStop();
  await vscode.commands.executeCommand("workbench.action.debug.stepBack");
  await Promise.race([stopped, timeout("stop after Step Back")]);
  stopped = nextStop();
  await session!.customRequest("continue", { threadId: 1 });
  await Promise.race([stopped, timeout("stop at the assertion again")]);
  assert.deepEqual(seen.stops, ["exception", "step", "exception"]);
  await session!.customRequest("continue", { threadId: 1 });
  await Promise.race([ended, timeout("end of the session")]);
  assert.equal(seen.exitCode, 1);
}

// VS Code sends the breakpoints in its Breakpoints view, a function
// breakpoint among them, and offers Step Into Target, because the adapter
// advertises supportsFunctionBreakpoints and supportsStepInTargetsRequest.
// Debugging the passing test with a line breakpoint on its assertion and a
// function breakpoint on `double`: the line breakpoint stops first; the
// statement's one step-in target is `double`, and stepping into it stops
// there, at the function breakpoint (which wins over the step); the
// variables view's `n` names where it is bound.
async function functionBreakpointAndStepInTarget(): Promise<void> {
  const editor = await open("sample_test.nomi");
  const lenses = await vscode.commands.executeCommand<vscode.CodeLens[]>(
    "vscode.executeCodeLensProvider",
    editor.document.uri,
  );
  const lens = (lenses ?? []).find((l) => l.command?.command === "nomi.debugTest" && l.range.start.line === 2);
  assert.ok(lens, "no Debug test lens on the passing test");
  const breakpoints = [
    new vscode.SourceBreakpoint(new vscode.Location(editor.document.uri, new vscode.Position(3, 0))),
    new vscode.FunctionBreakpoint("double"),
  ];
  vscode.debug.addBreakpoints(breakpoints);
  type Message = {
    type: string;
    command?: string;
    event?: string;
    body?: {
      reason?: string;
      exitCode?: number;
      supportsFunctionBreakpoints?: boolean;
      supportsStepInTargetsRequest?: boolean;
      breakpoints?: { verified: boolean }[];
    };
  };
  const seen: { stops: string[]; caps?: Message["body"]; functionBreakpoints?: boolean[]; exitCode?: number } = {
    stops: [],
  };
  let session: vscode.DebugSession | undefined;
  let onStop: (() => void) | undefined;
  const nextStop = () => new Promise<void>((resolve) => (onStop = resolve));
  let stopped = nextStop();
  const tracker = vscode.debug.registerDebugAdapterTrackerFactory("nomi", {
    createDebugAdapterTracker: (s) => {
      session = s;
      return {
        onDidSendMessage: (m: Message) => {
          if (m.type === "response" && m.command === "initialize") {
            seen.caps = m.body;
          }
          if (m.type === "response" && m.command === "setFunctionBreakpoints") {
            seen.functionBreakpoints = (m.body?.breakpoints ?? []).map((b) => b.verified);
          }
          if (m.type === "event" && m.event === "stopped") {
            seen.stops.push(m.body?.reason ?? "");
            onStop?.();
          }
          if (m.type === "event" && m.event === "exited") {
            seen.exitCode = m.body?.exitCode;
            tracker.dispose();
          }
        },
      };
    },
  });
  const ended = new Promise<void>((resolve) => {
    const sub = vscode.debug.onDidTerminateDebugSession(() => {
      sub.dispose();
      resolve();
    });
  });
  const timeout = (what: string) =>
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`no ${what} after 60s`)), 60_000));
  try {
    await vscode.commands.executeCommand(lens.command!.command, ...(lens.command!.arguments ?? []));
    await Promise.race([stopped, timeout("stop at the line breakpoint")]);
    assert.equal(seen.caps?.supportsFunctionBreakpoints, true, "the adapter does not advertise supportsFunctionBreakpoints");
    assert.equal(seen.caps?.supportsStepInTargetsRequest, true, "the adapter does not advertise supportsStepInTargetsRequest");
    assert.deepEqual(seen.functionBreakpoints, [true], "VS Code did not send the function breakpoint, or it is unverified");
    for (let i = 0; i < 600 && !(vscode.debug.activeStackItem instanceof vscode.DebugStackFrame); i++) {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    const frame = vscode.debug.activeStackItem as vscode.DebugStackFrame;
    const targets = await session!.customRequest("stepInTargets", { frameId: frame.frameId });
    assert.deepEqual(
      targets.targets.map((t: { label: string }) => t.label),
      ["double"],
    );
    stopped = nextStop();
    await session!.customRequest("stepIn", { threadId: frame.threadId, targetId: targets.targets[0].id });
    await Promise.race([stopped, timeout("stop in double")]);
    assert.deepEqual(seen.stops, ["breakpoint", "function breakpoint"]);
    const trace = await session!.customRequest("stackTrace", { threadId: frame.threadId });
    assert.equal(trace.stackFrames[0].name, "double");
    const scopes = await session!.customRequest("scopes", { frameId: trace.stackFrames[0].id });
    const vars = await session!.customRequest("variables", {
      variablesReference: scopes.scopes[0].variablesReference,
    });
    const n = vars.variables.find((v: { name: string }) => v.name === "n");
    assert.ok(n?.declarationLocationReference, "the local n has no declarationLocationReference");
    const loc = await session!.customRequest("locations", { locationReference: n.declarationLocationReference });
    assert.equal(`${loc.line}:${loc.column}`, "1:11");
    await session!.customRequest("continue", { threadId: frame.threadId });
    await Promise.race([ended, timeout("end of the session")]);
    assert.equal(seen.exitCode, 0);
  } finally {
    vscode.debug.removeBreakpoints(breakpoints);
  }
}

// Runs a command that starts a nomi task and returns the task's exit code.
async function taskExitCode(command: string): Promise<number | undefined> {
  return taskExitCodeOf(() => vscode.commands.executeCommand(command));
}

// Runs start, which starts a nomi task, and returns the task's exit code.
async function taskExitCodeOf(start: () => Thenable<unknown>): Promise<number | undefined> {
  const ended = new Promise<number | undefined>((resolve) => {
    const sub = vscode.tasks.onDidEndTaskProcess((e) => {
      if (e.execution.task.definition.type === "nomi") {
        sub.dispose();
        resolve(e.exitCode);
      }
    });
  });
  await start();
  const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error("no exit after 60s")), 60_000));
  return Promise.race([ended, timeout]);
}

async function testAtCursor(): Promise<void> {
  const editor = await open("sample_test.nomi");
  // Line 4 (0-based 3) is inside `test "passes"`; the file's other test fails.
  editor.selection = new vscode.Selection(3, 4, 3, 4);
  assert.equal(await taskExitCode("nomi.testAtCursor"), 0);
}

async function testFile(): Promise<void> {
  await open("sample_test.nomi");
  assert.equal(await taskExitCode("nomi.testFile"), 1);
}

// VS Code has no API that reads an editor's decorations back, so this reads
// the ranges the extension last passed to setDecorations.
async function attachedTestsDimmed(): Promise<void> {
  const api = vscode.extensions.getExtension<NomiExtensionApi>("nomi-language.nomi")!.exports;
  const editor = await open("attached.nomi");
  const config = () => vscode.workspace.getConfiguration("nomi.attachedTests");
  const dimmed = () =>
    (api.attachedTestDimmedRanges(editor) ?? []).map(
      (r) => `${r.start.line}:${r.start.character}-${r.end.line}:${r.end.character}`,
    );
  const expect = async (want: string[], what: string) => {
    const deadline = Date.now() + 5_000;
    while (JSON.stringify(dimmed()) !== JSON.stringify(want)) {
      if (Date.now() > deadline) {
        assert.deepEqual(dimmed(), want, what);
      }
      await new Promise((r) => setTimeout(r, 50));
    }
  };
  const all = ["0:3-0:25", "1:3-1:25", "4:3-4:25"];

  editor.selection = new vscode.Selection(2, 0, 2, 0);
  await expect(all, "cursor on a declaration");
  editor.selection = new vscode.Selection(1, 5, 1, 5);
  await expect(["4:3-4:25"], "cursor in the first attached test");
  editor.selection = new vscode.Selection(4, 0, 4, 0);
  await expect(["0:3-0:25", "1:3-1:25"], "cursor in the second attached test");

  // An edit is rescanned: a prompt line inserted below a group joins it.
  await editor.edit((b) => b.insert(new vscode.Position(2, 0), "//! assert double(1) == 2\n"));
  editor.selection = new vscode.Selection(3, 0, 3, 0);
  await expect(["0:3-0:25", "1:3-1:25", "2:3-2:25", "5:3-5:25"], "after inserting a prompt line");
  // The line is removed again by an edit, not the `undo` command: `undo`
  // acts on whatever has the window's keyboard focus, and once the previous
  // step's debug session has run, the test host's window may have none, so
  // the undo never reaches this document.
  await editor.edit((b) => b.delete(new vscode.Range(2, 0, 3, 0)));
  editor.selection = new vscode.Selection(2, 0, 2, 0);
  await expect(all, "after removing it again");

  editor.selection = new vscode.Selection(1, 0, 1, 0);
  await config().update("revealUnderCursor", false, vscode.ConfigurationTarget.Workspace);
  await expect(all, "revealUnderCursor off");
  await config().update("dim", false, vscode.ConfigurationTarget.Workspace);
  await expect([], "dim off");
  await config().update("dim", undefined, vscode.ConfigurationTarget.Workspace);
  await config().update("revealUnderCursor", undefined, vscode.ConfigurationTarget.Workspace);
  await expect(["4:3-4:25"], "settings back to their defaults");
  await config().update("dimOpacity", 0.3, vscode.ConfigurationTarget.Workspace);
  await expect(["4:3-4:25"], "a new opacity redraws");
  await config().update("dimOpacity", undefined, vscode.ConfigurationTarget.Workspace);
}
