# Nomi Implementation Status

A contributor dashboard. The spec, [`spec.md`](spec.md),
is canonical for what the language does: **if a feature appears there
without a "Status: not yet implemented" callout, it works.** This file does
not re-list language features. It tracks a punch list of spec sections that
are still aspirational, the parts of shipped features that are not built, the
tooling capabilities the spec does not describe, and known defects. How each
part is built is in [`implementation-notes.md`](implementation-notes.md).

For the forward view (big-ticket initiatives and their order) see
[`roadmap.md`](roadmap.md).

---

## Aspirational language features

Spec sections with a "Status: not yet implemented" callout.

None.


`grep -n "Status: not yet" docs/spec.md` should
match each item above.

## Decisions: intentionally not adopted

- **Interface composition / `extends`.** Multi-bound `where` clauses (`where T: A and B`) cover consumer-side composition, and `embeds` enums cover heterogeneous collections, so an interface-inheritance mechanism would add a second way to say both.
- **Variants as types.** A variant declared inside an enum is not a type (spec §4 *Variants as Parameter Types*). `embeds` already makes a struct or distinct type declared on its own a variant, a subtype of the enum and a narrowing target, so a function that takes one variant takes that type. Making inline variants types too would add a second way to say it, and a rule for whether `Shape.Rectangle{...}` builds a `Rectangle` or a `Shape`. Rust, Swift, Haskell, F#, Elm and Gleam make the same choice and write the wrapping struct; Scala 3 and Kotlin, whose enum cases are classes, do not.
- **Read-only (matchable but not constructable) fields.** `opaque` stays all-or-nothing: a type either exposes its fields for construction, matching and access, or hides all three behind its own functions.

## Known quirks and open defects

- **`continue` in `Iter.loop` retries with the same state.** By design, and stated in the spec's `loop` section. `continue #(i + 1, s)` is a checker error naming the working form (`internal/analysis/continue_value.go`).
- **Programs the checker accepts and the VM cannot run.** The IR builder declines some bodies. `nomi check` lowers the program without running it and reports each declined body that `main` or a test reaches as an error at the expression the builder stopped at (`vmhost.Program.Unsupported`), one per function; a function only a declined function calls is reported once its caller lowers. It takes test files and directories of them, and lowers each case as `nomi test` would; on a stdlib module it lowers the module's `//!` cases. `nomi run` stops with the same errors before its first effect, and `nomi test` reports each case it cannot run as `BLOCKED` with the same error; `NOMI_DEBUG_LOWERING=1` adds the builder's own reason to each as a hint. The language server reports them when a file is opened or saved and 400 ms after the last edit of a burst (`internal/lsp/lowering.go`). Each shape found is pinned in `vmhost/lowering_gaps_test.go` and listed here; none is known.

## Gaps in shipped features

Parts of a shipped feature that are not built. Each line is open work.

- **Checked literals.** `Url` and `Toml` are the std types that implement `CheckedLiteral`; `Regex`, `Route`, `Uuid` and the calendar types keep `Literal`, since their slots splice text or can fail. `LiteralError`'s fields have no defaults, so a `check` names all three. A checked literal with no slots is built each time it is reached, not once per site as a backtick literal is. A `Url` path slot of `.` or `..` is written `%2E` or `%2E%2E`, which a WHATWG URL parser, as browsers use, still reads as a dot segment. A `Toml` literal checks its static text with a stand-in for each slot, so two key slots that hold the same value at run time, or a key slot whose value matches a static key, write a key twice, which a TOML reader refuses; an `Int` slot outside 64 bits is written whole, which a reader may refuse too.

