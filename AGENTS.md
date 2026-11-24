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
| [`docs/tooling.md`](docs/tooling.md) | The `nomi` command and its flags, `nomi.toml`, modules and dependencies, the test runner, `nomi fmt`'s layout, builds, environment variables, editors and the debugger. |
| Package docs (`doc.go`) | How the implementation is built and why: each package's doc comment holds its design, the invariants it shares with other packages, and pointers to the harnesses that hold it. Read it (`go doc ./internal/<pkg>`, or the file) before working in a package. |
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
| `cmd/` | Binaries: `nomi` (the CLI, with the language server as `nomi lsp`, the debugger as `nomi dap` and the compiler's tools for coding agents as `nomi mcp`), `nomi-vm` (the VM binary: what `nomi build` appends a program to, and what runs a `.nomic` file; a release `nomi` carries its platform's two builds of it), `nomi-vm-reactor` (the wasip1 reactor a program with an `export fn` is appended to; with none appended it loads a `.nomic` through `nomi_load`), `nomi-wasm` (the browser build), `nomi-docgen` (stdlib reference). |
| `vmhost/` | The public embedding API. |
| `vmrunner/` | What `nomi-vm` and a built executable run. An embedder runs compiled programs with it. |
| `hostadapt/` | Support package for host tables: std's generated adapters and an embedder's own functions. |
| `internal/` | Everything else. Syntax: `lexer`, `parser`, `ast`, `token`, `strlit`. `analysis` (resolution and the type checker), `frontend` (the whole front end as one call), `dap` (`nomi dap`), `editors` (`nomi editor`: each editor's Nomi support, written from the files the packages in `editors/` and `tree-sitter-nomi/` embed), `mcp` (`nomi mcp`, the compiler as Model Context Protocol tools), `irbuild` (IR builder), `ir` (IR, lint, image encoding), `vm` (bytecode VM), `lsp`, `format`, `runners` (finding, unpacking, downloading, building and caching the runners `nomi build` appends to), `modcache` and `modfetch` (dependencies), `artifactcache` (what the compiler builds once and reads back: the std bundle), `std*` (Go-backed stdlib adapters), `websocket` (the WebSocket protocol under std/websocket), `mf2` (MessageFormat 2) and `catalog` (phrase catalogs), and the tooling behind them. |
| `std/` | The standard library's `.nomi` sources, embedded into the binaries. A module that helps test another is nested in that module's directory: `std/fs/testing.nomi` is `std::fs::testing`. |
| `lib/` | First-party Nomi modules outside std, each a directory with its own `nomi.toml`, versioned with the compiler by the repository's one release tag and naming its neighbors by `dir` (internal/modcache package doc): `lib/sql/sqlite` (the SQLite driver: SQLite compiled to WebAssembly in the module, run by std::wasm, its file layer in Nomi over std::fs), `lib/sql/postgres` (the Postgres driver, in Nomi over std::net and std::tls), `lib/sql/migrate` (migrations), `lib/sql/sqlkit` (checked SQL), `lib/sql/examples/shop`, `lib/markdown` (Markdown to std/html's `Html`, in Nomi), `lib/image` (decoding, resizing and encoding JPEG, PNG and WebP: C codecs compiled to WebAssembly in the module, run by std::wasm), `lib/jobs/postgres` (std/jobs's queue in a Postgres table), `lib/jobs/examples/notify`, `lib/storage/s3` (std/storage's backend for S3-protocol servers), `lib/storage/minio` (its tests against a MinIO server). They hold no Go, so the compiler's Go module contains their files and builds none of them. |
| `rt/` | The runtime library, `github.com/nomi-language/nomi/rt`. The VM runs over it and built executables link it. It may import only the standard library and `github.com/rivo/uniseg` (`TestRuntimeImportsOnlyItsAllowlist`). |
| `docs/` | The spec, style guide, cheat sheet, tooling reference, feature status, roadmap and install guide. |
| `tour/` | The Astro Starlight site at nomi-lang.org: the tour and the generated stdlib reference. |
| `tests/` | The test corpus. |
| `testdata/expectations/` | Golden files. |
| `benchmarks/` | Engine benchmark programs. |
| `tree-sitter-nomi/` | The tree-sitter grammar, source of truth for editor parsing. The generated `src/` and `tree-sitter-nomi.wasm` are committed. Published read-only to github.com/nomi-language/tree-sitter-nomi by `scripts/publish-grammar.sh`, which leaves out `embed.go`, the Go package that carries the C and the wasm into `nomi` for `nomi editor install`. |
| `editors/zed/` | Zed extension (highlighting, LSP launch, the `nomi dap` debug adapter and a locator for the run and test tasks; `cargo test --manifest-path editors/zed/Cargo.toml` tests the locator). |
| `editors/helix/` | Helix language config and runtime queries. |
| `editors/*/embed.go` | In `zed/`, `helix/` and `nvim/`: a Go package whose only job is to `//go:embed` the files `nomi editor install` writes (`internal/editors` package doc). |
| `editors/nvim/` | Neovim 0.11+ plugin: filetype, LSP config, generated queries, test runner, neotest adapter, nvim-dap adapter. |
| `editors/vscode/` | VS Code extension (TypeScript): language configuration, a TextMate grammar derived from the bat syntax, the `nomi lsp` client, run and test commands, the `nomi` debugger. |
| `editors/bat/` | `bat`/syntect syntax definition. |
| `scripts/` | Tour wasm build, release and its tags, grammar publish, full check, publish to `main`, the js/wasm test runner, the tree-sitter wrapper, git hooks, and editor sync scripts. |

## Architecture in brief

Every way of running Nomi uses the same pipeline:

```text
source → front end (parse, analyze, type-check) → internal/irbuild → IR
       → internal/vm (compiles each ir.Func to bytecode on first call) over rt values
```

