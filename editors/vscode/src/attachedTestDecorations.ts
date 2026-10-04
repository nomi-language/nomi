// Draws `//!` attached tests dimmed, as Neovim, Helix and Zed do: on each
// attached-test line, the text after the `//!` marker is drawn either in one
// muted colour, as a comment reads (style "comment", the default), or at
// reduced opacity, keeping its syntax colours (style "opacity"). The attached
// test a selection touches is drawn normally. The decoration sets only the
// text's colour or opacity, so diagnostics' squiggles, which VS Code draws in
// a separate layer, stay at full strength.
//
// An extension cannot read the theme's comment colour (token colours are not
// theme colours), so "comment" uses editorCodeLens.foreground, the muted
// colour themes give secondary text in the editor.
//
// Settings: nomi.attachedTests.dim, nomi.attachedTests.style,
// nomi.attachedTests.revealUnderCursor and nomi.attachedTests.dimOpacity.
import * as vscode from "vscode";
import { Group, attachedTestGroups, dimSpans, groupsUnder, splitLines } from "./attachedTests";

const section = "nomi.attachedTests";
const debounceMs = 150;

interface Scan {
  version: number;
  lines: string[];
  groups: Group[];
}

export class AttachedTestDimmer implements vscode.Disposable {
  private decoration: vscode.TextEditorDecorationType;
  private readonly scans = new Map<string, Scan>();
  private readonly pending = new Map<string, ReturnType<typeof setTimeout>>();
  // Per editor: the first lines of the revealed groups at the last draw, and
  // the ranges drawn dimmed.
  private readonly revealedKey = new WeakMap<vscode.TextEditor, string>();
  private readonly drawn = new WeakMap<vscode.TextEditor, vscode.Range[]>();
  private readonly subscriptions: vscode.Disposable[] = [];

  constructor() {
    this.decoration = createDecoration();
    this.subscriptions.push(
      vscode.window.onDidChangeVisibleTextEditors((editors) => editors.forEach((e) => this.draw(e, true))),
      vscode.window.onDidChangeTextEditorSelection((e) => {
        // A pending rescan redraws with the new selection anyway.
        if (!this.pending.has(e.textEditor.document.uri.toString())) {
          this.draw(e.textEditor, false);
        }
      }),
      vscode.workspace.onDidChangeTextDocument((e) => {
        if (e.document.languageId === "nomi" && e.contentChanges.length > 0) {
          this.schedule(e.document);
        }
      }),
      vscode.workspace.onDidCloseTextDocument((doc) => {
        const key = doc.uri.toString();
        this.scans.delete(key);
        clearTimeout(this.pending.get(key));
        this.pending.delete(key);
      }),
      vscode.workspace.onDidChangeConfiguration((e) => {
        if (!e.affectsConfiguration(section)) {
          return;
        }
        if (e.affectsConfiguration(`${section}.dimOpacity`) || e.affectsConfiguration(`${section}.style`)) {
          this.decoration.dispose();
          this.decoration = createDecoration();
        }
        this.drawAll();
      }),
    );
    this.drawAll();
  }

  /** The ranges last drawn dimmed in `editor`, for the smoke test. */
  dimmedRanges(editor: vscode.TextEditor): vscode.Range[] | undefined {
    return this.drawn.get(editor);
  }

  dispose(): void {
    this.pending.forEach((t) => clearTimeout(t));
    this.pending.clear();
    this.subscriptions.forEach((d) => d.dispose());
    this.decoration.dispose();
  }

  private drawAll(): void {
    vscode.window.visibleTextEditors.forEach((e) => this.draw(e, true));
  }

  private schedule(doc: vscode.TextDocument): void {
    const key = doc.uri.toString();
    clearTimeout(this.pending.get(key));
    this.pending.set(
      key,
      setTimeout(() => {
        this.pending.delete(key);
        vscode.window.visibleTextEditors.filter((e) => e.document === doc).forEach((e) => this.draw(e, true));
      }, debounceMs),
    );
  }

  private scan(doc: vscode.TextDocument): Scan {
    const key = doc.uri.toString();
    let scan = this.scans.get(key);
    if (!scan || scan.version !== doc.version) {
      const lines = splitLines(doc.getText());
      scan = { version: doc.version, lines, groups: attachedTestGroups(lines) };
      this.scans.set(key, scan);
    }
    return scan;
  }

  // Draws `editor`'s dimmed ranges. Unless `force`, it does nothing when the
  // revealed groups are the ones it drew last, so a cursor move within or
  // between undimmed lines costs no redraw.
  private draw(editor: vscode.TextEditor, force: boolean): void {
    if (editor.document.languageId !== "nomi") {
      return;
    }
    const config = vscode.workspace.getConfiguration(section, editor.document);
    if (!config.get<boolean>("dim", true)) {
      if (force || this.revealedKey.get(editor) !== "off") {
        editor.setDecorations(this.decoration, []);
        this.drawn.set(editor, []);
        this.revealedKey.set(editor, "off");
      }
      return;
    }
    const scan = this.scan(editor.document);
    const revealed = config.get<boolean>("revealUnderCursor", true)
      ? groupsUnder(
          scan.groups,
          editor.selections.map((s) => [s.start.line, s.end.line] as [number, number]),
        )
      : [];
    const key = revealed.map((g) => g.first).join(",");
    if (!force && this.revealedKey.get(editor) === key) {
      return;
    }
    const ranges = dimSpans(scan.lines, scan.groups, revealed).map(
      (s) => new vscode.Range(s.line, s.start, s.line, s.end),
    );
    editor.setDecorations(this.decoration, ranges);
    this.drawn.set(editor, ranges);
    this.revealedKey.set(editor, key);
  }
}

function createDecoration(): vscode.TextEditorDecorationType {
  const config = vscode.workspace.getConfiguration(section);
  // Text typed at either end of a dimmed span stays dimmed until the
  // debounced rescan redraws it.
  const rangeBehavior = vscode.DecorationRangeBehavior.ClosedClosed;
  if (config.get<string>("style", "comment") !== "opacity") {
    return vscode.window.createTextEditorDecorationType({
      color: new vscode.ThemeColor("editorCodeLens.foreground"),
      rangeBehavior,
    });
  }
  const opacity = config.get<number>("dimOpacity", 0.5);
  const clamped = Math.min(1, Math.max(0.1, Number.isFinite(opacity) ? opacity : 0.5));
  return vscode.window.createTextEditorDecorationType({
    opacity: String(clamped),
    // Text typed at either end of a dimmed span stays dimmed until the
    // debounced rescan redraws it.
    rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
  });
}
