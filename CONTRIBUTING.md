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

```sh
go build -o nomi ./cmd/nomi   # the CLI, at ./nomi (gitignored)
go install ./cmd/nomi         # `nomi` on your PATH, with the language server as `nomi lsp`
```

## Test

```sh
go test ./... -count=1         # compiler, VM, LSP, runtime library
go test ./rt/... -count=1      # the runtime library on its own
./nomi test tests
./nomi test std
```

The VM's output is checked against golden files in
`testdata/expectations/`. If your change moves one, regenerate it
(the command is in AGENTS.md) and say why each record moved in the commit
message.

## Environment variables for work on the compiler

These variables change what `nomi` does, for work on the compiler and the
VM. The ones a user of `nomi` sets are in the tooling reference's
"Environment variables" ([`docs/tooling.md`](docs/tooling.md)).

| Variable | What it does |
|---|---|
| `NOMI_DEBUG_VERSION` | Any value but `0` adds the version of the Go toolchain that built `nomi` to the line `nomi --version` and the REPL print: `nomi 0123456789ab (go1.27.0 darwin/arm64)`. |
| `NOMI_RELEASE_BASE_URL` | For tests. Where a release `nomi build` downloads the runners it lacks from, in place of the project's GitHub releases. |
| `NOMI_DEBUG_STD_BUNDLE` | `1` reports on stderr why a cached standard library lowering or its cached interfaces were not used. |
| `NOMI_STD_WHOLE` | `1` builds every standard library module when a command starts, as the language server does, rather than only the modules the program imports. |
| `NOMI_STD_STATE` | `off` builds the standard library's base modules instead of reading them back from the state a previous run kept, and keeps none. |
| `NOMI_STD_PATH` | The directory of standard library sources `nomi` reads, in place of the one it finds beside itself or in the checkout it was built from. |
| `NOMI_CHECK_FROM_INTERFACES` | `1` checks every project file again against the interfaces of the files it imports, and fails the program where the two checks differ. |
| `NOMI_SUBST_VERIFY` | Any value holds each type a generic instance's frame carries to the kind the compiler lowers it at, as every test binary does. |
| `NOMI_CALLEE_LOG` | A file that each difference between the function a call lowers to and the one the checker recorded is written to. |
| `NOMI_LINK_OBJECTS` | `1` links every program through objects encoded a module at a time; `perfile` lowers each file on its own and compares it with the whole lowering. |
| `NOMI_PERFILE_REPORT` | With `NOMI_LINK_OBJECTS=perfile`, `1` reports each file's comparison on standard error. |
| `NOMI_NO_INLINE` | Any value stops the VM compiling a small function's code into its callers. |
| `NOMI_NO_INPLACE` | Any value makes every update copy its value rather than change it in place. |
| `NOMI_NO_LOOP_JUMP` | Any value compiles each self tail call as a call rather than a jump. |
| `NOMI_NO_FAST_READS` | Any value sends each read the VM answers on a fast path, such as `Vector.get`, through its plain crossing. |
| `NOMI_NO_FUSED_DEFAULT` | Any value makes each `Option.with_default` and `Result.with_default` call its intrinsic rather than the fused form. |
| `NOMI_CPUPROFILE` | A file that `nomi run`, and a `nomi build` executable, write a Go CPU profile of the run to, for `go tool pprof <nomi or the executable> <file>`. The frames are the VM's Go functions, not the program's Nomi functions. |

## Format

Run `nomi fmt -w <paths>` on any `.nomi` file you edit, and `gofmt` on Go
files.

## Where things are written down

- [Language spec](docs/spec.md): canonical for what
  the language does. Update it in the same change as the feature.
- [Style guide](docs/style.md): how to write idiomatic Nomi.
- [Feature status](docs/feature-status.md): aspirational spec
  sections, gaps, tooling capabilities and known defects.
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

```sh
./scripts/release.sh v0.1.0
```

It writes `dist/nomi_<version>_<os>_<arch>.{tar.gz,zip}`, which holds
`nomi` alone, carrying the platform's runners; beside each a `_runners`
archive of those runners, for `nomi build --target` on another platform,
and a `_compiler` archive of std::compiler's runners; and
`dist/nomi_<version>_checksums.txt` (`NOMI_RELEASE_DIST=<dir>` writes them
elsewhere, and `NOMI_RELEASE_TARGETS="darwin/arm64"` builds only the
targets it lists, for a dry run). The version must be the release's tag: it is
stamped into `nomi` (`-X github.com/nomi-language/nomi/internal/runners.ReleaseVersion=<version>`), and
`nomi build` downloads a runner it does not have from that tag. The script
reads each binary's platform, build tags and commit back from its build
information and fails if they disagree with what it built or with each
other.

## License

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as in [README.md](README.md#license), without
any additional terms or conditions.
