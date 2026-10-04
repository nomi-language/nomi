#!/usr/bin/env bash
# Build every WASM/asset the language-tour runtime needs into
# tour/.wasm-build/ (gitignored), then stages them into tour/public/nomi/:
#
#   nomi.wasm            VM + analyzer (GOOS=js GOARCH=wasm) — nomiRun + nomiSemTokens
#                        (staged for the site as nomi.wasm.gz; see below)
#   wasm_exec.js         Go's wasm glue
#   tree-sitter-nomi.wasm  the grammar (parser + scanner), via the tree-sitter CLI
#   highlights.scm       the grammar's highlight queries (same file Zed uses)
#   web-tree-sitter.{js,wasm}  the web-tree-sitter runtime, vendored from npm
#
# So the tour examples highlight from the exact grammar + queries Zed uses,
# with the analyzer's semantic tokens overlaid — no separate highlight definition.
set -euo pipefail

ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
OUT="$ROOT/tour/.wasm-build"
TS="$ROOT/tree-sitter-nomi"
WTS_VERSION="0.26.9"
MARKED_VERSION="12.0.2"
DOMPURIFY_VERSION="3.2.4"

# The stdlib binding registry used to be generated here by
# cmd/nomi-stdlibbindings. That generator is gone: `std/` declares `host fn`
# and its Go lives in sibling packages, so internal/stdlibbindings is
# hand-written and committed. Nothing to generate before the wasm build.
echo "==> nomi wasm"
# -buildvcs=false, and runtime/tour_bundle_test.go's rebuild MUST pass the same
# flag. Without it Go stamps vcs.revision and vcs.modified into the binary, so
# the artifact's bytes move on every commit and on every clean<->dirty
# transition even when no source the build reads has changed. MEASURED on one
# tree, only the working-tree state differing: plain gave 1846d0a3... then
# 18f1ccf5..., -buildvcs=false gave d236b169... both times. The gate compares
# the staged file against a rebuild, so with the stamp in place it went red
# after every commit. Nothing reads a VCS stamp out of a 25MB browser asset,
# and it need not publish the commit hash.
#
# -trimpath too, also matched by the rebuild. Without it the binary embeds
# the absolute path of every Go source file it links, from this checkout and
# from the module cache, so the published bundle carries the build machine's
# directory layout and home directory. With it those paths are module-relative
# (github.com/nomi-language/nomi/...), and the bytes no longer depend on where
# the checkout lives.
GOOS=js GOARCH=wasm go build -C "$ROOT" -trimpath -buildvcs=false -o "$OUT/nomi.wasm" ./cmd/nomi-wasm
# Declare a 1 GiB maximum on the module's memory (Go's linker declares none),
# so a visitor's program that allocates without bound fails with Go's "out of
# memory" instead of taking the tab to 4 GiB. See internal/wasmmem;
# vmhost/tour_bundle_test.go applies the same cap to its rebuild.
go run -C "$ROOT" ./internal/wasmmem/capwasm "$OUT/nomi.wasm"
# The glue must come from the toolchain that built nomi.wasm, which is the one
# go.mod selects, not whatever `go` resolves to in the caller's cwd.
GOROOT_WASM="$(go env -C "$ROOT" GOROOT)"
WASM_EXEC="$GOROOT_WASM/lib/wasm/wasm_exec.js"
[ -f "$WASM_EXEC" ] || WASM_EXEC="$GOROOT_WASM/misc/wasm/wasm_exec.js"
# install, not cp: a toolchain from the module cache ships the file read-only,
# and cp would carry that mode into $OUT and public/nomi/, so the next build's
# cp over it fails.
install -m 0644 "$WASM_EXEC" "$OUT/wasm_exec.js"

echo "==> grammar wasm + highlight queries"
# Use the committed grammar WASM instead of building it during deployment:
# recent tree-sitter-cli binaries can require a newer glibc than Cloudflare's
# build image, while older CLIs require Docker/Emscripten for WASM.
cp "$TS/tree-sitter-nomi.wasm" "$OUT/tree-sitter-nomi.wasm"
cp "$TS/queries/highlights.scm" "$OUT/highlights.scm"

echo "==> vendor web-tree-sitter@$WTS_VERSION, marked@$MARKED_VERSION, dompurify@$DOMPURIFY_VERSION"
VENDOR="$OUT/.vendor"
npm i --prefix "$VENDOR" --no-fund --no-audit --no-save \
  "web-tree-sitter@$WTS_VERSION" \
  "marked@$MARKED_VERSION" \
  "dompurify@$DOMPURIFY_VERSION" >/dev/null 2>&1
cp "$VENDOR/node_modules/web-tree-sitter/web-tree-sitter.js" "$OUT/"
cp "$VENDOR/node_modules/web-tree-sitter/web-tree-sitter.wasm" "$OUT/"
# marked + DOMPurify drive the hover tooltip's markdown rendering — see
# `syntax()` in tour-client.mjs. Vendored locally (not CDN) so the tour
# works offline and isn't subject to a third-party network dependency.
cp "$VENDOR/node_modules/marked/lib/marked.esm.js" "$OUT/marked.esm.js"
cp "$VENDOR/node_modules/dompurify/dist/purify.es.mjs" "$OUT/purify.es.mjs"

# Generate the stdlib API reference (tour/src/content/docs/reference/) from
# the stdlib's /// doc comments — Nomi's godoc. Gitignored; regenerated here.
echo "==> generate stdlib reference (cmd/nomi-docgen)"
REF_DIR="$ROOT/tour/src/content/docs/reference"
mkdir -p "$REF_DIR"
rm -f "$REF_DIR"/*.md
go run -C "$ROOT" ./cmd/nomi-docgen "$REF_DIR"

# Stage assets for the Starlight tour (tour): built wasm/grammar from $OUT,
# plus the committed client JS from the site's src/lib. The Starlight build
# serves these statically from public/nomi/ (gitignored) — the editable,
# run-on-type examples load from there (see tour/src/lib/tour-client.mjs).
echo "==> stage assets for the Starlight tour (tour)"
SITE_NOMI="$ROOT/tour/public/nomi"
SITE_LIB="$ROOT/tour/src/lib"
mkdir -p "$SITE_NOMI"
# nomi.wasm ships GZIPPED as nomi.wasm.gz, and the uncompressed file is never
# staged. Cloudflare Pages refuses any file over 25 MiB; the wasm is about
# 27 MiB (26.6 MiB even with -ldflags='-s -w') and about 6.4 MiB
# gzipped. The browser decompresses it itself (run-worker.js), so nothing
# depends on the host setting Content-Encoding. -n drops the name and mtime
# from the header, so the same wasm always gives the same .gz. The
# uncompressed build stays in $OUT. `npm run build` (check-dist.mjs) fails on
# any file over 25 MiB and on an uncompressed nomi.wasm in dist/.
rm -f "$SITE_NOMI/nomi.wasm"
gzip -9 -n -c "$OUT/nomi.wasm" > "$SITE_NOMI/nomi.wasm.gz"
cp "$OUT/wasm_exec.js" "$OUT/tree-sitter-nomi.wasm" \
   "$OUT/web-tree-sitter.wasm" "$OUT/web-tree-sitter.js" "$OUT/highlights.scm" \
   "$OUT/marked.esm.js" "$OUT/purify.es.mjs" "$SITE_NOMI/"
cp "$SITE_LIB/highlight.mjs" "$SITE_LIB/run-worker.js" "$SITE_LIB/tour-client.mjs" "$SITE_NOMI/"

echo "done → $OUT (+ $SITE_NOMI)"
