# Editor support

Each directory here is one editor's support for Nomi: `zed/`, `helix/`,
`nvim/`, `vscode/` and `bat/`. Each has its own README for users, except Zed,
whose setup is below. A user sets up Helix, Neovim, VS Code or Zed with
`nomi editor install <editor>` (internal/editors), which writes these
directories' files from the ones `nomi` carries; from a checkout, `make
install-helix`, `install-nvim`, `install-vscode` and `install-zed` run it
from the checkout, building the VS Code and Zed extensions first. This file
is the setup for running each editor against this checkout while working
on Nomi. The checklist after a grammar change,
publishing the grammar and the git hooks are in the repository's `AGENTS.md`
("Grammar and editor extensions"); the rules behind the grammar's checks are
in `tree-sitter-nomi/README.md`.

## All editors

`make dev-editors` (`scripts/dev-editors.sh`) installs `nomi` from this
checkout, runs the Neovim and Helix dev setups, syncs Zed's grammar
checkout, says when Zed's dev extension must be reinstalled, and rebuilds and
installs the VS Code extension when `code` is on PATH. An editor that is not
installed is skipped; `make dev-nvim`, `make dev-helix` and `make dev-zed` run
one.

Every editor starts the language server as `nomi lsp`, from the `nomi` on
PATH, and sends a package's `nomi.toml` to it beside the `.nomi` files, and keeps its own TOML highlighting for it: Zed sends every TOML
buffer, VS Code's client and Neovim's `root_dir` select `nomi.toml` by name,
and Helix's `nomi-catalog` language, over Helix's TOML grammar, globs it.
The server answers only a `nomi.toml` and a package's phrase catalogs.

The `post-merge` hook (`make enable-hooks`) runs the parts of that a merge
changed. It acts only on `main`, or on the branches listed in
`git config nomi.editorBranches` (space-separated, for example `main next`),
so agent worktrees, which share the clone's config, are unaffected.

## Zed

For a dev install Zed uses `editors/zed/grammars/nomi/` (gitignored) as the
grammar checkout. It requires that directory's `origin` to equal
`extension.toml`'s `repository`, tries `git fetch origin <rev>` (a failure is
ignored), then runs `git checkout <rev>`.

Run `scripts/sync-zed-grammar.sh` in any fresh clone or worktree, and after
grammar changes. It makes that directory a git repository with the published
origin whose `dev` branch holds this checkout's `tree-sitter-nomi/`. When the
pinned rev's files equal the local grammar, `extension.toml` is left alone;
otherwise the script points `rev` at the local commit and says so. Do not
commit that rev: `scripts/sync-zed-grammar.sh --restore` puts the committed
one back, and a publish overwrites it.

`languages/nomi/highlights.scm` is generated, by `make build-zed-queries`
(`scripts/sync-zed-highlights.sh`), from the canonical query
`tree-sitter-nomi/queries/highlights.scm`: the same patterns in reverse
order, because Zed gives a node the last pattern that captures it and
tree-sitter's highlighter the first, with five capture names renamed to
ones Zed's themes style. `attached-test-dim.scm`, beside the extension's
manifest, is the one part written by hand: the patterns that draw `//!`
attached tests as comments, which the generator places just under the `//!`
markers. Edit the canonical query or that file, never the generated one.
Helix's and Neovim's highlights are generated from Zed's, so `make
build-helix` and `make build-nvim` run this first.

Then run "zed: install dev extension" and select the `editors/zed/` directory
itself. Install `nomi` with `go install ./cmd/nomi`; Zed launches
`nomi lsp` from PATH. `cargo test --manifest-path
editors/zed/Cargo.toml` tests the extension's task locator. Zed compiles
`extension.wasm` only on "install dev extension", so `make dev-zed` says to
run it again when `src/`, `Cargo.toml` or `Cargo.lock` is newer than
`extension.wasm`.

For people who only use Nomi, `nomi editor install zed` writes the extension
as Zed unpacks one from its registry, into Zed's
`extensions/installed/nomi`: `extension.toml`, `languages/`,
`debug_adapter_schemas/`, the grammar's committed `tree-sitter-nomi.wasm` as
`grammars/nomi.wasm`, and `extension.wasm`, which a release `nomi` carries
and `make install-zed` builds with cargo. It leaves a dev extension's link
alone unless given `--force`. The layout is Zed's internal format, not a
documented interface, and whether Zed's extension update check ever
removes an extension its registry does not list has not been checked. The
user settings an extension cannot set are in `docs/tooling.md`, "Setting up
an editor".

## Helix

`make dev-helix` runs `nomi editor install helix` from this checkout, then
links Helix's Nomi queries to this checkout, so query changes need no
reinstall. Run it again after a grammar change. See `helix/README.md`.

## Neovim

Load `editors/nvim` from this checkout (lazy.nvim `dir =`) and run
`make dev-nvim` to compile `editors/nvim/parser/nomi.so`. A snapshot
install, `nomi editor install nvim`, goes in Neovim's package directory
instead; remove it with `nomi editor uninstall nvim` while loading the
checkout. `make test-nvim` runs the plugin's headless checks. See
`nvim/README.md`, "Local development".

## VS Code

`make install-vscode` builds `editors/vscode/nomi.vsix` and installs it with
`nomi editor install vscode`, which runs `code --install-extension`; never
publish it to a marketplace. A release `nomi` carries the same `.vsix`.
`make test-vscode` runs its unit and grammar tests, and
`npm --prefix editors/vscode run test:smoke` runs the extension in a
downloaded VS Code. See `vscode/README.md`, "Working on the extension".

## bat

See `bat/README.md`, "Install for `bat`".
