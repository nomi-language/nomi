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
| [`docs/implementation-notes.md`](docs/implementation-notes.md) | How parts of the spec are built: formatter layout, Go FFI wrapper, std adapters. |
| [`docs/feature-status.md`](docs/feature-status.md) | What works today: compiler and tooling status, known defects. |
| [`docs/roadmap.md`](docs/roadmap.md) | Open big-ticket work and its order. Done items leave it. |
| [`docs/install.md`](docs/install.md) | Installing from a release or from source. |

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
| `cmd/` | Binaries: `nomi` (the CLI), `nomi-lsp`, `nomi-runner` (what `nomi build` appends a program to), `nomi-wasm` (the browser build), `nomi-docgen` (stdlib reference). |
| `vmhost/` | The public embedding API. |
| `vmrunner/` | What a built executable runs. Generated FFI runners import it. |
| `hostadapt/` | Support package that generated FFI wrappers import. |
| `internal/` | Everything else. Syntax: `lexer`, `parser`, `ast`, `token`, `strlit`. `analysis` (resolution and the type checker), `frontend` (the whole front end as one call), `irbuild` (IR builder), `ir` (IR, lint, image encoding), `vm` (bytecode VM), `lsp`, `format`, `ffirun` (Go FFI wrapper and runner builds), `std*` (Go-backed stdlib adapters), and the tooling behind them. |
| `std/` | The standard library's `.nomi` sources, embedded into the binaries. |
| `rt/` | The runtime library, `github.com/nomi-language/nomi/rt`. The VM runs over it and built executables link it. It may import only the standard library and `github.com/rivo/uniseg` (`TestRuntimeImportsOnlyItsAllowlist`). |
| `docs/` | The spec, style guide, feature status, roadmap and install guide. |
| `tour/` | The Astro Starlight site at nomi-lang.org: the tour and the generated stdlib reference. |
| `tests/` | The test corpus. |
| `testdata/expectations/` | Golden files. |
| `benchmarks/` | Engine benchmark programs. |
| `tree-sitter-nomi/` | The tree-sitter grammar, source of truth for editor parsing. The generated `src/` and `tree-sitter-nomi.wasm` are committed. Published read-only to github.com/nomi-language/tree-sitter-nomi by `scripts/publish-grammar.sh`. |
| `editors/zed/` | Zed extension (highlighting, LSP launch). |
| `editors/helix/` | Helix language config and runtime queries. |
| `editors/nvim/` | Neovim 0.11+ plugin: filetype, LSP config, generated queries, test runner, neotest adapter. |
| `editors/vscode/` | VS Code extension (TypeScript): language configuration, a TextMate grammar derived from the bat syntax, the `nomi-lsp` client, run and test commands. |
| `editors/bat/` | `bat`/syntect syntax definition. |
| `scripts/` | Tour wasm build, release, grammar publish, and editor sync scripts. |

## Architecture in brief

**One engine.** Every way of running Nomi uses the same pipeline:

```
source → front end (parse, analyze, type-check) → internal/irbuild → IR
       → internal/vm (compiles each ir.Func to bytecode on first call) over rt values
```

`nomi run`, `nomi test`, the REPL, the tour and `nomi build`'s executables all
run on the VM. There is no interpreter and no Go backend. `nomi check` and
`nomi fmt` execute nothing; `nomi check` lowers the program as `nomi run`
does, and a test file's cases as `nomi test` does, and reports each body the
run would refuse as an error at its source
(`vmhost.Program.Unsupported`); the LSP does the same in the background when a
file is opened or saved and 400 ms after the last edit of a burst
(`internal/lsp/lowering.go`). The LSP executes one thing, on the same VM: the
handler of a typed literal with no `${...}`, and only when
`vm.Machine.Effects` finds nothing it can reach acts outside the machine
(`internal/lsp/literal_eval.go`).

- **Front end.** `internal/frontend` parses, injects synthetic host
  declarations, lowers derives, synthesizes universal `Debug`, and runs
  whole-program analysis. The checker (`internal/analysis/checker.go`) is two-phase:
  `BuildTypes` registers types, `CheckTypes` walks bodies. Names resolve
  whole-program, so the import graph may be cyclic and import order never
  matters.
