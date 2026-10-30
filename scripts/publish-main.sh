#!/usr/bin/env bash
# publish-main.sh: publish next to main as one squash commit whose message
# is built from the release notes.
#
# Run it on next, on a clean tree, after `make check-full` passes.
#
# The notes file is next-notes.txt in the clone's common git directory
# (`git rev-parse --git-common-dir`; NOTES=<file> names another). It starts
# with the four header lines Added, Changed, Fixed and Removed, and then
# holds one line per change, gathered as branches merge into next:
#
#   Title: <the squash commit's subject line> (exactly one)
#   Highlight: <one of the batch's most important changes> (one to five)
#   Added: <a change>     Changed: <...>     Fixed: <...>     Removed: <...>
#
# Each change line is a commit's User-facing paragraph, in plain words.
# Blank lines are ignored; any other line is refused.
#
# The script:
#   1. Refuses when the notes have no Title line or more than one, no change
#      line, or no Highlight line or more than five, when the current branch is not next, when the tree is
#      dirty, or when next does not hold main's history. That last check:
#      origin/main is a squash commit, so it is no ancestor of next, and
#      which next commit it was built from is not recorded. What holds is
#      that every publish squashes a commit of next, so origin/main's tree
#      must equal the tree of some commit on next's first-parent history.
#      When none does, main moved outside this script or next was rewritten.
#      It also refuses when next's tree equals origin/main's: nothing to
#      publish.
#   2. When tree-sitter-nomi/ differs between origin/main and next, runs
#      scripts/publish-grammar.sh --remote "$REMOTE" and commits the two pin
#      files on next.
#   3. Builds the message (the Title, then Highlights, Added, Changed, Fixed
#      and Removed, each a `- ` list in file order, empty sections left out),
#      makes `git commit-tree next^{tree} -p origin/main`, shows the message
#      and hash, and pushes the commit to origin's main. scripts/git-hooks/
#      pre-push refuses any other push to main.
#   4. Renames the notes file to next-notes-<YYYY-MM-DD>-pushed.txt (with b,
#      c, ... after the date when that name is taken) and starts a fresh one
#      holding the four header lines.
#
# Usage:
#   scripts/publish-main.sh [--dry-run]
#
#   --dry-run   check everything and print the message; publish no grammar,
#               commit nothing, push nothing, rename nothing. A refusal is
#               reported and the message still printed when the notes allow.
#
#   NOTES=<file>    the notes file (default <git common dir>/next-notes.txt)
#   REMOTE=<url>    the grammar mirror publish-grammar.sh pushes to
#                   (default git@github.com:nomi-language/tree-sitter-nomi.git)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE="${REMOTE:-git@github.com:nomi-language/tree-sitter-nomi.git}"
ZED_TOML="editors/zed/extension.toml"
HELIX_TOML="editors/helix/languages.toml"

DRY_RUN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    *) echo "usage: $0 [--dry-run]" >&2; exit 2 ;;
  esac
done

die() { echo "publish-main: $*" >&2; exit 1; }
g() { git -C "$ROOT" "$@"; }

REFUSED=false
refuse() {
  if $DRY_RUN; then
    echo "publish-main: would refuse: $*" >&2
    REFUSED=true
  else
    die "$*"
  fi
}

COMMON="$(g rev-parse --path-format=absolute --git-common-dir)"
NOTES="${NOTES:-$COMMON/next-notes.txt}"
[[ -f "$NOTES" ]] || die "no notes file at $NOTES"

# --- 1. The notes.
title=""
highlights=()
added=()
changed=()
fixed=()
removed=()
n=0
while IFS= read -r line || [[ -n "$line" ]]; do
  n=$((n + 1))
  case "$line" in
    "" | Added | Changed | Fixed | Removed) ;;
    "Title: "*)
      [[ -z "$title" ]] || die "$NOTES:$n: a second Title line; the one title becomes the commit subject"
      title="${line#Title: }" ;;
    "Highlight: "*) highlights+=("${line#Highlight: }") ;;
    "Added: "*) added+=("${line#Added: }") ;;
    "Changed: "*) changed+=("${line#Changed: }") ;;
    "Fixed: "*) fixed+=("${line#Fixed: }") ;;
    "Removed: "*) removed+=("${line#Removed: }") ;;
    *) die "$NOTES:$n: not a header, Title:, Highlight:, Added:, Changed:, Fixed: or Removed: line: $line" ;;
  esac
done < "$NOTES"

