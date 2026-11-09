# Editor support

Each directory here is one editor's support for Nomi: `zed/`, `helix/`,
`nvim/`, `vscode/` and `bat/`. Each has its own README for users, except Zed,
whose setup is below. This file is the setup for running each editor against
this checkout while working on Nomi. The checklist after a grammar change,
publishing the grammar and the git hooks are in the repository's `AGENTS.md`
("Grammar and editor extensions"); the rules behind the grammar's checks are
in `tree-sitter-nomi/README.md`.

## All editors

`make dev-editors` (`scripts/dev-editors.sh`) installs `nomi` and `nomi-lsp`
from this checkout, runs the Neovim and Helix dev setups, syncs Zed's grammar
checkout, says when Zed's dev extension must be reinstalled, and rebuilds and
installs the VS Code extension when `code` is on PATH. An editor that is not
installed is skipped; `make dev-nvim`, `make dev-helix` and `make dev-zed` run
one.

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

Then run "zed: install dev extension" and select the `editors/zed/` directory
itself. Install the language server with `go install ./cmd/nomi-lsp`; Zed
launches `nomi-lsp` from PATH. `cargo test --manifest-path
editors/zed/Cargo.toml` tests the extension's task locator.

## Helix

`make dev-helix` links Helix's Nomi queries to this checkout and builds the
grammar from this checkout's `tree-sitter-nomi/`, so local grammar changes need
no publish. `make install-helix` copies a snapshot instead, for people who
only use Nomi. See `helix/README.md`.

## Neovim

Load `editors/nvim` from this checkout (lazy.nvim `dir =`) and run
`make dev-nvim` to compile `editors/nvim/parser/nomi.so`. `make install-nvim`
copies a snapshot into Neovim's package directory, for people who only use
Nomi. `make test-nvim` runs the plugin's headless checks. See `nvim/README.md`,
"Local development".

## VS Code

`make install-vscode` builds `editors/vscode/nomi.vsix` and installs it with
`code --install-extension`; never publish it to a marketplace.
`make test-vscode` runs its unit and grammar tests, and
`npm --prefix editors/vscode run test:smoke` runs the extension in a
downloaded VS Code. See `vscode/README.md`, "Working on the extension".

## bat

See `bat/README.md`, "Install for `bat`".