- **IR builder.** `internal/irbuild` lowers every body a program runs (module
  functions, impl functions, `once` initializers, test bodies, lambdas, stdlib
  bodies) into an `ir.Func`. Generic functions, types and impls are
  instantiated per type-argument tuple a program reaches; generic stdlib
  bodies are instantiated per program, never in the shared cache. Every IR
  temporary has a stored value type (`ir.ValType`), and a retained graph must
  pass `ir.Lint`. A body the builder declines is not retained; a program that
  reaches one fails before its first effect. `nomi run` prints the
  diagnostic `nomi check` gives, at the expression the builder stopped at
  and worded for the author ("this call to `f` is not supported yet, so
  `fn g` cannot run"), and `nomi test` reports the case as `BLOCKED` with
  that diagnostic. The builder's own decline reason is never shown unless
  `NOMI_DEBUG_LOWERING=1` is set, which adds it to each such diagnostic as a
  hint (`vmhost.DebugLowering`); `vmhost.Blocked.Reasons` and
  `CaseResult.Reasons` carry it for tests. The builder's own type is `kind` (a
  tag plus declaration pointers, spelled in Nomi by `nomi()`), and a value is
  an `expr`: its IR temporary and its kind. Do not add Go text or Go names to
  the builder.
- **VM.** `internal/vm` compiles each `ir.Func` to a flat `[]uint32` bytecode
  with three typed register banks (word, string, reference). Values are `rt`
  values: scalars, `*rt.Record` for structs, enums, tuples and the like, and
  `rt`'s persistent collections. Tail calls replace the caller's activation.
  Interface dispatch selects an impl body by the runtime type name of the
  declared `self` operand. The VM links no front end
  (`TestVM_ReadsTheIRAndNothingElse`).
- **rt.** The runtime library holds collections, checked arithmetic and its
  trap text, Float semantics, hashing, equality, rendering, the task and
  supervisor runtime, and the test reporter. It links no front end
  (`TestRuntimeArtifactLinksNoFrontEnd`) and imports nothing outside the
  standard library, `uniseg` and itself (`TestRuntimeImportsOnlyItsAllowlist`).
  Its internal organization may change freely to serve the VM.
- **Stdlib.** `std/` is one Nomi module embedded into the binaries. Each
  process analyzes it once (`std.Shared`) and lowers it once. Stdlib files
  import each other with bare paths (`import iter.Iter`); code outside writes
  `std/iter`.
- **Host functions.** A stdlib `host fn` is answered either by a VM intrinsic
  (tasks, channels, collection kernels, `io.print`) or by a generated adapter.
  `internal/stdlibbindings` is the one hand-maintained table: `Funcs()` for the
  Go-backed modules (`std/calendar`, `std/random`, `std/regex`) and
  `RtFuncs()` for functions over rt values. `internal/stdlibadapters/adapters_gen.go`
  is generated from it.
- **Embedding.** `github.com/nomi-language/nomi/vmhost` is the public Go API: `Load`/`LoadSource` with
  options (`WithHosts`, `WithVirtualFiles`, `WithOutput`, `WithInput`, ...),
  `Program.Run`, `Program.Call`, `Program.Test`, and `Check`. The CLI, REPL,
  tour wasm and FFI wrapper all go through it. A plain user `host fn` or
  `host type` is answered by an embedder's host table.
- **Go FFI.** A Nomi file declares `gopkg` handles and `go alias.Symbol`
  bindings. A Go standard library package needs no `go.mod`; its source is
  read from `go env GOROOT` and the toolchain version keys the cache
  (`internal/ffirun/gostd.go`). `internal/ffirun` discovers them, generates a wrapper `main.go`
  with typed adapters (`nomiHostTable`) under the user cache directory
  (`os.UserCacheDir()/nomi/builds/<hash>/`), and runs it. The wrapper's
  go.mod names the compiler module in one of two ways
  (`internal/ffirun/compilersource.go`). A versioned `nomi` (main module
  version is a tag, or a pseudo-version from `go install ...@version`, with no
  `+dirty`) requires `github.com/nomi-language/nomi` at that version and Go
  fetches it; the cache key uses the version instead of hashing a tree. Any
  other build, including one from a checkout at an untagged commit, replaces
  the module with the tree it was built from (located via
  `runtime.Caller`). `NOMI_COMPILER_SOURCE=<checkout>` forces the replace with
  that checkout in either case. `.nomi` edits need
  no rebuild; Go changes invalidate the cache key. Tests must set
  `NOMI_FFIRUN_CACHE_ROOT`: `cacheRoot()` refuses the real cache under
  `go test`. The cache is safe to delete.
