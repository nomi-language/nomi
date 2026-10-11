#!/usr/bin/env bash
# publish-grammar.sh: publish tree-sitter-nomi/ to its read-only repository
# and pin the editors to the published commit.
#
# The grammar is developed here, in tree-sitter-nomi/. Editors fetch it from
# https://github.com/nomi-language/tree-sitter-nomi at a pinned rev, because
# Zed and Helix build a grammar from a repository whose root is the grammar.
# That repository is written only by this script:
#
#   1. Fetch the grammar repository's main. If its tree already equals
#      HEAD's tree-sitter-nomi/, there is nothing to publish.
#   2. Otherwise make one commit whose tree is HEAD's tree-sitter-nomi/ and
#      whose parent is the fetched main, and push it as main. The push is a
#      fast-forward by construction and is never forced. Building on the
#      repository's own tip, rather than a `git subtree split` of this
#      repository's history, keeps publishing working after next is squashed
#      into main and the split history changes.
#   3. Write the published commit into editors/zed/extension.toml
#      ([grammars.nomi] rev) and editors/helix/languages.toml (the
#      [[grammar]] source rev).
#
# It refuses to run on a dirty tree, since it publishes committed files
# only. After it runs, commit the two pinned files; running it again then
# does nothing.
#
# Usage:
#   scripts/publish-grammar.sh [--dry-run] [--remote <url>]
#
#   --dry-run       report what would be published; push nothing and write nothing
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

HEAD_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
TREE="$(git -C "$REPO_ROOT" rev-parse "HEAD:tree-sitter-nomi")"
echo ">> Publishing tree-sitter-nomi/ at $HEAD_SHORT (tree $TREE)"

# The fetch writes FETCH_HEAD only; no local ref changes.
REMOTE_MAIN="$(git -C "$REPO_ROOT" ls-remote "$PUSH_TO" refs/heads/main | cut -f1)"
if [[ -n "$REMOTE_MAIN" ]]; then
  git -C "$REPO_ROOT" fetch --quiet "$PUSH_TO" main || die "could not fetch $PUSH_TO main"
  REMOTE_TREE="$(git -C "$REPO_ROOT" rev-parse "$REMOTE_MAIN^{tree}")"
else
  REMOTE_TREE=""
fi

if [[ "$REMOTE_TREE" == "$TREE" ]]; then
  SHA="$REMOTE_MAIN"
  echo ">> $PUSH_TO main ($SHA) already has this grammar; nothing to push"
else
  PARENT=()
  [[ -n "$REMOTE_MAIN" ]] && PARENT=(-p "$REMOTE_MAIN")
  SHA="$(git -C "$REPO_ROOT" commit-tree "$TREE" ${PARENT[@]+"${PARENT[@]}"} \
    -m "Publish tree-sitter-nomi from nomi $HEAD_SHORT")"
  [[ "$SHA" =~ ^[0-9a-f]{40}$ ]] || die "git commit-tree gave '$SHA'"
  echo "   commit: $SHA (parent ${REMOTE_MAIN:-none})"
  if $DRY_RUN; then
    echo ">> Dry run: would push $SHA to $PUSH_TO main and pin both editors to it"
    exit 0
  fi
  echo ">> Pushing to $PUSH_TO main (was ${REMOTE_MAIN:-empty})"
  git -C "$REPO_ROOT" push "$PUSH_TO" "$SHA:refs/heads/main" \
    || die "push refused; $PUSH_TO main moved while publishing. Run it again; this script never forces"
fi

if $DRY_RUN; then
  echo ">> Dry run: both editors would pin $SHA"
  exit 0
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
