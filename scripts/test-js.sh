#!/bin/sh
# test-js.sh runs Go tests built for js/wasm, the build the browser and the
# tour run, under Node (20 or later, on PATH). It takes go test's
# arguments:
#
#   scripts/test-js.sh ./internal/stdwasm -run '^Test' -count=1
#
# Go's own runner for js/wasm tests, go_js_wasm_exec, starts Node with
# --stack-size=8192, past the stack the main thread has, so a test whose
# WebAssembly overflows the stack crashes Node instead of throwing; and
# Go's loader refuses an environment of more than about 12 KB
# (internal/nodeenv). This script is the -exec that runs each test binary
# with Node's own stack size and an environment of PATH, HOME, TMPDIR and
# the NOMI_ variables.
set -eu

if [ "${1-}" = "--exec" ]; then
	shift
	loader="$(go env GOROOT)/lib/wasm/wasm_exec_node.js"
	set -- node "$loader" "$@"
	for kv in $(env | grep '^NOMI_' || true); do
		set -- "$kv" "$@"
	done
	exec env -i PATH="$PATH" HOME="${HOME-}" TMPDIR="${TMPDIR-/tmp}" "$@"
fi

self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
# go test takes -C only as its first flag.
if [ "${1-}" = "-C" ]; then
	dir=$2
	shift 2
	set -- -C "$dir" -exec "$self --exec" "$@"
else
	set -- -exec "$self --exec" "$@"
fi
exec env GOOS=js GOARCH=wasm go test "$@"