- **`nomi build`.** Writes one executable: a VM runner binary with the
  program's linked IR image appended (`ir.EncodeImage`). It is not native
  code; it skips the front end at startup. `--target goos/goarch`
  cross-compiles. A field added to an IR node fails
  `TestImage_EveryNodeFieldIsEncoded` until the encoder, decoder and
  `ir.FormatVersion` carry it.
- **REPL.** `nomi` with no arguments keeps one live VM and links each input
  into it as a new program (`cmd/nomi/repl_vm.go`, `vmhost.Session`). It is lexically
  scoped across inputs, as GHCi is.

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
(reinstalling the Zed dev extension restarts it). The LSP materializes the
embedded stdlib to the user cache once per process, for go-to-definition.

## Test

```
go test ./... -count=1         # everything, rt included
go test ./rt/... -count=1      # the runtime library on its own
make test                      # stdlib tests, corpus, then go test
make test-tour                 # tour doctests, wasm bundle, grammar
```

`nomi test <dir or file>` runs `test "…"` blocks. `TestTestCommand_VMCorpus`
and `TestTestCommand_VMStdlib` run `nomi test tests` and `nomi test std` and
require no failed or blocked case, and as many passing cases as
`corpus.expect` and `stdlib.expect` declare.

A program the front end accepts must lower. `go test ./vmhost -run '^$' -fuzz
'^FuzzFrontEndAcceptsSoItLowers$' -fuzztime 5m` mutates the corpus files, tour
blocks, spec programs and generated programs (`vmhost/lowering_gen_test.go`),
checks each as `nomi check` does without running it, and fails on one the
front end accepts and the IR builder declines, or on a compiler panic. Under
plain `go test`, `TestFrontEndAcceptsSoItLowers` checks the generated programs
and `TestKnownLoweringGaps` pins each known decline in `knownLoweringGaps`
(`vmhost/lowering_gaps_test.go`) by a reproducer; the generator avoids those
shapes, so remove a gap and its avoidance together when it is fixed.