- **Streams.** `io.is_terminal` and `Writer.from_fn`. Deadlines on pollable reads: a file read runs to completion, and only a read of standard input can be cancelled.
- **Capability classes.** No real target lacks `files` yet; `--web` prints a note when a program uses files. `nomi check --target` and a `[build] targets` list for the language server are not built. A refusal names a call's opening parenthesis as its column.
- **Recursion check.** Mutual recursion, calls inside lambdas and interface default bodies are not checked for calling themselves forever.
- **`io.replay`.** `Replayed` is not generic, so it does not carry `run`'s value.
- **Numbers and lists.** Compact and scientific notation, and ICU's contextual list words (Spanish `y`/`e`). `String.join_list` takes a `List<String>`, not an `Iter`, because a std function cannot take an `Iter<T>` unless it is generic, and a generic one's defaults cannot be left out.
- **Money.** Accounting patterns (`(€12.50)`), cash rounding (CHF's 0.05), `Currency.name`, and currency spacing beyond CLDR's `alphaNextToNumber` patterns. Names are carried only for currencies some region uses today; a historical currency's name is its code.
- **Dates and times.** Time zone names (a `Full` or `Long` time writes the GMT offset), flexible day periods (`B` is written as AM/PM), quarter and week names and fields, `OffsetDateTime.format`, `DateTime.parse_in`, `parse_in` of a `NaiveDateTime`, CLDR's `appendItems` (a skeleton field no available format has is appended after a space), and calendars other than the Gregorian.
- **Collation and case.** The `search`, `searchjl`, `unihan`, `zhuyin`, `eor` and `emoji` collation types and `alt` variants, `en-US-POSIX`'s rules, `maxVariable`, `-u-kr-` and `-u-kv-`, and Greek upper-casing without accents.
- **HTTP.** `std/http` covers Go's net/http, httputil, httptest, httptrace, cookiejar and mime/multipart, `std/tls` crypto/tls and `std/net` TCP, as `internal/stdhttp/inventory.txt` lists row by row. Go's callbacks into a program (TLS `certificate_for` and `verify`, a server's connection-state, error-log and shutdown hooks, HTTP/2's error counter, the proxy-connect hook) run on the task waiting in the crossing that set them off: the server's `serve`, the client's send, a handshake or a TLS accept. So a TLS listener runs each handshake on the accepting task, one at a time. Request tracing records events as data (`Tracer`) rather than calling back. `Request.read` and `Response.read` read the body whole, a byte at a time under Go's reader, and a multipart form read whole (`multipart_form`, `read_multipart_form`) is held in memory; `multipart_parts` streams. `Url` has no `ToJson`. On Windows std/net reports a refused or reset connection as `net.Error.Other`. A js/wasm build sends through `fetch` in a browser, and cannot serve or dial TCP; under Node, Go's js/wasm transport does not use `fetch`, so a request fails there.
- **Background jobs.** Recurring and cron jobs, unique jobs, priorities, a start rate shared across processes (`Rate` holds one worker process), a web UI, and a SQLite backend for `std/jobs`. `Inline` ignores the `Conn` an enqueue gives it, so a rolled-back enqueue still stores its job there.
- **Mail.** `std/mail` sends through SMTP only; a provider's HTTP API (tags, tracking, stored templates) would be a `lib/` module. Not built: internationalized addresses (SMTPUTF8, IDNA domains), quoted local parts and domain literals, inline images (`multipart/related`), CRAM-MD5 and XOAUTH2, connection reuse and pipelining, partial delivery (a refused recipient fails the whole send), DKIM signing, and setting a message's Message-ID.
- **Object storage.** Objects move whole, as `Bytes`: no listing (S3's `ListObjectsV2`, which needs an XML reader std does not have), multipart uploads, streamed gets and puts, copy, conditional writes, ETags, metadata beyond the content type and `Cache-Control`, tagging or versioning. `lib/storage/s3` takes a static key pair, with no session token, instance role or profile, and manages no buckets. `storage.handler` reads the whole object into memory before it answers. `storage.local` cannot hold keys `a` and `a/b` at once, and two concurrent puts to one key may pair one's bytes with the other's content type.
- **Logging and actors.** `std/log`'s times are written with a `+00:00` offset, not slog's `Z`.
- **Processes.** A started program's standard input from `Stdin.Inherit` or `Stdin.From` is copied through the running program, so the child sees a pipe rather than the terminal, and so do its output and error under `process.run`. No process groups and no signal but a kill; a program is looked up on the running program's `PATH`, not on one the command's `env` sets; and `with_spawned` does not use the command's `stdin`.
- **Hashing and encodings.** A digest of a stream (a file is hashed whole, as `Bytes`) and base32.
- **Messages and nouns.** Nouns built from data at run time. A noun declared in a source locale whose language has genders is refused. No quick fix adds a missing noun translation from its declaration, and none accepts every locale's stale entry at once. A message parameter whose type is an alias of `Int`, `Float`, `Decimal` or `String` must be written as that type. A stale entry rejected by `Catalog.load` shows the declaration's text in canonical MF2, not as written.
- **Locales in the browser.** The wasip1 reactor cannot load `.nomil` files through `nomi-wasi.js`. `nomi run --web` and `--watch` do not rebuild when only a catalog changes.
- **The Postgres driver.** `copy_from` (`COPY ... FROM STDIN`): it needs the driver's own connection, and a transaction's `Conn` reaches a program as the std interface, with no way back to it. Arrays of more than one dimension and composite values, which are refused. `postgres.scratch()` on Windows, which has no POSIX shell to stop its server.
- **sqlkit on Postgres.** A query's parameters bind through `ToSql` and are not checked against the type Postgres infers for them, so a mismatch is Postgres's error when the query runs. A type sqlkit has no family for (`interval`, `inet`, a composite) reads as text. A query whose generic plan cannot be read (`EXPLAIN EXECUTE` refuses it) has every table column nullable, with no note saying why.
- **Images.** `lib/image` does not read EXIF orientation, so a photo a phone stored rotated decodes rotated. It encodes no WebP (Go has no encoder) and reads no GIF, and its encoders write pixels only, with no metadata or color profile.
- **Migrations (`lib/sql/migrate`).** A built program reads its migrations directory when it runs: Nomi has no way to put data files inside a built executable, so a program that does not use sqlkit's generated `schema.migrations` needs the directory beside it. On SQLite a second process that migrates at the same time waits only the driver's 5-second busy timeout for the first. No down migrations, by design.
- **HTML templates.** A slot cannot stand for a whole attribute list (`<input ${attributes}>`), and `<script type="application/json">` takes no slot; pass data to a script in a `data-` attribute. Attribute names are not checked. `Html` is written by that name: a type alias of it is an ordinary prefix. Markdown is in `lib/markdown`; std renders none. The language server does not complete element or attribute names inside a literal.
- **Generated code.** `nomi check` and the language server do not check a generated file's header digest; only the generated test catches a stale file.
- **Modules.** Full minimal version selection over pure-Go modules' go.mod files (the wrapper's `go build -mod=mod` resolves those), `exclude` directives, the proxy's `/sumdb/` endpoint, and `GOINSECURE`. Old managed Go toolchains are never evicted.

## Type checker

- [ ] Unused file-private declarations are not reported.
- [ ] A discarded call (`_ = f(x)`) is not diagnosed. Deciding that it is dead needs effect tracking, and purity is not enough: Nomi traps, so `_ = a / b` can fail on a zero divisor and is observable.

## Language server

`nomi-lsp` provides:

- Diagnostics with spans, hints and related locations, published 50 ms after the last edit; `dbg` and `todo` as warnings.
- Code the compiler cannot lower yet, reported in the background as `nomi check` reports it.
- Static typed literals evaluated on the VM: an invalid one is an error, and hover shows a valid one's value.
- Hover: signature, doc and up to five `//!` examples; on literal and pattern punctuation, both characters of `|>`, `?`, `!` and a lambda's `fn` and `->`.
- Completion by position: keyword snippets, members, import paths, typed parameter names, constructors and fields that fit the expected type, field accessors, missing struct fields and `case` arms, missing impl functions, named arguments, derivable interfaces, pipe stages, typed-literal bodies, and auto-import.
- Signature help and inlay hints (parameter names, binding types, pipe stage types), each hint kind with an `inlayHints.*` setting.
- Go to definition, go to type definition, references, rename with prepare rename, document highlight, document and workspace symbols, document links on imports, and call hierarchy.
- Code lenses to run and debug each test, group, attached test and `fn main`.
- Quick fixes: imports, `source.fixAll`, implement missing functions, add missing fields or arms, generate function, call a bare pipe stage, insert `then`, and fixes the parser attaches to its errors.
- Refactors: convert to and from a pipe, between `case` and `if`/`else`, pattern match on a value, extract and inline variable, and between a capture and a lambda.
- Formatting.
- Renaming or moving a `.nomi` file or folder updates every import of it.
- Message catalogs: diagnostics in catalog files, hover per locale, completion of keys, and quick fixes for missing or stale translations, plural, enum and gender arms.
- A workspace index of closed files, and background diagnostics for them.
- Inline values while debugging.

Not yet implemented:

- [ ] A static typed literal whose `Ok` value's Debug rendering the VM refuses (a `Regex`, a host handle) shows no value on hover; its `Err` is still reported. A buffer that does not check, or whose project answers `host fn`s from an embedder's table, gets no literal evaluation. `nomi check` evaluates no double-quoted literal.
- [ ] Completion inside a statement that does not parse yet solves a generic call's type parameters from its arguments' types alone. A lambda argument or explicit type arguments (`f<Int>(xs)`) leave the result unknown, so `.` after such a call offers nothing.
- [ ] Go-to-definition on a function a type inherits from an interface default (`Dog.shout(rex)` where `shout` is `Speech`'s default) returns nothing.
- [ ] Sibling files read from disk parse with error recovery but not the editor's resilient parse, so a syntax error inside one of their functions drops that whole function from an open file's project analysis.
- [ ] Call hierarchy does not trace an interface-qualified call (`Display.to_string(x)`) to the implementation it dispatches to, and a stdlib function has no outgoing calls.
- [ ] The fill quick fix is not offered for an `else` binding's non-exhaustive arms.

## Debugger

`nomi dap` runs a program or a file's tests in process and provides:

- Launch `mode: "run"` (`main` of a file, or of a project with one entry point) and `mode: "test"` (a file's tests, or one by `line`).
- Line breakpoints, conditions, hit counts and logpoints; function breakpoints by the name the call stack shows.
- Step over, in and out at statement granularity; step into target; pause; `stopOnEntry`; just my code.
- Step back and reverse continue, by replaying the program from its start.
- Restart frame.
- Exception stops for traps, failed assertions and an `Err` from `main` (on by default), and a failed task reaching its `Task.await` (off by default).
- A thread per task, named by its spawn site.
- Evaluation in watches, hovers and the debug console; an expression with an effect needs `// allow effects`.
- Locals as a lazy tree, a Pipeline scope with each stage's value, and a scope for each enclosing `io.capture` or `io.replay`.
- Go to a variable's binding.
- Program input and output in the editor's terminal, and a test's own output in the console as it runs.

Editor support:

- **VS Code:** the `nomi` debug type, launch snippets, F5 on the active file, Debug lenses, inline values, step back and reverse continue, function breakpoints and Step Into Target. It offers no command that opens a variable's binding.
- **Neovim:** an nvim-dap adapter, Debug lenses, `:NomiDebug` (with `back`, `reverse`, `target` and `break <function>`), go to binding through nvim-dap's tag jump, and neotest's `dap` strategy.
- **Zed:** the `Nomi` debug adapter and a debug entry for each `nomi run` and `nomi test` task, with step back. Zed has no reverse continue and sends no step-into-target, function-breakpoint or `locations` request.
- **Helix:** a debugger block with run and test templates; no step back, step into target, function breakpoints or go to binding.

Not yet implemented:

- Step back in a program that starts a task, uses `concurrent`, a supervisor or a channel: it turns off at the first one, saying why. A replay cannot reproduce how tasks interleave.
- A step back's replay runs to its target without stopping; pause does not interrupt it.
- An expression evaluated with `// allow effects` runs outside the recording: what it reads or writes is not replayed.
- A type the frame cannot name that no program file, or two, declare `pub` (a private type of another file) does not compile in an expression.
- Programs with Go bindings (`gopkg`): `nomi dap` refuses to launch them, since they run in a generated wrapper process.
- A test launch names one file; a directory of test files is not supported.
- Attaching to a running process or a `nomi build` executable.
- The Helix debugger block is checked as TOML but has not been run in Helix; the Zed adapter is built and unit tested but has not been run in Zed.

## Formatter

- `nomi fmt` has no configuration: 100 display columns (a wide character counts two), 4-space indent.
- It is idempotent and does not change what a program means; tests hold it to both over every tracked file.
- `-w` writes, `-l` lists changed files, and with no path it reads stdin.
- [ ] `-d` prints each changed file's formatted text under a `--- path` line; it is not a unified diff.

## Builds and targets

- `nomi build` writes a native executable, cross-compiled with `--target goos/goarch`. It refuses a program with a `todo` or a `dbg`.
- `--target js/wasm` and `--target wasip1/wasm` write a WebAssembly module, and refuse a program that starts a process (`--allow-unsupported processes` builds it, and the start answers `Unsupported`). A wasip1 program's compiled image may be at most 4 MiB. A js/wasm build caps call depth at 1,000.
- A wasip1 program with an `export fn` builds as a WASI reactor, with `nomi-wasi.js` beside it to call its exports from browsers and Node.
- `--web` writes a folder a static host serves, with typed `call`s for exported functions; `nomi run --web` serves it on localhost and reloads on each change.
- `--no-vm` writes a `.nomic` compiled program, which needs no Go toolchain. `nomi build-vm` (`--compiler`, `--reactor`) writes a runner for `.nomic` files.
- A file starting with `#!` runs as a script, with or without the `.nomi` extension.
- Releases ship archives for five platforms, runner archives for `js/wasm` and `wasip1/wasm`, and a checksum manifest ([install.md](install.md)).

## Go FFI and embedding

- [ ] Runtime-typed dispatch (`import_impls`) for values whose impls the analyzer cannot record.
- [ ] `host type` field access and Nomi-side construction.
- [ ] Opaque handles for Go channels, functions and non-empty interfaces.

## Platforms

- [x] macOS and Linux: built, tested in CI on Linux (`.github/workflows/test.yml`) and day to day on macOS.
- [ ] Windows (amd64): `scripts/release.sh` builds a windows/amd64 archive, and nothing has run on Windows yet. Every package and its tests cross-compile (`GOOS=windows go vet ./...`), and CI has `windows` and `windows-irbuild` jobs, which have not run yet. What is written for Windows and unverified there:
  - Paths: std/fs takes `/` or `\` and answers `/`, with a drive as `C:/Users`; the compiler names files by OS path and modules by `/`.
  - Executables: `nomi build` writes `<entry>.exe` with no `-o`, the managed Go toolchain and release archives are `.zip`, and the FFI wrapper, `go` and `nomi-vm` are found by their `.exe` names.
  - The language server: document URIs are `file:///C:/...`, and each URI a client sends is made canonical (drive upper-cased, `%3A` unescaped) before a handler reads it.
  - The debugger: breakpoints match a client's path whatever the drive letter's case, and the integrated terminal connects over a Unix socket, which needs Windows 10 1803 or later.
  - Signals: `nomi run --watch` and `nomi run --web` stop on Ctrl-C through `os.Interrupt`; the tests that send a child Ctrl-C skip on Windows, since Go cannot.
  - Source with CRLF line ends reads as with LF, multi-line strings and comments included, and `nomi fmt` writes LF. Checked on macOS with CRLF text; the CI checkout is LF.
  - Not checked at all on Windows: the editor extensions, the tree-sitter and tour builds, and the js/wasm tests (the Windows jobs install no Node).