`nomi run`, `nomi test`, the REPL, the tour and `nomi build`'s executables all
run on the VM. There is no interpreter and no Go backend. `nomi fmt` executes
nothing. `nomi check` lowers the program as `nomi run` does and reports each
body the run would refuse as an error at its source; the language server does
the same in the background. Details:
[engine](vmhost/doc.go).

- **Front end.** `internal/frontend` parses, expands `phrase` and `noun`
  declarations, lowers derives and runs whole-program analysis
  (`internal/analysis`). Imports may be cyclic. Nomi has no macros; a
  library that generates code ships a `nomi tool`. Details:
  [front end](internal/frontend/doc.go),
  [keyword declarations](internal/frontend/doc.go).
- **Language server analysis.** `analysis.DocumentManager` keeps a checked
  program per project root and entry and rechecks edits incrementally, to
  the answer a full check gives. A program whose files are as an earlier
  server left them opens from a record of its interfaces in the artifact
  cache, each open document checked against them. Details:
  [language server analysis](internal/analysis/doc.go).
- **IR builder.** `internal/irbuild` lowers every body a program runs into an
  `ir.Func`; a program that reaches a body it declines fails before its first
  effect. A symbol another module names carries a linkage name built from
  its declaration, so modules encoded apart (`ir.EncodeObject`) link by
  name (`ir.Linker`). No Go text or Go names in the builder. Details:
  [IR builder](internal/irbuild/doc.go),
  [linkage names](internal/irbuild/doc.go).
- **VM and rt.** `internal/vm` compiles each `ir.Func` to register bytecode
  over `rt` values; `rt` holds collections, exact Int arithmetic, hashing,
  rendering and tasks. Neither links the front end. Details:
  [VM](internal/vm/doc.go), [rt](rt/doc.go),
  [debugging and `nomi dap`](internal/dap/doc.go).
- **Stdlib.** `std/` is one embedded module. Each process builds it from
  its modules' interfaces, only the modules its programs reach, and checks
  a module's bodies only when something lowers one (`std.Shared`,
  `StdLib.Ensure`, `StdLib.Bodies`; `std.All` builds every module);
  `programStdModules` are declared with each program and linked by name
  from one lowering of all of them. Its interfaces and its lowering are
  decoded from artifacts the binary embeds or caches. A
  `host fn` is a VM intrinsic or an
  adapter generated from `internal/stdlibbindings`. `lib/` holds first-party
  modules outside std. Details:
  [stdlib](std/shared.go),
  [the std bundle](internal/irbuild/doc.go),
  [the stdlib from its interfaces](std/shared.go),
  [the stdlib a module at a time](std/shared.go),
  [std and lib](std/doc.go),
  [host functions](internal/stdlibbindings/doc.go),
  [files](internal/stdio/doc.go),
  [processes](internal/stdprocess/doc.go),
  [terminals](internal/stdterminal/doc.go) (`std/terminal` and
  `io.is_terminal`),
  [HTTP, TLS and sockets](internal/stdhttp/doc.go) (`std/http`,
  `std/tls`, `std/net` and `std/udp` over `internal/stdhttp`),
  [WebAssembly](internal/stdwasm/doc.go) (`std/wasm`, modules run by
  wazero, their machine code kept in the artifact cache, and in a js
  build by the host's own engine; `scripts/test-js.sh` runs a package's
  tests built for js under Node),
  [logging and actors](std/doc.go)
  (`std/log`, `std/log/testing` and `std/actors`, written in Nomi alone).
- **Locale data and collation.** CLDR data from `internal/cldrgen` in
  `internal/cldrfull`, cut per program to its listed locales;
  `internal/collation` is a UCA collator over it. Details:
  [locale data](internal/cldrgen/doc.go),
  [collation](internal/collation/doc.go).
- **Embedding and modules.** `vmhost` is the public Go API that the CLI,
  REPL and tour wasm use, and through which a Go program hosts Nomi and
  answers its `host fn` declarations from host tables. Nomi source binds
  no Go code. `internal/modcache` and `internal/modfetch` find the
  dependencies each `nomi.toml` names, fetch each repository's tag from
  its host (or through git) into Nomi's module cache and lock them in
  `nomi.lock`, with no `go` command and no go.mod. Details:
  [embedding](vmhost/doc.go),
  [modules](internal/modcache/doc.go).
- **`nomi build` and the REPL.** `nomi build` appends a pruned IR image to
  `nomi-vm` or the wasip1 reactor, or writes a browser folder or a `.nomic`.
  `internal/runners` takes the runner a release `nomi` carries for its own
  platform, or finds it beside `nomi` or in a release, or builds it from
  the compiler's source, and caches it. A release is one file per
  platform: `nomi`, built with the `nomi_hostrunners` tag, carries that
  platform's two runners gzipped, and never builds one; the release ships
  each platform's runners in a `_runners` archive for `--target`, and
  std/compiler's in a `_compiler` archive. The runners come in two tiers,
  never a combination: the lean one (`nomi-vm-nonet`), built with every
  optional family's tag (`nomi_nonet`, `nomi_nowasm`), with no net/http,
  crypto/tls, net or wazero, which a program that reaches no family's
  crossing is appended to, and the full one, which has every family; a new
  family joins the full one. `go generate ./internal/stdlibadapters` writes
  the split. No wasip1 runner has WebAssembly modules. The same walk holds a
  program to the effects nomi.toml's `[effects]` lists and an
  embedding host's `vmhost.WithEffects` allow, in `check`, `run`,
  `test`, `build` and every `vmhost` load, run and call (vmhost's
  effects.go). The REPL links each input into one live VM. Details:
  [`nomi build`](vmrunner/doc.go),
  [runners](internal/runners/doc.go),
  [REPL](cmd/nomi/doc.go).

