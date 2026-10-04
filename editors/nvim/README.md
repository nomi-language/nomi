# Neovim support for Nomi

Neovim support for Nomi. Requires Neovim 0.11 or newer.

| Feature | Needs |
|---|---|
| `.nomi` → `nomi` filetype | plain Neovim (`ftdetect/nomi.lua`) |
| `nomi-lsp` client: diagnostics, hover, go-to-definition, formatting | plain Neovim (`lsp/nomi.lua`, enabled by `plugin/nomi.lua`) |
| Tree-sitter highlighting, including typed-literal injections | plain Neovim plus the compiled `nomi` parser |
| Dimmed `//!` attached tests, the one under the cursor shown normally (`lua/nomi/attached_tests.lua`) | plain Neovim plus the parser |
| Tree-sitter folding (`foldmethod=expr`) | plain Neovim plus the parser |
| `commentstring` `// %s`, 2-space indent | plain Neovim (`ftplugin/nomi.lua`) |
| Tree-sitter indentation (`indents.scm`) | [nvim-treesitter](https://github.com/nvim-treesitter/nvim-treesitter) |
| Function/class/parameter/comment textobjects (`textobjects.scm`) | [nvim-treesitter-textobjects](https://github.com/nvim-treesitter/nvim-treesitter-textobjects) |
| Scope/definition data (`locals.scm`) | a plugin that reads locals queries |
| Test explorer, per-test results and diagnostics (`lua/nomi/neotest.lua`) | [neotest](https://github.com/nvim-neotest/neotest) plus the parser |

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
| `<leader>tr` | `:NomiTest` / `:NomiTest nearest` | the `test`, `tests` group or attached test around the cursor (`nomi test <file> --line <header line>`) |
| `<leader>tt` | `:NomiTest file` | every test in the file |
| `<leader>tT` | `:NomiTest all` | `nomi test` from the project root |
| `<leader>tl` | `:NomiTest last` | the previous command again |
| `<leader>cx` | `:NomiRun` | `nomi run <file>` |

`nomi-lsp` also serves code lenses (Run test, Run tests, Run attached test,
and Run above `fn main`), and the plugin defines their commands, so
`vim.lsp.codelens.run()` on a lens's line runs it in the same terminal.
Neovim draws lenses only after `vim.lsp.codelens.refresh()`; to keep them
current, call it from an autocmd, for example on `LspAttach`, `BufEnter` and
`InsertLeave` for `*.nomi` (LazyVim does this when `codelens.enabled` is set
in its lspconfig options).

`nomi test --line` matches a test's first line only, so the cursor may be
anywhere inside the test: tree-sitter finds the enclosing one. Set
`vim.g.nomi_keymaps = false` to skip the keys; the commands stay. The keys
follow LazyVim's test extra.

### neotest

`lua/nomi/neotest.lua` is a [neotest](https://github.com/nvim-neotest/neotest)
adapter. Nothing in the plugin requires it or neotest; it loads only when a
neotest spec names it:

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
`test` declarations, `tests` groups (as namespaces) and attached `//!` tests
in every `.nomi` file, and runs them with `nomi test --format json`, which
prints one JSON record per test (see the spec's §36). Results are matched to
positions by file and first line.

When the adapter is loaded, `<leader>tr`, `<leader>tt`, `<leader>tT` and
`<leader>tl` call neotest (`run.run()`, `run.run(<file>)`, `run.run(<project
root>)`, `run.run_last()`); requiring neotest at that point loads it if it
is lazy. Without the adapter they use the terminal runner, and neotest is
never loaded. `<leader>cx` always uses the terminal. A `nomi` too old for
`--format json` shows up in neotest as failed tests whose error says so.

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
blocks, `tests` groups), `@parameter.{inner,outer}` (parameters, type
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