**Every expression has a type after checking.** `analysis.UnresolvedExprs`
lists each value expression of a checked file with no type in
`FileAnalysis.ExprTypes`, a nil one, or an unsolved variable as its own type
(one nested in a type argument, as in `[]`'s `List<?1>`, is allowed). It
runs in tests only: `vmhost`'s `checkLowers` reports it beside the
builder's declines, so `TestFrontEndAcceptsSoItLowers` and the lowering fuzz
target name a checker skip at its source, and `TestSeedExprsAreTyped` and
`TestStdlibExprsAreTyped` hold the corpus, tour, spec and stdlib to it. A
known skip is pinned in `knownCheckerGaps` (`vmhost/checker_gaps_test.go`)
with a reproducer that `TestKnownCheckerGaps` checks; a fix removes its
entry in the same change.

`nomi fmt` must not change what a program means. `TestFormatKeepsMeaning`
(`vmhost/format_meaning_test.go`, about 4 seconds) formats every tracked
`.nomi` file, tour and spec block, corpus seed and generated program, and two
layout variants of each (`vmhost/format_layout_test.go`: respaced, broken
across lines, commented, parenthesized, renamed long), and fails when the
output does not parse, its syntax tree or comments differ
(`format.SameMeaning`), a second format changes it, or the front end accepts
one of source and output and rejects the other. `go test ./vmhost -run '^$' -fuzz
'^FuzzFormatKeepsMeaning$' -fuzztime 5m -parallel 4` fuzzes the same check.
`TestKnownFormatGaps` pins each known break in `knownFormatGaps`
(`vmhost/format_gaps_test.go`) by a reproducer that fails once it is fixed.

**Rotating sets.** Those fixed inputs are the regression baseline; three
tests check a rotating set beside them, from a start seed that changes
every UTC day (`internal/rotation`), so each day checks inputs no earlier
run did. `TestFrontEndAcceptsSoItLowersRotating` checks 150 more generated
programs, `TestFormatKeepsMeaningRotating` two more layout variants of every
format input, and `TestLSPSurvivesTypingRotating` types five more documents
of at most 400 lines. Each adds 2 to 4 seconds. The log names the start
seed. `NOMI_GEN_SEED=<n>` pins it and makes the run repeatable, and
`NOMI_GEN_COUNT=<n>` changes how many seeds the set holds. A failure there
says it came from the rotating set and prints its seed and a command that
checks exactly that program, variant (with `NOMI_GEN_INPUT=<name>`) or
document. Such a failure may be a bug that was already there and that day's
inputs found, not one the change under test made: check it on `next`
before blaming the change, then fix it or pin it as a known gap.

CI (`.github/workflows/test.yml`, on pushes to `main` and on pull requests)
runs `go vet ./...` and `go test ./...` with `internal/irbuild` as its own
job, builds the tour bundle and runs `TestTourWasm*` against it, and runs
`make test-vscode`'s tests. It does not run the tree-sitter tests.

**Verification cadence.** The whole `go test ./internal/irbuild
-count=1 -timeout 0` run takes about 100 seconds, so it is not the per-change
check:

- **Per change:** the tests of the packages you touched, with narrow `-run`
  filters over the tests that exercise the change (for example
  `go test ./lsp ./analysis`, or
  `go test ./internal/irbuild -run 'AppField|Scoped'`), plus
  `go test ./rt/...` if `rt` changed.
- **Before merging a batch to `main`:** `go build` and `go vet`, and
  `go test ./...`. Run the whole
  irbuild package one at a time, never several in parallel.
- **A change to what the front end accepts or rejects** can break any
  `internal/irbuild` fixture, whatever files it touched, so it always needs
  the whole irbuild package before merging.
- When running several suites, capture each exit status separately; a chain
  reports only the last one.
- For concurrency changes, also run
  `go test -race ./internal/vm ./vmhost ./rt/...`.
- `rt`'s supervisor and timer tests and `vmhost/synctest_test.go` carry
  wall-clock assertions. Re-run a failure in isolation before attributing it.
- For language server changes, `TestLSPSurvivesTyping`
  (`internal/lsp/typing_test.go`) types a fixed sample of files into the
  server and fails on any panic it recovers. `NOMI_LSP_TYPING=full` types
  every tracked `.nomi` file and tour block instead (about 7 minutes split
  four ways with `NOMI_LSP_TYPING_SHARD=k/4`), and
  `go test ./internal/lsp -run '^$' -fuzz '^FuzzLSPSurvivesEdits$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4`
  mutates them. A known panic is pinned in `knownTypingGaps` with a
  reproducer.

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

The last command rewrites `vm-corpus-programs.txt` and
`vm-corpus-reports.txt`: the corpus files `internal/vm`'s harnesses compare
whole, as programs and as test reports. Every tour and failure record must
compare, so those populations have no list. Run it after the `internal/expectation`
command, since it reads `corpus.expect`. A corpus file leaving a list stopped
comparing and needs a named cause. No Go test pins a population count, and
tests name a tour block by a substring of its code
(`tourBlock(t, "pipes.md", marker)` in `internal/vm`), never by its
`chapter:L<line>` id.

`vm-retained.txt` lists every function the builder retains over the corpus
with whether the VM ran it (`TestIRRetainedPopulationRuns`). A new corpus file
or a builder change moves it; regenerate with
`NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^TestIRRetainedPopulationRuns$' -count=1`
(the whole-package regeneration above also rewrites it) and name each moved
function's cause. A function that ran and no longer runs fails by name.
`vm-std-retained.txt` is the same list for the cached std modules, keyed by
the file under `std/` and the function's index name
(`calendar.nomi:Date.Add<Days, Date>.add`), from
`TestIRRetainedStdPopulationRuns`; a change to `std/` or to how the builder
lowers it moves it. Regenerate with
`NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^TestIRRetainedStdPopulationRuns$' -count=1`.
`TestIRParamShapeAgreesWithTheVM` and its `ForStd` counterpart read their
expected `ran` counts from these two lists.

A changed golden file needs a named reason in the commit message, per moved
record or per cause. Regenerating to turn a red test green defeats the check.
`irbuild.expect` records are keyed by a hash of the program's files, with
each `.nomi` file hashed as its tokens. Comments are tokens too, with their
text. Reformatting a fixture keeps its key, but any edit to its code or its
comments needs regeneration, and so does a layout change that moves a source
line its output prints. Do not delete `irbuild.expect` to prune
it. A transcript of a program the VM cannot run (a "not supported yet"
error from `nomi run`, a `BLOCKED` case from `nomi test`;
`expectation.VMGap`) is never recorded.

Corpus assertions embed source line numbers (`line 74: check failed`), so
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

## Language conventions

The spec is canonical; these are the rules most often gotten wrong.

- **No `let`.** Bindings are `name = expr`, with optional `: T`. A binding may
  shadow an earlier one in the same scope.
- **Type bodies are newline-separated items.** Struct fields are bare
  `name: Type` lines with no commas and no `field` keyword (`field` is for
  interface requirements). Enum variants are bare, one per line, no `|`.
  Behavior lives in `impl Type { ... }` and `impl Iface for Type { ... }`
  blocks and `derive Iface for Type` declarations, all top-level.
- **Calls are qualified; there is no `x.method()`.** Interface-impl functions
  are called type-qualified (`Int.to_string(n)`), interface-qualified
  (`Display.to_string(x)`), or through a bounded type parameter
  (`T.to_string(x)`). Bare dispatch (`to_string(n)`) and file-qualified
  dispatch (`int.to_string(n)`) are errors. File qualification reaches
  file-level functions (`io.print(x)`) and nothing declared in an `impl`
  block; owner qualification reaches inherent functions (`String.trim(s)`,
  `Map.get(m, k)`). `duration.seconds(1)` is the checker's "file 'duration'
  has no member 'seconds'" error, whose hint names the owner. `x.field` is only
  field access.
- **Iteration.** `Iter` is a push protocol (`each_while`, `known_count`), and
  every generic algorithm is an owner function: `Iter.map`, `Iter.filter`,
  `Iter.reduce`, `Iter.count`. Lazy adapters fuse; materialize explicitly with
  `Iter.to_list()`, `to_set`, `to_map` or `to_vector`, and join an
  `Iter<String>` with `String.join`. There is no `List.map`.
  Container-specific operations stay on the type (`Map.get`, `Set.union`,
  `List.head`).
- **Pipe nested calls.** A call whose result feeds another call becomes a `|>`
  chain. See the style guide for the details.
- **Visibility.** Top-level declarations are file-private unless `pub`. A
  `.nomi` file is the import and visibility unit; a Nomi module is the
  directory tree rooted at `nomi.toml` (plus `go.mod` when distributed) and is
  the orphan-rule unit.
- **Naming.** Types PascalCase; functions, bindings and parameters snake_case.
- **Values are immutable; closures capture values at creation.** `return` and
  `try` inside a lambda exit the lambda.
- **Scoped application fields.** `fn boot(startup: Startup): App`, in an
  entry file, returns the app struct, which has at most one `Context` field
  (boot builds it with `Context.root()`). Its fields are read as `App.field`.
  The statement `with App.field = value` replaces one field from that line to
  the end of its block, as a `defer` there would run; several fields are
  several lines, and there is no block form. A boot must return the type every
  field read reachable from its `main` or test names.
- **Test groups.** A `tests` group holds at most one `clock`, `boot` and
  `setup`, in that order, then its tests, and groups do not nest.
  `boot server.boot(startup)` calls an entry's boot. `setup` returns any value,
  and a test binds it with an irrefutable pattern (`test "x", db { ... }`).
  Each test runs clock, boot, setup, then body, fresh.
- **`once name = expr`** is the only file-level value binding: lazy, cached,
  forced once even under concurrency.
- **Concurrency.** `concurrent { }` scopes tasks (`Task.spawn`,
  `Task.await` from `std/tasks`); `std/channels` splits a channel into
  `Sender` and `Receiver`; `Supervisor.new` runs only during `boot`.

## Compiler conventions

- A Go package goes under `internal/` unless code outside this module must
  import it. The public ones are `vmhost`, `vmrunner`, `hostadapt` and `std`:
  embedders and generated FFI wrappers and runners import them.
- Type references in the AST are `TypeExpr` nodes, never a string plus
  position.
- Syntax gets an AST node. Do not synthesize data in the analysis builder for
  something the parser should emit.
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
   function, `RtFuncs()` for a function over rt values.
3. `go generate ./internal/stdlibadapters`.
4. Add recorded cases for the new row (`internal/stdlibadapters/testdata/*.golden`).
5. `go test ./internal/hostpair ./internal/stdlibadapters ./internal/hostgen/...`.

`TestGeneratedFileIsCurrent` fails until you regenerate, and generation refuses
a row whose Go signature does not match its declaration. An adapter returning a
Nomi struct must build it with the declared type's descriptor, or every impl
lookup on it misses.

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
2. Update `tree-sitter-nomi/queries/highlights.scm` (first match wins:
   specific patterns first) and `queries/injections.scm`.
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
`scripts/publish-grammar.sh` (on a clean tree) runs
`git subtree split --prefix=tree-sitter-nomi HEAD`, pushes the split commit to
the mirror's `main` without forcing, and writes its sha into both files;
commit those two files afterwards. The split is deterministic, so a rerun with
no grammar change does nothing. `--dry-run` splits and reports only;
`--remote <url>` pushes somewhere other than the https URL (for example over
ssh).

**Zed dev extension.** For a dev install Zed uses `editors/zed/grammars/nomi/`
(gitignored) as the grammar checkout: it requires that directory's `origin` to
equal `extension.toml`'s `repository`, tries `git fetch origin <rev>` (a
failure is ignored), then runs `git checkout <rev>`. Run
`scripts/sync-zed-grammar.sh` in any fresh clone or worktree, and after grammar
changes. It makes that directory a git repository with the published origin
whose `dev` branch holds this checkout's `tree-sitter-nomi/`. When the pinned
rev's files equal the local grammar, `extension.toml` is left alone; otherwise
the script points `rev` at the local commit and says so. Do not commit that
rev: `scripts/sync-zed-grammar.sh --restore` puts the committed one back, and
a publish overwrites it. Then run "zed: install dev extension" and select the
`editors/zed/` directory itself. Install the LSP with
`go install ./cmd/nomi-lsp` (Zed launches `nomi-lsp` from PATH).

