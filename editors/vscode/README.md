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
- Message catalogs: the client also attaches `nomi-lsp` to each module's
  catalogs, the TOML files under the directory its `nomi.toml` names in
  `[i18n].catalogs` (`locales/` unless it names another), for their
  diagnostics, completion of message names, parameters and variant keys,
  hover, and quick fixes. It attaches to no other TOML file. The extension
  reads every `nomi.toml` in the workspace when it starts, and restarts the
  client when a change to one moves the catalogs.
- Commands: **Nomi: Run File** (`nomi run <file>`), **Nomi: Test File**
  (`nomi test <file>`), **Nomi: Test at Cursor** (`nomi test <file> --line N`
  for the `test`, `group` or `//!` attached test under the cursor) and
  **Nomi: Restart Language Server**. Runs happen in a shared task terminal,
  from the nearest directory holding `nomi.toml` (else `.git`, else the
  workspace folder). A run still in flight is stopped first. A test run's
  failures (an assertion, a trap, a test stopped by its time limit) appear
  in the Problems panel at their lines, through the extension's `$nomi-test`
  problem matcher.
- Code lenses from `nomi-lsp`: **Run test**, **Run group** and **Run attached
  test** above each `test`, `group` and `//!` test (the lens command
  `nomi.runTest`, which runs `nomi test <file> --line N`), and **Run** above
  `fn main` (`nomi.runMain`, which runs `nomi run <file>`), in the same task
  terminal. Beside each is a **Debug** lens (`nomi.debugTest`,
  `nomi.debugMain`), which starts the debugger on the same test or program.
- A debugger: the debug type `nomi`, which runs `nomi dap` (see
  [Debugging](#debugging)), with breakpoints in `.nomi` files, launch.json
  snippets, and **Nomi: Debug File** and **Nomi: Debug Test at Cursor**.
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

## Debugging

The debug type `nomi` starts `nomi dap` (from `nomi.path`), which runs the
program in the debugger. With no launch.json, F5 debugs the active file: its
`fn main`, or its tests when it has no `main` or is a `_test.nomi` file. A
launch.json configuration takes:

| Attribute | Default | What it is |
|---|---|---|
| `mode` | `"run"` | `"run"` runs `main`, as `nomi run` does; `"test"` runs the file's tests, as `nomi test` does. |
| `program` | the active file | The `.nomi` file. In run mode, a directory names a project whose `nomi.toml` lists one entry point. |
| `line` | none | In test mode, only the test, `group` or `//!` test starting on this line. |
| `args` | `[]` | The program's arguments. |
| `cwd` | the file's root | The working directory. |
| `env` | none | Environment variables for the program. |
| `console` | `"integratedTerminal"` for a program, `"internalConsole"` for tests | Where input and output go. The debug console cannot type into the program, which reads end of input there. |
| `justMyCode` | `true` | Step through the standard library without stopping in it. |
| `stopOnEntry` | `false` | Stop at the first statement. |

The exception breakpoints are traps, failed assertions, an `Err` returned
from `main` (all on by default) and uncaught task failures (off by default;
the trap itself stops in the task already).

While paused:

- Watches, hovers and the debug console evaluate Nomi in the selected frame.
  An expression with an effect (printing, files, the clock) is refused unless
  it ends with `// allow effects`; a hover never runs one. Each evaluation
  stops after 1,000,000 steps or 2 seconds.
- Breakpoints take a condition, a hit count (`5`, `>= 5`, `% 10`) or a log
  message with `{expression}` parts (Edit Breakpoint in the gutter's menu).
- The **Pipeline** scope shows each stage of the `|>` chain the frame is in or
  just finished; a lazy `Iter` stage shows what passed through it once the
  pipeline ran.
- Inside `io.capture` or `io.replay`, a **Captured output** scope per call
  shows what the code printed so far, which goes into the capture rather than
  the console; for a replay it also shows the transcript as the replay
  compares it, the script's input read and to come, and where the transcript
  already differs from the script.
- In a test, what the test prints outside a capture appears in the debug
  console as it prints it.
- Restart Frame (the call stack's context menu) runs a function again from
  its first statement; what it already printed stays printed.
- Step Back (the toolbar's back arrow) goes to the previous statement, passing
  over calls, and Reverse Continue to the latest earlier breakpoint stop, or
  the start. `nomi dap` replays the program from its start, answering input,
  the clock, randomness and files from what the session recorded, so nothing
  is printed or read twice; forward past the point reached, the program runs
  live again. A program that starts a task cannot go back, and says why.
- Function breakpoints: **+** in the Breakpoints view takes a function's
  name as the call stack shows it (`transact`, `Account.deposit`,
  `Card.to_string`, `Display.to_string` for every impl, or file-qualified,
  `tiny_bank.transact`), with an optional condition or hit count. It stops
  at the function's first statement each time it is called, in every
  instance of a generic function. An unknown name is unverified, and its
  message suggests close ones.
- Step Into Target (the editor's context menu, or
  `workbench.action.debug.stepIntoTarget`) lists the calls on the paused
  line, lambdas passed to `Iter` adapters included, and steps into the one
  you pick; if the line finishes without calling it, the step stops at the
  next statement and the debug console says so.
- Each variable carries where it is bound (`declarationLocationReference`),
  but VS Code (1.140) offers no command that opens it.
- Values appear beside the code (inline values, from `nomi-lsp`).
- Task threads are named by their spawn site, and Pause works while the
  program waits for input.

## Settings

| Setting | Default | What it is |
|---|---|---|
| `nomi.path` | `nomi` | The CLI the run, test and debug commands invoke. |
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
  no tree-sitter, so `src/testLine.ts` scans the text for `test` and `group`
  headers and `//!` blocks, matching braces outside comments and literals.
  Zed and Neovim find the same node with tree-sitter.
- `test/unit/attachedTests.test.ts`: which lines form an attached test (a run
  of consecutive `//!` lines; any other line ends it, as in tree-sitter-nomi's
  scanner), which spans `src/attachedTests.ts` dims, and which attached tests
  a cursor or selection reveals.
- `test/unit/debugConfig.test.ts`: the debugger contribution in
  `package.json` against `src/debugConfig.ts`: the initial configurations,
  every attribute a configuration sets being documented, the configuration
  a Debug lens starts, and what F5 with no launch.json debugs.
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
  Cursor and Test File run the right tests, each test's code lens runs it, a
  Debug test lens starts `nomi dap` and stops at the test's failed assertion,
  from which VS Code's Step Back goes back and Continue stops there again, that
  VS Code sends a function breakpoint from its Breakpoints view and that the
  adapter's step-in target and a local's binding location work in it, and
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
