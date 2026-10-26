#!/usr/bin/env bash
# Point each lib/ module's requires of its siblings at a published commit.
#
#   scripts/pin-lib.sh [--verify] <commit>
#
# A lib/ module that requires a sibling replaces it with the sibling's
# directory, so builds in this tree read the tree; users read the require.
# After a commit containing lib/ is pushed to main, run this with that
# commit: it writes the commit's pseudo-version
# (v0.0.0-<commit time, UTC>-<12-char sha>) into every lib/ go.mod require
# of a github.com/nomi-language/nomi/lib/... module. Commit the go.mod
# files it changes afterwards.
#
# --verify then adds lib/sql/sqlkit from the module proxy into a fresh
# project in a temporary directory with the nomi built from this checkout
# (needs the network, and the commit on the proxy).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
verify=0
if [[ "${1:-}" == "--verify" ]]; then
  verify=1
  shift
fi
if [[ $# -ne 1 ]]; then
  echo "usage: scripts/pin-lib.sh [--verify] <commit>" >&2
  exit 2
fi

sha="$(git -C "$root" rev-parse --verify "$1^{commit}")"
if ! git -C "$root" cat-file -e "$sha:lib"; then
  echo "pin-lib: commit $sha has no lib/ directory" >&2
  exit 1
fi
stamp="$(TZ=UTC git -C "$root" show -s --format=%cd --date=format-local:%Y%m%d%H%M%S "$sha")"
version="v0.0.0-${stamp}-${sha:0:12}"
prefix="github.com/nomi-language/nomi/lib/"

changed=0
while IFS= read -r gomod; do
  dir="$(dirname "$gomod")"
  # Required lib/ modules: the require lines (a block or single) whose
  # path starts with the prefix; the module and replace lines never match.
  reqs="$(awk -v p="$prefix" '
    /^require[ \t]*\(/ { inreq = 1; next }
    inreq && /^\)/ { inreq = 0; next }
    inreq && index($1, p) == 1 { print $1 }
    /^require[ \t]+[^(]/ && index($2, p) == 1 { print $2 }
  ' "$gomod" | sort -u)"
  for mod in $reqs; do
    go -C "$dir" mod edit -require="$mod@$version"
    echo "pin-lib: ${gomod#"$root"/}: $mod@$version"
    changed=1
  done
done < <(find "$root/lib" -name go.mod -not -path '*/testdata/*' | sort)

if [[ $changed -eq 0 ]]; then
  echo "pin-lib: no lib/ module requires a sibling"
fi

if [[ $verify -eq 1 ]]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  go -C "$root" build -o "$tmp/nomi" ./cmd/nomi
  mkdir "$tmp/app"
  (cd "$tmp/app" && "$tmp/nomi" init example.com/pincheck >/dev/null)
  (cd "$tmp/app" && "$tmp/nomi" add "${prefix}sql/sqlkit@$version")
  echo "pin-lib: nomi add ${prefix}sql/sqlkit@$version works"
fi