**Helix.** `make dev-helix` links your Helix config's Nomi queries to this
checkout and installs a `languages.toml` block whose grammar source is this
checkout's `tree-sitter-nomi/` path, so local grammar changes need no publish.
`make install-helix` copies the queries instead, for people who only use Nomi.

**Neovim.** Load `editors/nvim` from this checkout (lazy.nvim `dir =`) and run
`make dev-nvim` to compile `editors/nvim/parser/nomi.so`. `make install-nvim`
copies a snapshot into Neovim's package directory, for people who only use
Nomi. Nothing under `plugin/` or `ftplugin/` may require neotest. See
`editors/nvim/README.md`.

**Local editors after a change.** `make dev-editors`
(`scripts/dev-editors.sh`) installs `nomi` and `nomi-lsp` from this checkout,
runs the Neovim and Helix dev setups, syncs Zed's grammar checkout, says
when Zed's dev extension must be reinstalled, and rebuilds and installs the
VS Code extension when `code` is on PATH. `scripts/git-hooks/post-merge`
runs the parts a merge into `main` needs (Go or `std/` changes, grammar or
editor changes, `editors/vscode/` changes). Enable it once per clone with
`make enable-hooks`. It acts only on `main`, or on the branches listed in
`git config nomi.editorBranches` (space-separated, for example `main next`
when work lands on a local branch first), so agent worktrees, which share
the clone's config, are unaffected.

