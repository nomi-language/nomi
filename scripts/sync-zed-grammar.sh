#!/usr/bin/env bash
# sync-zed-grammar.sh: prepare editors/zed/ for a Zed dev-extension install
# from this checkout.
#
# editors/zed/extension.toml names the published grammar repository and a rev:
#
#   [grammars.nomi]
#   repository = "https://github.com/nomi-language/tree-sitter-nomi"
#   rev = "<commit of that repository>"
#
# To install a dev extension, Zed uses editors/zed/grammars/nomi/ as the
# grammar checkout. If the directory exists, Zed requires its `origin` remote
# to equal `repository`, runs `git fetch origin <rev>` (a failure is ignored)
# and then `git checkout <rev>`, so the commit only has to exist locally.
#
# This script makes editors/zed/grammars/nomi/ (gitignored) a git repository
# whose origin is the published URL and whose HEAD holds this checkout's
# tree-sitter-nomi/, tracked and untracked files alike. Then:
#
#   - If extension.toml's rev can be found (already in the directory, in this
#     repository's objects, recreated by splitting HEAD, or fetched from
#     origin) and its files equal the local grammar, extension.toml is left
#     alone.
#   - Otherwise (the grammar changed locally, or was never published) rev is
#     set to the local commit, and the script says so. Do not commit that
#     line: `scripts/sync-zed-grammar.sh --restore` puts the committed rev
#     back, and scripts/publish-grammar.sh overwrites it when it publishes.
#
# Running it again with nothing changed does nothing.
#
# Usage:
#   scripts/sync-zed-grammar.sh            seed or update, then report
#   scripts/sync-zed-grammar.sh --restore  restore extension.toml's committed rev

set -euo pipefail

# editors/zed/grammars/nomi is its own repository. An inherited GIT_DIR (a
# git hook exports one) would make every `git -C "$SUBREPO"` below act on
# the caller's repository instead: rewrite its origin, commit the grammar
# there and check that commit out.
unset $(git rev-parse --local-env-vars)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TS_DIR="$REPO_ROOT/tree-sitter-nomi"
ZED_DIR="$REPO_ROOT/editors/zed"
EXT_TOML="$ZED_DIR/extension.toml"
SUBREPO="$ZED_DIR/grammars/nomi"

