#!/usr/bin/env bash
#
# Print the Homebrew formula for a release, rendered from
# packaging/homebrew/nomi.rb with the release's version and the SHA-256 of
# its four macOS and Linux archives, read from the checksums file
# scripts/release.sh writes. The tap repository, nomi-language/homebrew-nomi,
# holds the result as Formula/nomi.rb.
#
# Usage:
#
#	scripts/homebrew-formula.sh <version> <checksums file> > nomi.rb
#
# The version is the release's tag, such as v0.0.1; the formula's version
# drops the v. A checksums file that lists no archive for one of the four
# platforms prints nothing and exits 1. TestHomebrewFormulaRendersFromTheChecksums
# (cmd/nomi) runs it on a fixture.

set -euo pipefail

if [ "$#" -ne 2 ]; then
	echo "usage: scripts/homebrew-formula.sh <version> <checksums file>" >&2
	exit 2
fi
version="$1"
sums="$2"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template="$repo_root/packaging/homebrew/nomi.rb"

if ! [[ "$version" =~ ^v[0-9][0-9A-Za-z.+-]*$ ]]; then
	echo "homebrew-formula.sh: $version is not a release version such as v0.0.1" >&2
	exit 2
fi

# sha_of prints the SHA-256 the checksums file gives the archive named $1.
# A line is `<sha256>  ./<name>`, as sha256sum and shasum write it.
sha_of() {
	awk -v name="$1" '{ f = $2; sub(/^\*/, "", f); sub(/^\.\//, "", f); if (f == name) { print $1; exit } }' "$sums"
}

args=(-e "s/@VERSION@/${version#v}/")
for platform in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
	archive="nomi_${version}_${platform}.tar.gz"
	sha="$(sha_of "$archive")"
	if ! [[ "$sha" =~ ^[0-9a-f]{64}$ ]]; then
		echo "homebrew-formula.sh: $sums gives no SHA-256 for $archive" >&2
		exit 1
	fi
	placeholder="$(echo "$platform" | tr '[:lower:]' '[:upper:]')"
	args+=(-e "s/@SHA256_${placeholder}@/$sha/")
done

# The template's opening comment describes the template; the rendered
# formula says where it comes from instead.
{
	echo "# Rendered by scripts/homebrew-formula.sh in github.com/nomi-language/nomi"
	echo "# from packaging/homebrew/nomi.rb and the checksums of release $version."
	echo "# Do not edit: render it again for the next release."
	sed -n '/^class /,$p' "$template" | sed "${args[@]}"
}
