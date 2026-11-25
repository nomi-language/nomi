#!/usr/bin/env bash
#
# Build the per-platform release artifacts for the `nomi` toolchain.
#
# A platform's release is one file: `nomi` (the CLI, whose `nomi lsp` is the
# language server every editor integration launches), alone in its archive.
# It carries the platform's two runners, gzipped: `nomi-vm` (the VM with
# every optional family: `nomi build` appends a program to it, and it runs a
# .nomic file) and `nomi-vm-nonet` (the VM with none, so without the network
# and WebAssembly modules). The runners come in those two tiers and never a
# combination, so a new family joins `nomi-vm` and adds no runner. This
# script builds the two runners first, then hands them to `nomi`'s build
# with the nomi_hostrunners tag (internal/runners/hostrunnersgen), so `nomi
# build` for the platform needs no file beside `nomi` and no network
# (internal/runners/prebuilt.go). A release `nomi` never builds a runner.
#
# `nomi` also carries the editor extensions that are built rather than
# copied, which `nomi editor install` writes: the VS Code extension
# (editors/vscode/nomi.vsix, built with npm, under the nomi_vsix tag) and
# the Zed extension's WebAssembly (editors/zed built with cargo for
# wasm32-wasip2, under the nomi_zedwasm tag). Both are built once, before
# the targets, and staged by hostrunnersgen with the runners, so the
# release machine needs Node 22+ with npm, and rustup with the
# wasm32-wasip2 target (`rustup target add wasm32-wasip2`). Every other
# editor file is embedded from the repository in any build
# (internal/editors).
#
# The same two runners also ship in a runners archive per platform, which
# `nomi build --target` on another platform downloads. Every runner must
# come from the same commit as `nomi`, which `nomi build` checks against the
# VCS stamp both carry; this script checks it too, and that each runner
# records the build tags that name its variant.
#
# std/compiler's runners link the whole front end (13.8 MB gzipped against
# nomi-vm's 6.7 MB on darwin/arm64), so they ship in a compiler archive of
# their own per target: nomi-vm-compiler, built with the nomi_compiler tag, and for wasip1
# nomi-vm-reactor-compiler. Neither has a lean variant, since the front end
# is most of its size.
#
# Built at its tag, every binary carries the tag as its main module's version.
# This script checks the stamp when HEAD carries the tag being released.
#
# `nomi` is stamped with the release version
# (`-X github.com/nomi-language/nomi/internal/buildversion.stamp=<version>`),
# which is the release it is (internal/buildversion package doc): `nomi
# --version` names it, `nomi build --target` downloads another platform's
# runners archive from the release of that tag and checks it against this
# script's checksum manifest, and `nomi add` and `nomi update` take the
# first-party lib/ packages at that version. The stamp also tells `nomi`
# that it carries its runners and builds none.
#
# This script makes no tag and pushes nothing, so it is safe to run as a dry
# run. A release also needs its tag, the one tag that versions the compiler
# and every lib/ package: scripts/release-tags.sh <version> prints it and
# checks the libraries' nomi.toml files, and with --create makes it.
#
# js/wasm and wasip1/wasm have no `nomi`, only the runners and compiler
# archives, for `nomi build --target js/wasm`, `--target wasip1/wasm` and
# `--web`. The js/wasm archives carry Go's wasm_exec.js from the toolchain
# that built the runners, since the two must match; `nomi build --web`
# copies it into the folder it writes. The wasip1 runners archive carries
# nomi-vm and nomi-vm-nonet, the WASI command modules a program with no
# `export fn` is appended to, and nomi-vm-reactor and nomi-vm-reactor-nonet,
# the same VMs linked with -buildmode=c-shared as WASI reactors
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
# `version` defaults to the tag at HEAD; away from a tag, name one, such as
# v0.0.0-dryrun for a dry run. It must be a release version (vX.Y.Z or a
# prerelease of it), since `nomi` is stamped with it. Artifacts land in dist/, or in
# $NOMI_RELEASE_DIST when it is set (a local dry run into a scratch
# directory). $NOMI_RELEASE_TARGETS, a space-separated list such as
# "darwin/arm64", builds only those targets, for a dry run:
#
#	dist/nomi_<version>_<os>_<arch>.tar.gz            (nomi; .zip on windows)
#	dist/nomi_<version>_<os>_<arch>_runners.tar.gz    (nomi-vm, nomi-vm-nonet; .zip on windows)
#	dist/nomi_<version>_<os>_<arch>_compiler.tar.gz   (nomi-vm-compiler; .zip on windows)
#	dist/nomi_<version>_js_wasm_runners.tar.gz        (nomi-vm, nomi-vm-nonet, wasm_exec.js)
#	dist/nomi_<version>_js_wasm_compiler.tar.gz       (nomi-vm-compiler, wasm_exec.js)
#	dist/nomi_<version>_wasip1_wasm_runners.tar.gz    (nomi-vm, nomi-vm-nonet, nomi-vm-reactor, nomi-vm-reactor-nonet)
#	dist/nomi_<version>_wasip1_wasm_compiler.tar.gz   (nomi-vm-compiler, nomi-vm-reactor-compiler)
#	dist/nomi_<version>_checksums.txt
#	dist/nomi.rb                                      (the Homebrew formula, for a vX.Y.Z version without NOMI_RELEASE_TARGETS)
#
# `nomi build` downloads a runner it does not carry and that is not
# installed beside `nomi` from the release archive that holds it and checks
# it against the checksum manifest.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module_dir="$repo_root"
dist_dir="${NOMI_RELEASE_DIST:-$repo_root/dist}"

