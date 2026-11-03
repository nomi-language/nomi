# Nomi: guide for contributors and coding agents

Nomi is a fully immutable, functional-first, statically typed language
implemented in Go. This file is the working guide to the repository: its
layout, how the pieces fit, how to build and test, and the conventions changes
are held to. Human contributors should also read
[`CONTRIBUTING.md`](CONTRIBUTING.md).

The canonical references:

| Document | What it owns |
|---|---|
| [`docs/spec.md`](docs/spec.md) | The spec: what the language does. A feature there without a `Status: not yet implemented` callout works. |
| [`docs/style.md`](docs/style.md) | How to write Nomi well (idiom, not semantics). |
| [`docs/cheatsheet.md`](docs/cheatsheet.md) | The rules most often written wrong, with checked examples: the short form for a reader or a model. |
| [`docs/tooling.md`](docs/tooling.md) | The `nomi` command and its flags, `nomi.toml`, modules and dependencies, the test runner, `nomi fmt`'s layout, builds, Go bindings, environment variables, editors and the debugger. |
| [`docs/implementation-notes.md`](docs/implementation-notes.md) | How the implementation is built, subsystem by subsystem, and the test harnesses that hold it. Read the section for the area you work in. |
| [`docs/feature-status.md`](docs/feature-status.md) | What is not built yet: aspirational spec sections, gaps in shipped features, tooling capabilities, known defects. |
| [`docs/roadmap.md`](docs/roadmap.md) | Open big-ticket work and its order. Done items leave it. |
| [`docs/install.md`](docs/install.md) | Installing from source; published as the Tooling section's first page. |

## Pre-release: no compatibility

Nomi is unreleased and nobody depends on it yet. A change needs no deprecation
path, migration note or compatibility shim. If the current design is wrong,
change it. Living docs (the spec, the tour, feature-status, this file) and code
comments describe current reality only: never "formerly X" or "renamed from Y".
History belongs in git.

## Repository layout

One repository holds the language and its editor support. Keep it together.
The repository root is the compiler's Go module,
`github.com/nomi-language/nomi`.

