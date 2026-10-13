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
- [ ] **Debugger follow-ups.** The debugger plan has landed (feature-status's *Debugger* section). Left: attaching to a running process or a `nomi build` executable (the image keeps the debug info); debugging a program with Go bindings, which runs in a generated wrapper process; and step back in a program with more than one task, which a replay cannot reproduce without recording the scheduler's choices too.
- [ ] **Programs the builder declines.** The checker accepts some programs the IR builder cannot lower, and `nomi run` refuses them before running with a "not supported yet" error at the code the builder stopped at. Every recorded program runs, so each remaining shape is a gap an ordinary program can hit. The known shapes are listed under feature-status's known defects.
- [ ] **The builder's `mobile` flag.** The builder's `lower` still returns whether a value may be read more than once without a copy, and about a hundred call sites pass it on. The VM evaluates each temporary once, so the flag and the `irCopyForce` copies it guards could go. It is a large mechanical change with no behaviour behind it.
- [ ] **FFI follow-ups.**
  - Runtime-typed dispatch (an `import_impls` escape hatch) for values from FFI or deserialization whose impls the analyzer cannot record statically.
  - `host type` field access and Nomi-side construction.
- [ ] **Startup cost of `nomi run`.** Hello world takes about 0.13 s on an Apple-silicon Mac. `nomi check` on the same file, which runs the front end including the stdlib's analysis, takes about 0.08 s; the remaining 0.05 s is lowering and running. The stdlib is analyzed and lowered again in every process. A `nomi build` executable skips the front end and the lowering and starts in under 10 ms.
- [ ] **A prebuilt `nomi` still needs Go for Go FFI.** `nomi run`/`test`/`check`/`build` on a Go-FFI project build a wrapper or a nomi-vm that links the compiler's own Go module beside the project's bindings, so they need a Go toolchain. A versioned `nomi` (a release, or `go install ...@version`) requires that module at its own version and Go fetches it; without Go it refuses with a message (`internal/ffirun`, `internal/gotoolchain`). A program importing `std/compiler` needs Go for `nomi build` too, since its nomi-vm variant is not shipped. Homebrew and Scoop packages are not written: they need a tap and a bucket repository.
- [ ] **Standalone WASM programs, what remains.** `nomi build --target js/wasm` and `--target wasip1/wasm` write valid modules that run under Node, the wasip1 one with the program in its own memory and the js/wasm one with it in a `nomi.image` custom section. A hello world is 11 MB (2.0 MB brotli) and starts in about 130 ms under Node, most of it compiling the module. GC behaviour, wasmtime and the browser's start time are unmeasured. `nomi build --web` is checked by hand in Chromium only. It refuses Go bindings (some pure-Go bindings would compile to js/wasm, but the FFI `nomi-vm` build has never been tried there), refuses file access where a `files` option served from the worker's `fs` could stand in, and has no `nomi test` counterpart. A wasip1 `nomi-vm` takes a compiled program (`.nomic`) framed on stdin (`nomi build --no-vm`), which a browser WASI shim could use to run many programs on one cached `nomi-vm`; that is tested under `node:wasi` with no preopens and has not been tried in a browser. The wasip1 reactor a program with an `export fn` builds is tested under `node:wasi` with no preopens only, not yet in a browser's WASI shim, and it cannot link a project's Go bindings.
- [ ] **Calling JavaScript from Nomi.** A page calls a Nomi program's exported functions (`nomi.js`'s `load` and `call`), but Nomi cannot call the page: no DOM, `fetch` or local storage. The mirror of `export fn` would be a small set of host functions in a `std/web`-like module, or the page passing callbacks to `load`. Nothing is designed.
- [ ] **An npm package for the wasm build** (`cmd/nomi-wasm`, the tour's runtime) with a small JS API: run source, hover, semantic tokens.
- [ ] **Tour hardening, what remains.** The stack-depth limit (`rt/calldepth_js.go`, 1,000 calls) and the run-worker's recovery were measured in Chrome and Node only; Firefox and Safari are unmeasured. And a Go fatal error prints a goroutine trace to the worker's console naming the build machine's paths; `-trimpath` would remove them, and it must go on both `scripts/build-tour-wasm.sh` and `vmhost/tour_bundle_test.go`'s rebuild at once.
- [ ] **Doc-gen, registry and package-manager UX.** `nomi doc`, publishing, distribution polish. Nomi modules ride the Go module proxy.

## Track 2: Irreversible semantics

- [ ] **Language versioning and compatibility.** The mechanism (editions, fixers, a compatibility promise) for when the language stops breaking. Until then it breaks freely, which is correct before a first release.

## Track 3: Architecture to leave room for

- [ ] **A stable serialization format.** The linked-IR image (`ir.EncodeImage`/`DecodeImage`) is the one serialized form today and carries no compatibility promise. Incremental compilation will want front-end artifacts serialized too; design the formats together rather than ad hoc.
- [ ] **WASM Component Model interop** for cross-language imports and exports. WIT interfaces and `wasip2` components are the standard that would replace the wasip1 reactor's generic `nomi_call` ABI, which passes function names and JSON as data because one prebuilt runner serves every program and Go fixes a module's export names when it links it. Go cannot produce components yet. Track it; nothing is designed.

## Track 4: Big but additive

- [ ] **Unified container type/value syntax.** Tuples (`(Int, String)` ↔ `(1, "x")`) and anonymous structs (`{a: Int}` ↔ `{a: 1}`) already use one outer syntax for type and value; lists and maps do not (`List<Int>` vs `[1, 2, 3]`). Proposal: also accept `[Int]` and `{String => Int}` as type forms. Needs its own design.
- [ ] **Field-projection derive** (`derive Getters for T`). For an interface whose functions are pure field projections (`fn hour(value: self): Int`), synthesize the trivial impl, which would remove boilerplate like `std/calendar`'s `DateParts`/`TimeParts` impls. `derive` covers `Equatable`, `Hashable`, `Comparable`, `Debug`, `Display`, `ToJson` and `FromJson` today.
- [ ] **Opaque handles for exported functions.** Every argument and result of an `export fn` crosses as JSON, so a value the host passes back unchanged is encoded and decoded on each call. A handle (a value that stays in the VM, referred to by an id) would matter for large values and values with no JSON form. Wait for a program that needs it.
- [ ] **Checked examples in stdlib `///` comments.** The generated reference renders a declaration's `//!` attached tests as runnable editors, and those are the stdlib's examples. A ` ```nomi ` block inside a `///` comment is only highlighted: nothing parses, checks or runs it, so one can go stale unnoticed. Either check those blocks or settle that `//!` is the only example form.
