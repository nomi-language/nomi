#!/usr/bin/env bash
# publish-grammar.sh: publish tree-sitter-nomi/ to its read-only repository
# and pin the editors to the published commit.
#
# The grammar is developed here, in tree-sitter-nomi/. Editors fetch it from
# https://github.com/nomi-language/tree-sitter-nomi at a pinned rev, because
# Zed and Helix build a grammar from a repository whose root is the grammar.
# That repository is written only by this script:
#
#   1. `git subtree split --prefix=tree-sitter-nomi HEAD` gives the commit
#      whose tree is tree-sitter-nomi/. The split is deterministic: the same
#      history gives the same commit, so a publish with no grammar change
#      since the last one is a no-op.
#   2. Push that commit to the grammar repository's main branch. The push is
#      never forced; if the remote has diverged, the script stops.
#   3. Write the commit into editors/zed/extension.toml ([grammars.nomi] rev)
#      and editors/helix/languages.toml (the [[grammar]] source rev).
#
# It refuses to run on a dirty tree, since the split reads committed history
# only. After it runs, commit the two pinned files; running it again then
# does nothing.
#
# Usage:
#   scripts/publish-grammar.sh [--dry-run] [--remote <url>]
#
#   --dry-run       split and report; push nothing and write nothing
#   --remote <url>  push to <url> instead of the default below (for example
#                   git@github.com:nomi-language/tree-sitter-nomi.git)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
URL="https://github.com/nomi-language/tree-sitter-nomi"
ZED_TOML="$REPO_ROOT/editors/zed/extension.toml"
HELIX_TOML="$REPO_ROOT/editors/helix/languages.toml"

DRY_RUN=false
PUSH_TO="$URL"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    --remote) PUSH_TO="${2:?--remote needs a URL}"; shift 2 ;;
    *) echo "usage: $0 [--dry-run] [--remote <url>]" >&2; exit 2 ;;
  esac
done

die() { echo "publish-grammar: $*" >&2; exit 1; }

if [[ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]]; then
  git -C "$REPO_ROOT" status --short >&2
  die "the working tree is dirty; commit or stash first"
fi

grep -qF "repository = \"$URL\"" "$ZED_TOML" || die "$ZED_TOML does not name $URL"
grep -qF "git = \"$URL\"" "$HELIX_TOML" || die "$HELIX_TOML does not name $URL"

echo ">> Splitting tree-sitter-nomi/ at $(git -C "$REPO_ROOT" rev-parse --short HEAD)"
SHA="$(git -C "$REPO_ROOT" subtree split --prefix=tree-sitter-nomi HEAD 2>/dev/null)"
[[ "$SHA" =~ ^[0-9a-f]{40}$ ]] || die "git subtree split gave '$SHA'"
echo "   split commit: $SHA"

if $DRY_RUN; then
  echo ">> Dry run: would push $SHA to $PUSH_TO main and pin both editors to it"
  exit 0
fi

REMOTE_MAIN="$(git -C "$REPO_ROOT" ls-remote "$PUSH_TO" refs/heads/main | cut -f1)"
if [[ "$REMOTE_MAIN" == "$SHA" ]]; then
  echo ">> $PUSH_TO main is already $SHA; nothing to push"
else
  echo ">> Pushing to $PUSH_TO main (was ${REMOTE_MAIN:-empty})"
  git -C "$REPO_ROOT" push "$PUSH_TO" "$SHA:refs/heads/main" \
    || die "push refused; the remote main is not an ancestor of $SHA. Inspect it; this script never forces"
fi

changed=()
pin() { # file sed-expression
  local tmp
  tmp="$(mktemp)"
  sed -E "$2" "$1" > "$tmp"
  if cmp -s "$1" "$tmp"; then
    rm -f "$tmp"
  else
    mv "$tmp" "$1"
    changed+=("${1#"$REPO_ROOT"/}")
  fi
}
pin "$ZED_TOML" "s|^([[:space:]]*rev[[:space:]]*=[[:space:]]*)\"[^\"]*\"|\1\"$SHA\"|"
pin "$HELIX_TOML" "s|^(source = \{ git = \"[^\"]*\", rev = )\"[^\"]*\"|\1\"$SHA\"|"

if [[ ${#changed[@]} -eq 0 ]]; then
  echo ">> Both editors already pin $SHA"
else
  echo ">> Pinned $SHA in:"
  printf '     %s\n' "${changed[@]}"
  echo "   Commit them, e.g.:"
  echo "     git commit -m 'editors: pin the grammar to tree-sitter-nomi ${SHA:0:12}' -- ${changed[*]}"
fi