**VS Code.** `make install-vscode` builds `editors/vscode/nomi.vsix` with
`vsce package` and installs it with `code --install-extension`; never publish
it. `make test-vscode` runs its unit tests, which include tokenizing every
`.nomi` file under `tests/` and `std/` with the grammar, and its
grammar assertions (`editors/vscode/test/grammar/syntax.nomi-test`).
`npm --prefix editors/vscode run test:smoke` runs the extension in a
downloaded VS Code against `nomi` and `nomi-lsp` built from the checkout. Test
at Cursor finds the enclosing test by scanning text (`src/testLine.ts`), since
VS Code has no tree-sitter. See `editors/vscode/README.md`.

**bat.** `cp editors/bat/nomi.sublime-syntax "$(bat --config-dir)/syntaxes/" && bat cache --build`.

## The tour and stdlib reference

`tour/` is an Astro Starlight site. Chapters are
`src/content/docs/*.md`, listed in `astro.config.mjs`'s sidebar. A
` ```nomi-run ` block is doctested and becomes a live editor in the browser;
put its expected output in a hidden `<!-- expect ... -->` block right after
the fence. Plain ` ```nomi ` blocks are highlighted only. Highlighting,
hovers and runs happen client-side with the same grammar, queries, analyzer
and VM the editor uses (compiled to wasm).

