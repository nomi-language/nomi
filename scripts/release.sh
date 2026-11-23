#!/usr/bin/env bash
#
# Build the per-platform release artifacts for the `nomi` toolchain.
#
# A release carries THREE binaries per platform: `nomi` (the CLI), `nomi-lsp`
# (the editor language server) and `nomi-vm` (the VM: `nomi build`
# appends a program to it, and it runs a .nomic file). The editor integrations name `nomi-lsp` —
# editors/helix/languages.toml runs it by name and the Zed extension does
# `worktree.which("nomi-lsp")` — so shipping only `nomi` would leave every
# editor on the from-source path. `nomi build` looks for `nomi-vm` beside
# `nomi` (internal/runners/prebuilt.go). A release `nomi` never builds a
# runner, so the release ships every runner `nomi build` can
# ask for. nomi-vm must come from the same commit as `nomi`, which `nomi build`
# checks against the VCS stamp both carry; this script checks it too, and that
# each runner records the build tags that name its variant.
#
# std/compiler's runners link the whole front end (6.5 MB gzipped against the
# plain one's 3.7 MB), so they ship in an archive of their own per target, and
# the main archives stay their size: nomi-vm-compiler and nomi-vm-compiler-nonet,
# built with the nomi_compiler tag, and for wasip1 nomi-vm-reactor-compiler and
# nomi-vm-reactor-compiler-nonet.
#
# Built at its tag, every binary carries the tag as its main module's version.
# This script checks the stamp when HEAD carries the tag being released.
#
# `nomi` is stamped with the release tag
# (`-X github.com/nomi-language/nomi/internal/runners.ReleaseVersion=<version>`): `nomi build --target`
# downloads another platform's archive from the release of that tag and
# checks it against this script's checksum manifest, and `nomi add` and
# `nomi update` take the first-party lib/ modules at that version.
#
# This script makes no tag and pushes nothing, so it is safe to run as a dry
# run. A release also needs a tag for the compiler and one per lib/ module
# (lib/markdown/<version>), which the module proxy serves each library's
# release from: scripts/release-tags.sh <version> lists them and checks that
# the libraries name each other at <version>, and with --create makes them.
#
# Two more archives carry only runners: js/wasm and wasip1/wasm, for
# `nomi build --target js/wasm`, `--target wasip1/wasm` and `--web`. The
# js/wasm archive carries nomi-vm and Go's wasm_exec.js from the toolchain
# that built it, since the two must match; `nomi build --web` copies it into
# the folder it writes. The wasip1 archive carries nomi-vm, the WASI command
# module a program with no `export fn` is appended to, and nomi-vm-reactor,
# the same VM linked with -buildmode=c-shared as a WASI reactor
# (cmd/nomi-vm-reactor), which a program with an `export fn` is appended to.
#
# Nothing else ships. The standard library is `//go:embed`ed into each binary
# (std/std.go: `//go:embed *.nomi nomi.toml all:_fixtures`), so there
# is no data directory to install beside them, and an archive is exactly the
# executables (and wasm_exec.js).
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
#	dist/nomi_<version>_<os>_<arch>.tar.gz            (.zip on windows)
#	dist/nomi_<version>_<os>_<arch>_compiler.tar.gz   (nomi-vm-compiler, nomi-vm-compiler-nonet; .zip on windows)
#	dist/nomi_<version>_js_wasm.tar.gz                (nomi-vm, nomi-vm-nonet, wasm_exec.js)
#	dist/nomi_<version>_js_wasm_compiler.tar.gz       (nomi-vm-compiler, nomi-vm-compiler-nonet, wasm_exec.js)
#	dist/nomi_<version>_wasip1_wasm.tar.gz            (nomi-vm, nomi-vm-nonet, nomi-vm-reactor, nomi-vm-reactor-nonet)
#	dist/nomi_<version>_wasip1_wasm_compiler.tar.gz   (the same four, each -compiler before any -nonet)
#	dist/nomi_<version>_checksums.txt
#
# `nomi build` downloads a runner that is not installed beside `nomi` from
# the release archive that holds it and checks it against the checksum
# manifest.

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

# The WebAssembly targets ship only nomi-vm (and for wasip1 its reactor):
# `nomi` and `nomi-lsp` do not run there.
vm_targets=(
	"js/wasm"
	"wasip1/wasm"
)

# The wasip1 reactor, built -buildmode=c-shared so the module exports
# `_initialize` and the nomi_call ABI instead of `_start`.
wasip1_reactor="nomi-vm-reactor:./cmd/nomi-vm-reactor"
wasip1_reactor_nonet="nomi-vm-reactor-nonet:./cmd/nomi-vm-reactor"

