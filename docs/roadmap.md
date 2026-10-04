# Nomi Roadmap: Big-Ticket Items

The strategic punch list of large, cross-cutting initiatives: the things that
take a design pass and several changes. It complements:

- [`spec.md`](spec.md), the canonical spec for what the language does.
- [`feature-status.md`](feature-status.md), what works today: shipped compiler and tooling milestones, the aspirational-spec punch list and known defects.

This file is the forward view: open work only. When an item lands, delete it here and record the result in feature-status.md.

## The organizing principle

Sort by retrofit cost: how expensive a thing is to change after code depends
on it.

1. **There is one engine.** The front end (parse, analyze, type-check) feeds
   `internal/irbuild`, which lowers every body to IR; the VM compiles that IR to
   bytecode and runs it over `rt`'s values. `nomi run`, `nomi test`, the REPL,
   the tour, `nomi build`'s executables and Go embedders all use it. Golden
   files under `testdata/expectations/` are the only oracle.
2. **Irreversible semantics before additive features.** A numeric, string or
   collection decision baked into every program matters more than any feature
   that can be added later without breaking anyone.

## Track 1: Execution and tooling

- [ ] **Incremental compilation.** Front-end caching keyed by content hash, with dirty tracking. It pays into everything that runs the front end: the LSP, `nomi check`, `nomi run` and the REPL. It builds on `BuildProjectWithCache` and the module index (`internal/analysis/module_index.go`).
- [ ] **Incremental REPL checking.** Each REPL input re-checks and re-lowers every live declaration from source, so an input costs more as the session grows. Types, impls and imports are not versioned the way functions and values are: redeclaring a type drops everything that names it, and a redefined `impl` replaces the old one for earlier definitions too (`cmd/nomi/repl_vm.go`). Both go away when an input can be checked against the session's earlier analysis instead of its source.
- [ ] **A debugger for the VM.** There is none. Every IR instruction carries a mandatory source position (`ir.Lint`'s `RulePositionValid`) and the VM keeps a per-instruction site table, so a breakpoint is a check in the dispatch loop and locals are a frame read. Expose it over DAP. A first cut at a host crossing should show the arguments, the result and any fault rather than step into Go; an embedded VM is a Go process, so `dlv` still breaks inside a Go adapter.
- [ ] **Programs the builder declines.** The checker accepts some programs the IR builder cannot lower, and the VM reports them as `BLOCKED` before running. Every recorded program runs, so each remaining shape is a gap an ordinary program can hit. The known shapes are listed under feature-status's known defects.
- [ ] **The builder's `mobile` flag.** The builder's `lower` still returns whether a value may be read more than once without a copy, and about a hundred call sites pass it on. The VM evaluates each temporary once, so the flag and the `irCopyForce` copies it guards could go. It is a large mechanical change with no behaviour behind it.
- [ ] **FFI follow-ups.**
  - Runtime-typed dispatch (an `import_impls` escape hatch) for values from FFI or deserialization whose impls the analyzer cannot record statically.
  - `host type` field access and Nomi-side construction.
- [ ] **Startup cost of `nomi run`.** Hello world takes about 0.13 s on an Apple-silicon Mac. `nomi check` on the same file, which runs the front end including the stdlib's analysis, takes about 0.08 s; the remaining 0.05 s is lowering and running. The stdlib is analyzed and lowered again in every process. A `nomi build` executable skips the front end and the lowering and starts in under 10 ms.
- [ ] **A prebuilt `nomi` still needs Go for Go FFI.** `nomi run`/`test`/`check`/`build` on a Go-FFI project build a wrapper or runner that links the compiler's own Go module beside the project's bindings, so they need a Go toolchain. A versioned `nomi` (a release, or `go install ...@version`) requires that module at its own version and Go fetches it; without Go it refuses with a message (`internal/ffirun`, `internal/gotoolchain`). A program importing `std/compiler` needs Go for `nomi build` too, since its runner variant is not shipped. Homebrew and Scoop packages are not written: they need a tap and a bucket repository.
- [ ] **Standalone WASM programs.** `nomi build --target wasip1/wasm` writes a file, but it is not a valid WebAssembly module: the IR image is appended after the module's last section, and a WASI host (Node 24's `node:wasi`) rejects it with `unknown section code`. The image needs to go into a custom section, or the runner has to load it from elsewhere, before size, startup and GC behaviour can be measured.
- [ ] **An npm package for the wasm build** (`cmd/nomi-wasm`, the tour's runtime) with a small JS API: run source, hover, semantic tokens.
- [ ] **Tour hardening, what remains.** The stack-depth limit (`rt/calldepth_js.go`, 1,000 calls) and the run-worker's recovery were measured in Chrome and Node only; Firefox and Safari are unmeasured. And a Go fatal error prints a goroutine trace to the worker's console naming the build machine's paths; `-trimpath` would remove them, and it must go on both `scripts/build-tour-wasm.sh` and `vmhost/tour_bundle_test.go`'s rebuild at once.
- [ ] **Doc-gen, registry and package-manager UX.** `nomi doc`, publishing, distribution polish. Nomi modules ride the Go module proxy.

## Track 2: Irreversible semantics

- [ ] **Language versioning and compatibility.** The mechanism (editions, fixers, a compatibility promise) for when the language stops breaking. Until then it breaks freely, which is correct before a first release.

## Track 3: Architecture to leave room for

- [ ] **A stable serialization format.** The linked-IR image (`ir.EncodeImage`/`DecodeImage`) is the one serialized form today and carries no compatibility promise. Incremental compilation will want front-end artifacts serialized too; design the formats together rather than ad hoc.
- [ ] **WASM Component Model interop** for cross-language imports and exports. Track it; nothing is designed.

## Track 4: Big but additive

- [ ] **Unified container type/value syntax.** Tuples (`(Int, String)` ↔ `(1, "x")`) and anonymous structs (`{a: Int}` ↔ `{a: 1}`) already use one outer syntax for type and value; lists and maps do not (`List<Int>` vs `[1, 2, 3]`). Proposal: also accept `[Int]` and `{String => Int}` as type forms. Needs its own design.
- [ ] **Field-projection derive** (`derive Getters for T`). For an interface whose functions are pure field projections (`fn hour(value: self): Int`), synthesize the trivial impl, which would remove boilerplate like `std/calendar`'s `DateParts`/`TimeParts` impls. `derive` covers `Equatable`, `Hashable`, `Comparable`, `Debug`, `Display`, `ToJson` and `FromJson` today.
- [ ] **Checked examples in stdlib `///` comments.** The generated reference renders a declaration's `//!` attached tests as runnable editors, and those are the stdlib's examples. A ` ```nomi ` block inside a `///` comment is only highlighted: nothing parses, checks or runs it, so one can go stale unnoticed. Either check those blocks or settle that `//!` is the only example form.
