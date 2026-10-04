#!/usr/bin/env bash
# dev-editors.sh: make this machine's editors follow this checkout.
#
#   scripts/dev-editors.sh            everything below
#   scripts/dev-editors.sh cli        go install nomi and nomi-lsp from this checkout
#   scripts/dev-editors.sh nvim       regenerate the Neovim queries and compile
#                                     editors/nvim/parser/nomi.so, which a plugin
#                                     loaded from this checkout (lazy.nvim `dir =`) uses
#   scripts/dev-editors.sh helix      link Helix's Nomi queries to this checkout and
#                                     build the grammar from it
#   scripts/dev-editors.sh zed        sync editors/zed/grammars/nomi and check that
#                                     Zed's dev extension points at editors/zed
#   scripts/dev-editors.sh vscode     build editors/vscode/nomi.vsix and install it
#                                     with `code` (make install-vscode)
#
# Several targets may be named at once. An editor that is not installed is
# skipped. This is the setup for working on Nomi; `make install-nvim` and
# `make install-helix` instead copy a snapshot for people who only use it.
# scripts/git-hooks/post-merge runs this after a merge into main.

set -uo pipefail

# Never inherit a caller's repository: a git hook exports GIT_DIR, and every
# `git -C <dir>` below (the Zed grammar checkout is its own repository) would
# act on the caller's repository instead.
unset $(git rev-parse --local-env-vars)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
status=0

run() { # label command...
  if ! "${@:2}"; then
    echo "dev-editors: $1 failed" >&2
    status=1
    return 1
  fi
}

do_cli() {
  run "go install" go install -C "$ROOT" ./cmd/nomi ./cmd/nomi-lsp &&
    echo "dev-editors: installed nomi and nomi-lsp from $ROOT"
}

do_nvim() {
  if ! command -v nvim >/dev/null; then
    echo "dev-editors: nvim not found, skipping Neovim"
    return
  fi
  run "Neovim parser" "$ROOT/scripts/sync-nvim-runtime.sh" --parser "$ROOT/editors/nvim/parser"
  run "Neovim queries" "$ROOT/scripts/sync-nvim-runtime.sh"
  local snapshot="${XDG_DATA_HOME:-$HOME/.local/share}/nvim/site/pack/nomi"
  if [[ -e "$snapshot" ]]; then
    echo "dev-editors: $snapshot holds a snapshot install (make install-nvim)." >&2
    echo "  Neovim loads its parser beside this checkout's and it goes stale; remove it." >&2
  fi
}

do_helix() {
  if ! command -v hx >/dev/null; then
    echo "dev-editors: hx not found, skipping Helix"
    return
  fi
  run "Helix" "$ROOT/scripts/sync-helix-runtime.sh" --dev
}

zed_installed_link() {
  local dir
  for dir in "$HOME/Library/Application Support/Zed" "${XDG_DATA_HOME:-$HOME/.local/share}/zed"; do
    if [[ -d "$dir/extensions" ]]; then
      echo "$dir/extensions/installed/nomi"
      return
    fi
  done
}

do_vscode() {
  if ! command -v code >/dev/null; then
    echo "dev-editors: code not found, skipping VS Code"
    return
  fi
  if ! command -v npm >/dev/null; then
    echo "dev-editors: npm not found, skipping VS Code" >&2
    status=1
    return
  fi
  run "VS Code extension" make -C "$ROOT" install-vscode >/dev/null &&
    echo "dev-editors: installed the VS Code extension; reload any open VS Code window"
}

do_zed() {
  local link
  link="$(zed_installed_link)"
  if [[ -z "$link" ]]; then
    echo "dev-editors: Zed not found, skipping Zed"
    return
  fi
  local grammar="$ROOT/editors/zed/grammars/nomi" before after
  before="$(git -C "$grammar" rev-parse -q --verify HEAD 2>/dev/null || true)"
  run "Zed grammar" "$ROOT/scripts/sync-zed-grammar.sh" >/dev/null
  after="$(git -C "$grammar" rev-parse -q --verify HEAD 2>/dev/null || true)"

  local target=""
  if [[ -L "$link" ]]; then
    target="$(readlink "$link")"
  fi
  if [[ "$target" != "$ROOT/editors/zed" ]]; then
    echo "dev-editors: Zed's Nomi extension is ${target:-not a dev install of this checkout}."
    echo "  In Zed, run \"zed: install dev extension\" and select $ROOT/editors/zed"
  elif [[ "$before" != "$after" ]]; then
    # Zed's extension builder skips recompiling grammars/nomi.wasm when it
    # judges the existing build up to date, even after the checkout moved, and
    # then loads the new queries against the old grammar ("Invalid node type").
    # Removing the build forces the next install to compile the parser.
    rm -f "$ROOT/editors/zed/grammars/nomi.wasm"
    echo "dev-editors: the grammar changed. In Zed, run \"zed: install dev extension\""
    echo "  and select $ROOT/editors/zed again to rebuild it."
  else
    echo "dev-editors: Zed's dev extension follows this checkout"
  fi
}

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(cli nvim helix zed vscode)
fi
for t in "${targets[@]}"; do
  case "$t" in
    cli) do_cli ;;
    nvim) do_nvim ;;
    helix) do_helix ;;
    zed) do_zed ;;
    vscode) do_vscode ;;
    *)
      echo "usage: scripts/dev-editors.sh [cli] [nvim] [helix] [zed] [vscode]" >&2
      exit 2
      ;;
  esac
done
exit "$status"