version="${1:-}"
if [ -z "$version" ]; then
	if ! version="$(git -C "$repo_root" describe --tags --exact-match 2>/dev/null)"; then
		echo "release.sh: HEAD carries no tag; name the version, such as v0.0.0-dryrun for a dry run" >&2
		exit 2
	fi
fi
# The version must be a release version, which release-tags.sh checks
# with the libraries a release needs.
"$repo_root/scripts/release-tags.sh" "$version" >/dev/null

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
# `nomi` does not run there.
vm_targets=(
	"js/wasm"
	"wasip1/wasm"
)

# The wasip1 reactor, built -buildmode=c-shared so the module exports
# `_initialize` and the nomi_call ABI instead of `_start`.
wasip1_reactor="nomi-vm-reactor:./cmd/nomi-vm-reactor"
wasip1_reactor_nonet="nomi-vm-reactor-nonet:./cmd/nomi-vm-reactor"

# The binaries, the runners first, since `nomi` carries them. `nomi-wasm`
# and `nomi-docgen` are deliberately absent: nomi-wasm is a browser artifact
# built by scripts/build-tour-wasm.sh for the tour, and nomi-docgen
# generates reference pages at build time and is never installed by a user.
binaries=(
	"nomi-vm:./cmd/nomi-vm"
	"nomi-vm-nonet:./cmd/nomi-vm"
	"nomi:./cmd/nomi"
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

# `nomi` embeds the stdlib's lowering (internal/irbuild's std bundle), its
# interfaces and facts (std/shared.go) and its base modules' state
# (std/state.go), so it never analyzes or lowers std at startup and does not
# build the base modules on its first run. stdbundlegen makes all
# three once, natively, from this tree, and writes a `go build -overlay`
# file that supplies them under the nomi_stdbundle tag. None depends on the
# target platform; each records the stdlib's digest, the bundle the
# host-function digest and the state its codec's, a binary checks it
# against.
bundle_dir="$tools_dir/stdbundle"
mkdir -p "$bundle_dir"
go run -C "$module_dir" ./internal/irbuild/stdbundlegen "$bundle_dir"

# The editor extensions `nomi` carries, built once for every platform.
editors_dir="$tools_dir/editors"
mkdir -p "$editors_dir"
npm --prefix "$repo_root/editors/vscode" ci
npm --prefix "$repo_root/editors/vscode" run package
cp "$repo_root/editors/vscode/nomi.vsix" "$editors_dir/nomi.vsix"
cargo build --release --target wasm32-wasip2 --manifest-path "$repo_root/editors/zed/Cargo.toml"
cp "$repo_root/editors/zed/target/wasm32-wasip2/release/zed_nomi.wasm" "$editors_dir/extension.wasm"

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

# pack writes the archive called $2 from the directory $1 and removes the
# directory.
pack() {
	if [ "$goos" = "windows" ]; then
		(cd "$1" && zip -q -X "../$2.zip" ./*)
	else
		tar -czf "$dist_dir/$2.tar.gz" -C "$1" .
	fi
	rm -rf "$1"
	packed=$((packed + 1))
}

all_targets=("${targets[@]}" "${vm_targets[@]}")
if [ -n "${NOMI_RELEASE_TARGETS:-}" ]; then
	read -ra all_targets <<<"$NOMI_RELEASE_TARGETS"
fi
built=0
packed=0
for target in "${all_targets[@]}"; do
	goos="${target%%/*}"
	goarch="${target##*/}"
	ext=""
	if [ "$goos" = "windows" ]; then
		ext=".exe"
	fi

	archive_base="nomi_${version}_${goos}_${goarch}"
	nomi_stage="$dist_dir/$archive_base"
	runners_stage="$dist_dir/${archive_base}_runners"
	compiler_stage="$dist_dir/${archive_base}_compiler"
	mkdir -p "$runners_stage" "$compiler_stage"

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
		stage="$runners_stage"
		ldflags="-s -w"
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
		nomi)
			# `nomi` carries the two runners just built, gzipped, and the
			# editor extensions beside the std bundle, in one overlay.
			embed_dir="$tools_dir/hostrunners_${goos}_${goarch}"
			go run -C "$module_dir" ./internal/runners/hostrunnersgen \
				-vsix "$editors_dir/nomi.vsix" -zed "$editors_dir/extension.wasm" "$embed_dir" \
				"$runners_stage/nomi-vm$ext" "$runners_stage/nomi-vm-nonet$ext" "$bundle_dir/overlay.json"
			stage="$nomi_stage"
			mkdir -p "$stage"
			tags="nomi_stdbundle,nomi_hostrunners,nomi_vsix,nomi_zedwasm"
			overlay=(-overlay "$embed_dir/overlay.json")
			ldflags="$ldflags -X github.com/nomi-language/nomi/internal/buildversion.stamp=$version"
			;;
		esac
		build_one "$stage" "$name" "$pkg" "$buildmode" "$tags" "$ldflags" ${overlay[@]+"${overlay[@]}"}
		# The full runner's std/compiler variant goes in the compiler
		# archive: the same build with the nomi_compiler tag, as
		# internal/runners' Variant.buildTags writes it. It has no lean
		# variant.
		case "$name" in
		nomi-vm | nomi-vm-reactor)
			build_one "$compiler_stage" "$name-compiler" "$pkg" "$buildmode" "nomi_compiler" "$ldflags"
			;;
		esac
	done

	if [ "$goos" = "js" ]; then
		# The JS support file of the Go that just built nomi-vm: the same
		# `go` in the same module directory, so a toolchain line selects the
		# same toolchain. Each js/wasm archive carries its own copy.
		goroot="$(go -C "$module_dir" env GOROOT)"
		for dir in "$runners_stage" "$compiler_stage"; do
			cp "$goroot/lib/wasm/wasm_exec.js" "$dir/wasm_exec.js"
			chmod 0644 "$dir/wasm_exec.js"
		done
	fi

	if [ -d "$nomi_stage" ]; then
		pack "$nomi_stage" "$archive_base"
	fi
	pack "$runners_stage" "${archive_base}_runners"
	pack "$compiler_stage" "${archive_base}_compiler"
	built=$((built + 1))