## Build

From the repository root:

```sh
go build -o nomi ./cmd/nomi   # local CLI at ./nomi (gitignored)
go install ./cmd/nomi         # `nomi` on PATH, and the language server editors launch as `nomi lsp`
make build                    # all of the above plus tour wasm and editor queries
```

On a Mac with Xcode's command line tools, `make build-cli` and `dev-editors`
link `nomi` with `CGO_ENABLED=1 -ldflags=-linkmode=external`: the system
linker writes chained fixups, so `nomi` reaches `main` about 1 ms sooner. On
macOS cgo picks no different std files. Releases are cross-built with `CGO_ENABLED=0` on one Linux runner and
keep Go's linker, and so does every `nomi-vm`, which gains nothing measurable.

The stdlib is embedded with `//go:embed`, so after editing `std/`
rebuild `nomi`, and restart the running language server
(reinstalling the Zed dev extension restarts it). When the server has no
source tree to point at, go-to-definition into std opens the embedded std
written to `~/.cache/nomi/std/<version>/`, one directory per std version
(`internal/stdcache`), so an old server still running never shares files with
a new one. A version directory unused for 14 days is removed by the next
server that starts navigating. Tests never write there: `NOMI_STD_CACHE_ROOT`
replaces `~/.cache/nomi`, and under `go test` without it the files go under
the system temp directory.

A binary built from source analyzes and lowers the stdlib on its first run
and caches the stdlib's interfaces and facts and its lowering, a std
bundle, under its build ID in `os.UserCacheDir()/nomi/artifacts/std/`
(`NOMI_ARTIFACT_CACHE` replaces the directory; `off` keeps none), so each
new build of `nomi` pays one slower first run (about 0.8 s for hello
world), and the base modules' state on its second. Every cache Nomi
keeps, under the user cache and under the system temp directory for
tests, drops an entry nothing has used for 14 days, and a build's std
artifacts after a day and a kept program after five
(internal/artifactcache package doc, "Pruning").
`scripts/build-tour-wasm.sh` and `scripts/release.sh` embed all three
instead (the `nomi_stdbundle` tag). Under `go test` the cache is under
the system temp directory, for the test and every process it starts.
Details: [the std bundle](internal/irbuild/doc.go).

## Test

