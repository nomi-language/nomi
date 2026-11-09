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
# `nomi` (internal/ffirun/prebuilt.go), and without it a release
# could not build executables without Go. nomi-vm must come from the same
# commit as `nomi`, which `nomi build` checks against the VCS stamp both carry;
# this script checks it too. Only the plain nomi-vm ships: std/compiler's variant
# links the whole front end (6.5 MB gzipped against the plain one's 3.7 MB),
# so a program importing std/compiler has its nomi-vm built from the compiler
# module at the release's version, with the `go` on PATH or the managed Go
# toolchain (internal/gotoolchain).
#
# Built at its tag, every binary carries the tag as its main module's version,
# and `nomi` builds Go bindings against the compiler module at that version,
# which the Go module proxy serves once the tag is pushed to GitHub. This
# script checks the stamp when HEAD carries the tag being released.
#
# `nomi` is stamped with the release tag
# (`-X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=<version>`): `nomi build --target`
# downloads another platform's archive from the release of that tag and
# checks it against this script's checksum manifest.
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
# Each native platform and js/wasm also get a lib archive, the first-party
# lib/ modules' Go side prebuilt (internal/ffirun/libprebuilt.go): nomi-lib,
# the FFI wrapper `nomi run`, `test` and `check` run a program of lib/ modules
# in, and nomi-vm-lib, the runner `nomi build` appends one to (js/wasm: the
# runner and wasm_exec.js). Both are built from the librunners/ module, whose
# main packages `go run ./internal/ffirun/libgen` generates; this script
# refuses stale ones. They are a separate archive because they double a
# platform's download, and `nomi` fetches it the first time a project's Go
# bindings are all lib/ modules' at the release's commit, which `nomi` is
# stamped with as a pseudo-version
# (`-X github.com/nomi-language/nomi/internal/ffirun.LibVersion=<version>`):
# the version `nomi add` takes for a lib/ module. wasip1 has none:
# modernc.org/sqlite does not build for it.
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
#	dist/nomi_<version>_<os>_<arch>.tar.gz   (.zip on windows)
#	dist/nomi_<version>_js_wasm.tar.gz       (nomi-vm, nomi-vm-nonet, wasm_exec.js)
#	dist/nomi_<version>_wasip1_wasm.tar.gz   (nomi-vm, nomi-vm-nonet, nomi-vm-reactor, nomi-vm-reactor-nonet)
#	dist/nomi_<version>_<os>_<arch>_lib.tar.gz   (nomi-lib, nomi-vm-lib; .zip on windows)
#	dist/nomi_<version>_js_wasm_lib.tar.gz   (nomi-vm-lib, wasm_exec.js)
#	dist/nomi_<version>_checksums.txt
#
# `nomi build --target` downloads another platform's archive from the release
# and checks it against the checksum manifest.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module_dir="$repo_root"
dist_dir="${NOMI_RELEASE_DIST:-$repo_root/dist}"

version="${1:-}"
if [ -z "$version" ]; then
	version="$(git -C "$repo_root" describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

# The lib/ modules' version at this commit, as the Go module proxy names an
# untagged commit of a nested module: v0.0.0-<UTC commit time>-<12-hex sha>.
lib_version="v0.0.0-$(TZ=UTC git -C "$repo_root" log -1 --format=%cd --date=format-local:%Y%m%d%H%M%S)-$(git -C "$repo_root" rev-parse --short=12 HEAD)"

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

# With no `go` on PATH, `nomi` builds Go bindings with the Go release it was
# built with, downloaded from go.dev and checked against a checksum compiled
# into it (internal/gotoolchain/pinned_gen.go). A release built with a Go
# that file does not pin could not verify its own toolchain.
if ! go -C "$module_dir" run ./internal/gotoolchain/pingen -check; then
	echo "release.sh: the Go building this release has no pinned archive checksums" >&2
	exit 1
fi

# The lib runners' main packages must be what the lib/ modules generate.
if ! go -C "$module_dir" run ./internal/ffirun/libgen -check; then
	echo "release.sh: librunners/ is stale; run go run ./internal/ffirun/libgen" >&2
	exit 1
fi

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

# build_one builds package pkg of the module in dir as stage/name for
# $goos/$goarch, then checks what the artifact says about itself: its
# platform, its VCS stamp (the release's one), and, when check_version is
# true and HEAD carries the tag, its module version. Extra arguments go to
# go build before the package.
build_one() {
	local dir="$1" stage="$2" name="$3" pkg="$4" buildmode="$5" tags="$6" ldflags="$7" check_version="$8"
	shift 8
	echo "building $name for $goos/$goarch"
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -C "$dir" -trimpath -buildmode="$buildmode" -tags "$tags" "$@" -ldflags "$ldflags" \
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
	# tree and VCS stamping are both required), and `nomi` builds a
	# project's Go bindings against the compiler module at that version,
	# fetched through the Go toolchain. A release without it would treat
	# itself as a development build and refuse Go bindings. The lib
	# binaries' main module is librunners/, which the tag does not name.
	if [ "$at_tag" = "true" ] && [ "$check_version" = "true" ]; then
		mod_version="$(buildinfo "$out" | awk '$1 == "mod" { print $3; exit }')"
		if [ "$mod_version" != "$version" ]; then
			echo "release.sh: $out carries module version '$mod_version', not the tag $version; is the tree clean?" >&2
			exit 1
		fi
	fi
}

