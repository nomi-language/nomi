#!/usr/bin/env bash
#
# Build the per-platform release artifacts for the `nomi` toolchain.
#
# A release carries THREE binaries per platform: `nomi` (the CLI), `nomi-lsp`
# (the editor language server) and `nomi-runner` (the VM runner `nomi build`
# appends a program to). The editor integrations name `nomi-lsp` —
# editors/helix/languages.toml runs it by name and the Zed extension does
# `worktree.which("nomi-lsp")` — so shipping only `nomi` would leave every
# editor on the from-source path. `nomi build` looks for `nomi-runner` beside
# `nomi` (internal/ffirun/prebuilt.go), and without it a release
# could not build executables without Go. The runner must come from the same
# commit as `nomi`, which `nomi build` checks against the VCS stamp both carry;
# this script checks it too. Only the plain runner ships: std/compiler's variant
# links the whole front end (6.5 MB gzipped against the plain runner's 3.7 MB),
# so a program importing std/compiler needs Go, which builds its runner from
# the compiler module at the release's version.
#
# Built at its tag, every binary carries the tag as its main module's version,
# and `nomi` builds Go bindings against the compiler module at that version.
# This script checks the stamp when HEAD carries the tag being released.
#
# `nomi` is stamped with the release tag
# (`-X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=<version>`): `nomi build --target`
# downloads another platform's archive from the release of that tag and
# checks it against this script's checksum manifest.
#
# Nothing else ships. The standard library is `//go:embed`ed into each binary
# (std/std.go: `//go:embed *.nomi nomi.toml all:_fixtures`), so there
# is no data directory to install beside them, and an archive is exactly the
# executables.
#
# Go cross-compiles the whole matrix from one machine: no cross-toolchain, no
# per-OS runner. CGO is off so every artifact is a static binary with no libc
# dependency at the target.
#
# Usage:
#
#	scripts/release.sh [version]
#
# `version` defaults to `git describe`. Artifacts land in dist/, or in
# $NOMI_RELEASE_DIST when it is set (a local dry run into a scratch directory):
#
#	dist/nomi_<version>_<os>_<arch>.tar.gz   (.zip on windows)
#	dist/nomi_<version>_checksums.txt
#
# The checksum manifest is not tidiness. Part two of the roadmap's
# "Distributing the `nomi` binary itself" entry fetches a pinned Go toolchain
# and wants that download verified against a manifest; publishing checksums for
# our own artifacts from day one is the same discipline applied to ourselves,
# and it is what Homebrew's `sha256` field and Scoop's `hash` field consume.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module_dir="$repo_root"
dist_dir="${NOMI_RELEASE_DIST:-$repo_root/dist}"

version="${1:-}"
if [ -z "$version" ]; then
	version="$(git -C "$repo_root" describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

# Whether this is the real release build: HEAD carries the tag being released.
at_tag=false
if git -C "$repo_root" tag --points-at HEAD 2>/dev/null | grep -qxF "$version"; then
	at_tag=true
fi

# The conventional five. Every one is verified to build below, and the
# verification reads the artifact's own recorded GOOS/GOARCH rather than
# trusting that the environment variables took effect.
targets=(
	"darwin/arm64"
	"darwin/amd64"
	"linux/amd64"
	"linux/arm64"
	"windows/amd64"
)

# The three commands. `nomi-wasm` and `nomi-docgen` are deliberately absent:
# nomi-wasm is a browser artifact built by scripts/build-tour-wasm.sh for the
# tour, and nomi-docgen generates reference pages at build time and is never
# installed by a user.
binaries=(
	"nomi:./cmd/nomi"
	"nomi-lsp:./cmd/nomi-lsp"
	"nomi-runner:./cmd/nomi-runner"
)

sha256() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$@"
	else
		sha256sum "$@"
	fi
}

rm -rf "$dist_dir"
mkdir -p "$dist_dir"

