import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import { LanguageClient, LanguageClientOptions, ServerOptions, TransportKind } from "vscode-languageclient/node";
import { AttachedTestDimmer } from "./attachedTestDecorations";
import { enclosingTestLine } from "./testLine";

const installGuide = "https://github.com/nomi-language/nomi/blob/main/docs/install.md";

let client: LanguageClient | undefined;

/** What activate returns: the smoke test reads the attached-test dimming. */
export interface NomiExtensionApi {
  attachedTestDimmedRanges(editor: vscode.TextEditor): vscode.Range[] | undefined;
}

export async function activate(context: vscode.ExtensionContext): Promise<NomiExtensionApi> {
  const dimmer = new AttachedTestDimmer();
  context.subscriptions.push(
    dimmer,
    vscode.commands.registerCommand("nomi.runFile", () => runOnFile((file) => ["run", file])),
    vscode.commands.registerCommand("nomi.testFile", () => runOnFile((file) => ["test", file])),
    vscode.commands.registerCommand("nomi.testAtCursor", () => testAtCursor()),
    // The commands of nomi-lsp's code lenses (internal/lsp/code_lens.go).
    vscode.commands.registerCommand("nomi.runTest", (uri: string, line: number) =>
      runOnUri(uri, (file) => ["test", file, "--line", String(line)]),
    ),
    vscode.commands.registerCommand("nomi.runMain", (uri: string) => runOnUri(uri, (file) => ["run", file])),
    vscode.commands.registerCommand("nomi.restartLanguageServer", () => restartClient()),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("nomi.lsp.path")) {
        void restartClient();
      }
    }),
  );
  await startClient();
  return { attachedTestDimmedRanges: (editor) => dimmer.dimmedRanges(editor) };
}

export async function deactivate(): Promise<void> {
  await stopClient();
}

// ---- Language server ----------------------------------------------------

async function startClient(): Promise<void> {
  const configured = vscode.workspace.getConfiguration("nomi").get<string>("lsp.path") || "nomi-lsp";
  const command = findExecutable(configured);
  if (!command) {
    void notFound(
      `Nomi: the language server '${configured}' was not found. Install it from a Nomi checkout with ` +
        "`go install ./cmd/nomi-lsp`, or set nomi.lsp.path.",
      "nomi.lsp.path",
    );
    return;
  }

  const serverOptions: ServerOptions = { command, args: [], transport: TransportKind.stdio };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", language: "nomi" },
      { scheme: "untitled", language: "nomi" },
    ],
  };
  client = new LanguageClient("nomi", "Nomi Language Server", serverOptions, clientOptions);
  try {
    await client.start();
  } catch (err) {
    void vscode.window.showErrorMessage(`Nomi: could not start ${command}: ${String(err)}`);
  }
}

async function stopClient(): Promise<void> {
  const running = client;
  client = undefined;
  if (running) {
    await running.stop().catch(() => undefined);
  }
}

async function restartClient(): Promise<void> {
  await stopClient();
  await startClient();
}

async function notFound(message: string, setting: string): Promise<void> {
  const choice = await vscode.window.showErrorMessage(message, "Open Install Guide", "Open Settings");
  if (choice === "Open Install Guide") {
    void vscode.env.openExternal(vscode.Uri.parse(installGuide));
  } else if (choice === "Open Settings") {
    void vscode.commands.executeCommand("workbench.action.openSettings", setting);
  }
}

/**
 * Resolves a configured program to an executable path: an absolute path (or
 * one starting with `~`) as given, otherwise a search of PATH and then the Go
 * install directories, since an editor started from the macOS Dock or a
 * desktop launcher often has a PATH without ~/go/bin.
 */