done

# Anti-vacuity: a loop whose matrix went empty, or whose archive step silently
# skipped, would otherwise reach the checksum step and write an empty manifest
# that looks like a clean run. Each target packs a runners and a compiler
# archive, and each target `nomi` runs on also packs `nomi`'s.
expected=0
for target in "${all_targets[@]}"; do
	case "$target" in
	*/wasm) expected=$((expected + 2)) ;;
	*) expected=$((expected + 3)) ;;
	esac
done
if [ "$built" -ne "${#all_targets[@]}" ] || [ "$packed" -ne "$expected" ]; then
	echo "release.sh: built $built target(s) into $packed archive(s), expected ${#all_targets[@]} into $expected" >&2
	exit 1
fi

(cd "$dist_dir" && shopt -s nullglob && sha256 ./*.tar.gz ./*.zip > "nomi_${version}_checksums.txt")

archives="$(find "$dist_dir" -maxdepth 1 \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l | tr -d ' ')"
sums="$(grep -c . "$dist_dir/nomi_${version}_checksums.txt")"
if [ "$archives" -ne "$sums" ] || [ "$archives" -ne "$expected" ]; then
	echo "release.sh: $archives archive(s) and $sums checksum line(s), expected $expected of each" >&2
	exit 1
fi

echo
echo "$archives archives for $version in $dist_dir"
cat "$dist_dir/nomi_${version}_checksums.txt"

# The Homebrew formula for the tap, rendered from the checksums just written,
# when the build holds the four macOS and Linux archives it names and the
# version is a tag's. It is not a release asset: the tap repository holds it
# (docs/releasing.md).
if [ -z "${NOMI_RELEASE_TARGETS:-}" ] && [[ "$version" =~ ^v[0-9] ]]; then
	"$repo_root/scripts/homebrew-formula.sh" "$version" "$dist_dir/nomi_${version}_checksums.txt" >"$dist_dir/nomi.rb"
	echo
	echo "Homebrew formula: $dist_dir/nomi.rb"
fi