```sh
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
counts nine. The modules under `lib/` hold no Go, so `go test ./...`
finds no package there; `TestLibModules` (`cmd/nomi`) runs their Nomi
tests.

**Harnesses.** Fuzzers and tests hold the implementation to properties.
Each harness's doc comment, in the file named here, gives its inputs, its
known-gaps list, its commands and its environment variables. A fuzz target
runs as `go test ./<pkg> -run '^$' -fuzz '^<Name>$' -fuzztime 5m`. A
harness named with `NOMI_CORPUS_WALKS=full` walks a fixed sample of the
corpus by default and every input under the variable
(`internal/rotation`'s `CorpusWalk`).

- A program the front end accepts lowers: `FuzzFrontEndAcceptsSoItLowers`,
  `knownLoweringGaps` (`vmhost/lowering_fuzz_test.go`).
- A program that lowers runs, the same twice (the second with every update
  copying) and as its built image:
  `FuzzLoweredProgramsRun`, `knownRunGaps` (`vmhost/run_fuzz_test.go`).
  A generated function of many parameters, literals and locals, looping by
  tail calls, prints what its generator computed, on those machines and a
  debug one: `TestLargeFunctionsRunAsWritten`
  (`vmhost/large_function_test.go`).
- Every function a run enters is one the reach walk reaches from that run's
  roots, which is what a built image keeps and an effects list reads:
  the corpus's and the tour's programs, and each test case of the corpus,
  the failure fixtures, the tour and std run on its own:
  `TestReach_EveryEnteredFunctionIsReached`
  (`internal/vm/reachsound_test.go`).
- Every feature in every context, a std module's own tests among them, is
  rejected as expected or runs: `TestFeatureContextMatrix`,
  `NOMI_MATRIX=full`, `NOMI_MATRIX_ONLY` (`vmhost/feature_matrix_test.go`).
- Every value type under every operation (a tuple, a field, a collection,
  a pattern, `==`, a channel, a generic call) is rejected as expected or
  runs: `TestTypeOperationGrid`, `knownTypeOpGaps`, `TestKnownTypeOpGaps`,
  `NOMI_TYPE_OPS=full`, `NOMI_TYPE_OPS_ONLY` (`vmhost/type_ops_test.go`).
- Load order does not change a program: `TestLoadOrderDoesNotChangeAProgram`,
  `NOMI_ORDER_COUNT` (`vmhost/load_order_test.go`).
- Every switch that lowers an operator, or a qualified call of an operator
  interface's function, by the form of its record has an arm or a stated
  reason for every form the checker records (`formArms`):
  `TestEveryOperatorFormHasAnArm` (`internal/irbuild/formarms_test.go`).
  A use with no record its switch reads declines, and each class's
  distinct sites only go down: `noteUnreadOperator`, `noteUnreadCall`,
  `NOMI_SUBST_VERIFY=1`, `maxOperatorFormSites`, checked by
  `TestIRGolden_RecordsMatchTheVM`
  (`internal/irbuild/operatorform_ratchet_test.go`).
- Every comparator a sort, a Range or a tuple orders by is chosen from
  the checker's record of `Comparable.compare`, and every tuple slot's
  and distinct's inner equality from the record of `Equatable.equal` at
  the part's type; the distinct sites of an element with no checker type
  or an unresolved per-instance record, chosen by kind, only go down:
  `noteElemImpl`, `NOMI_SUBST_VERIFY=1`, `maxElemImplSites`, checked
  by `TestIRGolden_RecordsMatchTheVM`
  (`internal/irbuild/comparatorform_ratchet_test.go`).
- Every Debug body a rendering names for a nested part is chosen from the
  checker's record of `Debug.inspect` at the part's type, and the
  distinct sites of a part with no checker type, of a type declared in a
  body, held as an interface value or under an Iter, chosen by kind, only
  go down:
  `noteDebugImpl`,
  `NOMI_SUBST_VERIFY=1`, `maxDebugImplSites`, checked by
  `TestIRGolden_RecordsMatchTheVM`
  (`internal/irbuild/debugimplform_ratchet_test.go`).
- Every std operator impl only the machine computes has a row in
  `machineImpls`, and each row's operators, and each Debug row's
  rendering, answer as a call of its body: `TestMachineImplsAreComplete`,
  `TestMachineImplsComputeTheirBodies`
  (`internal/irbuild/machineimpl_test.go`).
- Whether a pattern can fail is the checker's record, which irbuild reads,
  and each pattern it answers for with no record is a listed class whose
  distinct sites only go down: `patternAlwaysMatches`,
  `irrefutableClasses`, `maxIrrefutableSites`, checked by
  `TestIRGolden_RecordsMatchTheVM`
  (`internal/irbuild/irrefutableform_ratchet_test.go`).
- Every place irbuild can decline is listed with a class, and an undecided
  one with its cause. The total, the dead sites and the undecided sites
  only go down: `TestDeclineSitesAreListed`,
  `testdata/lowering-declines.txt`, `maxSites`, `maxDeadSites`,
  `maxUndecidedSites` (`internal/declinesites/sites_test.go`). A gap an
  unreached site turns out to hold is pinned in `inventoryLoweringGaps`
  (`vmhost/lowering_gaps_inventory_test.go`).
- Every expression has a type after checking: `TestSeedExprsAreTyped`,
  `knownCheckerGaps` (`vmhost/checker_gaps_test.go`).
- Every std signature position and `once` has a type the checker
  recorded, and the positions whose type has no shared kind and names no
  type parameter fall in named classes whose counts only go down:
  `TestStdSignaturePositionsProject`, `maxSigPositionRefusals`
  (`internal/irbuild/stdsigkinds_test.go`).
- A local irbuild reads is the declaration the checker resolved the read
  to: the builder keeps its locals only by that declaration's key
  (`readKey`, `localAt`), so the checker's References are the one source
  of what a read names, and a read with no key fails the lowering
  (`readWithoutKey`). The places that resolve a name by its spelling only
  go down: `TestSpellingResolutionsOnlyGoDown`
  (`internal/irbuild/localread_ratchet_test.go`).
- The kind irbuild lowers each expression at is the type the checker
  recorded for it, or the type a Conversion, Coercion or settled
  Infallible gives it there, in every test binary: `verifyExprKind`,
  `knownExprKindClasses`, and each class's distinct sites only go down:
  `maxExprKindSites` (`internal/irbuild/exprkind_ratchet_test.go`).
- Each call route that chooses a function by an owner's spelling or an
  operand's kind (qualImplPlan's routes after the record's, typeQualCall's
  helpers, call's intrinsic owner arms, stdPick) is a listed route whose
  distinct sites, written, derived and made, only go down:
  `noteCallRoute`, `callRoutes`, `maxCallRouteSites`, checked by
  `TestIRGolden_RecordsMatchTheVM`
  (`internal/irbuild/callroute_ratchet_test.go`).
- The AST nodes irbuild constructs, per node type, and the `ast.Call`
  literals that carry no record only go down:
  `TestASTConstructionSitesOnlyGoDown`, `maxASTConstructionSites`,
  `maxUncarriedCallLiterals`
  (`internal/irbuild/astconstruction_ratchet_test.go`).
- Modules encoded apart link to the same program, and each file lowered on
  its own matches the whole lowering: `TestObjectsLinkToTheSameProgram`,
  `TestFilesLowerOnTheirOwnAsInTheProgram`, `NOMI_OBJECTS_LINK=full`,
  `NOMI_LINK_OBJECTS` (`internal/expectation/objects_test.go`).
- Every program irbuild.expect records links through objects to its whole
  lowering's functions: `TestStdObjects_ProgramsLinkAsTheirWholeLowering`
  (`internal/irbuild/stdobjects_test.go`). What keeps each std module
  lowered once for the process instead of with each program (a host
  function, a host type, a process-wide type, an import by such a module) is
  pinned per module: `TestStdObjects_WhatHoldsEachModuleShared`,
  `sharedModuleHolds` (same file).
- A debug machine runs every program as a plain one does:
  `TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes`,
  `NOMI_DEBUG_IDENTICAL=full` (`internal/expectation/debugmachine_test.go`).
- Kept and relinked programs run as fresh ones: `FuzzCachedBuildMatchesFresh`
  (`cmd/nomi/artifact_cache_test.go`), `TestCachedRunsMatchTheRecords`,
  `NOMI_CACHED_RUNS=full` (`internal/expectation/cached_test.go`).
- A test `nomi test` answers as unchanged from a stored pass is one that
  passes when every test runs, after each edit of a seeded set, whether
  its file was loaded or answered from its passes record (which a test
  binary also loads and decides, panicking on a difference), and an edit
  that only moves lines runs no test with no effects again:
  `TestSkippedCasesWouldPass`, `NOMI_CORPUS_WALKS=full`, `NOMI_GEN_SEED`
  (`cmd/nomi/skipped_cases_test.go`). A function's sum without positions
  stays when its code moves and changes when its code does:
  `TestBodySum_*` (`vmhost/bodysum_test.go`).
- The std bundle decodes to this process's lowering:
  `TestStdBundle_DecodesToTheLowering` (`internal/irbuild/stdbundle_test.go`).
  A shared module decoded when the process needs it decodes what it names,
  and every recorded program lowers over modules decoded that way as over
  every module decoded, and a program links its own closure of shared
  modules, so its image is the same bytes whatever the process decoded
  before: `TestStdObjects_AModuleDecodesWithWhatItNames`,
  `TestStdObjects_DecodedWhenNeededLowersAsAllDecoded`,
  `TestStdObjects_AProgramBuildsTheSameImageInEveryProcess`
  (`internal/irbuild/stdobjects_decode_test.go`).
- The stdlib from its interfaces, and built a module at a time, is the
  whole one: `TestShared_ModulesAreLoadsModules`,
  `TestShared_LazyModulesAreEagerModules` (`std/shared_test.go`),
  `TestStdLazyRunsAsWholeSample`, `NOMI_STD_WHOLE=1`
  (`cmd/nomi/std_lazy_test.go`).
- The stdlib's state reads back as the build it was written from, field by
  field: `TestStdState_ReadsBackAsTheBuild`, `TestStdState_AProcessReadsIt`
  (`std/state_test.go`), `NOMI_STD_STATE=off`. An embedded state is read,
  and every other module built over it, in a process that finds no stdlib
  directory, as the browser and a release find none (a copy of the test
  built with `-trimpath`): `TestStdState_TheEmbeddedStateIsRead`.
- The checker hands irbuild every callee, type argument and expression
  type closed: `TestElaborationGate` fails on a call or expression outside
  derived bodies without one; `TestElaborationCensus`,
  `NOMI_ELAB_CENSUS=1`, `NOMI_ELAB_DIR` count the rest (derived bodies,
  implicit operations) (`vmhost/elaboration_census_test.go`).
- `nomi fmt` keeps meaning: `FuzzFormatKeepsMeaning`, `knownFormatGaps`
  (`vmhost/format_meaning_test.go`).
- An incremental check matches a full one: `FuzzIncrementalMatchesFull`
  (`internal/analysis/incremental_fuzz_test.go`),
  `TestIncrementalMatchesFull`, `NOMI_CORPUS_WALKS=full`
  (`internal/analysis/incremental_test.go`).
- A file checked against its imports' interfaces matches a full check:
  `FuzzCheckFromInterfacesMatchesFull`, `TestCheckFromInterfacesMatchesFull`,
  `NOMI_CORPUS_WALKS=full`, `NOMI_CHECK_FROM_INTERFACES=1`
  (`internal/analysis/interface_fuzz_test.go`).
- A program opened from its record answers as one checked whole:
  `FuzzOpenFromRecordMatchesFull` (`internal/analysis/stored_program_test.go`),
  and so does every language server request:
  `TestOpenFromRecordMatchesAFullCheck`,
  `TestOpenFromRecordEditsMatchAFullCheck`, `NOMI_CORPUS_WALKS=full`
  (`internal/lsp/stored_open_test.go`, `stored_edit_test.go`).
- Stored facts reduce as the program does:
  `FuzzStoredReductionsMatchTheProgram`,
  `TestStoredReductionsMatchTheProgram`, `NOMI_CORPUS_WALKS=full`
  (`internal/analysis/stored_facts_test.go`).
- A document answered through its kept program matches its own build:
  `TestClosedFilesFromProgramsMatchTheirOwnBuilds`,
  `TestOpenDocumentEditsMatchFreshBuilds`, `NOMI_CORPUS_WALKS=full`
  (`internal/analysis/closed_kept_test.go`, `document_programs_test.go`).
- A build over the stdlib's base matches one that folds every file:
  `TestStdBase_ABuildOverTheBaseMatchesOneThatFoldsEveryFile`,
  `NOMI_CORPUS_WALKS=full` (`internal/analysis/std_base_test.go`).
- The language server survives typing: `TestLSPSurvivesTyping`,
  `FuzzLSPSurvivesEdits`, `knownTypingGaps`, `NOMI_LSP_TYPING=full`
  (`internal/lsp/typing_test.go`).
- Scale: generated projects check, run and pass
  (`internal/projectgen/projectgen_test.go`); edit work stays flat as a
  project grows (`internal/analysis/scaling_test.go`); the timing table is
  `NOMI_SCALE_TABLE=1` (`internal/scalebench/scale_test.go`) and the
  language server's is `NOMI_SCALE_LSP=1` (`internal/lsp/scale_open_test.go`,
  `scale_edit_test.go`).

A test about how a blocked program is reported blocks on a planted decline (a
call to `planted_decline`, which `irbuild.PlantDeclineForTest` makes the
builder decline), never on a real gap, which a fix would close. A known gap
is pinned in its harness's `known...Gaps` list by a reproducer, and a fix
removes the entry in the same change.

**Rotating sets.** The `...Rotating` tests check, beside their fixed
inputs, a set of inputs from a start seed that changes every UTC day. The
log names the seed; `NOMI_GEN_SEED=<n>` pins it and `NOMI_GEN_COUNT=<n>`
changes how many seeds the set holds. A failure there says it came from the
rotating set and prints a command that checks exactly that input
(`NOMI_GEN_INPUT=<name>` for a named variant). It
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
-count=1 -timeout 30m` run takes about 150 seconds and `go test ./...` about
five minutes, so neither is a per-change check:

