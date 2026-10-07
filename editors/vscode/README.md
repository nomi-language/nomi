# Nomi for VS Code

Language support for [Nomi](https://github.com/nomi-language/nomi) in VS Code
and its forks:

- `.nomi` files, and extensionless scripts whose `#!` line names `nomi`:
  comment toggling, bracket matching, auto-closing and surrounding pairs,
  indentation, and `///`, `//!` and `//#` lines that continue themselves on
  Enter.
- Highlighting from a TextMate grammar, refined by the language server's
  semantic tokens.
- The language server, `nomi-lsp`: diagnostics, hover, completion,
  go-to-definition, formatting and the rest of what it serves.
- Commands: **Nomi: Run File** (`nomi run <file>`), **Nomi: Test File**
  (`nomi test <file>`), **Nomi: Test at Cursor** (`nomi test <file> --line N`
  for the `test`, `tests` group or `//!` attached test under the cursor) and
  **Nomi: Restart Language Server**. Runs happen in a shared task terminal,
  from the nearest directory holding `nomi.toml` (else `.git`, else the
  workspace folder). A run still in flight is stopped first.
- Code lenses from `nomi-lsp`: **Run test**, **Run tests** and **Run attached
  test** above each `test`, `tests` group and `//!` test (the lens command
  `nomi.runTest`, which runs `nomi test <file> --line N`), and **Run** above
  `fn main` (`nomi.runMain`, which runs `nomi run <file>`), in the same task
  terminal.
- `//!` attached tests drawn dimmed, so they do not compete with the code they
  test. Everything after each `//!` marker is drawn in one muted colour, as a
  comment reads (or, with `nomi.attachedTests.style` set to `opacity`, at
  reduced opacity with its syntax colours kept); the marker keeps its comment
  color, and diagnostics' squiggles are not dimmed.
  The attached test under the cursor, or any a selection touches, is drawn
  normally.

It is not on the Marketplace or Open VSX. Build it from a checkout.

## Install

Install `nomi` and `nomi-lsp` first ([docs/install.md](../../docs/install.md)),
for example from the repository root:

```sh
go install ./cmd/nomi ./cmd/nomi-lsp
```

Then, with Node 22+ and npm, from the repository root:

```sh
make install-vscode
```

That runs `npm ci` and `vsce package` in `editors/vscode/`, which writes
`editors/vscode/nomi.vsix`, and installs it with `code --install-extension`.
`make build-vscode` only builds the `.vsix`; install it in a fork with that
editor's "Install from VSIX..." command.

## Settings

| Setting | Default | What it is |
|---|---|---|
| `nomi.path` | `nomi` | The CLI the run and test commands invoke. |
| `nomi.lsp.path` | `nomi-lsp` | The language server. Changing it restarts the server. |
| `nomi.attachedTests.dim` | `true` | Draw `//!` attached tests dimmed. |
| `nomi.attachedTests.revealUnderCursor` | `true` | Draw the attached test under the cursor normally. Off, it is dimmed too. |
| `nomi.attachedTests.style` | `"comment"` | `"comment"` draws a dimmed test in one muted colour (the theme's `editorCodeLens.foreground`; an extension cannot read the theme's comment colour); `"opacity"` keeps its syntax colours at reduced opacity. |
| `nomi.attachedTests.dimOpacity` | `0.5` | With the opacity style, the opacity of the dimmed text, from 0.1 to 1. |

The attached-test settings take effect as soon as they change.

A bare name is looked up on `PATH`, then in `$GOBIN`, `$GOPATH/bin` and
`~/go/bin`, because VS Code started from the Dock or a desktop launcher often
has a `PATH` without Go's install directory. If `nomi-lsp` is not found, the
extension says so and links to the install guide; highlighting and the
commands still work. The run and test commands go through your shell, so
`nomi` there resolves on your shell's `PATH`.

## Working on the extension

```sh
npm --prefix editors/vscode ci
npm --prefix editors/vscode run compile     # tsc into out/
npm --prefix editors/vscode test            # unit and grammar tests
npm --prefix editors/vscode run test:smoke  # the extension in a real VS Code
code --extensionDevelopmentPath="$PWD/editors/vscode" path/to/project
```

The last line opens a VS Code window running the extension from the checkout;
reload that window after `npm run compile`. `make test-vscode` runs `npm ci`
and `npm test`.

What the tests cover:

- `test/unit/testLine.test.ts`: finding the test under the cursor. VS Code has
  no tree-sitter, so `src/testLine.ts` scans the text for `test` and `tests`
  headers and `//!` blocks, matching braces outside comments and literals.
  Zed and Neovim find the same node with tree-sitter.
- `test/unit/attachedTests.test.ts`: which lines form an attached test (a run
  of consecutive `//!` lines; any other line ends it, as in tree-sitter-nomi's
  scanner), which spans `src/attachedTests.ts` dims, and which attached tests
  a cursor or selection reveals.
- `test/unit/corpus.test.ts`: tokenizes every `.nomi` file under `tests/`
  and `std/` with the grammar and fails if a string, comment or
  interpolation runs past a top-level declaration or the end of a file, or if
  any token is marked invalid.
- `test/grammar/syntax.nomi-test`: scope assertions for
  [vscode-tmgrammar-test](https://github.com/PanAeon/vscode-tmgrammar-test)
  (interpolation, typed literals, raw and triple-quoted strings, doc comments,
  attached tests, `todo`, `.field` and `.Variant`, numbers, operators). A line
  `// <~~~~---- scope` asserts `scope` on the previous code line, starting at
  column 4 (the number of `~`) for 4 characters (the number of `-`).
- `test/smoke/`: builds `nomi` and `nomi-lsp` from this checkout, downloads
  VS Code into `.vscode-test/` (once; about 900 MB unpacked on macOS), and checks in its
  extension host that a `.nomi` file activates the extension, `nomi-lsp`
  reports a type error, the outline lists the file's declarations, Test at
  Cursor and Test File run the right tests, each test's code lens runs it, and
  attached tests are dimmed except the one under the cursor, following edits
  and the settings. VS Code cannot read an editor's decorations back, so that
  step checks the ranges the extension passed to `setDecorations` (exposed as
  the extension's `attachedTestDimmedRanges` export), not the drawn text.
  It opens a VS Code window while it runs. Set `VSCODE_EXECUTABLE` to use an
  installed VS Code instead.

## The grammar

`syntaxes/nomi.tmLanguage.json` is derived from the bat syntax,
`editors/bat/nomi.sublime-syntax`. Both are regex grammars with the same
structure (comments, strings, numbers, keywords, operators, identifiers,
punctuation), so a change to Nomi's keywords, literals or operators goes into
both by hand. The tree-sitter grammar and its queries do not feed either.

Where it goes further than the bat syntax:

- `//!` lines are highlighted as Nomi code after a comment-colored marker.
- `///` and `//#` lines are documentation comments.
- A typed literal's tag (`Date"..."`, ``Regex`...` ``) is scoped
  `entity.name.type.literal`.
- `.name` and `.Variant` with no expression before the dot are the field
  accessor and variant shorthand; after an expression, `.name` is a field and
  `.name(` a qualified call.
- Types are `entity.name.type` rather than `storage.type`, which VS Code
  themes color as types rather than as keywords.
- An escape the spec rejects (`"\q"`) is marked invalid.
- Interpolations are `meta.embedded`, so themes draw the code inside them as
  code rather than as string.