toml_string() { # key file: the first `key = "value"` in file
  grep -E "^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"" "$2" | head -1 | sed -E 's/.*"([^"]*)".*/\1/'
}

set_rev() { # new-rev
  local tmp
  tmp="$(mktemp)"
  sed -E "s|^([[:space:]]*rev[[:space:]]*=[[:space:]]*)\"[^\"]*\"|\1\"$1\"|" "$EXT_TOML" > "$tmp"
  mv "$tmp" "$EXT_TOML"
}

[[ -f "$EXT_TOML" ]] || { echo "sync-zed-grammar: $EXT_TOML is missing" >&2; exit 1; }

if [[ "${1:-}" == "--restore" ]]; then
  committed="$(git -C "$REPO_ROOT" show HEAD:editors/zed/extension.toml | grep -E '^[[:space:]]*rev[[:space:]]*=' | head -1 | sed -E 's/.*"([^"]*)".*/\1/')"
  set_rev "$committed"
  echo "extension.toml rev restored to $committed"
  exit 0
elif [[ -n "${1:-}" ]]; then
  echo "usage: $0 [--restore]" >&2
  exit 2
fi

[[ -f "$TS_DIR/grammar.js" && -f "$TS_DIR/src/parser.c" ]] \
  || { echo "sync-zed-grammar: $TS_DIR has no grammar.js or src/parser.c" >&2; exit 1; }

URL="$(toml_string repository "$EXT_TOML")"
PIN="$(toml_string rev "$EXT_TOML")"
[[ -n "$URL" ]] || { echo "sync-zed-grammar: no grammar repository in $EXT_TOML" >&2; exit 1; }

# The directory must be a git repository whose origin is the published URL.
if [[ ! -d "$SUBREPO/.git" ]]; then
  echo ">> Creating $SUBREPO"
  rm -rf "$SUBREPO"
  mkdir -p "$SUBREPO"
  git -C "$SUBREPO" init --quiet -b dev
fi
if [[ "$(git -C "$SUBREPO" remote get-url origin 2>/dev/null || true)" != "$URL" ]]; then
  git -C "$SUBREPO" remote remove origin 2>/dev/null || true
  git -C "$SUBREPO" remote add origin "$URL"
  echo ">> Set origin to $URL"
fi

# Local commits go on branch `dev`, so a rev Zed checked out (which detaches
# HEAD) is never the only thing keeping one reachable.
git -C "$SUBREPO" symbolic-ref HEAD refs/heads/dev

# Replace its files with the local grammar: every file under tree-sitter-nomi/
# that git tracks or would track, so the tree matches what a publish splits out.
find "$SUBREPO" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
git -C "$REPO_ROOT" ls-files -co --exclude-standard -z -- tree-sitter-nomi \
  | while IFS= read -r -d '' path; do
      [[ -f "$REPO_ROOT/$path" ]] || continue # tracked but deleted
      rel="${path#tree-sitter-nomi/}"
      mkdir -p "$SUBREPO/$(dirname "$rel")"
      cp -p "$REPO_ROOT/$path" "$SUBREPO/$rel"
    done
git -C "$SUBREPO" add -A -f .
if ! git -C "$SUBREPO" rev-parse --verify --quiet HEAD >/dev/null \
  || ! git -C "$SUBREPO" diff --cached --quiet; then
  git -C "$SUBREPO" -c user.name=nomi -c user.email=nomi@localhost \
    commit --quiet -m "Local grammar from tree-sitter-nomi/"
  echo ">> Committed the local grammar in $SUBREPO"
fi
LOCAL="$(git -C "$SUBREPO" rev-parse HEAD)"
LOCAL_TREE="$(git -C "$SUBREPO" rev-parse 'HEAD^{tree}')"

# Can Zed use the committed rev as it stands?
pin_matches() {
  [[ "$(git -C "$SUBREPO" rev-parse --verify --quiet "$PIN^{tree}" 2>/dev/null)" == "$LOCAL_TREE" ]]
}
if [[ "$PIN" =~ ^[0-9a-f]{40}$ ]]; then
  if ! git -C "$SUBREPO" cat-file -e "$PIN^{commit}" 2>/dev/null; then
    # The split is deterministic, so when the committed grammar is the
    # published one, splitting HEAD recreates the pinned commit without the
    # network. A publish from this repository also leaves it in its objects.
    if ! git -C "$REPO_ROOT" cat-file -e "$PIN^{commit}" 2>/dev/null; then
      git -C "$REPO_ROOT" subtree split --prefix=tree-sitter-nomi HEAD >/dev/null 2>&1 || true
    fi
    if git -C "$REPO_ROOT" cat-file -e "$PIN^{commit}" 2>/dev/null; then
      # Forced: `published` only keeps the pinned commit reachable, and a
      # rewritten mirror history is not a fast-forward of the last one.
      git -C "$REPO_ROOT" push --quiet --force "$SUBREPO" "$PIN:refs/heads/published" 2>/dev/null || true
    else
      GIT_TERMINAL_PROMPT=0 git -C "$SUBREPO" fetch --quiet --depth 1 origin "$PIN" 2>/dev/null || true
    fi
  fi
fi

if pin_matches; then
  REV="$PIN"
  echo ">> extension.toml's rev $PIN matches the local grammar; extension.toml unchanged"
else
  REV="$LOCAL"
  if [[ "$PIN" != "$LOCAL" ]]; then
    set_rev "$LOCAL"
    echo ">> extension.toml rev set to the local commit ${LOCAL:0:12} (was ${PIN:0:12})."
    echo "   Do not commit it; run scripts/sync-zed-grammar.sh --restore when done."
  fi
fi

echo
echo "Ready. In Zed, run \"zed: install dev extension\" and select:"
echo "  $ZED_DIR"
echo "  grammar checkout: $SUBREPO"
echo "  rev:              $REV"
echo "  origin:           $URL"