- **Per change, including an agent's change in its own worktree:** `go build`,
  `go vet ./...`, your new tests, and the existing tests that exercise the
  change, always with a narrow `-run` filter (for example
  `go test ./internal/irbuild -run 'AppField|Scoped'`), plus any golden-file
  regeneration the change needs and `go test ./rt/...` if `rt` changed. Never
  run a whole package without `-run` per change: `internal/analysis` and
  `internal/lsp` take about 3 minutes each with their corpus walks
  sampled, `internal/irbuild` 2 to 3. Never
  run `go test ./...`, the fuzzers or their seed corpora, or editor suites you
  did not touch, even for a change to what the front end accepts, and even
  if a brief lists them: the wider checks run after the merge. Say in your
  report which wider checks you skipped.
- **Before squashing a batch to `main`:** `make check-full`, once
  (`scripts/check-full.sh`: gofmt, the tour wasm, build and vet, every package with
  vmhost, rt and the whole irbuild package each on its own, `TestLibModules`,
  the tree-sitter tests, every recorded program linked through objects, the
  expectation suites with every file checked again against interfaces, the
  corpus walks whole (a `walk-*` step each, where a plain run of
  `internal/analysis` and `internal/lsp` takes their samples), and
  the race detector; the fuzz targets' saved inputs replay as ordinary tests;
  `STEPS="pkgs race"` runs only the steps named). It prints each step's exit
  status and fails when any failed. A failure there is attributed to the
  batch's changes by rerunning narrow tests on each. Then `make publish-main`
  (see "Docs discipline").
