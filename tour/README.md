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