# lib_targets get a lib archive: every native target, and js/wasm.
lib_targets=("${targets[@]}" "js/wasm")

all_targets=("${targets[@]}" "${vm_targets[@]}")
built=0
for target in "${all_targets[@]}"; do
	goos="${target%%/*}"
	goarch="${target##*/}"
	ext=""
	if [ "$goos" = "windows" ]; then
		ext=".exe"
	fi

	stage="$dist_dir/nomi_${version}_${goos}_${goarch}"
	mkdir -p "$stage"

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
			ldflags="$ldflags -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=$version"
			ldflags="$ldflags -X github.com/nomi-language/nomi/internal/ffirun.LibVersion=$lib_version"
		fi
		buildmode="default"
		case "$name" in nomi-vm-reactor*) buildmode="c-shared" ;; esac
		# A -nonet runner is built without the network (nomi_nonet): nomi
		# build appends a program that reaches no network crossing to it.
		tags=""
		overlay=()
		case "$name" in
		*-nonet) tags="nomi_nonet" ;;
		nomi | nomi-lsp)
			tags="nomi_stdbundle"
			overlay=(-overlay "$bundle_dir/overlay.json")
			;;
		esac
		build_one "$module_dir" "$stage" "$name" "$pkg" "$buildmode" "$tags" "$ldflags" true ${overlay[@]+"${overlay[@]}"}
	done

	if [ "$goos" = "js" ]; then
		# The JS support file of the Go that just built nomi-vm: the same
		# `go` in the same module directory, so a toolchain line selects the
		# same toolchain.
		goroot="$(go -C "$module_dir" env GOROOT)"
		cp "$goroot/lib/wasm/wasm_exec.js" "$stage/wasm_exec.js"
		chmod 0644 "$stage/wasm_exec.js"
	fi

	archive_base="nomi_${version}_${goos}_${goarch}"
	if [ "$goos" = "windows" ]; then
		(cd "$stage" && zip -q -X "../$archive_base.zip" ./*)
	else
		tar -czf "$dist_dir/$archive_base.tar.gz" -C "$stage" .
	fi
	rm -rf "$stage"
	built=$((built + 1))

	case " ${lib_targets[*]} " in *" $target "*) ;; *) continue ;; esac
	lib_stage="$dist_dir/${archive_base}_lib"
	mkdir -p "$lib_stage"
	lib_specs=("nomi-lib:./nomi-lib" "nomi-vm-lib:./nomi-vm-lib")
	if [ "$goarch" = "wasm" ]; then
		lib_specs=("nomi-vm-lib:./nomi-vm-lib")
	fi
	for spec in "${lib_specs[@]}"; do
		# nomi-lib runs the front end, so it embeds the std bundle as nomi
		# does; the overlay names the compiler module's files, which
		# librunners/ replaces with this tree.
		if [ "${spec%%:*}" = "nomi-lib" ]; then
			build_one "$module_dir/librunners" "$lib_stage" nomi-lib ./nomi-lib default nomi_stdbundle "-s -w" false \
				-overlay "$bundle_dir/overlay.json"
		else
			build_one "$module_dir/librunners" "$lib_stage" "${spec%%:*}" "${spec##*:}" default "" "-s -w" false
		fi
	done
	if [ "$goos" = "js" ]; then
		cp "$(go -C "$module_dir" env GOROOT)/lib/wasm/wasm_exec.js" "$lib_stage/wasm_exec.js"
		chmod 0644 "$lib_stage/wasm_exec.js"
	fi
	if [ "$goos" = "windows" ]; then
		(cd "$lib_stage" && zip -q -X "../${archive_base}_lib.zip" ./*)
	else
		tar -czf "$dist_dir/${archive_base}_lib.tar.gz" -C "$lib_stage" .
	fi
	rm -rf "$lib_stage"
	built=$((built + 1))
done

# Anti-vacuity: a loop whose matrix went empty, or whose archive step silently
# skipped, would otherwise reach the checksum step and write an empty manifest
# that looks like a clean run.
expected=$((${#all_targets[@]} + ${#lib_targets[@]}))
if [ "$built" -ne "$expected" ]; then
	echo "release.sh: built $built archive(s), expected $expected" >&2
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