- **After a push:** `make fuzz`, each fuzz target at the same time for
  `FUZZTIME`, 5m by default (`FUZZ=1 make check-full` runs them with the rest).
  A fuzzer finds inputs no program the batch changed relies on, so it does not
  hold a push; what it finds is fixed on `next`, with the failing input kept
  under the target's `testdata/fuzz`, for the following batch.
- When running several suites, capture each exit status separately; a chain
  reports only the last one.
- Never pass `-timeout 0`. A test that does not end then holds the run
  until someone looks. `scripts/check-full.sh` gives each test binary
  `TEST_TIMEOUT` (30m), and a whole package run by hand takes `-timeout
  30m`: past it `go test` kills the binary, names the test that was running
  and prints every goroutine's stack.
- A change to a walk over statements or expressions in `internal/analysis`
  runs the cost tests with its narrow checks: `go test ./internal/analysis
  -run 'TestSelfRecursion_VisitsEachNodeOnce|TestLeaves_Cost' -count=1`.
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

```sh
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/expectation -run 'TestExpectation_(Corpus|Stdlib|Tour|Failure)$' -count=1
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -count=1 -timeout 30m
NOMI_REGENERATE_EXPECTATIONS=1 go test ./cmd/nomi-docgen -run TestReferenceInteractiveTestsOnTheVM -count=1
NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/vm -run TestIRImage_DecodedProgramsMatchTheCommittedRecords -count=1
```

Run the last after the `internal/expectation` command, since it reads
`corpus.expect`. The other golden files are described where their tests
are: `vm-corpus-programs.txt` and `vm-corpus-reports.txt`
(`vmComparableLists`, `internal/vm/expectation_test.go`), `vm-retained.txt`
and `vm-std-retained.txt` (`internal/irbuild/vmretained_test.go`,
`vmstdretained_test.go`), and the debug-session transcripts in
`internal/dap/testdata` (`internal/dap/harness_test.go`).

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
  node visits under `-tags rtmapcount`), and give the counter a bound it
  panics past, so that work that grows without limit fails the test at once
  and never hangs (`TestLeaves_CostGrowsWithTheSquareOfTheDepth`,
  `internal/analysis/leavescount_on.go`).

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
TestDocExamplesAreFormatted` fails for every ```` ```nomi ````
and ```` ```nomi-run ```` block (per `// FILE:` file, Nomi files only) in the
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
  embedders import them.
- Type references in the AST are `TypeExpr` nodes, never a string plus
  position.
- Syntax gets an AST node. Do not synthesize data in the analysis builder for
  something the parser should emit.
- `ast.Children` and `ast.Inspect` run a type switch generated from the
  node types (`internal/ast/children_gen.go`, by `internal/ast/walkgen`)
  that follows the reflective `ast.ReflectChildren`'s rules. After adding,
  removing or reordering a node type or field, run
  `go generate ./internal/ast` and `go generate ./internal/ast/astcodec` (the
  interface codec; its package doc, "File interfaces");
  `TestGeneratedChildrenAreCurrent` and `TestGeneratedCodecIsCurrent` fail
  until you do. A new field that can hold a child node also gets a class in
  `operandFields` (`internal/ast/operands.go`), which says whether the
  node always evaluates it; `TestEveryChildFieldHasAnOperandClass` fails
  until it has one. A new node type gets its forms in `nodeForms`
  (`internal/irbuild/nodeforms.go`), and an arm in `lowerNode` when it is a
  value or in `irScalarBlock` when it is a statement;
  `TestEveryNodeTypeHasAForm` fails until it does. After changing a field
  of any type the stdlib's built analysis holds (a `FileAnalysis`, a symbol, a type, a node), run
  `go generate ./internal/analysis` (the stdlib's state codec; the
  analysis package doc, "The stdlib's state");
  `TestGeneratedStateCodecIsCurrent` (`internal/analysis/stategen`) fails
  until you do.
- The reserved and contextual words are the lexer's lists
  (`lexer.ReservedWords`, `lexer.ContextualWords`, `lexer.IsKeyword`). Go
  code reads them. After adding or removing a word, update the lists
  outside Go that `TestKeywordListsMatchTheLexer` (`internal/docscheck`)
  names: the spec, `grammar.js`, the highlight query and the bat and VS
  Code grammars. After adding, removing or renaming a token type, run
  `go generate ./internal/token` (`TestTokenNamesAreCurrent` fails until
  you do).
- Operator precedence and associativity are one table,
  `internal/token/precedence.go`. The parser parses with it and the
  formatter decides parentheses with `token.OperandNeedsParens`, which
  `TestOperandNeedsParensIsTheParsersAnswer` (`internal/parser`) holds to
  the parser. After changing a level, update `grammar.js` and the spec's
  table, which `TestPrecedenceTablesMatch` (`internal/docscheck`) holds to
  it.
