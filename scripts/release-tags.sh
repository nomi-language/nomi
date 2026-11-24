#!/usr/bin/env bash
#
# Print, and with --create make, the git tag a release of nomi needs.
#
# One tag versions the whole repository: the compiler and every first-party
# library under lib/. A project names a library by this repository, its
# directory and the tag (`sqlite = { repo = "github.com/nomi-language/nomi",
# subdir = "lib/sql/sqlite", version = "v0.5.0" }`), and a release `nomi`
# takes the libraries at its own version (internal/modfetch's Options.Release),
# so a release without its tag fails each `nomi add` of a library with the tag
# it lacks.
#
# Before tagging, the script checks the libraries, every directory under
# lib/ that holds a nomi.toml:
#
#   - each nomi.toml's [module] gives the module a name;
#   - no [dependencies] entry names this repository. A library names
#     another by `dir` alone (`sqlite = { dir = "../sqlite" }`), which a
#     project that fetches it reads as the neighbor at the same tag. An
#     entry with this repository's version would pin a library to an older
#     release of its neighbors.
#
# It lists each problem and exits 1. TestReleaseTagsChecksTheLibModules
# (cmd/nomi) runs it on this tree and on a tree with each problem.
#
# Usage:
#
#	scripts/release-tags.sh <version>             print the tag and check the libraries
#	scripts/release-tags.sh --create <version>    then create it, annotated, at HEAD
#
# It never pushes. After --create it prints the `git push` that publishes
# the tag. scripts/release.sh builds the archives and makes no tag.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
this_repo="github.com/nomi-language/nomi"

usage() {
	echo "usage: scripts/release-tags.sh [--create] <version>" >&2
	exit 2
}

create=false
version=""
for arg in "$@"; do
	case "$arg" in
	--create) create=true ;;
	-*) usage ;;
	*)
		[ -z "$version" ] || usage
		version="$arg"
		;;
	esac
done
[ -n "$version" ] || usage

# A release version is a vX.Y.Z tag, or a prerelease vX.Y.Z-..., not a Go
# pseudo-version and not `git describe`'s name for a commit past a tag; a
# `nomi` stamped with one of those takes the libraries at the latest
# release instead of its own (cmd/nomi's release).
if ! [[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
	[[ "$version" =~ [0-9]{14}-[0-9a-f]{12}$ ]] ||
	[[ "$version" =~ -[0-9]+-g[0-9a-f]+(-dirty)?$|-dirty$ ]]; then
	echo "release-tags.sh: $version is not a release version such as v0.5.0" >&2
	exit 2
fi

modules=()
while IFS= read -r manifest; do
	modules+=("$(dirname "${manifest#"$repo_root"/}")")
done < <(find "$repo_root/lib" \( -name testdata -o -name node_modules -o -name '.*' \) -prune -o -name nomi.toml -print | LC_ALL=C sort)

if [ "${#modules[@]}" -eq 0 ]; then
	echo "release-tags.sh: found no module under lib/" >&2
	exit 1
fi

problems=0
for m in "${modules[@]}"; do
	while IFS= read -r line; do
		echo "release-tags.sh: $line" >&2
		problems=$((problems + 1))
	done < <(awk -v file="$m/nomi.toml" -v repo="$this_repo" '
		/^[[:space:]]*\[/ {
			module = ($0 ~ /^[[:space:]]*\[module\][[:space:]]*(#.*)?$/)
			deps = ($0 ~ /^[[:space:]]*\[dependencies(\.[^]]*)?\][[:space:]]*(#.*)?$/)
			next
		}
		module && $0 ~ /^[[:space:]]*name[[:space:]]*=[[:space:]]*"[^"]+"/ { named = 1 }
		deps && index($0, "\"" repo "\"") > 0 {
			name = $1; sub(/[[:space:]]*=.*/, "", name)
			printf "%s:%d: %s names %s by version; a library names another by dir alone\n", file, NR, name, repo
		}
		END {
			if (!named) printf "%s: [module] gives no name\n", file
		}
	' "$repo_root/$m/nomi.toml")
done
if [ "$problems" -gt 0 ]; then
	echo "release-tags.sh: $problems problem$([ "$problems" -eq 1 ] || echo s) above; fix, commit, and run this again" >&2
	exit 1
fi

echo "$version"

if [ "$create" != "true" ]; then
	exit 0
fi

if [ -n "$(git -C "$repo_root" status --porcelain)" ]; then
	echo "release-tags.sh: the tree has uncommitted changes; tag a clean commit" >&2
	exit 1
fi
if git -C "$repo_root" rev-parse -q --verify "refs/tags/$version" >/dev/null; then
	echo "release-tags.sh: the tag $version exists already" >&2
	exit 1
fi
git -C "$repo_root" tag -a "$version" -m "nomi $version"
echo
echo "created $version at $(git -C "$repo_root" rev-parse --short HEAD); publish it with:"
echo "  git push origin $version"