# The three commands. `nomi-wasm` and `nomi-docgen` are deliberately absent:
# nomi-wasm is a browser artifact built by scripts/build-tour-wasm.sh for the
# tour, and nomi-docgen generates reference pages at build time and is never
# installed by a user.
binaries=(
	"nomi:./cmd/nomi"
	"nomi-lsp:./cmd/nomi-lsp"
	"nomi-vm:./cmd/nomi-vm"
	"nomi-vm-nonet:./cmd/nomi-vm"
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

# `go version -m` cannot read a WebAssembly module, so every artifact's build
# info is read with internal/gobuildinfo, which prints the same layout.
tools_dir="$(mktemp -d)"
trap 'rm -rf "$tools_dir"' EXIT
CGO_ENABLED=0 GOOS= GOARCH= go build -C "$module_dir" -o "$tools_dir/gobuildinfo" ./internal/gobuildinfo/cmd/gobuildinfo
buildinfo() {
	"$tools_dir/gobuildinfo" "$1"
}

# `nomi` and `nomi-lsp` embed the stdlib's lowering (internal/irbuild's std
# bundle), its interfaces and facts (std/shared.go) and its base modules'
# state (std/state.go), so they never analyze or lower std at startup and
# do not build the base modules on their first run. stdbundlegen makes all
# three once, natively, from this tree, and writes a `go build -overlay`
# file that supplies them under the nomi_stdbundle tag. None depends on the
# target platform; each records the stdlib's digest, the bundle the
# host-function digest and the state its codec's, a binary checks it
# against.
bundle_dir="$tools_dir/stdbundle"
mkdir -p "$bundle_dir"
go run -C "$module_dir" ./internal/irbuild/stdbundlegen "$bundle_dir"

# Every binary of the release carries one VCS stamp.
revisions=""

# build_one builds package pkg as stage/name for $goos/$goarch, then checks
# what the artifact says about itself: its platform, its build tags, its VCS
# stamp (the release's one), and, when HEAD carries the tag, its module
# version. Extra arguments go to go build before the package.
build_one() {
	local stage="$1" name="$2" pkg="$3" buildmode="$4" tags="$5" ldflags="$6"
	shift 6
	echo "building $name for $goos/$goarch"
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -C "$module_dir" -trimpath -buildmode="$buildmode" -tags "$tags" "$@" -ldflags "$ldflags" \
		-o "$stage/$name$ext" "$pkg"

	out="$stage/$name$ext"
	if [ ! -s "$out" ]; then
		echo "release.sh: $out is missing or empty" >&2
		exit 1
	fi

	# Read the platform back off the artifact. Exporting GOOS/GOARCH and
	# then trusting them proves nothing: a matrix that silently produced
	# five host-native copies would look identical from the outside.
	got_os="$(buildinfo "$out" | awk '$1 == "build" && $2 ~ /^GOOS=/ { sub(/^GOOS=/, "", $2); print $2 }')"
	got_arch="$(buildinfo "$out" | awk '$1 == "build" && $2 ~ /^GOARCH=/ { sub(/^GOARCH=/, "", $2); print $2 }')"
	if [ "$got_os" != "$goos" ] || [ "$got_arch" != "$goarch" ]; then
		echo "release.sh: $out reports $got_os/$got_arch, wanted $goos/$goarch" >&2
		exit 1
	fi

	# `nomi build` tells a runner's variant by the tags its build info
	# records (internal/runners' checkRunner), so they must be the ones asked
	# for.
	got_tags="$(buildinfo "$out" | awk '$1 == "build" && $2 ~ /^-tags=/ { sub(/^-tags=/, "", $2); print $2 }')"
	if [ "$got_tags" != "$tags" ]; then
		echo "release.sh: $out records the build tags '$got_tags', wanted '$tags'" >&2
		exit 1
	fi

	# `nomi build` refuses a nomi-vm from another commit than `nomi`, so
	# every binary in one archive must carry the same VCS stamp.
	stamp="$(buildinfo "$out" | awk '$1 == "build" && ($2 ~ /^vcs.revision=/ || $2 ~ /^vcs.modified=/) { print $2 }' | tr '\n' ' ')"
	if [ -z "$stamp" ]; then
		echo "release.sh: $out carries no VCS stamp; nomi build could not check it against nomi" >&2
		exit 1
	fi
	if [ -n "$revisions" ] && [ "$stamp" != "$revisions" ]; then
		echo "release.sh: $out is stamped '$stamp', the release's other binaries '$revisions'" >&2
		exit 1
	fi
	revisions="$stamp"

	# At its tag, Go stamps the tag as the main module's version (a clean
	# tree and VCS stamping are both required), which `nomi --version`
	# reports.
	if [ "$at_tag" = "true" ]; then
		mod_version="$(buildinfo "$out" | awk '$1 == "mod" { print $3; exit }')"
		if [ "$mod_version" != "$version" ]; then
			echo "release.sh: $out carries module version '$mod_version', not the tag $version; is the tree clean?" >&2
			exit 1
		fi
	fi
}

# compiler_name is the file name of the std/compiler variant of the runner
# called $1, as internal/runners' Variant.BinaryName spells it: -compiler
# before any -nonet.
compiler_name() {
	case "$1" in
	*-nonet) echo "${1%-nonet}-compiler-nonet" ;;
	*) echo "$1-compiler" ;;
	esac
}