- No fake symbols: a new concept gets its own representation at every layer.
- Nothing recognizes a diagnostic by its message. Code that acts on one
  kind of error reads its `Code`, and what it names from `Args` or `Fix`
  (internal/analysis package doc, "Diagnostic codes"); a new code is an
  exported `<Kind>Code` constant in the file that builds the error, with
  an explanation in `internal/mcp`. `TestNoCodeRecognizesADiagnosticByItsMessage`
  (`internal/docscheck`) fails on a strings test, a regexp or `==` over
  message text outside its listed exceptions.
- No workarounds for type information the checker should provide; fix the
  checker.
- Nominal type identity is (declaring file, name). Two files may each declare
  a `Point`.
- Prefer one implementation in `rt` that two components call over two
  implementations kept in agreement by a test.
- Keep it simple: a layer whose only payoff is one reader is ceremony.
- One piece of code does each job. Before adding a case, find the code that
  already handles the nearest one and extend it. Do not add a parallel path,
  a second scanner or table, a special case per syntactic position, or a
  hand-written switch over a closed set that a shared or generated one could
  cover. If a change seems to need a second mechanism, stop and raise it.
- The checker decides and the builder lowers. `internal/irbuild` reads what
  `internal/analysis` recorded (a call's placement and callee, a type, a
  conversion) and works out none of it again from names or syntax. A
  decision the builder needs and the checker does not record is recorded in
  the checker, held by `TestElaborationGate`, and then read.
- What the checker records describes what the program means, with nothing
  about how the VM represents it.
- A closed set (node types, type kinds, child fields) is enumerated once,
  with a test that fails for a member a consumer does not handle, as
  `TestEveryChildFieldHasAnOperandClass` and `TestTypeChildrenNamesEveryKind`
  do.
- A refactor that is meant to preserve behavior leaves every golden file
  byte-identical. One that moves has changed behavior.

## Adding a stdlib host function

1. Declare the `host fn` in `std/<module>.nomi`. A first-party
   facade names no Go symbol.
2. Add a row to `internal/stdlibbindings`: `Funcs()` for an adapter package's
   function, `RtFuncs()` for a function over rt values. A function that acts
   outside the machine in a way some target cannot gets the row's `Class`
   (`files`), which `nomi build` checks against the target
   (`vmhost.TargetLacks`, `vmhost.Program.CheckTarget`). A function that
   computes its answer from its operands alone and acts on nothing outside
   the machine gets `Pure`, so a refinement rule or a typed literal's
   handler may call it; unset, a call to it is an effect. A `Pure` function
   whose one call costs more as an operand's value grows also gets
   `Unbounded`, and those evaluations refuse it as unbounded. A `Pure`
   function whose answer is a hash keyed by the run's seed also gets
   `Seeded`, and a refinement rule, a typed literal's handler and a `check`
   refuse it, since their answer must hold in every run. A row that is not
   `Pure` sets `World: Inside` or `World: Outside`, whether a call to it
   acts on or reads from anything outside the machine (the
   `internal/stdlibbindings` package doc, "Effects and World");
   `TestEveryRowDecidesItsWorld` fails for one that sets neither.
3. `go generate ./internal/stdlibadapters`.
4. Add recorded cases for the new row (`internal/stdlibadapters/testdata/*.golden`).
5. `go test ./internal/hostpair ./internal/stdlibadapters ./internal/hostgen/...`.
6. A row that is neither `Pure` nor `Held` has an effect, and each public
   std function that reaches it is in the tooling reference's table of
   functions with effects (`docs/tooling.md`, "Effects lists"), which
   holds what the stdlib reference marks "Has effects";
   `TestEffectTableMatchesTheReference` (`cmd/nomi-docgen`) fails
   until the table holds it, and prints the table to paste.

`TestGeneratedFileIsCurrent` fails until you regenerate, and generation refuses
a row whose Go signature does not match its declaration. A Go parameter or
result for a Nomi `Int` is `rt.Int` when the function handles an Int of any
size, and `int64` when it needs a machine integer; generation refuses an
`int64` parameter whose declared type admits Ints past its range, so declare
it `Int64` (std::int) or take `rt.Int` and define what a large value means.
An adapter returning a
Nomi struct must build it with the declared type's descriptor, or every impl
lookup on it misses.