| Path | What it is |
|---|---|
| `cmd/` | Binaries: `nomi` (the CLI), `nomi-lsp`, `nomi-vm` (the VM binary: what `nomi build` appends a program to, and what runs a `.nomic` file), `nomi-vm-reactor` (the wasip1 reactor a program with an `export fn` is appended to; with none appended it loads a `.nomic` through `nomi_load`), `nomi-wasm` (the browser build), `nomi-docgen` (stdlib reference). |
| `vmhost/` | The public embedding API. |
| `vmrunner/` | What `nomi-vm` and a built executable run. Generated FFI runners import it. |
| `hostadapt/` | Support package that generated FFI wrappers import. |
| `internal/` | Everything else. Syntax: `lexer`, `parser`, `ast`, `token`, `strlit`. `analysis` (resolution and the type checker), `frontend` (the whole front end as one call), `dap` (`nomi dap`), `mcp` (`nomi mcp`, the compiler as Model Context Protocol tools), `irbuild` (IR builder), `ir` (IR, lint, image encoding), `vm` (bytecode VM), `lsp`, `format`, `ffirun` (Go FFI wrapper and runner builds), `gotoolchain` (the `go` builds run, and the managed toolchain), `modcache` and `modfetch` (dependencies), `std*` (Go-backed stdlib adapters), `mf2` (MessageFormat 2) and `catalog` (message catalogs), and the tooling behind them. |
| `std/` | The standard library's `.nomi` sources, embedded into the binaries. |
| `lib/` | First-party Nomi modules outside std, each its own nested Go module with its own `go.mod` and `nomi.toml`: `lib/sql/sqlite` (the SQLite driver), `lib/sql/postgres` (the Postgres driver), `lib/sql/migrate` (migrations), `lib/sql/sqlkit` (checked SQL), `lib/sql/examples/shop`, `lib/markdown` (Markdown to std/html's `Html`, over goldmark), `lib/image` (decoding, resizing and encoding JPEG, PNG and WebP, over golang.org/x/image), `lib/jobs/postgres` (std/jobs's queue in a Postgres table), `lib/jobs/examples/notify`, `lib/storage/s3` (std/storage's backend for S3-protocol servers), `lib/storage/minio` (its tests against a MinIO server). The compiler's module neither contains nor requires them. |
| `rt/` | The runtime library, `github.com/nomi-language/nomi/rt`. The VM runs over it and built executables link it. It may import only the standard library and `github.com/rivo/uniseg` (`TestRuntimeImportsOnlyItsAllowlist`). |
| `docs/` | The spec, style guide, implementation notes, feature status, roadmap and install guide. |
| `tour/` | The Astro Starlight site at nomi-lang.org: the tour and the generated stdlib reference. |
| `tests/` | The test corpus. |
| `testdata/expectations/` | Golden files. |
| `benchmarks/` | Engine benchmark programs. |
| `tree-sitter-nomi/` | The tree-sitter grammar, source of truth for editor parsing. The generated `src/` and `tree-sitter-nomi.wasm` are committed. Published read-only to github.com/nomi-language/tree-sitter-nomi by `scripts/publish-grammar.sh`. |
| `editors/zed/` | Zed extension (highlighting, LSP launch, the `nomi dap` debug adapter and a locator for the run and test tasks; `cargo test --manifest-path editors/zed/Cargo.toml` tests the locator). |
| `editors/helix/` | Helix language config and runtime queries. |
| `editors/nvim/` | Neovim 0.11+ plugin: filetype, LSP config, generated queries, test runner, neotest adapter, nvim-dap adapter. |
| `editors/vscode/` | VS Code extension (TypeScript): language configuration, a TextMate grammar derived from the bat syntax, the `nomi-lsp` client, run and test commands, the `nomi` debugger. |
| `editors/bat/` | `bat`/syntect syntax definition. |
| `scripts/` | Tour wasm build, release, grammar publish, full check, publish to `main`, and editor sync scripts. |

## Architecture in brief

Every way of running Nomi uses the same pipeline:

```
source → front end (parse, analyze, type-check) → internal/irbuild → IR
       → internal/vm (compiles each ir.Func to bytecode on first call) over rt values
```

`nomi run`, `nomi test`, the REPL, the tour and `nomi build`'s executables all
run on the VM. There is no interpreter and no Go backend. `nomi fmt` executes
nothing. `nomi check` lowers the program as `nomi run` does and reports each
body the run would refuse as an error at its source; the language server does
the same in the background. Details:
[engine](docs/implementation-notes.md#the-engine-and-nomi-check).

- **Front end.** `internal/frontend` parses, expands `message` and `noun`
  declarations, lowers derives and runs whole-program analysis
  (`internal/analysis`). Imports may be cyclic. Nomi has no macros; a
  library that generates code ships a `nomi tool`. Details:
  [front end](docs/implementation-notes.md#front-end),
  [keyword declarations](docs/implementation-notes.md#keyword-declarations-and-their-inputs).
- **Language server analysis.** `analysis.DocumentManager` keeps a checked
  program per project root and entry and rechecks edits incrementally, to
  the answer a full check gives. Details:
  [language server analysis](docs/implementation-notes.md#language-server-analysis).
- **IR builder.** `internal/irbuild` lowers every body a program runs into an
  `ir.Func`; a program that reaches a body it declines fails before its first
  effect. No Go text or Go names in the builder. Details:
  [IR builder](docs/implementation-notes.md#ir-builder).
- **VM and rt.** `internal/vm` compiles each `ir.Func` to register bytecode
  over `rt` values; `rt` holds collections, exact Int arithmetic, hashing,
  rendering and tasks. Neither links the front end. Details:
  [VM](docs/implementation-notes.md#vm), [rt](docs/implementation-notes.md#rt),
  [debugging and `nomi dap`](docs/implementation-notes.md#debugging-and-nomi-dap).
- **Stdlib.** `std/` is one embedded module, analyzed and lowered once per
  process except `programStdModules`. A `host fn` is a VM intrinsic or an
  adapter generated from `internal/stdlibbindings`. `lib/` holds first-party
  modules that need third-party Go code. Details:
  [stdlib](docs/implementation-notes.md#stdlib),
  [std and lib](docs/implementation-notes.md#std-and-lib),
  [host functions](docs/implementation-notes.md#host-functions),
  [files](docs/implementation-notes.md#files),
  [processes](docs/implementation-notes.md#processes),
  [HTTP, TLS and TCP](docs/implementation-notes.md#http) (`std/http`,
  `std/tls` and `std/net` over `internal/stdhttp`),
  [logging and actors](docs/implementation-notes.md#logging-and-actors)
  (`std/log`, `std/log_testing` and `std/actors`, written in Nomi alone).
- **Locale data and collation.** CLDR data from `internal/cldrgen` in
  `internal/cldrfull`, cut per program to its listed locales;
  `internal/collation` is a UCA collator over it. Details:
  [locale data](docs/implementation-notes.md#locale-data),
  [collation](docs/implementation-notes.md#collation).
- **Embedding, Go FFI and modules.** `vmhost` is the public Go API that the
  CLI, REPL, tour wasm and FFI wrapper use. `internal/ffirun` builds a cached
  Go wrapper for `go` bindings; `internal/modcache` and `internal/modfetch`
  find and fetch dependencies with no `go` command. Details:
  [embedding](docs/implementation-notes.md#embedding),
  [Go FFI](docs/implementation-notes.md#go-ffi-wrapper),
  [modules](docs/implementation-notes.md#modules).
- **`nomi build` and the REPL.** `nomi build` appends a pruned IR image to
  `nomi-vm` or the wasip1 reactor, or writes a browser folder or a `.nomic`.
  Each runner has a variant built with the `nomi_nonet` tag
  (`nomi-vm-nonet`), with no net/http, crypto/tls or net, which a program
  that reaches no network crossing is appended to; `go generate
  ./internal/stdlibadapters` writes the split. The REPL links each input
  into one live VM. Details:
  [`nomi build`](docs/implementation-notes.md#nomi-build),
  [REPL](docs/implementation-notes.md#repl).

## Build

From the repository root:

```
go build -o nomi ./cmd/nomi   # local CLI at ./nomi (gitignored)
go install ./cmd/nomi         # `nomi` on PATH
go install ./cmd/nomi-lsp     # the language server editors launch
make build                    # all of the above plus tour wasm and editor queries
```

The stdlib is embedded with `//go:embed`, so after editing `std/`
rebuild both `nomi` and `nomi-lsp`, and restart the running language server
(reinstalling the Zed dev extension restarts it). When the server has no
source tree to point at, go-to-definition into std opens the embedded std
written to `~/.cache/nomi/std/<version>/`, one directory per std version
(`internal/stdcache`), so an old server still running never shares files with
a new one. A version directory unused for 14 days is removed by the next
server that starts navigating. Tests never write there: `NOMI_STD_CACHE_ROOT`
replaces `~/.cache/nomi`, and under `go test` without it the files go under
the system temp directory.

## Test

```
go test ./... -count=1         # everything, rt included
go test ./rt/... -count=1      # the runtime library on its own
make test                      # stdlib tests, corpus, then go test
make test-tour                 # tour doctests, wasm bundle, grammar
make check-full                # the full check before publishing to main
```

`nomi test <dir or file>` runs `test "…"` blocks. `TestTestCommand_VMCorpus`
and `TestTestCommand_VMStdlib` run `nomi test tests` and `nomi test std` and
require no failed or blocked case, and as many passing cases as
`corpus.expect` and `stdlib.expect` declare. A case is a test, or each row of
the `subtests` tables a test runs (`rt.CountCases`), so a test of nine rows
counts nine. The modules under `lib/` are Go modules of their own, which root
`go test ./...` and `go vet ./...` skip; `TestLibModules` (`cmd/nomi`) tests
them.

**Harnesses.** Fuzzers and tests hold the implementation to properties: a
program the front end accepts lowers (`FuzzFrontEndAcceptsSoItLowers`), one
that lowers runs (`FuzzLoweredProgramsRun`), `nomi fmt` keeps meaning
(`FuzzFormatKeepsMeaning`), an incremental check matches a full one
(`FuzzIncrementalMatchesFull`), the language server survives typing
(`FuzzLSPSurvivesEdits`), and others for the feature × context matrix,
debug machines, load order, typed expressions, decline sites and scale.
Each, with its commands and environment variables, is in
[implementation-notes.md, "Test harnesses"](docs/implementation-notes.md#test-harnesses).

A test about how a blocked program is reported blocks on a planted decline (a
call to `planted_decline`, which `irbuild.PlantDeclineForTest` makes the
builder decline), never on a real gap, which a fix would close. A known gap
is pinned in its harness's `known...Gaps` list by a reproducer, and a fix
removes the entry in the same change.

**Rotating sets.** The `...Rotating` tests check, beside their fixed
inputs, a set of inputs from a start seed that changes every UTC day. The
log names the seed; `NOMI_GEN_SEED=<n>` pins it. A failure there says it came
from the rotating set and prints a command that checks exactly that input. It
may be a bug that was already there and that day's inputs found, not one the
change under test made: check it on `next` before blaming the change, then fix
it or pin it as a known gap.

**CI and Windows.** CI (`.github/workflows/test.yml`) runs vet and the Go
tests, including on Windows with `core.autocrlf` off. A test that builds an
executable and runs it names it with `exe()` (`cmd/nomi/main_test.go`), since
Windows runs only a `.exe`, and a test that cannot hold on Windows skips there
with its reason. A file URI goes through `internal/fileuri`, never
`"file://" + path`.

**Verification cadence.** Full runs happen once, right before a batch is
squashed to `main`, not per change. The whole `go test ./internal/irbuild
-count=1 -timeout 0` run takes about 150 seconds and `go test ./...` about
five minutes, so neither is a per-change check:

- **Per change, including an agent's change in its own worktree:** `go build`,
  `go vet ./...`, and the tests of the packages you touched, with narrow `-run`
  filters over the tests that exercise the change (for example
  `go test ./lsp ./analysis`, or
  `go test ./internal/irbuild -run 'AppField|Scoped'`), plus
  `go test ./rt/...` if `rt` changed. Do not run the whole irbuild package,
  `go test ./...` or the fuzzers per change, even for a change to what the
  front end accepts; say in your report which wider checks you skipped.
- **Before squashing a batch to `main`:** `make check-full`, once
  (`scripts/check-full.sh`: gofmt, the tour wasm, build and vet, every package with
  vmhost, rt and the whole irbuild package each on its own, `TestLibModules`,
  the tree-sitter tests, the race detector, and each fuzz target for
  `FUZZTIME`, 5m by default; `STEPS="pkgs fuzz-FuzzFormatKeepsMeaning"` runs
  only the steps named). It prints each step's exit status and fails when any
  failed. A failure there is attributed to the batch's changes by rerunning
  narrow tests on each. Then `make publish-main` (see "Docs discipline").
- When running several suites, capture each exit status separately; a chain
  reports only the last one.
- For concurrency changes, also run
  `go test -race ./internal/vm ./vmhost ./rt/...`.
- `rt`'s supervisor and timer tests and `vmhost/synctest_test.go` carry
  wall-clock assertions. Re-run a failure in isolation before attributing it.
- For language server changes, run `TestLSPSurvivesTyping`
  (`internal/lsp/typing_test.go`).

**Golden files.** `testdata/expectations/*.expect` hold the
normalized output, exit status and case count of every corpus file, stdlib
module, tour block, deliberately failing fixture, and every program an
`internal/irbuild` test runs, and `reference.expect` lists every stdlib
reference editor (a `//!` prompt as its reference page renders and runs it)
with its outcome. The VM's output must match them byte for byte.
Regenerate with:

```
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/expectation -run 'TestExpectation_(Corpus|Stdlib|Tour|Failure)$' -count=1
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -count=1 -timeout 0
NOMI_REGENERATE_EXPECTATIONS=1 go test ./cmd/nomi-docgen -run TestReferenceInteractiveTestsOnTheVM -count=1
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/vm -run 'TestVMExpectation_TheVMIsComparedAgainstTheCommittedArtifacts|TestVMReport_TheVMIsComparedAgainstTheReportShapedRecords' -count=1
```

Run the last after the `internal/expectation` command, since it reads
`corpus.expect`. The other golden files (`vm-corpus-programs.txt`,
`vm-retained.txt`, `vm-std-retained.txt`, the debug-session transcripts) are
described in
[implementation-notes.md](docs/implementation-notes.md#golden-files-beyond-the-expectations).

- A changed golden file needs a named reason in the commit message, per moved
  record or per cause. Regenerating to turn a red test green defeats the
  check.
- `irbuild.expect` records are keyed by a hash of the program's files, with
  each `.nomi` file hashed as its tokens. Comments are tokens too, with their
  text, and so are line breaks: a reformat that joins or splits lines changes
  the key, and only one that changes spacing within lines keeps it. Any edit
  to a fixture's code or comments needs regeneration, and so does a layout
  change that moves a source line its output prints. Do not delete
  `irbuild.expect` to prune it.
- A transcript of a program the VM cannot run (a "not supported yet" error
  from `nomi run`, a `BLOCKED` case from `nomi test`; `expectation.VMGap`) is
  never recorded.
- Corpus assertions embed source line numbers (`line 74: check failed`), so
  adding or removing a line above one in a `tests` file breaks it.

**Test-writing rules enforced by review:**

- A fixture asserting "the front end accepts this, and irbuild declines it"
  must fail if the front end starts rejecting the source
  (`if err != nil { t.Fatalf("the front end rejects this, ...") }`, as in
  `internal/irbuild/fieldaccess_test.go`). The mirror holds for a test that
  asserts rejection: it must fail if the source starts being admitted, and it
  checks the message.
- Assert costs by counting, not timing (`rt`'s `TestMeasureMapCosts` counts
  node visits under `-tags rtmapcount`).

## Format

`nomi fmt -w <paths>` after editing any `.nomi` file (`make fmt` formats
`std` and `tests`). The formatter owns layout; the style
guide owns idiom. Unused imports are analyzer errors, and so is re-importing a
prelude name; `fmt` does not remove either (it runs without analysis), but the
LSP's `source.fixAll` does.
`go test ./internal/format -run TestTrackedNomiFilesAreFormatted` fails for
every tracked `.nomi` file that `nomi fmt` would change, except the few in its
`unformattedAllowed` list, whose layout a test depends on.

Examples are held to the formatter too, since the tour's editors format an
example once the reader edits it. `go test ./vmhost -run
TestDocExamplesAreFormatted` fails for every ```` ```nomi ```` and
```` ```nomi-run ```` block (per `// FILE:` file, Nomi files only) in the
tour, `docs/`, `README.md` and `CONTRIBUTING.md`, every ```` ```nomi ```` fence
in a std `///` or `//#` comment, and every std `//!` prompt as its reference
editor formats it (`format.FormatAttachedTestBody`), that `nomi fmt` would
change. A fragment with `...` is formatted with each `...` held by an
identifier of the same width; a block that does not parse even so is skipped
and counted in the log. `NOMI_FORMAT_DOC_EXAMPLES=1` writes the formatted text
into the sources; a moved line in a tour block moves the `dbg line N` of its
expect block and its tour.expect key.

## Language conventions

The rules most often gotten wrong are in
[`docs/cheatsheet.md`](docs/cheatsheet.md), each with an example the doctests
run. Read it before writing Nomi; the spec is canonical where the two differ.
The site publishes it as the cheat sheet page, whose examples a reader can
run, and `cheatsheet.md`, and `llms-full.txt` holds it beside the spec, the
style guide and the stdlib's API (`nomi-docgen -llms`). The page is committed,
since tour.expect records its blocks: after editing `docs/cheatsheet.md`, run
`go run ./cmd/nomi-docgen -llms . tour` (`TestCheatSheetPageIsCurrent` fails
until you do).

## Compiler conventions

- A Go package goes under `internal/` unless code outside this module must
  import it. The public ones are `vmhost`, `vmrunner`, `hostadapt` and `std`:
  embedders and generated FFI wrappers and runners import them.
- Type references in the AST are `TypeExpr` nodes, never a string plus
  position.
- Syntax gets an AST node. Do not synthesize data in the analysis builder for
  something the parser should emit.
- `ast.Children` and `ast.Inspect` run a type switch generated from the
  node types (`internal/ast/children_gen.go`, by `internal/ast/walkgen`)
  that follows the reflective `ast.ReflectChildren`'s rules. After adding,
  removing or reordering a node type or field, run
  `go generate ./internal/ast`; `TestGeneratedChildrenAreCurrent` fails
  until you do.
- No fake symbols: a new concept gets its own representation at every layer.
- No workarounds for type information the checker should provide; fix the
  checker.
- Nominal type identity is (declaring file, name). Two files may each declare
  a `Point`.
- Prefer one implementation in `rt` that two components call over two
  implementations kept in agreement by a test.
- Keep it simple: a layer whose only payoff is one reader is ceremony.

## Adding a stdlib host function

1. Declare the `host fn` in `std/<module>.nomi`. A first-party
   facade names no Go symbol.
2. Add a row to `internal/stdlibbindings`: `Funcs()` for an adapter package's
   function, `RtFuncs()` for a function over rt values. A function that acts
   outside the machine in a way some target cannot gets the row's `Class`
   (`files`), which `nomi build` checks against the target
   (`vmhost.TargetLacks`, `vmhost.Program.CheckTarget`).
3. `go generate ./internal/stdlibadapters`.
4. Add recorded cases for the new row (`internal/stdlibadapters/testdata/*.golden`).
5. `go test ./internal/hostpair ./internal/stdlibadapters ./internal/hostgen/...`.

`TestGeneratedFileIsCurrent` fails until you regenerate, and generation refuses
a row whose Go signature does not match its declaration. A Go parameter or
result for a Nomi `Int` is `rt.Int` when the function handles an Int of any
size, and `int64` when it needs a machine integer, where the adapter refuses
an Int past its range. An adapter returning a
Nomi struct must build it with the declared type's descriptor, or every impl
lookup on it misses.

A new Go-backed module, a std type a public signature names, and a module
written only in Nomi each need more: see
[implementation-notes.md, "Host functions"](docs/implementation-notes.md#host-functions).

## Grammar and editor extensions

The grammar in `tree-sitter-nomi/` is copied into several query sets, and every
query file references grammar node names. Renaming or removing a node breaks
each query that names it, and in Zed a single bad query file stops the whole
language from loading (no highlighting and no LSP).

After changing `tree-sitter-nomi/grammar.js`:

1. `scripts/tree-sitter.sh generate` (runs the CLI from `tree-sitter-nomi/`),
   then `scripts/tree-sitter.sh test`. The script pins tree-sitter-cli 0.20.8,
   which generated the committed `src/parser.c` (ABI 14); a newer CLI
   rewrites it at ABI 15. Never run a bare `npx tree-sitter generate`.
   Then `go test ./internal/parser -run TestTreeSitterParsesWhatTheParserAccepts`,
   which holds tree-sitter to the Go parser over every tracked `.nomi` file
   and tour and spec block; a known difference is pinned in
   `treeSitterParityGaps` with a reason.
2. Update `tree-sitter-nomi/queries/highlights.scm` (first match wins:
   specific patterns first) and `queries/injections.scm`.
   `scripts/tree-sitter.sh test` also checks the `// ^ capture` assertions
   in `tree-sitter-nomi/test/highlight/*.nomi` against this file; their
   rules are in [implementation-notes.md](docs/implementation-notes.md#grammar-checks).
3. Update `editors/zed/languages/nomi/highlights.scm` by hand. Its priority
   order is reversed: catch-alls first, specific patterns last.
4. `cp tree-sitter-nomi/queries/injections.scm editors/zed/languages/nomi/injections.scm`
   (a byte-identical copy).
5. Check `editors/zed/languages/nomi/outline.scm` and `runnables.scm`:
   `grep -rn '<node>' editors/zed/languages/nomi/ editors/helix/runtime/queries/nomi/`.
6. `make build-helix` and `make build-nvim`. Helix's `highlights.scm` and all
   of Neovim's queries are generated; never edit them by hand. `make
   build-nvim` also recompiles the gitignored `editors/nvim/parser/nomi.so`
   that a Neovim loading the plugin from this checkout uses. Confirm
   `grep -rn '<node>' editors/nvim/queries/nomi/` is empty.
7. Update `editors/bat/nomi.sublime-syntax` and
   `editors/vscode/syntaxes/nomi.tmLanguage.json` if highlighting rules changed
   (both are regex-based and independent of tree-sitter; the VS Code grammar
   is derived from the bat one), then `make test-vscode`.
8. `make build-tour-grammar-wasm` and commit the regenerated
   `tree-sitter-nomi/tree-sitter-nomi.wasm`; the tour deploy copies it. It
   compiles the committed `parser.c` with tree-sitter-cli 0.27.0, pinned in
   the Makefile, which needs no Docker or Emscripten and rebuilds the
   committed wasm byte for byte.
9. To ship the change to editor users, publish the grammar (below).

**Publishing the grammar.** Zed and Helix build the grammar from
github.com/nomi-language/tree-sitter-nomi, a read-only mirror of
`tree-sitter-nomi/`, at the rev pinned in `editors/zed/extension.toml` and
`editors/helix/languages.toml`. Never commit to the mirror directly.
`scripts/publish-grammar.sh` (on a clean tree) fetches the mirror's `main`
and, when its tree differs from HEAD's `tree-sitter-nomi/`, makes one commit
with that tree on top of the mirror's tip and pushes it as `main` (a
fast-forward, never forced). It then writes the published sha into both
files; commit those two files afterwards. A rerun with no grammar change does
nothing. `--dry-run` reports only; `--remote <url>` pushes somewhere other
than the https URL (for example over ssh). `make publish-main` runs it, over
ssh, whenever `tree-sitter-nomi/` differs between `origin/main` and `next`,
and commits the two pins on `next`.

**Local editors.** `make dev-editors` installs `nomi` and `nomi-lsp` from this
checkout and sets up each editor's dev install. `make enable-hooks` enables,
once per clone, the `post-merge` hook, which does the parts of that a merge
into `main` needs, and the `pre-push` hook, which refuses any push to `main`
(the grammar mirror's excepted) that `make publish-main` does not make.
Per-editor setup (the Zed dev extension, Helix, Neovim, VS Code, bat) is in
[implementation-notes.md, "Editor development setup"](docs/implementation-notes.md#editor-development-setup).

## The tour and stdlib reference

`tour/` is an Astro Starlight site. Chapters are `src/content/docs/*.md`,
listed in `astro.config.mjs`'s sidebar. Plain ` ```nomi ` blocks are
highlighted only. A ` ```nomi-run ` block is doctested
(`go test ./vmhost -run TestTourDoctests`) and becomes a live editor; put
what its page shows, its output and then its tests' report, in a hidden
`<!-- expect ... -->` block right after the fence.
`NOMI_REGENERATE_EXPECTATIONS=1 go test ./vmhost -run TestTourDoctests`
writes those blocks from the runs. The page shows the expect block as the
block's output and runs nothing until the reader edits it. A block without
`error` whose run stops fails the doctest, and so does one the VM cannot
run. A fence may carry these words after `nomi-run`, and any other fails
the doctest:

- **`error`**: the block fails on purpose, to compile or while it runs. Its
  expect block holds what the tour shows, the compile error or the output
  and then the failure (`error: div by zero`); copy it from
  `TestTourDoctests`' failure. The doctest fails if the block runs to the
  end, and if the page could not tell the failure from the output in the
  record: it must be one line, or a process exit's message and `exit status
  N`.
- **`no-run`**: output that reads the clock. It must compile and is not run
  by the doctest; the page runs it when it scrolls into view.
- **`compile-only`**: a block a browser cannot run, such as one that listens
  on a port. It must compile and is never run; the page shows it as a plain
  highlighted block with no editor or output.
- **`manual`**: an expensive run (a tool runs first, or it computes a lot).
  The page runs it only on Run, with a 60-second budget instead of 6, and
  labels its expect block, which it needs, as recorded output until then. It
  combines with `error`. Make a slow block cheaper before making it manual:
  under `make check-full`,
  `TestTourWasmAnswersTheTourBlocks` fails a run over half its budget.
- **`read-only:<glob>`** (repeatable, `path.Match` over the `// FILE:`
  path): data files the example only names. The doctest fails a marked file
  the run reads, an unmarked data file it never reads, and a glob that
  matches nothing.
- **`tool:<module>/<name>`** (repeatable): runs `nomi tool <module> <name>`
  in the block's project before each run, as a reader runs `sqlkit generate`
  before `nomi run`.

A block runs on an in-memory file system. A `// FILE:` section that is not
Nomi source or `nomi.toml` is a data file the block starts with; one named
`go.mod` makes the block a project of modules. A block's entry is its first
file with a `fn main`, else its first Nomi file that declares tests, else
its first Nomi file. After editing a block's `go` bindings or Go file, or
the Nomi or Go side of a `lib/` module the tour links (`sqlite`, `sqlkit`),
run `go generate ./internal/tourgo`; `TestGeneratedFileIsCurrent`
(`internal/tourgo/tourgogen`) fails until you do, and `make start-tour`
does not build. How blocks of modules, sibling projects, Go
bindings and the linked `sqlite` and `sqlkit` modules run, and the live
editor's layout, is in
[implementation-notes.md, "Tour internals"](docs/implementation-notes.md#tour-internals).

The stdlib reference (`src/content/docs/reference/`, gitignored) is generated
by `cmd/nomi-docgen` from `///` doc comments and `//#` module comments in
`std/*.nomi`. Edit those comments, never the generated pages. `docgen` does not
prune: after renaming or removing a stdlib module, delete its old reference
page. Each `//!` prompt becomes a live editor, except one that reaches a
capability class the js/wasm build lacks (a socket, a name lookup, a
process), which docgen finds by the walk `nomi build` checks a target with
and shows as highlighted code with a note, as a `compile-only` tour block
is shown; no list names them. `go test ./vmhost -run
TestTourWasmAnswersTheReferenceEditors` runs every live editor through the
staged wasm.

The Specification section (`src/content/docs/spec/`, gitignored) is generated
from `docs/spec.md`, one page per numbered chapter named after its heading
(`## 12. Interfaces, implementations and derive` is
`/spec/interfaces-implementations-and-derive/`), by `go run ./cmd/nomi-docgen
-spec docs/spec.md tour/src/content/docs/spec`, which
`scripts/build-tour-wasm.sh` runs. Edit the spec, never the pages. A `§N`
or `§N *Heading*` reference becomes a link, and `TestSpecReferencesResolve`
(`cmd/nomi-docgen`) fails on one that names no chapter, heading or bold
lead-in there. Chapters are numbered and their sections are not, so a
citation names a heading (`§7 *Captures*`). Outside the spec, every `spec §N`
and `§N *Heading*` citation in `docs/*.md` and in Go and Nomi comments is
held to the spec by `internal/docscheck`'s `TestSpecCitations_*`, so a
renumbered or retitled spec fails there until its citations move.

The Writing Nomi section (`src/content/docs/writing/`, gitignored) is
generated from `docs/style.md`, one page per `## Topic` with its numbered
rules, by `go run ./cmd/nomi-docgen -style docs/style.md
tour/src/content/docs/writing`, which `scripts/build-tour-wasm.sh` runs; a
`§N` there links rule N, `spec §N` the spec page, and
`TestStyleReferencesResolve` fails on one that resolves to nothing.

The Tooling section (`src/content/docs/tooling/`, gitignored) is generated
from `docs/tooling.md` the same way, one page per `##` section, by `go run
./cmd/nomi-docgen -tooling docs/tooling.md tour/src/content/docs/tooling`,
and `TestToolingReferencesResolve` holds its links.

The Examples section (`src/content/docs/examples/`, committed) is generated
from `tests/exercism/<slug>/` by `go run ./cmd/nomi-docgen -examples
tests/exercism tour/src/content/docs/examples`, which
`scripts/build-tour-wasm.sh` also runs. Edit the exercise, then regenerate
the pages and tour.expect; `TestExamplePagesAreCurrent` fails while a page is
stale, missing or left over. A page holds NOTES.md's title, first paragraph
and `## Choices`, and the exercise's files, solution first, with the tests'
report as its expect block; a `Run: manual` line under `Subject:` makes the
block manual. NOTES.md's `## Friction` section logs what made the exercise
harder than it should be. It is never published. The generator checks it
(known tags, `None.` for no item, no item that says `Fixed:`, since a fix
deletes the item), and a Choices line may not point at it.

Needs Node 22+. `NODE22_BIN` in the Makefile defaults to Homebrew's path;
override it elsewhere.

```
make start-tour                           # wasm build + dev server at http://localhost:4321/
make deploy-tour                          # production build into tour/dist
go test ./vmhost -run TestTourDoctests    # run every nomi-run block
```

`make deploy-tour` rebuilds the wasm bundle and runs `npm run build`, which
ends with `csp-audit.mjs` (the built HTML must match `public/_headers`'
hash-pinned CSP) and `check-dist.mjs` (no file over 25 MiB); the tour's tests
run in `make check-full`, not here. Deployment settings are in
`tour/README.md`. Re-run `scripts/build-tour-wasm.sh` after changing tour
content, the grammar, client JS, stdlib doc comments, or any compiler, VM or
rt code linked into wasm.

## Docs discipline

- When you ship, change or remove a language feature, update the spec in the
  same change. Aspirational spec text carries a `> **Status: not yet
  implemented**` callout, and each such callout is listed in
  feature-status.md's "Aspirational language features".
- New authoring conventions go in `style.md`.
- A change to the language or `std/` updates the cheat sheet
  (`docs/cheatsheet.md`) and the guides for other languages
  (`tour/src/content/docs/from-rust.md`, `from-go.md`, `from-elixir.md`) in
  the same change, when it touches a rule or an example there. Their examples
  are ```` ```nomi-run ```` blocks that `TestTourDoctests` runs.
- A change to how a subsystem is built, or to a test harness, updates its
  section of `docs/implementation-notes.md` in the same change.
- A change that affects users (the language, `std/`, the CLI, the editor
  extensions) ends its commit message with a paragraph that starts
  `User-facing:` and says what changed for someone writing or running Nomi,
  in one or two plain sentences, with no internal file paths or commit
  hashes. When the tour or the stdlib reference covers it, add the section's
  URL (`https://nomi-lang.org/pipes/#the-then-stage`). Internal refactors and
  test-only changes have none. As branches merge into `next`, the maintainer
  gathers these into `next-notes.txt` in the clone's common git directory:
  the header lines `Added`, `Changed`, `Fixed` and `Removed`, then one line
  per change (`Added: ...`, `Changed: ...`, `Fixed: ...`, `Removed: ...`),
  one to five `Highlight: ...` lines and one `Title: ...`, the commit's
  subject. `make publish-main` (`scripts/publish-main.sh`, on `next`, clean,
  after `make check-full`; `--dry-run` prints the message) builds the squash
  commit's message from it (the title, then `Highlights`, `Added`, `Changed`,
  `Fixed` and `Removed` as lists), commits `next`'s tree on `origin/main`,
  pushes it, and moves the file to `next-notes-<date>-pushed.txt`.
- Code examples in the spec are parsed by `internal/parser/spec_examples_test.go`,
  and every complete program (a `fn main` at column 0, no `...`) is loaded
  and run by `vmhost.TestSpecPrograms`.
- When a change affects a workflow described here, update this file in the
  same change.

## Tooling notes

Rules enforced by something in the repository:

- `internal/docscheck` rejects merge-conflict markers in tracked Markdown and
  Go files, and any tracked go.mod whose `go` line differs from the root
  go.mod's (it shells out to `git`, so it needs a git checkout). Bump every
  go.mod together.
  It also fails on a spec citation outside the spec that names no chapter,
  or a heading its chapter lacks.
- `TestScriptsNameGoPackagesThatExist` and `TestScriptsAreExecutable` hold
  `scripts/` to the tree.
- `internal/ffirun`'s cache refuses the real user cache under `go test`; set
  `NOMI_FFIRUN_CACHE_ROOT`. The managed toolchain's cache refuses it too
  (`NOMI_TOOLCHAIN_ROOT`), and a module download refuses a `GOMODCACHE`
  the test did not set.
- The tree-sitter CLI reads a global parser-directories config that may point
  at another checkout; `scripts/tree-sitter.sh` runs it from this checkout's
  `tree-sitter-nomi/`.
- Test output embeds absolute paths, so when comparing output from two
  checkouts byte for byte, use paths of equal length or normalize them.
