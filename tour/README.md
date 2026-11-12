# Nomi Lang

This is the public static documentation site for Nomi. It is an Astro
Starlight site with client-side runnable examples powered by the Nomi VM built
to WASM, the tree-sitter grammar, and the same hover/highlight assets used
by the editor integration.

## Local Development

From the repository root:

```sh
make start-tour
```

Open <http://localhost:4321/>.

Use Node 22 or newer. The repository `Makefile` prepends Homebrew's Node 22
path by default on macOS; in other environments, make sure `node` and `npm`
already point at Node 22+.

## Production Build

From the repository root:

```sh
make deploy-tour
```

The deployable static site is written to:

```txt
tour/dist
```

That directory is the only thing a static host needs to serve. It includes the
Astro/Starlight HTML, the generated stdlib reference, the `/nomi/` runtime
assets, and the `_headers` file copied from `public/_headers`, plus
`sitemap-index.xml` / `sitemap-0.xml`.

The Nomi wasm ships gzipped, as `/nomi/nomi.wasm.gz` (about 6.5 MiB), and
`run-worker.js` decompresses it in the browser with `DecompressionStream`.
Cloudflare Pages refuses any file over 25 MiB, and the uncompressed module is
about 27 MiB. The site does not depend on the host setting `Content-Encoding`.
`npm run build` ends with `check-dist.mjs`, which fails the build if any file
in `dist/` is over 25 MiB or if an uncompressed `nomi.wasm` is in it.

The build copies the committed grammar artifact at
`tree-sitter-nomi/tree-sitter-nomi.wasm`. After changing the Tree-sitter
grammar, regenerate that file locally before deploying:

```sh
make build-tour-grammar-wasm
make deploy-tour
```

Commit the updated `tree-sitter-nomi/tree-sitter-nomi.wasm` along with the
grammar source changes before pushing.

## How the site runs examples

A ` ```nomi-run ` block becomes a live editor. The authoring rules (fence
words, expect blocks, the Examples pages) are in the repository's
`AGENTS.md`. The Go side is documented in the packages that run blocks:
`vmhost.LoadMarked` and `vmhost.BlockFiles` (a block's files, data files,
modules and sibling projects), `vmhost.ToolMarked` (`tool:` steps),
`internal/tourgo` (Go bindings and the `lib/` modules the tour links) and
`cmd/nomi-docgen` (the stdlib reference's editors). This section is the
browser side.

**Recorded output.** A page runs nothing when it loads. Until a block's
first run its output panel shows its expect block, over the files as the
block holds them, and the page asks the worker only for the analyzer's
tokens. The build carries the expect text in the page beside the block's
code (`src/lib/rehype-shiki.mjs`, empty for a block with no expect block).
The reader's first edit runs the block. A `manual` block runs only on its
Run button, labelled "Recorded output. Press Run to run it." until then. A
`no-run` block runs when it scrolls into view, as a stdlib reference example
does. The panel shows the record as a run's output: an error block's failure
is its last line, or its last two when the last is `exit status N`, or all of
it when its first line names a file, line and column (`showRecorded` in
`src/lib/tour-client.mjs`, which vmhost's `recordedFailure` mirrors).
`astro.config.mjs` passes a hash of `rehype-shiki.mjs` as the plugin's
option, so an edit to it clears Astro's cache of rendered pages.

**The worker.** `src/lib/run-worker.js` runs the Nomi wasm. It lowers the
standard library (`nomiWarm`, `vmhost.Warm`) before its first run, inside the
page's load allowance (`LOAD_BUDGET_MS`) and outside the run's budget; tokens,
hovers and the formatter need only the analysis, so a page that is only read
never lowers it. The wasm reaches nothing outside the worker: std/fs goes to
an in-memory file system (`src/lib/go-memfs.js`), and the stdlib has no
networking module there. Memory is capped at 1 GiB and call depth at 1,000.
The worker starts the wasm with `GODEBUG=gcstoptheworld=1`, for the reason its
comment gives: js/wasm has one thread and no preemption, so without it a busy
machine lets the heap grow without bound.

**The live editor.** A block of up to four files, none under a directory,
picks files from a tab strip; a larger one, or one with a file under a
directory, from a file tree beside the editor. Every runnable block has an
"Open large" button that moves the editor itself into a modal `<dialog>` and
back, so its edits and output carry over. In the large view a block's files
can be split into two groups of tabs, and the output can move below the code.
A block that is a solution and its test opens the first time, in a large view
at least 1100px wide, with the two side by side. A file is editable only when
an edit can change what the block prints (`readOnlyReason`): Go files,
`go.mod`, `go.sum`, a `nomi.toml` whose only table is `[module]`, and data
files a `read-only:` word marks are locked. Highlighting, hovers and runs use
the same grammar, queries, analyzer and VM the editors use, compiled to wasm.

**Tests.** `npm --prefix tour test` loads recorded, manual and unrecorded
blocks into headless Chrome with a fake worker and holds them to the rules
above (`test/`; it skips without Chrome or the staged `public/nomi/`).

## Cloudflare Pages

Cloudflare Pages settings:
```txt
Framework preset: None
Build command: make deploy-tour
Build output directory: tour/dist
Root directory: /
```

Environment variables:

```txt
NODE_VERSION=22
GO_VERSION=1.27.0
```

`GO_VERSION` should match the `go` line of `go.mod`. An older Go
still builds, because Go downloads the toolchain `go.mod` names
(`GOTOOLCHAIN=auto`), but that adds a download to every deploy.

The build command intentionally runs from the repository root because it needs
both the Go module at the root and the Astro site under `tour/`.

### Site origin

`astro.config.mjs` sets `site` from `SITE_URL`, defaulting to
`https://nomi-lang.org`, where the tour is served. It is not cosmetic:
`@astrojs/sitemap` (which Starlight registers automatically) skips itself with
only a build warning when `site` is unset, and the sitemap's URLs use it. Set
the variable only to build for another origin:

```txt
SITE_URL=https://example.org
```

## Notes

The site is static, but the runnable examples need WASM. Keep
`public/_headers` in place so hosts that support the `_headers` convention apply
the CSP and security headers to the built site.

`public/_headers` is not advisory. `csp-audit.mjs` runs at the end of
`npm run build` and fails the build when the built HTML and the CSP disagree:
the policy allows inline `<script>` by sha256 hash rather than
`'unsafe-inline'`, and all nine hashes belong to Starlight boot scripts we do
not author. An upgrade that changes one of those scripts would otherwise
break the theme picker, mobile menu or sidebar state on the deployed site and
nowhere else. The failure prints the corrected `script-src` to paste in. Run
it standalone against any built directory with:

```sh
node csp-audit.mjs dist
```