export function findExecutable(name: string): string | undefined {
  const expanded = name.startsWith("~") ? path.join(os.homedir(), name.slice(1)) : name;
  if (path.isAbsolute(expanded) || expanded.includes("/") || expanded.includes("\\")) {
    return isExecutable(expanded) ? expanded : undefined;
  }
  const exts = process.platform === "win32" ? (process.env.PATHEXT || ".EXE").split(";").concat([""]) : [""];
  const dirs = (process.env.PATH || "").split(path.delimiter).filter(Boolean);
  if (process.env.GOBIN) {
    dirs.push(process.env.GOBIN);
  }
  for (const gopath of (process.env.GOPATH || "").split(path.delimiter).filter(Boolean)) {
    dirs.push(path.join(gopath, "bin"));
  }
  dirs.push(path.join(os.homedir(), "go", "bin"));
  for (const dir of dirs) {
    for (const ext of exts) {
      const candidate = path.join(dir, expanded + ext);
      if (isExecutable(candidate)) {
        return candidate;
      }
    }
  }
  return undefined;
}

function isExecutable(file: string): boolean {
  try {
    if (!fs.statSync(file).isFile()) {
      return false;
    }
    fs.accessSync(file, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

// ---- Run and test commands ----------------------------------------------

async function activeNomiFile(): Promise<vscode.TextEditor | undefined> {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.document.languageId !== "nomi") {
    void vscode.window.showWarningMessage("Nomi: open a .nomi file first.");
    return undefined;
  }
  if (editor.document.isUntitled) {
    void vscode.window.showWarningMessage("Nomi: save the file before running it.");
    return undefined;
  }
  if (editor.document.isDirty && !(await editor.document.save())) {
    return undefined;
  }
  return editor;
}

async function runOnFile(args: (file: string) => string[]): Promise<void> {
  const editor = await activeNomiFile();
  if (editor) {
    await runNomi(args(editor.document.uri.fsPath), editor.document.uri);
  }
}

// Runs nomi on the file a code lens names, saving it first.
async function runOnUri(uri: string, args: (file: string) => string[]): Promise<void> {
  const file = vscode.Uri.parse(uri);
  const doc = vscode.workspace.textDocuments.find((d) => d.uri.toString() === file.toString());
  if (doc?.isDirty && !(await doc.save())) {
    return;
  }
  await runNomi(args(file.fsPath), file);
}

async function testAtCursor(): Promise<void> {
  const editor = await activeNomiFile();
  if (!editor) {
    return;
  }
  const line = enclosingTestLine(editor.document.getText(), editor.selection.active.line);
  if (line === undefined) {
    void vscode.window.showWarningMessage("Nomi: the cursor is not inside a test.");
    return;
  }
  await runNomi(["test", editor.document.uri.fsPath, "--line", String(line)], editor.document.uri);
}

/**
 * Runs `nomi <args>` in a shell task from the file's root (the nearest
 * directory holding nomi.toml, else .git, else the workspace folder), so it
 * gets the user's shell PATH as the Zed and Neovim runners do. A run still in
 * flight is stopped first, so output always lands in one terminal.
 */
async function runNomi(args: string[], file: vscode.Uri): Promise<void> {
  const nomi = vscode.workspace.getConfiguration("nomi", file).get<string>("path") || "nomi";
  for (const running of vscode.tasks.taskExecutions) {
    if (running.task.definition.type === "nomi") {
      running.terminate();
    }
  }
  const task = new vscode.Task(
    { type: "nomi", args },
    vscode.workspace.getWorkspaceFolder(file) ?? vscode.TaskScope.Workspace,
    `nomi ${args.map((a) => (a === file.fsPath ? path.basename(a) : a)).join(" ")}`,
    "nomi",
    new vscode.ShellExecution(nomi, args, { cwd: rootOf(file) }),
  );
  task.presentationOptions = {
    reveal: vscode.TaskRevealKind.Always,
    panel: vscode.TaskPanelKind.Shared,
    clear: true,
    showReuseMessage: false,
  };
  await vscode.tasks.executeTask(task);
}

function rootOf(file: vscode.Uri): string {
  for (const marker of ["nomi.toml", ".git"]) {
    let dir = path.dirname(file.fsPath);
    for (;;) {
      if (fs.existsSync(path.join(dir, marker))) {
        return dir;
      }
      const parent = path.dirname(dir);
      if (parent === dir) {
        break;
      }
      dir = parent;
    }
  }
  return vscode.workspace.getWorkspaceFolder(file)?.uri.fsPath ?? path.dirname(file.fsPath);
}
