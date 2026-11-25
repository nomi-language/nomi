#!/bin/sh
# Remove what older install instructions left on this machine: the binaries
# that `go install ./cmd/...` once put beside `nomi` and that nothing uses
# now. `make clean-legacy` runs it, and `make use-release` runs it first.
#
#   nomi-lsp     the language server, which is `nomi lsp` now; an editor
#                configuration that still launches it runs an old server
#   nomi-vm      a runner; `nomi build` uses the one a release carries, or
#                builds one, and never a runner on PATH from another commit
#   nomi-runner  nomi-vm's earlier name
#   nomi-docgen  the docs generator, run with `go run` in a clone
#
# It looks in Go's bin directory only ($GOBIN, else $GOPATH/bin, else
# ~/go/bin), removes files of exactly these names, and prints each one. It
# leaves `nomi` itself: the install script's replace mode removes a build
# from source (scripts/install.sh, NOMI_REPLACE). Editor files written by
# older `make install-*` targets are where `nomi editor` looks today, so
# `nomi editor install --installed --force` replaces them.
#
# This script goes when no install from before the first release is left.
set -eu

bin="${GOBIN:-}"
if [ -z "$bin" ] && command -v go >/dev/null 2>&1; then
	bin="$(go env GOBIN 2>/dev/null || true)"
	if [ -z "$bin" ]; then
		gopath="$(go env GOPATH 2>/dev/null || true)"
		[ -n "$gopath" ] && bin="${gopath%%:*}/bin"
	fi
fi
[ -n "$bin" ] || bin="$HOME/go/bin"

removed=0
for name in nomi-lsp nomi-vm nomi-runner nomi-docgen; do
	for file in "$bin/$name" "$bin/$name.exe"; do
		if [ -f "$file" ] || [ -L "$file" ]; then
			if rm -f "$file"; then
				echo "clean-legacy: removed $file"
				removed=$((removed + 1))
			else
				echo "clean-legacy: could not remove $file" >&2
			fi
		fi
	done
done
[ "$removed" -gt 0 ] || echo "clean-legacy: nothing to remove in $bin"