The stdlib reference (`src/content/docs/reference/`, gitignored) is generated
by `cmd/nomi-docgen` from `///` doc comments and `//#` module comments in
`std/*.nomi`. Edit those comments, never the generated pages. `docgen` does not
prune: after renaming or removing a stdlib module, delete its old reference
page.

Needs Node 22+. `NODE22_BIN` in the Makefile defaults to Homebrew's path;
override it elsewhere.

```
make start-tour                           # wasm build + dev server at http://localhost:4321/
make deploy-tour                          # production build into tour/dist
go test ./vmhost -run TestTourDoctests    # run every nomi-run block
```

`make deploy-tour` rebuilds the wasm bundle, gates it against the source
tree, and runs `npm run build`, which ends with `csp-audit.mjs` (the built HTML
must match `public/_headers`' hash-pinned CSP) and `check-dist.mjs` (no file
over 25 MiB). Deployment settings are in `tour/README.md`.
Re-run `scripts/build-tour-wasm.sh` after changing tour content, the grammar,
client JS, stdlib doc comments, or any compiler, VM or rt code linked into
wasm.

## Docs discipline

- When you ship, change or remove a language feature, update the spec in the
  same change. Aspirational spec text carries a `> **Status: not yet
  implemented**` callout, and each such callout is listed in
  feature-status.md's "Aspirational language features".
- New authoring conventions go in `style.md`.
- A change that affects users (the language, `std/`, the CLI, the editor
  extensions) ends its commit message with a paragraph that starts
  `User-facing:` and says what changed for someone writing or running Nomi,
  in one or two plain sentences, with no internal file paths or commit
  hashes. When the tour or the stdlib reference covers it, add the section's
  URL (`https://nomi-lang.org/pipes/#the-then-stage`). Internal refactors and
  test-only changes have none. The maintainer gathers these into the message
  of the squashed commit that publishes `main`: plain text, opening with
  `Highlights` (at most five one-line items), then `Added`, `Changed`,
  `Fixed` and `Removed`.
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
- `TestScriptsNameGoPackagesThatExist` and `TestScriptsAreExecutable` hold
  `scripts/` to the tree.
- `internal/ffirun`'s cache refuses the real user cache under `go test`; set
  `NOMI_FFIRUN_CACHE_ROOT`.
- The tree-sitter CLI reads a global parser-directories config that may point
  at another checkout; `scripts/tree-sitter.sh` runs it from this checkout's
  `tree-sitter-nomi/`.
- Test output embeds absolute paths, so when comparing output from two
  checkouts byte for byte, use paths of equal length or normalize them.