A new Go-backed module, a std type a public signature names, and a module
written only in Nomi each need more: see
the [`internal/stdlibbindings` package doc](internal/stdlibbindings/doc.go).

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
   rules are in [`tree-sitter-nomi/README.md`](tree-sitter-nomi/README.md#checks-in-the-main-repository).
3. `make build-zed-queries` (step 6's targets run it too).
   `editors/zed/languages/nomi/highlights.scm` is generated from the
   canonical query by `scripts/sync-zed-highlights.sh`: its patterns in
   reverse order, since Zed's last capture wins, the capture names Zed
   styles, and the attached-test dim, written by hand in
   `editors/zed/attached-test-dim.scm`. Never edit the generated file;
   `go test ./internal/docscheck -run TestZedHighlightsAreGenerated` fails
   while it is stale.
4. `cp tree-sitter-nomi/queries/injections.scm editors/zed/languages/nomi/injections.scm`
   (a byte-identical copy).
5. Check `editors/zed/languages/nomi/outline.scm` and `runnables.scm`:
   `grep -rn '<node>' editors/zed/languages/nomi/ editors/helix/runtime/queries/nomi/`.
6. `make build-helix` and `make build-nvim`. Helix's `highlights.scm` and all
   of Neovim's queries are generated from Zed's; never edit them by hand. `make
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

**Local editors.** A user sets up an editor with `nomi editor install
<editor>` (`internal/editors`); `make install-helix`, `install-nvim`,
`install-vscode` and `install-zed` run it from this checkout, building the
VS Code and Zed extensions first. `make dev-editors` installs `nomi` from
this checkout and sets up each editor's dev install. `make enable-hooks` enables, once per
clone, the `post-merge` hook, which does the parts of that a merge
into `main` needs, and the `pre-push` hook, which refuses any push to `main`
(the grammar mirror's excepted) that `make publish-main` does not make.
Per-editor setup (the Zed dev extension, Helix, Neovim, VS Code, bat) is in
[`editors/README.md`](editors/README.md).

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
- **`lesson`**: a First Steps task (`tour/src/content/docs/learn/`). One file
  holds the function the reader finishes and the `test` blocks that check
  it, with a comment saying the tests need no change. It runs only on its
  Check button and has no expect block. A ` ```nomi-hint ` fence (plain
  text) and a ` ```nomi-solution ` fence (the file, solved) follow it, and
  the page reveals them on request. The doctest requires the starter to
  compile and fail a test, the solution to pass every test, and both to
  hold the same tests from the first `test` line on
  (`vmhost/tour_lesson_test.go`). A ` ```nomi-bug ` fence after a lesson
  (the lesson's code with a mistake planted in it, never shown) makes the
  reader write the tests: Check runs them on the lesson's code, where all
  must pass, then on the bug, where one must fail. There the starter and
  solution share the code before the first `test` line instead, and the
  doctest requires the starter's tests to pass on both and the solution's
  to catch the bug. A Check that does not compile shows a
  plain note for its first error from `tour/public/lesson-notes.json`;
  `TestLessonNotesMatchTheirErrors` compiles each note's example and
  requires its first error to find that note first, so reword a message
  and its note's match together.

A block runs on an in-memory file system. A `// FILE:` section that is not
Nomi source or `nomi.toml` is a data file the block starts with. A file
that is not text is a section marked `// FILE: <path> (base64)`, whose
lines hold its bytes in standard base64; the editor shows its tab as one
read-only line with its size, and `read-only:` and the data-file rules
apply to it as to any data file. The generated sections of a tour module
carry the files its `embed` declarations name this way. A
`nomi.toml` section in a directory under the block's root, or a root
`nomi.toml` with a `[dependencies]` table, makes the block a project of
modules; with no root `nomi.toml` and a `nomi.toml` in two or more
top-level directories, it is a block of sibling projects. A block's entry is its first
file with a `fn main`, else its first Nomi file that declares tests, else
its first Nomi file. After editing a `lib/` module the tour links
(`tourmodules.Modules`), run `go generate ./internal/tourmodules`;
`TestGeneratedFileIsCurrent` (`internal/tourmodules/tourmodulesgen`) fails until you
do. A block that imports a linked module runs with the module's files
attached, in `TestTourDoctests` as on the page. How blocks of modules,
sibling projects and the linked modules run is in the
[`internal/tourmodules` package doc](internal/tourmodules/doc.go); the live editor's
layout is in [`tour/README.md`](tour/README.md).

The stdlib reference (`src/content/docs/reference/`, gitignored) is generated
by `cmd/nomi-docgen` from `///` doc comments and `//#` module comments in
`std/*.nomi`. Edit those comments, never the generated pages. `docgen` does not
prune: after renaming or removing a stdlib module, delete its old reference
page. Each `//!` prompt becomes a live editor, except one that reaches an
effect class the js/wasm build lacks (a socket, a name lookup, a
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

```sh
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
- A change to how a subsystem works updates that package's doc comment
  (its `doc.go`) in the same change, and a change to a test harness updates
  the harness's own doc comment. Revise the text so it says how the
  package works now, folding the change into it; do not append a paragraph
  per fix. A package has one package comment, in `doc.go`; a long one uses
  `# Heading` sections after an overview. Cite a section from elsewhere as
  `(internal/irbuild package doc, "Linkage names")`.
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
- `TestEveryFenceHasALanguageTheSiteRenders` (`internal/docscheck`) fails
  for a fence in authored Markdown or a std doc comment with no language,
  or one `tour/src/lib/rehype-shiki.mjs` gives no form. The forms: `nomi`
  for code; `console` for commands (with a `$` prompt) and anything a
  terminal prints, diagnostics and test reports included; `tree` for a
  directory listing; `gomod` and `toml` for go.mod and nomi.toml; `text`
  for anything else that is not code. A file only GitHub renders uses
  `sh` for commands to copy.
- `TestScriptsNameGoPackagesThatExist` and `TestScriptsAreExecutable` hold
  `scripts/` to the tree, and `TestAgentsNamesWhatExists`
  (`internal/docscheck`) holds this file to it: every test, repository
  path and `NOMI_` variable it names must exist.
- `nomi run`, `check`, `test` and `build` keep each program in the artifact
  cache, with a record of its files from which an edit that changes only
  bodies relinks it ([kept programs](vmhost/doc.go)). Under
  `go test` a process keeps none unless the test sets `NOMI_ARTIFACT_CACHE`
  to a directory of its own; a `nomi` a test starts keeps them under the
  directory the test binary chose. `NOMI_DEBUG_ARTIFACT_CACHE=1` says why a
  command read or kept nothing.
- The build cache of runners (`internal/runners`) refuses the real user
  cache under `go test`; set
  `NOMI_RUNNER_CACHE_ROOT`. The module cache refuses the real user
  cache under `go test`; set `NOMI_MODULE_CACHE`.
- The tree-sitter CLI reads a global parser-directories config that may point
  at another checkout; `scripts/tree-sitter.sh` runs it from this checkout's
  `tree-sitter-nomi/`.
- Test output embeds absolute paths, so when comparing output from two
  checkouts byte for byte, use paths of equal length or normalize them.
