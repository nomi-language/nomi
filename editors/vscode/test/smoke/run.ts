// Smoke test in a real VS Code: `npm run test:smoke`. It builds nomi and
// nomi-lsp from this checkout, writes a scratch workspace that points the
// extension at them, downloads VS Code into .vscode-test/ (once), and runs
// ./suite.ts inside its extension host. Set VSCODE_EXECUTABLE to use an
// installed VS Code instead of downloading one.
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { runTests } from "@vscode/test-electron";

const extensionRoot = path.resolve(__dirname, "../../..");
const repo = path.resolve(extensionRoot, "../..");

async function main(): Promise<void> {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "nomi-vscode-smoke-"));
  const bin = path.join(scratch, "bin");
  execFileSync("go", ["build", "-o", bin + path.sep, "./cmd/nomi", "./cmd/nomi-lsp"], { cwd: repo, stdio: "inherit" });

  const workspace = path.join(scratch, "workspace");
  fs.mkdirSync(path.join(workspace, ".vscode"), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, ".vscode", "settings.json"),
    JSON.stringify({ "nomi.path": path.join(bin, "nomi"), "nomi.lsp.path": path.join(bin, "nomi-lsp") }, null, 2),
  );
  fs.writeFileSync(path.join(workspace, "bad.nomi"), 'fn main() {\n    x: Int = "s"\n}\n');
  fs.writeFileSync(
    path.join(workspace, "sample_test.nomi"),
    [
      "fn double(n: Int): Int { n * 2 }",
      "",
      'test "passes" {',
      "    assert double(2) == 4",
      "}",
      "",
      'test "fails" {',
      "    assert double(2) == 5",
      "}",
      "",
    ].join("\n"),
  );

  fs.writeFileSync(
    path.join(workspace, "attached.nomi"),
    [
      "//! assert double(2) == 4",
      "//! assert double(0) == 0",
      "fn double(n: Int): Int { n * 2 }",
      "",
      "//! assert triple(1) == 3",
      "fn triple(n: Int): Int { n * 3 }",
      "",
    ].join("\n"),
  );

  try {
    await runTests({
      vscodeExecutablePath: process.env.VSCODE_EXECUTABLE || undefined,
      cachePath: path.join(extensionRoot, ".vscode-test"),
      extensionDevelopmentPath: extensionRoot,
      extensionTestsPath: path.join(__dirname, "suite.js"),
      launchArgs: [
        workspace,
        "--disable-extensions",
        "--disable-workspace-trust",
        "--user-data-dir",
        path.join(scratch, "user-data"),
        "--extensions-dir",
        path.join(scratch, "extensions"),
      ],
    });
  } finally {
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