built=0
for target in "${targets[@]}"; do
	goos="${target%%/*}"
	goarch="${target##*/}"
	ext=""
	if [ "$goos" = "windows" ]; then
		ext=".exe"
	fi

	stage="$dist_dir/nomi_${version}_${goos}_${goarch}"
	mkdir -p "$stage"

	revisions=""
	for spec in "${binaries[@]}"; do
		name="${spec%%:*}"
		pkg="${spec##*:}"
		ldflags="-s -w"
		if [ "$name" = "nomi" ]; then
			ldflags="$ldflags -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=$version"
		fi
		echo "building $name for $goos/$goarch"
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
			go build -C "$module_dir" -trimpath -ldflags "$ldflags" \
			-o "$stage/$name$ext" "$pkg"

		out="$stage/$name$ext"
		if [ ! -s "$out" ]; then
			echo "release.sh: $out is missing or empty" >&2
			exit 1
		fi

		# Read the platform back off the artifact. Exporting GOOS/GOARCH and
		# then trusting them proves nothing: a matrix that silently produced
		# five host-native copies would look identical from the outside.
		got_os="$(go version -m "$out" | awk '$1 == "build" && $2 ~ /^GOOS=/ { sub(/^GOOS=/, "", $2); print $2 }')"
		got_arch="$(go version -m "$out" | awk '$1 == "build" && $2 ~ /^GOARCH=/ { sub(/^GOARCH=/, "", $2); print $2 }')"
		if [ "$got_os" != "$goos" ] || [ "$got_arch" != "$goarch" ]; then
			echo "release.sh: $out reports $got_os/$got_arch, wanted $goos/$goarch" >&2
			exit 1
		fi

		# `nomi build` refuses a runner from another commit than `nomi`, so
		# every binary in one archive must carry the same VCS stamp.
		stamp="$(go version -m "$out" | awk '$1 == "build" && ($2 ~ /^vcs.revision=/ || $2 ~ /^vcs.modified=/) { print $2 }' | tr '\n' ' ')"
		if [ -z "$stamp" ]; then
			echo "release.sh: $out carries no VCS stamp; nomi build could not check its runner" >&2
			exit 1
		fi
		if [ -n "$revisions" ] && [ "$stamp" != "$revisions" ]; then
			echo "release.sh: $out is stamped '$stamp', the archive's other binaries '$revisions'" >&2
			exit 1
		fi
		revisions="$stamp"

		# At its tag, Go stamps the tag as the main module's version (a clean
		# tree and VCS stamping are both required), and `nomi` builds a
		# project's Go bindings against the compiler module at that version,
		# fetched through the Go toolchain. A release without it would treat
		# itself as a development build and refuse Go bindings.
		if [ "$at_tag" = "true" ]; then
			mod_version="$(go version -m "$out" | awk '$1 == "mod" { print $3; exit }')"
			if [ "$mod_version" != "$version" ]; then
				echo "release.sh: $out carries module version '$mod_version', not the tag $version; is the tree clean?" >&2
				exit 1
			fi
		fi
	done

	archive_base="nomi_${version}_${goos}_${goarch}"
	if [ "$goos" = "windows" ]; then
		(cd "$stage" && zip -q -X "../$archive_base.zip" ./*)
	else
		tar -czf "$dist_dir/$archive_base.tar.gz" -C "$stage" .
	fi
	rm -rf "$stage"
	built=$((built + 1))
done

# Anti-vacuity: a loop whose matrix went empty, or whose archive step silently
# skipped, would otherwise reach the checksum step and write an empty manifest
# that looks like a clean run.
if [ "$built" -ne "${#targets[@]}" ]; then
	echo "release.sh: built $built archive(s), expected ${#targets[@]}" >&2
	exit 1
fi

(cd "$dist_dir" && sha256 ./*.tar.gz ./*.zip > "nomi_${version}_checksums.txt")

archives="$(find "$dist_dir" -maxdepth 1 \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l | tr -d ' ')"
sums="$(grep -c . "$dist_dir/nomi_${version}_checksums.txt")"
if [ "$archives" -ne "$sums" ]; then
	echo "release.sh: $archives archive(s) but $sums checksum line(s)" >&2
	exit 1
fi

echo
echo "$built archives for $version in $dist_dir"
cat "$dist_dir/nomi_${version}_checksums.txt"
