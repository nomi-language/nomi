#!/usr/bin/env bash
#
# Print, and with --create make, the git tags a release of nomi needs.
#
# The compiler's module, github.com/nomi-language/nomi, is tagged with the
# release's version. Each first-party library under lib/ is a Go module of
# its own, with a go.mod beside its nomi.toml, and Go's module proxy serves
# a version of github.com/nomi-language/nomi/lib/<path> only from a tag
# named lib/<path>/<version>. A release `nomi` takes the libraries at its
# own version (internal/modfetch's atRelease), so a release without their
# tags fails each `nomi add` of a library with the tag it lacks.
#
# The libraries are read from the tree: every directory under lib/ that
# holds both a nomi.toml and a go.mod. TestReleaseTagsNameEveryLibModule
# (cmd/nomi) holds the list to the modules whose nomi.toml gives them a
# path under lib/.
#
# A library names the libraries it needs in its nomi.toml with a version
# and a dir. The dir is read only in this checkout; in a copy a project
# downloads, the version is what a project that does not name that library
# itself gets. So before tagging, every such entry must name the release's
# version, or a project that adds only sqlkit takes migrate at whatever
# commit sqlkit's nomi.toml names. The script lists each entry that does not
# and exits 1: set each to `version = "<version>"`, commit, and run it
# again on that commit.
#
# Usage:
#
#	scripts/release-tags.sh <version>             print the tags and check the entries
#	scripts/release-tags.sh --create <version>    then create them, annotated, at HEAD
#
# It never pushes. After --create it prints the `git push` that publishes
# the tags. scripts/release.sh builds the archives and makes no tags.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lib_prefix="github.com/nomi-language/nomi/lib/"

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

# A release version is a semantic version, not a pseudo-version and not
# `git describe`'s name for a commit past a tag; a release `nomi` stamped
# with anything else takes the libraries at their latest commit
# (cmd/nomi's libRelease).
if ! [[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
	[[ "$version" =~ [0-9]{14}-[0-9a-f]{12}$ ]] ||
	[[ "$version" =~ -[0-9]+-g[0-9a-f]+(-dirty)?$|-dirty$ ]]; then
	echo "release-tags.sh: $version is not a release version such as v0.5.0" >&2
	exit 2
fi

modules=()
while IFS= read -r manifest; do
	dir="$(dirname "$manifest")"
	if [ -f "$dir/go.mod" ]; then
		modules+=("${dir#"$repo_root"/}")
	fi
done < <(find "$repo_root/lib" \( -name testdata -o -name node_modules -o -name '.*' \) -prune -o -name nomi.toml -print | LC_ALL=C sort)

if [ "${#modules[@]}" -eq 0 ]; then
	echo "release-tags.sh: found no module under lib/" >&2
	exit 1
fi

tags=("$version")
for m in "${modules[@]}"; do
	tags+=("$m/$version")
done
printf '%s\n' "${tags[@]}"

# Each lib/ entry of each library's [dependencies] that does not name this
# version.
problems=0
for m in "${modules[@]}"; do
	while IFS= read -r line; do
		echo "release-tags.sh: $line" >&2
		problems=$((problems + 1))
	done < <(awk -v file="$m/nomi.toml" -v prefix="$lib_prefix" -v want="$version" '
		/^[[:space:]]*\[/ { deps = ($0 ~ /^[[:space:]]*\[dependencies\][[:space:]]*$/); next }
		deps && index($0, "module = \"" prefix) > 0 {
			name = $1
			mod = $0; sub(/.*module = "/, "", mod); sub(/".*/, "", mod)
			ver = ""
			if (index($0, "version = \"") > 0) { ver = $0; sub(/.*version = "/, "", ver); sub(/".*/, "", ver) }
			if (ver != want) {
				printf "%s:%d: %s names %s at %s, not %s\n", file, NR, name, mod, (ver == "" ? "no version" : ver), want
			}
		}
	' "$repo_root/$m/nomi.toml")
done
if [ "$problems" -gt 0 ]; then
	echo "release-tags.sh: $problems entr$([ "$problems" -eq 1 ] && echo y || echo ies) above must name $version before the libraries are tagged: set each to version = \"$version\", commit, and run this again" >&2
	exit 1
fi

if [ "$create" != "true" ]; then
	exit 0
fi

if [ -n "$(git -C "$repo_root" status --porcelain)" ]; then
	echo "release-tags.sh: the tree has uncommitted changes; tag a clean commit" >&2
	exit 1
fi
for tag in "${tags[@]}"; do
	if git -C "$repo_root" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
		echo "release-tags.sh: the tag $tag exists already" >&2
		exit 1
	fi
done
for tag in "${tags[@]}"; do
	git -C "$repo_root" tag -a "$tag" -m "nomi $version"
done
echo
echo "created ${#tags[@]} tags at $(git -C "$repo_root" rev-parse --short HEAD); publish them with:"
echo "  git push origin ${tags[*]}"
