#!/usr/bin/env bash
# dev-editors.sh: make this machine's editors follow this checkout.
#
#   scripts/dev-editors.sh            everything below
#   scripts/dev-editors.sh cli        go install nomi from this checkout
#   scripts/dev-editors.sh nvim       regenerate the Neovim queries and compile
#                                     editors/nvim/parser/nomi.so, which a plugin
#                                     loaded from this checkout (lazy.nvim `dir =`) uses
#   scripts/dev-editors.sh helix      link Helix's Nomi queries to this checkout and
#                                     build the grammar from it
#   scripts/dev-editors.sh zed        sync editors/zed/grammars/nomi and check that
#                                     Zed's dev extension points at editors/zed
#   scripts/dev-editors.sh vscode     build editors/vscode/nomi.vsix and install it
#                                     with `code` (make install-vscode)
#   scripts/dev-editors.sh zed-link   not one of "everything": link Zed's extension
#                                     back to editors/zed where `nomi editor install
#                                     zed` wrote a copy over the dev extension
#                                     (make use-source)
#
# Several targets may be named at once. An editor that is not installed is
# skipped. This is the setup for working on Nomi; `nomi editor install`
# (`make install-nvim`, `make install-helix` and the others from a checkout)
# instead copies a snapshot for people who only use it.
# scripts/git-hooks/post-merge runs this after a merge into main, or into a
# branch listed in git config nomi.editorBranches.

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

# On a Mac with Xcode's command line tools, link with the system linker,
# which writes chained fixups: nomi reaches main about 1 ms sooner. The
# Makefile's build-cli makes the same choice and says why.
do_cli() {
  local env=() flags=()
  if [[ "$(uname -s)" == Darwin ]] && xcode-select -p >/dev/null 2>&1; then
    env=(CGO_ENABLED=1)
    flags=(-ldflags=-linkmode=external)
  fi
  run "go install" env ${env[@]+"${env[@]}"} go install -C "$ROOT" ${flags[@]+"${flags[@]}"} ./cmd/nomi &&
    echo "dev-editors: installed nomi from $ROOT"
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
    echo "dev-editors: $snapshot holds a snapshot install (nomi editor install nvim)." >&2
    echo "  Neovim loads its parser beside this checkout's and it goes stale; run \`nomi editor uninstall nvim\`." >&2
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

# zed_extension_stale reports whether the dev extension's WebAssembly is
# missing or older than the Rust it is built from, which Zed compiles only
# on "install dev extension": a stale one keeps running the old code.
zed_extension_stale() {
  local zed="$ROOT/editors/zed"
  [[ ! -f "$zed/extension.wasm" ]] ||
    [[ -n "$(find "$zed/src" "$zed/Cargo.toml" "$zed/Cargo.lock" -newer "$zed/extension.wasm" -print -quit)" ]]
}

# do_zed_link puts Zed's dev extension back where `nomi editor install zed`
# wrote a copy over it, as `make use-release` does: a directory that holds
# that command's marker gives way to a link to this checkout's editors/zed.
# Zed takes a link in extensions/installed for a dev extension and loads the
# builds in it, extension.wasm and grammars/nomi.wasm, which only its
# "install dev extension" makes. Without both in the checkout this changes
# nothing and names that step. A dev extension, no extension, and one nomi
# did not write are left to do_zed.
do_zed_link() {
  local link zed="$ROOT/editors/zed"
  link="$(zed_installed_link)"
  if [[ -z "$link" || -L "$link" || ! -f "$link/nomi-version" ]]; then
    return
  fi
  if [[ ! -f "$zed/extension.wasm" || ! -f "$zed/grammars/nomi.wasm" ]]; then
    echo "dev-editors: Zed has the extension \`nomi editor install zed\` wrote, and this checkout holds no build of the dev extension."
    echo "  In Zed, run \"zed: install dev extension\" and select $zed"
    return
  fi
  if ! { rm -rf "$link" && ln -s "$zed" "$link"; }; then
    echo "dev-editors: could not link $link to $zed" >&2
    status=1
    return
  fi
  echo "dev-editors: linked Zed's Nomi extension to $zed, in place of the one \`nomi editor install zed\` wrote. Restart Zed."
  echo "  If Zed then lists no Nomi dev extension, run \"zed: install dev extension\" and select $zed"
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
  elif zed_extension_stale; then
    echo "dev-editors: the extension's Rust changed since editors/zed/extension.wasm was built."
    echo "  In Zed, run \"zed: install dev extension\" and select $ROOT/editors/zed again to rebuild it."
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
    zed-link) do_zed_link ;;
    vscode) do_vscode ;;
    *)
      echo "usage: scripts/dev-editors.sh [cli] [nvim] [helix] [zed] [zed-link] [vscode]" >&2
      exit 2
      ;;
  esac
done
exit "$status"
