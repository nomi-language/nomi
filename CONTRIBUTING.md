# Contributing to Nomi

Nomi is not accepting pull requests yet. The language and compiler still
change too quickly for an outside change to land without being reworked.
Bug reports, questions and feedback are welcome as
[issues](https://github.com/nomi-language/nomi/issues). The rest of this file
is how to build and test Nomi from source.

Nomi is early and pre-release. The language changes freely, with no
compatibility promise yet, so expect breaking changes and prefer the better
design over the backward-compatible one.

[`AGENTS.md`](AGENTS.md) is the full working guide to this repository
(architecture, verification cadence, grammar workflow, conventions). It is
written for human contributors and coding agents alike. This file is the short
version.

## Prerequisites

- Go (the version on the `go` line of `go.mod`; with `GOTOOLCHAIN=auto` an
  older Go downloads it).
- For the tour site: Node 22 or newer.
- For grammar work: Node (for the tree-sitter CLI via `npx`); for the Zed
  extension build, Rust with the `wasm32-wasip2` target.

## Build

```
go build -o nomi ./cmd/nomi   # the CLI, at ./nomi (gitignored)
go install ./cmd/nomi         # `nomi` on your PATH
go install ./cmd/nomi-lsp     # the language server
```

## Test

```
go test ./... -count=1         # compiler, VM, LSP, runtime library
go test ./rt/... -count=1      # the runtime library on its own
./nomi test tests
./nomi test std
```

The VM's output is checked against golden files in
`testdata/expectations/`. If your change moves one, regenerate it
(the command is in AGENTS.md) and say why each record moved in the commit
message.

## Format

Run `nomi fmt -w <paths>` on any `.nomi` file you edit, and `gofmt` on Go
files.

## Where things are written down

- [Language spec](docs/spec.md): canonical for what
  the language does. Update it in the same change as the feature.
- [Style guide](docs/style.md): how to write idiomatic Nomi.
- [Feature status](docs/feature-status.md): compiler and tooling
  status, known defects.
- [Roadmap](docs/roadmap.md): the larger work ahead.
- [Tour](tour/): the documentation site; see its README to run
  it locally.

## Changes

Keep each commit one coherent change with its tests. Describe what changed
and why in the commit message. For a change to the grammar, follow the
checklist in AGENTS.md: the grammar feeds the Zed, Helix, Neovim and bat
syntax definitions, and the tour's grammar wasm.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`, which runs
`scripts/release.sh <tag>` and attaches its output to a GitHub release. The
script can be run by hand too:

```
./scripts/release.sh v0.1.0
```

It writes `dist/nomi_<version>_<os>_<arch>.{tar.gz,zip}` and
`dist/nomi_<version>_checksums.txt` (`NOMI_RELEASE_DIST=<dir>` writes them
elsewhere, for a dry run). The version must be the release's tag: it is
stamped into `nomi` (`-X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=<version>`), and
`nomi build --target` downloads other platforms' runners from that tag. The
script reads each binary's platform and commit back with `go version -m` and
fails if they disagree with the target or with each other.

## License

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as in [README.md](README.md#license), without
any additional terms or conditions.
