# Neovim support for Nomi

Neovim support for Nomi. Requires Neovim 0.11 or newer.

| Feature | Needs |
|---|---|
| `.nomi`, or an extensionless file whose `#!` line names `nomi` → `nomi` filetype | plain Neovim (`ftdetect/nomi.lua`, `lua/nomi/filetype.lua`) |
| `nomi-lsp` client: diagnostics, hover, go-to-definition, formatting | plain Neovim (`lsp/nomi.lua`, enabled by `plugin/nomi.lua`) |
| Tree-sitter highlighting, including typed-literal injections | plain Neovim plus the compiled `nomi` parser |
| Dimmed `//!` attached tests, the one under the cursor shown normally (`lua/nomi/attached_tests.lua`) | plain Neovim plus the parser |
| Tree-sitter folding (`foldmethod=expr`) | plain Neovim plus the parser |
| `commentstring` `// %s`, 4-space indent, `textwidth=100` (`nomi fmt`'s width) | plain Neovim (`ftplugin/nomi.lua`) |
| Tree-sitter indentation (`indents.scm`) | [nvim-treesitter](https://github.com/nvim-treesitter/nvim-treesitter) |
| Function/class/parameter/comment textobjects (`textobjects.scm`) | [nvim-treesitter-textobjects](https://github.com/nvim-treesitter/nvim-treesitter-textobjects) |
| Scope/definition data (`locals.scm`) | a plugin that reads locals queries |
| Test explorer, per-test results and diagnostics (`lua/nomi/neotest.lua`) | [neotest](https://github.com/nvim-neotest/neotest) plus the parser |
| Debugging: breakpoints, stepping, the stack and locals through `nomi dap` (`lua/nomi/dap.lua`) | [nvim-dap](https://github.com/mfussenegger/nvim-dap) |

The ftplugin sets `indentexpr` to nvim-treesitter's when that plugin is
installed; without it Neovim keeps `autoindent`.

## Install

From the repository root, install `nomi` and the language server `nomi-lsp`
and put them on `PATH` (the plugin also finds `nomi-lsp` in `$GOBIN` or
`~/go/bin`; the test commands run `nomi`):

```sh
go install ./cmd/nomi ./cmd/nomi-lsp
```

### Package directory (recommended)

This needs `make`, `python3` and a C compiler (`cc`). From the repository
root:

```sh
make install-nvim
```

This regenerates the queries, copies this directory to
`${XDG_DATA_HOME:-~/.local/share}/nvim/site/pack/nomi/start/nomi`, and compiles
`tree-sitter-nomi/src/*.c` into `parser/nomi.so` inside it with
`cc -shared -fPIC -O2`. Neovim loads everything under `pack/*/start` on
startup. Re-run it after a grammar or query change. Remove the directory to
uninstall.

### lazy.nvim, from a local checkout

Use this under lazy.nvim (including LazyVim) rather than `make install-nvim`:
lazy.nvim resets `packpath` by default, so a copy under `pack/*/start` never
loads.

Compile the parser into this directory (it is gitignored), then point lazy.nvim
at it:

```sh
make dev-nvim
```

Run it again after a grammar change, or enable the repository's post-merge
hook (`make enable-hooks`), which runs it after a
merge into `main` changes the grammar or the editor files.

```lua
{
  dir = '/path/to/nomi/editors/nvim', -- your checkout
  name = 'nvim-nomi',
  lazy = false,
  init = function()
    -- vim.g.nomi_lsp = false -- uncomment to skip starting nomi-lsp
  end,
}
```

Load it eagerly (`lazy = false`) or with `ft = 'nomi'`; with `ft`, lazy.nvim
still sources `ftdetect/`, so `.nomi` detection works before the plugin loads.

## Running tests

Like Zed's runnables, the plugin runs `nomi` in a terminal split from the
project root (the nearest `nomi.toml`, else `.git`):

| Key | Command | Runs |
|---|---|---|
| `<leader>tr` | `:NomiTest` / `:NomiTest nearest` | the `test`, `group` or attached test around the cursor (`nomi test <file> --line <header line>`) |
| `<leader>tt` | `:NomiTest file` | every test in the file |
| `<leader>tT` | `:NomiTest all` | `nomi test` from the project root |
| `<leader>tl` | `:NomiTest last` | the previous command again; a single test is found again by name (an attached test by its owner and its place among that owner's prompts) and run at the line it starts on now, or reported gone |
| `<leader>td` | `:NomiDebug` / `:NomiDebug nearest` | the test around the cursor under the debugger, or the file's `main` when the cursor is in no test (see [Debugging](#debugging)) |
| `<leader>dh` | `:NomiDebug back` | step back in the paused debug session |
| `<leader>dH` | `:NomiDebug reverse` | reverse continue in the paused debug session |
| | `:NomiDebug target` | step into one of the calls on the paused line, chosen from a list |
| | `:NomiDebug break <function>` | toggle a function breakpoint (`:NomiDebug break` lists them) |
| `<leader>cx` | `:NomiRun` | `nomi run <file>` |

`nomi-lsp` also serves code lenses (Run test, Run group, Run attached test,
and Run above `fn main`, each with a Debug lens beside it), and the plugin
defines their commands, so `vim.lsp.codelens.run()` on a lens's line runs it
in the same terminal, or starts the debugger on it.
Neovim draws lenses only after `vim.lsp.codelens.refresh()`; to keep them
current, call it from an autocmd, for example on `LspAttach`, `BufEnter` and
`InsertLeave` for `*.nomi` (LazyVim does this when `codelens.enabled` is set
in its lspconfig options).

`nomi test --line` matches a test's first line, so the cursor may be
anywhere inside the test: tree-sitter finds the enclosing one. A line no test
starts on is an error (`no test at <file>:<line>`), not an empty pass. Set
`vim.g.nomi_keymaps = false` to skip the keys; the commands stay. The keys
follow LazyVim's test extra.

### neotest

`lua/nomi/neotest.lua` is a [neotest](https://github.com/nvim-neotest/neotest)
adapter. Nothing in the plugin requires it; it loads only when a neotest
spec names it:

```lua
{
  'nvim-neotest/neotest',
  dependencies = { 'nvim-neotest/nvim-nio', 'nvim-lua/plenary.nvim', 'nvim-nomi' },
  opts = {
    adapters = { require('nomi.neotest') },
  },
}
```

With LazyVim's test extra (`lazyvim.plugins.extras.test.core`), add it to the
extra's adapter list instead of writing a second neotest spec:

```lua
{
  'nvim-neotest/neotest',
  opts = function(_, opts)
    opts.adapters = opts.adapters or {}
    table.insert(opts.adapters, require('nomi.neotest'))
  end,
}
```

Over the terminal runner, neotest adds a per-test pass/fail mark in the sign
column, failure diagnostics on the failing assertion's line, the summary
tree, the per-test output panel, and jumping between failures. It discovers
`test` declarations, test groups (as namespaces) and attached `//!` tests
in every `.nomi` file, and runs them with `nomi test --format json`, which
prints one JSON record per test (see the spec's §36). Results are matched to
positions by file and first line. The run sets `NOMI_COLOR=always`, so each
test's output in the output panel is the text report's block for it, coloured
and with its Nomi source highlighted; diagnostics and the summary's short
text stay plain.

When neotest is configured with the adapter, `<leader>tr`, `<leader>tt`,
`<leader>tT` and `<leader>tl` call neotest (`run.run()`, `run.run(<file>)`,
`run.run(<project root>)`, `run.run_last()`), and `<leader>td` calls
`run.run({ strategy = 'dap' })`, neotest's debug strategy (needs nvim-dap). The buffer's keys hide
LazyVim's global ones, which are what would load a lazy neotest, so at
keypress the plugin requires neotest itself when it is installed (in
lazy.nvim's plugin list, or on 'runtimepath'). That loads it and runs its
spec's `opts`, and the keys use neotest when the `nomi` adapter is among its
adapters. Otherwise they use the terminal runner. When neotest is not
installed it is never loaded; when it is installed without the adapter, the
first test key loads it to find that out. `<leader>cx` always uses the terminal. A `nomi` too old for
`--format json` shows up in neotest as failed tests whose error says so.

## Debugging

`nomi dap` is a Debug Adapter Protocol server, and `lua/nomi/dap.lua` makes it
an [nvim-dap](https://github.com/mfussenegger/nvim-dap) adapter named `nomi`
(`{ type = 'executable', command = 'nomi', args = { 'dap' } }`) with three
configurations for Nomi buffers: debug the file's `main`, the test under the
cursor, or every test in the file. Nothing in `plugin/` or `ftplugin/`
requires nvim-dap: the adapter is registered when nvim-dap is already loaded,
when lazy.nvim loads it (its `User LazyLoad` event), or at the first debug
command. An adapter or configurations you define for `nomi` yourself are
kept.

With nvim-dap installed:

- `<leader>td` or `:NomiDebug` debugs the test around the cursor, or `main`
  when the cursor is in no test; `:NomiDebug file` debugs every test in the
  file, and `:NomiDebug run` the file's `main`.
- In a paused session, `<leader>dh` or `:NomiDebug back` steps back to the
  previous statement (reverse next: it passes over calls), and `<leader>dH`
  or `:NomiDebug reverse` reverse continues to the latest earlier breakpoint
  stop, or to the program's first statement. They are nvim-dap's
  `step_back()` and `reverse_continue()`; `nomi dap` replays the program from
  its start, answering input, the clock, randomness and files from what the
  session recorded, so nothing is printed or read twice. A program that
  starts a task cannot go back, and the session says why.
- In a paused session, `:NomiDebug target` is nvim-dap's
  `step_into({ askForTargets = true })`: it lists the calls on the paused
  line, lambdas passed to `Iter` adapters included (`Iter.map(|n| ...)
  callback`), and steps into the one you pick. If the line finishes without
  calling it, the step stops at the next statement and the REPL says so.
- `:NomiDebug break transact` toggles a function breakpoint, which stops at
  the function's first statement each time it is called. nvim-dap keeps only
  line breakpoints, so the plugin keeps the names and sends them to the
  running Nomi session and to each new one before its line breakpoints. Name
  a function as the call stack does: `transact`, `Account.deposit`,
  `Card.to_string`, `Display.to_string` (every impl) or `tiny_bank.transact`.
  A name nothing matches is reported with the closest ones. `:NomiDebug
  break` alone lists them.
- `Ctrl-]` on a variable in nvim-dap's scopes or REPL widget jumps to where
  it is bound (nvim-dap's tag function, through the `locations` request).
- The Debug code lenses start the same sessions.
- `dap.continue()` in a Nomi buffer offers the three configurations.
- With neotest and the adapter configured, `<leader>td` runs the nearest
  test with neotest's `dap` strategy, and neotest shows its result as for
  any other run (the session reports `nomi test --format json` records).

A launch configuration takes `mode` (`run` or `test`), `program`, `line` (one
test, as `nomi test --line` selects it), `args`, `cwd`, `env`, `console`,
`justMyCode` (default true: step through the standard library without
stopping in it) and `stopOnEntry`. A program's `console` defaults to
`integratedTerminal`: nvim-dap opens a terminal (its `runInTerminal`) where
the program reads its input and prints, so an interactive program works.
Tests default to `internalConsole`, the REPL buffer, where the program has
no input. The exception breakpoints are `trap`, `assertion` and `mainError`
(on by default) and `taskFailure` (off); set them with
`require('dap').set_exception_breakpoints({ 'trap', 'assertion', 'mainError', 'taskFailure' })`.

While paused, the REPL (`:DapToggleRepl`), `require('dap.ui.widgets').hover()`
and nvim-dap-ui's watches evaluate Nomi in the selected frame. An expression
with an effect is refused unless it ends with `// allow effects`. Breakpoints
take a condition, a hit count or a log message:
`require('dap').set_breakpoint('attempt == 3')`,
`require('dap').set_breakpoint(nil, '% 10')`,
`require('dap').set_breakpoint(nil, nil, 'total is {total}')`. The scopes
include **Pipeline**, each stage of the `|>` chain the frame is in, and
**Captured output**, one per `io.capture` or `io.replay` call the frame is
inside, with what it printed so far and, for a replay, the transcript, the
script's input read and to come, and where it already diverged. A test's own
output reaches the REPL buffer as the test prints it.
`require('dap').restart_frame()` runs the selected frame again from its first
statement. For values beside the code, install
[nvim-dap-virtual-text](https://github.com/theHamsta/nvim-dap-virtual-text)
and call `require('nvim-dap-virtual-text').setup()`; it reads the paused
frame's variables from `nomi dap`, so it needs nothing Nomi-specific.

## Configuration

- **Turn off the LSP client:** set `vim.g.nomi_lsp = false` before the plugin
  loads.
- **Change the LSP client:** `vim.lsp.config('nomi', { cmd = { '/path/to/nomi-lsp' } })`.
  The root is the nearest directory with `nomi.toml`, then the nearest `.git`.
- **Change buffer options:** put overrides in `after/ftplugin/nomi.lua`.

### Inlay hints

`nomi-lsp` serves three kinds of inlay hint, each with its own server
setting:

| Setting | Default | Shows |
|---|---|---|
| `inlayHints.pipeTypes` | on | the type a stage line of a multi-line pipeline produces, at the end of the line, where it changes |
| `inlayHints.parameterNames` | on | `x:` before a positional argument |
| `inlayHints.bindingTypes` | on | `: Int` after an unannotated binding or lambda parameter |

Neovim shows inlay hints only where they are enabled
(`vim.lsp.inlay_hint.enable(true)`; LazyVim enables them for every buffer
unless `inlay_hints.exclude` names the filetype), and it has one switch for
all kinds. Choose the kinds on the server instead. For pipe types without
parameter names or binding types:

```lua
vim.lsp.config('nomi', {
  init_options = { inlayHints = { parameterNames = false, bindingTypes = false } },
})
```

The same table under `settings = { nomi = { inlayHints = { ... } } }` works
too, and a change sent later with `workspace/didChangeConfiguration` applies
at once. A setting left out keeps its default.

Pipe hints go only on pipelines with a `|>` that starts a line; a pipe on
one line gets none. The head line gets a hint when the head is not a name, a
field read or a literal. A stage line gets one only when its type differs
from the line above's (the previous stage's, or the head's), so a pipeline
whose stages keep one type shows nothing. The last stage gets none when its
type is already on screen: a written annotation on the binding it
initializes, that binding's own type hint (with `bindingTypes` on), or the
declared return type of the function whose body it ends. A type longer than
40 characters is cut, with the full type in the hint's tooltip. A stage the
checker could not type gets no hint.

### Attached tests

`//!` attached tests are drawn dimmed, like comments, so they do not compete
with the declaration they test. The `//!` markers keep their documentation
comment color. The attached test under the cursor is drawn normally; moving
the cursor out dims it again. Each window follows its own cursor.

The dim is the `NomiAttachedTest` highlight group. By default it is your
colorscheme's `Comment` with `nocombine`, so a keyword's bold or a string's
italic underneath does not show through. The plugin defines it with
`default = true` and again on every `ColorScheme` event, so your own
definition wins; set it after your colorscheme loads, or in a `ColorScheme`
autocmd:

```lua
vim.api.nvim_set_hl(0, 'NomiAttachedTest', { fg = '#5c6370', italic = true, nocombine = true })
```

It is drawn at priority 130: above tree-sitter (100) and LSP semantic tokens
(125 to 127), so `nomi-lsp`'s tokens do not undo it, and below diagnostics
(150), so a diagnostic's underline in an attached test is drawn at full
strength. `nomi-lsp` marks tokens on attached-test lines with the
`attachedTest` modifier; the plugin leaves `@lsp.mod.attachedTest.nomi`
undefined, because it dims those lines itself.

Two settings, read whenever a window is drawn:

| Setting | Effect |
|---|---|
| `vim.g.nomi_dim_attached_tests = false` | no dimming |
| `vim.g.nomi_reveal_attached_test = false` | dim the attached test under the cursor too |

Run `:redraw!` after changing one at runtime.

### Optional: nvim-treesitter

Nothing extra is needed for indents and textobjects beyond installing the two
plugins; they read `queries/nomi/indents.scm` and `queries/nomi/textobjects.scm`
from this plugin. Map the textobjects as usual, for example with the `main`
branch of nvim-treesitter-textobjects:

```lua
vim.keymap.set({ 'x', 'o' }, 'af', function()
  require('nvim-treesitter-textobjects.select').select_textobject('@function.outer', 'textobjects')
end)
```

Captures available: `@function.{inner,outer}` (functions, lambdas, `boot`,
`test` cases), `@class.{inner,outer}` (structs, enums, types, interfaces, impl
blocks, test groups), `@parameter.{inner,outer}` (parameters, type
parameters, call arguments, and the entries of lists, vectors, tuples, maps,
struct literals and updates, plus enum variants and struct fields), and
`@comment.{inner,outer}`.

If you prefer nvim-treesitter to build the parser instead of `make`
(`:TSInstall nomi`), register it on its `main` branch and skip the `parser/`
step above; the queries still come from this plugin. The grammar's published
home is [nomi-language/tree-sitter-nomi](https://github.com/nomi-language/tree-sitter-nomi),
a read-only mirror of this repository's `tree-sitter-nomi/`:

```lua
vim.api.nvim_create_autocmd('User', {
  pattern = 'TSUpdate',
  callback = function()
    require('nvim-treesitter.parsers').nomi = {
      install_info = {
        url = 'https://github.com/nomi-language/tree-sitter-nomi',
        -- Pin it with the rev in editors/zed/extension.toml:
        -- revision = '<sha>',
        -- Or build from a local checkout instead of the mirror:
        -- path = '/path/to/nomi/tree-sitter-nomi',
      },
    }
  end,
})
```

## Local development

The queries in `queries/nomi/` are generated. Do not edit them; run
`make build-nvim` after changing any source or the grammar. It regenerates the
queries (`scripts/sync-nvim-runtime.sh`) and recompiles `parser/nomi.so`, so a
checkout loaded through lazy.nvim's `dir =` gets queries and a parser built
from the same grammar:

| File | Source |
|---|---|
| `highlights.scm` | `editors/zed/languages/nomi/highlights.scm` (last-wins order, which Neovim also uses), captures renamed to Neovim's standard groups, without Zed's attached-test dim block (the plugin dims from Lua), plus a `@type.builtin` rule for the prelude scalars |
| `injections.scm` | byte-identical copy of `tree-sitter-nomi/queries/injections.scm` |
| `locals.scm`, `folds.scm`, `indents.scm`, `textobjects.scm` | `editors/helix/runtime/queries/nomi/`, captures renamed to Neovim / nvim-treesitter names |

The script fails if a rename no longer finds its source text or if any output
capture is not a name Neovim or the nvim-treesitter plugins recognise.

`make test-nvim` builds the parser and runs `tests/run.lua` headless: the
test keys' neotest detection, the runner's rerun of a moved or deleted test,
the neotest adapter's position ids and its `dap` strategy spec, and the
nvim-dap adapter's registration (never loading nvim-dap), the Debug lenses
and the debug key, with neotest, nvim-dap, lazy.nvim and snacks.nvim
stubbed.