[[ -n "$title" ]] || die "$NOTES has no Title: line; add one, which becomes the commit subject"
changes=$(( ${#added[@]} + ${#changed[@]} + ${#fixed[@]} + ${#removed[@]} ))
[[ $changes -gt 0 ]] || die "$NOTES has no Added:, Changed:, Fixed: or Removed: line"
[[ ${#highlights[@]} -gt 0 ]] || die "$NOTES has no Highlight: line; add one to five"
[[ ${#highlights[@]} -le 5 ]] || die "$NOTES has ${#highlights[@]} Highlight: lines; at most five"

# --- 1. The checkout.
branch="$(g symbolic-ref --short -q HEAD || echo '(detached HEAD)')"
[[ "$branch" == next ]] || refuse "the current branch is $branch, not next"
if [[ -n "$(g status --porcelain)" ]]; then
  g status --short >&2
  refuse "the working tree is dirty"
fi

g fetch --quiet origin main || die "could not fetch origin main"
main_tree="$(g rev-parse 'origin/main^{tree}')"
next_trees="$(g log --first-parent --format=%T next)"
grep -qxF "$main_tree" <<<"$next_trees" \
  || refuse "no commit on next's first-parent history has origin/main's tree ($main_tree): main moved outside publish-main, or next was rewritten"
[[ "$(g rev-parse 'next^{tree}')" != "$main_tree" ]] \
  || refuse "nothing to publish: next's tree equals origin/main's"

# --- 2. The grammar.
if ! g diff --quiet origin/main next -- tree-sitter-nomi/; then
  if $DRY_RUN; then
    echo ">> tree-sitter-nomi/ differs from origin/main: would run scripts/publish-grammar.sh --remote $REMOTE and commit the pins"
  elif ! $REFUSED; then
    "$ROOT/scripts/publish-grammar.sh" --remote "$REMOTE"
    if ! g diff --quiet -- "$ZED_TOML" "$HELIX_TOML"; then
      sha="$(sed -nE 's/^[[:space:]]*rev[[:space:]]*=[[:space:]]*"([0-9a-f]+)".*/\1/p' "$ROOT/$ZED_TOML" | head -n 1)"
      [[ -n "$sha" ]] || die "no rev in $ZED_TOML after publishing the grammar"
      g commit --quiet -m "editors: pin the grammar to tree-sitter-nomi ${sha:0:12}" -- "$ZED_TOML" "$HELIX_TOML"
      echo ">> committed the grammar pins on next: $(g log -1 --format='%h %s')"
    fi
  fi
fi

# --- 3. The message.
MSG="$(mktemp "${TMPDIR:-/tmp}/nomi-publish-main.XXXXXX")"
trap 'rm -f "$MSG"' EXIT
section() { # heading items...
  local heading="$1"; shift
  [[ $# -gt 0 ]] || return 0
  printf '\n%s\n' "$heading"
  printf -- '- %s\n' "$@"
}
{
  printf '%s\n\n' "$title"
  printf 'Highlights\n'
  printf -- '- %s\n' "${highlights[@]}"
  section Added ${added[@]+"${added[@]}"}
  section Changed ${changed[@]+"${changed[@]}"}
  section Fixed ${fixed[@]+"${fixed[@]}"}
  section Removed ${removed[@]+"${removed[@]}"}
} > "$MSG"

echo ">> Message:"
sed 's/^/   /' "$MSG"

tree="$(g rev-parse 'next^{tree}')"
if $DRY_RUN; then
  echo ">> Dry run: would commit tree $tree on origin/main ($(g rev-parse --short origin/main)) and push it to origin main"
  $REFUSED && exit 1
  exit 0
fi

hash="$(g commit-tree "$tree" -p origin/main -F "$MSG")"
[[ "$hash" =~ ^[0-9a-f]{40}$ ]] || die "git commit-tree gave '$hash'"
echo ">> Commit: $hash"
NOMI_PUBLISH_MAIN=1 g push origin "$hash:refs/heads/main" \
  || die "push refused; origin main moved while publishing. Run it again; this script never forces"

# --- 4. The notes.
dir="$(dirname "$NOTES")"
base="$dir/next-notes-$(date +%Y-%m-%d)"
target="$base-pushed.txt"
for s in b c d e f g h i j k l m n o p q r s t u v w x y z; do
  [[ -e "$target" ]] || break
  target="$base$s-pushed.txt"
done
[[ -e "$target" ]] && die "pushed $hash, but every name up to $target is taken; move $NOTES aside by hand"
mv "$NOTES" "$target"
printf 'Added\nChanged\nFixed\nRemoved\n' > "$NOTES"
echo ">> Published $hash to main. Notes moved to $target; $NOTES starts afresh."