# pack writes the archive called $2 from the directory $1 and removes the
# directory.
pack() {
	if [ "$goos" = "windows" ]; then
		(cd "$1" && zip -q -X "../$2.zip" ./*)
	else
		tar -czf "$dist_dir/$2.tar.gz" -C "$1" .
	fi
	rm -rf "$1"
}

all_targets=("${targets[@]}" "${vm_targets[@]}")
built=0
for target in "${all_targets[@]}"; do
	goos="${target%%/*}"
	goarch="${target##*/}"
	ext=""
	if [ "$goos" = "windows" ]; then
		ext=".exe"
	fi

	archive_base="nomi_${version}_${goos}_${goarch}"
	stage="$dist_dir/$archive_base"
	compiler_stage="$dist_dir/${archive_base}_compiler"
	mkdir -p "$stage" "$compiler_stage"

	specs=("${binaries[@]}")
	if [ "$goarch" = "wasm" ]; then
		specs=("nomi-vm:./cmd/nomi-vm" "nomi-vm-nonet:./cmd/nomi-vm")
	fi
	if [ "$goos" = "wasip1" ]; then
		specs+=("$wasip1_reactor" "$wasip1_reactor_nonet")
	fi
	for spec in "${specs[@]}"; do
		name="${spec%%:*}"
		pkg="${spec##*:}"
		ldflags="-s -w"
		if [ "$name" = "nomi" ]; then
			ldflags="$ldflags -X github.com/nomi-language/nomi/internal/runners.ReleaseVersion=$version"
		fi
		buildmode="default"
		case "$name" in nomi-vm-reactor*) buildmode="c-shared" ;; esac
		# A -nonet runner is the lean one, built with every optional
		# family's tag (stdlibadapters.LeanTags: the network and
		# WebAssembly modules): nomi build appends a program that reaches
		# no family's crossing to it.
		tags=""
		overlay=()
		case "$name" in
		*-nonet) tags="nomi_nonet,nomi_nowasm" ;;
		nomi | nomi-lsp)
			tags="nomi_stdbundle"
			overlay=(-overlay "$bundle_dir/overlay.json")
			;;
		esac
		build_one "$stage" "$name" "$pkg" "$buildmode" "$tags" "$ldflags" ${overlay[@]+"${overlay[@]}"}
		# Each runner's std/compiler variant goes in the compiler archive:
		# the same build with the nomi_compiler tag first, as
		# internal/runners' Variant.buildTags writes it.
		case "$name" in
		nomi-vm*)
			tags="nomi_compiler${tags:+,$tags}"
			build_one "$compiler_stage" "$(compiler_name "$name")" "$pkg" "$buildmode" "$tags" "$ldflags"
			;;
		esac
	done

	if [ "$goos" = "js" ]; then
		# The JS support file of the Go that just built nomi-vm: the same
		# `go` in the same module directory, so a toolchain line selects the
		# same toolchain. Each js/wasm archive carries its own copy.
		goroot="$(go -C "$module_dir" env GOROOT)"
		for dir in "$stage" "$compiler_stage"; do
			cp "$goroot/lib/wasm/wasm_exec.js" "$dir/wasm_exec.js"
			chmod 0644 "$dir/wasm_exec.js"
		done
	fi

	pack "$stage" "$archive_base"
	pack "$compiler_stage" "${archive_base}_compiler"
	built=$((built + 1))
done

# Anti-vacuity: a loop whose matrix went empty, or whose archive step silently
# skipped, would otherwise reach the checksum step and write an empty manifest
# that looks like a clean run.
if [ "$built" -ne "${#all_targets[@]}" ]; then
	echo "release.sh: built $built target(s), expected ${#all_targets[@]}" >&2
	exit 1
fi

(cd "$dist_dir" && sha256 ./*.tar.gz ./*.zip > "nomi_${version}_checksums.txt")

archives="$(find "$dist_dir" -maxdepth 1 \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l | tr -d ' ')"
sums="$(grep -c . "$dist_dir/nomi_${version}_checksums.txt")"
if [ "$archives" -ne "$sums" ] || [ "$archives" -ne $((2 * built)) ]; then
	echo "release.sh: $archives archive(s) and $sums checksum line(s), expected $((2 * built)) of each" >&2
	exit 1
fi

echo
echo "$archives archives for $version in $dist_dir"
cat "$dist_dir/nomi_${version}_checksums.txt"
