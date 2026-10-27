#!/usr/bin/env bash
# Point each lib/ module's requires of its siblings at a published commit.
#
#   scripts/pin-lib.sh <commit>
#   scripts/pin-lib.sh --verify <commit>
#
# A lib/ module that requires a sibling replaces it with the sibling's
# directory, so builds in this tree read the tree; users read the require.
# A sibling require is only a floor: nomi add and nomi update keep every
# lib/ module a project requires on one commit (modfetch.alignLib), so the
# floors need not move with each push. When a module comes to need a newer
# sibling, run this with a published commit that holds lib/ (origin/main):
# it writes that commit's pseudo-version (v0.0.0-<commit time,
# UTC>-<12-char sha>) into every lib/ go.mod require of a
# github.com/nomi-language/nomi/lib/... module. Commit the go.mod files it
# changes on next.
#
# --verify edits nothing: it adds lib/sql/sqlkit at <commit>'s
# pseudo-version from the module proxy into a fresh project in a temporary
# directory, with the nomi built from this checkout, and runs a program that
# opens a sqlite database. Give it the commit that holds the pins (pushed
# after the one you pinned to), since that is the sqlkit whose require of
# sqlite resolves. It needs the network.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
verify=0
if [[ "${1:-}" == "--verify" ]]; then
  verify=1
  shift
fi
if [[ $# -ne 1 ]]; then
  echo "usage: scripts/pin-lib.sh <commit> | scripts/pin-lib.sh --verify <commit>" >&2
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

if [[ $verify -eq 1 ]]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  go -C "$root" build -o "$tmp/nomi" ./cmd/nomi
  mkdir "$tmp/app"
  (cd "$tmp/app" && "$tmp/nomi" init example.com/pincheck >/dev/null)
  (cd "$tmp/app" && "$tmp/nomi" add "${prefix}sql/sqlkit@$version")
  cat > "$tmp/app/main.nomi" <<'NOMI'
import {
    std/io
    std/sql.{self, Conn, Error, Sql}
    sqlite
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        _ = try Conn.exec(db, Sql"CREATE TABLE t (x INTEGER)")
        io.print("sqlite works")
        Ok(Unit)
    })
}
NOMI
  (cd "$tmp/app" && "$tmp/nomi" run main.nomi)
  echo "pin-lib: nomi add ${prefix}sql/sqlkit@$version works"
  exit 0
fi

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

