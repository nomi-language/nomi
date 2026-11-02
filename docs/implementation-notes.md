# Nomi implementation notes

Why the implementation is built the way it is: the paths through the system
that no single file shows, the invariants that span packages, the outside
constraints the code obeys, and the tests that guard each rule. A language
user does not need any of this; the [spec](spec.md) says what the language
does. [`AGENTS.md`](../AGENTS.md) holds the rules every change follows and a
short map of the pipeline that links each subsystem here. What a function
does, and where something lives, is for grep and the code's own comments.

## Contents

**How the implementation is built.** [The engine and `nomi check`](#the-engine-and-nomi-check), [Front end](#front-end), [Keyword declarations and their inputs](#keyword-declarations-and-their-inputs), [Language server analysis](#language-server-analysis), [IR builder](#ir-builder), [VM](#vm), [Debugging and `nomi dap`](#debugging-and-nomi-dap), [rt](#rt), [Int: exact at any size](#int-exact-at-any-size), [Stdlib](#stdlib), [std and lib](#std-and-lib), [Host functions](#host-functions), [Why std uses `host` rather than `go`](#why-std-uses-host-rather-than-go), [Files](#files), [Logging and actors](#logging-and-actors), [Locale data](#locale-data), [Collation](#collation), [Embedding](#embedding), [Go FFI wrapper](#go-ffi-wrapper), [Modules](#modules), [`nomi build`](#nomi-build), [REPL](#repl).

**Formatter and syntax.** [Formatter: layout](tooling.md#formatting), [Formatter: brace-bounded field constructs](#formatter-brace-bounded-field-constructs), [Formatter: fixed layouts](#formatter-fixed-layouts), [Formatter: one-line `if` and binding `else`](#formatter-one-line-if-and-binding-else), [Formatter: a line break after a literal's opening bracket](#formatter-a-line-break-after-a-literals-opening-bracket), [Formatter: filled literal lists](#formatter-filled-literal-lists), [Formatter: comparison and boolean operators](#formatter-comparison-and-boolean-operators), [Formatter: compound depth](#formatter-compound-depth), [Formatter: comments](#formatter-comments), [`or` patterns](#or-patterns), [Captures](#captures).

**Testing, editors and the tour.** [Test harnesses](#test-harnesses), [Grammar checks](#grammar-checks), [Editor development setup](#editor-development-setup), [Tour internals](#tour-internals).

## The engine and `nomi check`

`nomi check` lowers the program as `nomi run` does, and a test file's cases
as `nomi test` does, so it reports each body a run would refuse as an error
at its source. A check that only type-checked would pass programs that then
fail to run.

The language server does the same in the background, 400 ms after the last
edit of a burst and on open and save. It lowers from its own analysis of
the program when the program it keeps checked cleanly and every other file
it read still holds that text on disk. That is several times faster than
reading the files again, and `TestLoweringFromAnalysisMatchesTheSource`
holds it to report exactly what a run from the text reports. An imported
file open with unsaved edits sends the run to the text, since `nomi check`
reads that file from disk. A new edit stops the document's run in progress.

Every load, `nomi check`'s included, runs one thing on the VM before the
program runs: the handler of each backtick typed literal whose handler
returns a `Result`. An `Err` is a compile error at the literal. A handler
that can reach something acting outside the machine is refused, so a
compile never has effects. The language server also evaluates double-quoted
typed literals with no `${...}` this way, for hovers and diagnostics.

## Front end

The front end parses, injects synthetic host declarations, expands keyword
declarations, lowers derives, synthesizes universal `Debug`, and then
analyzes the whole program. The checker has two phases: one registers every
type, and the second walks bodies. Names resolve whole-program, so the
import graph may be cyclic and import order never matters.

The lexer leaves `>>>` as three `>` tokens. The parser joins them into the
shift operator only after an operand, so nested type arguments such as
`List<Maybe<Box<Int>>>` still close one `>` at a time.

## Keyword declarations and their inputs

A top-level declaration that starts with a keyword and a name
(``message cart_items(count: Int) `...` ``) is read by one rule whatever the
keyword. `message` and `noun` are the only keywords Nomi knows, and any other
is an error. Nomi has no macros: a library that generates code ships a tool
(`nomi tool`) that writes ordinary `.nomi` files.

Every path that prepares a file expands these declarations, so the checker,
the language server and the builder all see the same functions. A `message`
becomes the function it declares, in its place, with the declaration's
parameter nodes and its name's position, and a body in the synthesized band.
A `noun` becomes the `once` of std/message's `Noun` it names.

An expansion gets two hidden imports (`message$`, `message_format$`) that no
source can name. They are the only way in to std's internals: std/message's
private constructors and the internal modules `std/message_format` and
`std/money_format`. A program that names one is told it is internal, and
docgen does not document them. This is why the IR builder lowers a call to a
std module's private function.

The expansion reads inputs, the catalogs, and records each with its text and
a span map back to the declaration and each catalog entry. An error in an
input is reported at the input: `nomi check` prints the input's text, and
the language server publishes it on the input's URI. Agreement of nouns and
messages is checked over the whole program.

A kept program records its inputs. A changed input is an edit to the files
that read it, with their text unchanged, so a catalog edit that keeps every
message's signature rebuilds one file's bodies.

## Language server analysis

An open document is analyzed as part of a whole program that the
`DocumentManager` keeps per project root and entry: the program of the first
entry candidate that reaches it, else its own.

An edit that leaves each edited file's interface fingerprint as it was
rebuilds only those files' bodies, in place. A `once`'s value counts as a
body; an unannotated one is inferred again and must keep its type (early
cutoff). Any other edit checks the edited files, and the files that import
them directly or through others, afresh, and keeps every other file's
analysis. The whole program is checked afresh instead when the edit changes
the impls a file brings, when a kept file holds something of a rechecked
one, or when the rechecked files are every file.

The program-wide reductions over bodies (coherence, whole-file imports, a
rootless file's application-field roots) keep each file's part with the
analysis it came from, so a body edit reads only the edited files again.

A recheck rewrites installed analyses in place. So a reader of one holds
`DocumentManager.View`, and a recheck takes that lock exclusively until it
has installed its answer.

`TestIncrementalMatchesFull` and `FuzzIncrementalMatchesFull` hold a recheck
to a full check, dump for dump, with no kept analysis holding one a recheck
replaced (`StaleForTest`). `TestOpenDocumentEditsMatchFreshBuilds` holds each
document's diagnostics to an analysis of the file on its own. See
[incremental checking](#incremental-checking).

The server never applies an edit computed from an analysis older than the
text it edits. `didChange` stores the text and returns, and the document is
analyzed 50 ms after the last edit. Completion and signature help answer at
once from the last finished analysis, with positions mapped into it, since
waiting made menus arrive too late. Hover, definition, references, symbols,
semantic tokens and inlay hints wait up to 2 s for the current analysis.
Rename and code actions fail with `ContentModified` rather than answer from
an older one.

## IR builder

The builder lowers every body a program runs into an `ir.Func`: module and
impl functions, `once` initializers, test bodies, lambdas and stdlib bodies.
Generic functions, types and impls are instantiated per type-argument tuple
a program reaches. Generic stdlib bodies are instantiated per program, never
in the shared cache.

A std module in `programStdModules` is not in the shared cache at all. It is
lowered with each program that imports it, as one more module of the
program, so its types are the program's types and one program has one
descriptor for each. Its interfaces are program interfaces, boxed and
dispatched by the VM's ordinary path. Its positions are stdlib positions, so
a trap inside it is reported at the program's call, as one inside a shared
module is. `TestProgramStdModulesQualify` holds each listed module to the
rule: no `host fn` or `host type`, no shared std module imports it, and it
checks and lowers with no decline.

A `Regex<C>`'s capture type C is phantom. The builder lowers every
`Regex<X>` as one host handle, and a std function whose type parameters
appear only inside `Regex<...>` is not generic to it. `String.captures`
builds the C value from a match's groups by C's shape alone, through a VM
intrinsic, so no std body is instantiated per capture type.

Every IR temporary has a stored value type, and a retained graph must pass
`ir.Lint`. The shared stdlib lowering skips lint outside `go test`, since it
is the same in every process of a binary; `TestStdlibLowersTheSameWithoutLint`
requires that lowering to pass lint and to retain what the linted one
retains.

A body the builder declines is not retained, and a program that reaches one
fails before its first effect. `nomi run` prints the diagnostic `nomi check`
gives, at the expression the builder stopped at and worded for the author
("this call to `f` is not supported yet, so `fn g` cannot run"). `nomi test`
reports the case as `BLOCKED` with that diagnostic. The builder's own
decline reason is shown only with `NOMI_DEBUG_LOWERING=1`, which adds it to
each such diagnostic as a hint.

The builder's own type is `kind` (a tag plus declaration pointers, spelled
in Nomi by `nomi()`), and a value is an `expr`: its IR temporary and its
kind. Do not add Go text or Go names to the builder.

### Instantiation and dispatch

A generic function, type or impl is specialized to each set of concrete type
arguments a program uses it at. Inside a body, `T` is a known type, and
`T.format(x)` calls that type's implementation directly.

Interface calls on existentials dispatch at run time. An interface-typed
parameter, field or collection element erases its concrete type (spec §4).
Values carry their type identity, and the call selects the implementation by
the receiver's identity. What a program may do with a value of interface type
is the spec's §12 *Interface values*.

## VM

The VM compiles each `ir.Func` to a flat `[]uint32` bytecode with three typed
register banks (word, string, reference). Values are `rt` values. An `Int`
that fits int64 is the word itself; a larger one is described in
[Int: exact at any size](#int-exact-at-any-size). Tail calls replace the
caller's activation.

Interface dispatch selects an impl body by the runtime type name of the
declared `self` operand. A value whose runtime type names no impl (a std
container, a value widened into an `embeds` variant) is boxed where it
becomes an interface value. The box names the static type. Dispatch selects
by that name and opens the box for a body that takes the concrete type, and
hashing and rendering otherwise see through the box.

`==` on two interface values, a Set element and a Map key compare by
run-time type. Values of two types (two box names) are unequal. Values of
one type compare through its hand-written `Equatable` and `Hashable` when
the module records them, and structurally otherwise. So an interface value
satisfies an `Equatable` or `Hashable` bound: `Equatable.equal` on two is the
same comparison, and `Hashable.hash` on one hashes the same keys.

The VM links no front end (`TestVM_ReadsTheIRAndNothingElse`).

## Debugging and `nomi dap`

The builder records each function's variables (name, temporary, Nomi type,
scope) and statement marks (statements, arms, loop iterations, pipe stages),
and the image carries both. A machine opened with a debugger compiles a
`stmt` hook at each mark and a `pc` hook before each instruction that may
call back into Nomi. A machine without one compiles neither, so a plain run
pays nothing (`TestDebug_AMachineWithoutADebuggerCompilesNoHooks`).

`nomi dap` is a Debug Adapter Protocol server over stdio. It loads the
program with `vmhost` and runs `main` or a file's tests in process. A launch
with `console: "integratedTerminal"` asks the client to `runInTerminal` the
hidden `nomi dap-terminal <socket>`, which connects back over a Unix socket
and carries the program's input and output. The VS Code extension and the
Neovim plugin start it; `nomi-lsp`'s Debug lenses name `nomi.debugTest` and
`nomi.debugMain`.

Step back replays the program rather than restoring a saved state. A debug
machine counts the statements it runs and records each crossing out of the
machine whose answer a replay cannot compute (input, the clock, randomness,
timers, files, std/compiler's hosts) with its result and statement. Going
back abandons the run and replays the program from its start on a fresh
machine, answering each crossing from the recording and checking that it is
the same call at the same statement. There are no checkpoints, so going back
from statement N runs every statement before the target again. A read that
an `io.capture` or `io.replay` answers runs again instead of being recorded,
so the capture's transcript replays too. A replay cannot reproduce how tasks
interleave, so step back turns off at the first task, `concurrent`,
supervisor or channel.

An evaluated expression (a watch, a hover, the debug console) is checked and
lowered as a function appended to the frame's file, whose parameters are the
frame's variables it names, at their recorded types. It runs on a view of
the stopped machine that links its lowering without changing the machine.
An expression that can reach an effect is refused unless it ends with
`// allow effects`, and such an expression runs outside the step-back
recording.

go-dap v0.12.0 has neither the `declarationLocationReference` field nor the
`locations` request, so `internal/dap/protocol.go` defines them over its
types.

## rt

The runtime library holds collections, exact Int arithmetic, Float
semantics, hashing, equality, rendering, the task and supervisor runtime,
and the test reporter. Built executables link it, so it links no front end
(`TestRuntimeArtifactLinksNoFrontEnd`) and imports nothing outside the
standard library, `uniseg` and itself (`TestRuntimeImportsOnlyItsAllowlist`).
Its internal organization may change freely to serve the VM.

## Int: exact at any size

An `Int` holds a whole number of any size, and its arithmetic is exact. The
representation keeps the common case, a value that fits in 64 bits, as cheap
as a plain 64-bit integer.

**In `rt`.** A value that fits int64 is an `int64` everywhere: a `Value`, a
VM word register, a record's word slot. A value past int64's range is a
`*rt.BigInt`, an immutable `math/big` integer. There is one canonical form:
every operation answers an int64 when the value fits, so `big - big` that
lands back in range is an int64 again. Equality, hashing, ordering, Map keys
and rendering therefore compare like with like and need no normalization
where they are used.

**In the VM's registers.** Int stays in the word bank. A pointer cannot live
in a word: the collector does not scan the word bank, and a pointer stored
there as bits would be freed under it. So the word bank has a shadow, a
slice of `any` with one entry per word. A big Int occupies the word
`rt.BigWord` (2^63, the bits of `math.MinInt64`) and its `*rt.BigInt` sits
in the shadow at the same index. A word of `rt.BigWord` with a nil shadow is
the int64 `math.MinInt64`.

A reader looks beside a word only when the word is `rt.BigWord`, so the
shadow beside any other word may be stale. Every path that can write the
word `rt.BigWord` writes its shadow too: constant loads, moves, call
arguments, returns, tail calls, field reads and the slow path of each Int
operation. A closing window clears its shadows only once a big value has
been written on that stack, so a program that stays within int64 pays one
predictable branch per return.

The fast path of addition adds two words when neither is `rt.BigWord` and
the result fits int64 and is not `rt.BigWord`; anything else goes to a slow
path kept out of the loop, which answers exactly. Multiplication detects a
product past int64 with `bits.Mul64` rather than a division. Equality
compares words and looks at the shadows only when both are `rt.BigWord`,
since in canonical form a big value never equals an int64. Division and
remainder fault on a zero divisor and on nothing else.

**In records.** An Int field is a word slot. A big value is `rt.BigWord` in
the slot and the `*rt.BigInt` in the record's big slots, which a record
allocates only when it holds a big Int. A record that holds none keeps its
plain layout, and code that writes an int64 word straight into a slot stays
right, since a word with no big slot beside it is its own int64.

**Alternatives not taken.**

- Int in the reference bank, as `any`: Go allocates when it boxes an int64 of
  256 or more into an interface, so most arithmetic results would allocate.
- A 16-byte register of a word and a pointer: it doubles the word bank, makes
  every Float and Bool register a pointer the collector scans, and adds a
  store to every write.
- A tagged word holding an index into a table of big values: entries would
  need a collector of their own to be freed.
- A separate `BigInt` type beside a 64-bit `Int`: it keeps the fast path, but
  every API has to choose between two integer types, and `Int` arithmetic
  still traps where it runs out of bits.

**Constants and the image.** A literal too long for int64 is carried through
the AST and IR as a big constant, and the image encodes it as base-10 text.

**64-bit modular arithmetic.** `derive Hashable`'s mixers and
`Int.wrapping_add`, `wrapping_sub` and `wrapping_mul` answer the low 64 bits
of the exact result as a signed int64, so a hash stays a 64-bit number.

**Where an Int must fit a machine integer.**

- An index, a count or a size (`List.get`, `Iter.take`, `String.slice`,
  `Bytes.get`, ...) reads an Int past int64's range as past any collection:
  out of range, all of it or none of it.
- A Go function bound with `go`: an `int64`, `int` or narrower parameter
  refuses a value it cannot hold, and the VM raises that at the call as a
  fault naming the Go function and parameter. A `uint64` result past int64's
  range is a big Int. A `*big.Int` or `rt.Int` parameter or result takes any
  Int.
- A std host function written in Go takes `rt.Int` when it handles every Int
  (`Int.pow`, `Int.format`, `Bytes.get`), and `int64` otherwise, where the
  adapter refuses a value that does not fit.
- `Duration` and `Instant` are 64-bit counts of nanoseconds, which clocks,
  timers and Go's `time` package count in. Their constructors and operators
  fault past that.
- SQLite's `INTEGER` is 64 bits, so lib/sql/sqlite refuses an `Int`
  parameter past that range with `sql.Error.Database` and the code
  `SQLITE_MISMATCH`, rather than writing it as a float or as text that reads
  back as another value.
- JSON: an integer of any length decodes to an exact `Int` and encodes as its
  digits. nomi.js and nomi-wasi.js hand JavaScript an Int past
  `Number.MAX_SAFE_INTEGER` as a BigInt and take a BigInt argument as the Int
  it holds; nomi.d.ts types an Int as `number | bigint`.
- A Range's count protocol inside rt counts in int64; a range of more
  elements than that declines there, while `known_count` answers the exact
  count.

## Stdlib

`std/` is one Nomi module embedded into the binaries. Each process analyzes
it once and lowers it once, except the modules in `programStdModules`, which
are lowered with each program that imports them (see
[IR builder](#ir-builder)).

The analysis also works out the stdlib's share of a program's impl index and
coherence facts once. A build folds only its own files and lays them over
that base, which no build writes. The `TestStdBase_*` tests in
`internal/analysis` and `vmhost` hold it to a build that folds every file.

Stdlib files import each other with bare paths (`import iter.Iter`); code
outside writes `std/iter`.

`std/_fixtures/<name>.nomi` files are test-only modules
(`std/_fixtures/<name>`). They are in the stdlib only of a test binary run
with `NOMI_STD_FIXTURES=1`, so a test that needs one runs itself again in a
child process with it set. They are always lowered per program.

## std and lib

std holds what Go's standard library can back with no third-party code:
`std/fs`, `std/sql`'s vocabulary, later `std/http` over `net/http`. `lib/`
holds first-party modules that need third-party Go code or are opinionated
layers over std: database drivers (`lib/sql/sqlite`, over
modernc.org/sqlite), `lib/sql/sqlkit`, later an HTTP router or framework.

Each lib module is its own module with its own `go.mod` and `nomi.toml`,
nested in this repository, so the compiler's module excludes it and its
requirements never enter the compiler's go.mod. It is versioned with nomi's
commits, with no tags. A module that requires a sibling `replace`s it with
the sibling's directory, and requires a published commit only as a floor.
`nomi add` and `nomi update` keep every `github.com/nomi-language/nomi/lib/...`
module a project requires on one commit, the one added or the newest, so a
sibling require never needs moving when a batch is pushed.
`scripts/pin-lib.sh <commit>` moves the floors when a module comes to need a
newer sibling, and `scripts/pin-lib.sh --verify <commit>` adds sqlkit at a
pushed commit from the proxy into a fresh project and runs sqlite.

`TestLibModules` in `cmd/nomi` runs their Nomi tests, the shop example's
generated-code check and the driver's Go tests. It first runs
`go mod download` in the driver, which needs the network when the Go module
cache lacks modernc.org/sqlite. CI's lib job vets and tests their Go side. By hand: `go test -C lib/sql/sqlite ./...`, and `go vet` the same
way in `lib/sql/sqlite` and `lib/sql/sqlkit`. The tour links a lib module's
browser build through a workspace (see [Tour internals](#tour-internals)).

## Host functions

A stdlib `host fn` is answered either by a VM intrinsic (tasks, channels,
collection kernels, `io.print`) or by an adapter generated from the one
hand-maintained table, `internal/stdlibbindings`. AGENTS.md's "Adding a
stdlib host function" lists the steps for a new row.

A new Go-backed module also needs:

- its package in `hostPackages` (`internal/irbuild/stdlib.go`);
- its crossings in `vm`'s `pureCrossings` when they act on nothing outside
  the machine, since a typed literal's handler may only reach those;
- a row in `stdlibInventory`.

In a module lowered once for the process, a std type that a public
signature names (an enum, an opaque distinct, a carrier struct a host
function returns) needs an rt Go type and a row in `internal/irbuild`'s
`stdenum.go`, `opaque.go` or `stdstruct.go`. An interface used as a value
needs a `stdIfaceSpecs` row and an rt dispatch table. Without them, every
call whose signature names the type is reported as not supported yet.

The shared std modules are lowered once, in import order, and a body that
calls into a module lowered after its own is refused. So a foundational
module that names a later one's type (std/int naming `Locale`) passes the
value to its host function rather than calling the later module's functions
on it (`"${locale}"` would call its `Display`). `typeOnlyImportModules`
keeps such an import from reordering the modules. In a shared module, a
function's parameter cannot be an `Iter<T>` unless it is generic, and a
generic function's defaults cannot be left out at a call.

A module written only in Nomi (no `host fn`, no `host type`) that no shared
std module imports needs none of that: add its name to `programStdModules`
(`internal/irbuild/programstd.go`) and a row in `stdlibInventory`, and it is
lowered with each program like the program's own files (`std/uuid` is).

## Why std uses `host` rather than `go`

`host` means the implementation is outside Nomi and the standard library owns
it. Where the Go code sits is a layout question the binding table answers,
not something the declaration spells. That is OCaml's arrangement under one
keyword rather than two: `external length : string -> int = "%string_length"`
marks a compiler primitive and `external open : ... = "unix_open"` marks a C
stub, both `external`, with the symbol saying which.

The implementation sits in one of two places:

- **`rt`**, the runtime library the VM runs over. The language primitives:
  `String.trim`, `Map.get`, `Int.add`, `Bytes.length`, the `Iter` adapters.
- **A package in the compiler's module** (`internal/stdcalendar`,
  `internal/stdio`, `internal/stdregex`, ...). These wrap a library or an OS
  facility, and keeping them out of `rt` puts their dependency and their
  binary size on the programs that ask for them. `std/calendar` is why a
  hello-world artifact contains no `time/tzdata`. The stdlib is one Nomi
  module, so such a package is not separately distributable and carries
  neither `nomi.toml` nor `go.mod`.

Why std does not use the `go` spelling:

- **It would need a Go toolchain.** A `go`-bound module makes `internal/ffirun`
  stage and build a wrapper, so a program importing it needs a Go toolchain;
  on a machine without Go, the first `nomi run` downloads one (the managed
  toolchain, about 70 MB) and every new set of bindings is a Go build.
  `internal/ffirun/hostkeyword_toolchain_test.go` asserts that no stdlib import
  takes that path.
- **It pays the FFI projection on every call.** An `rt`-backed `host fn` is
  called with the VM's own values; a `go` binding goes through the FFI
  projection, which converts each argument into a Go value and the result
  back. `Bytes` crossing as `[]byte` makes an O(1) length read cost an O(n)
  copy.
- **`rt`'s own types do not project.** `rt.Bytes` is a Go string, so the
  boundary would read it as a Nomi `String`, and `rt.Maybe[rt.Byte]` is a
  struct, so a `Maybe<Byte>` return would arrive as a record of its Go fields.
  `rt` chose its representations for a path that has no projection.

A user's `go` binding pays the same projection, which is correct: it crosses a
real FFI boundary into code the compiler does not own.

## Files

`std/fs`, `std/path` and `std/fs_testing` are lowered with each program.
std/fs's crossings go through the internal module `std/fs_host` (`files`
class) to a `Backend` in `internal/stdio`. There are two backends: the
operating system's, one implementation for the whole disk and for an
`*os.Root` with every path written with `/` on every system, and an
in-memory one, which resolves symlinks a name at a time.

Each `rt.Frame` carries the backend it reaches: the machine's, else the
build's (the disk natively, one in-memory file system on js/wasm). `nomi
test`, in process and in a Go-FFI wrapper, gives a test file the disk with
its working directory at its module root. `fs_testing.run` and `fs.within`
replace the backend for their body, and tasks inherit it as they inherit
`io.capture`.

`fs_testing.conformance` holds the in-memory backend to the disk; std's
tests run it against both. A test that touches the disk does it inside
`fs.with_temp_dir` or a Go `t.TempDir()`.

## HTTP

`std/url` and `std/http` are lowered with each program (`programStdModules`), so their structs stand in public signatures; their Go crossings are the internal modules `std/url_host` (rt/url.go, pure) and `std/http_host` (internal/stdhttp). A `Url` crosses as a list of its 14 parts. A request crosses as its parts too: method, URL text, headers as alternating names and values, the body as bytes or a `Reader`; `http_host.send` answers an `Exchange` handle (rt.HTTPExchange) whose accessors read the response, or an `http.Error` variant's name and fields. A `Client` holds a `Client` handle (rt.HTTPClient) over Go's `http.Client`, which owns the pool; `http.default_client()` is one Go client for the process. `send` carries the `network` class, which wasip1 lacks. On js/wasm the transport keeps Go's `fetch` path by setting no dial function (`transport_js.go`).

`http_testing.run` is three crossings the VM answers (internal/vm/network.go): `run_on` installs the answer function on the frame (rt.EnterNetwork), `answer_on` calls it for a request and records the request, and `recorded_on` hands back the requests. The builder lowers them as it lowers `run_on` for files (`answerOn`, `recordedOn` in ircapture.go).

A server is Go's `http.Server` (internal/stdhttp/server.go). Go calls its handler on a goroutine per request; that handler hands the request to the program over a channel (`http_host.next`) and waits until the program has answered (`http_host.finish`), so no Nomi code runs on Go's goroutines. `http.serve` loops on `next` in the calling task and spawns each request on the supervisor it was given. The request task runs its handler through `http_host.request_on`, a VM crossing (`networkRequestHost`, lowered by `requestOn` in ircapture.go): the handler gets the request's context, the active context with the server's `request_timeout` as a deadline, on a frame whose app `Context` field reads it and which stops when the client goes away (`rt.EnterRequest`, which ends the frame's Go context when the request's does). Every write to the `ResponseWriter` holds the request's lock, and once Go's handler has returned a write fails rather than touch it. A request answered with nothing written gets a 500 from the deferred `finish`; a request whose task a supervisor restarts finds it answered and returns. `Body.Produce` writes through an rt `Writer` whose sink flushes to the client on each write it passes on. `http_host.listen` carries the `listen` class, which js/wasm and wasip1 lack.

`Router` builds Go's `ServeMux` over its patterns (`http_host.new_mux`), each with a handler that only records which pattern matched and its wildcards' values; `http_host.route` runs the mux on a recorder and answers the match, or what the mux wrote itself (a redirect, a 405, a 404). So pattern syntax, precedence, conflicts and path cleaning are Go's own. `Response.redirect` and `Response.content` run Go's `Redirect` and `ServeContent` on a recorder the same way; `http.serve_file` reads through std/fs and hands the bytes to `ServeContent`. Cookies cross as lists of their fields, and a `CookieJar` is Go's cookiejar as a handle (rt.HTTPJar).

A listener registers a drain hook (`rt.OnDrain`) when it binds, so when the program starts draining its supervisors the server runs Go's `Server.Shutdown` before any grace period: it stops accepting, the requests it took finish, and `serve` returns, which ends the drain. With `Redirects.Decide` the Go client follows no redirect and std/http's `send_following` follows them on the calling task, since Go would run the callback on its own goroutine. A multipart form crosses as a flat `List<Bytes>` (`http_host.parse_multipart`, `encode_multipart`), parsed and written by Go's `mime/multipart` over bytes already read; `Body.Multipart`'s boundary is a digest of its parts, so its `Content-Type` and its bytes, computed separately, agree. The timeout, cross-origin and query-semicolon middleware are Nomi functions that wrap a handler.

`std/tls` and `std/net` are lowered with each program too, and their crossings sit in `std/http_host` beside HTTP's, implemented in internal/stdhttp (tls.go, netconn.go), so the HTTP, TLS and TCP Go sides share one adapter package. A `ClientConfig` or `ServerConfig` is a Nomi value; each use builds Go's `*tls.Config` from its parts (`http_host.tls_config`, then `tls_with_certificate` per certificate) and holds it as a `TlsConfig` handle for that use. A `Certificate` is a handle over Go's `tls.Certificate`, its key never read back. A `Connection` is a handle (rt.NetConn) over a `net.Conn`, TCP or `*tls.Conn`, with one rt `Reader` and one unbuffered `Writer` made when it opens; a `context.AfterFunc` on the context of the task that dialed sets a past deadline when it ends, so a cancelled task's reads and writes stop. A listener (rt.NetListener) accepts on a goroutine into a channel, as the HTTP server hands requests over, so `Listener.accept` can stop for the waiting task's cancellation or an accept deadline; it registers a drain hook that closes it. `net.Error` sorts Go's errors with `errors.Is` against the system's error numbers (not matched on Windows). `http.serve_here` answers requests as tasks of the enclosing `concurrent` block, which `http_testing.with_server` uses with a certificate `http_host.test_certificate` makes; `http.reverse_proxy` is Nomi over the client's send.

Go calls a program's hooks on goroutines of its own, where no Nomi code may run. A broker (internal/stdhttp/broker.go) carries each call to the Nomi task waiting in the crossing whose work set it off, which runs it on its own frame: a server's broker is served by `http_host.next`, a client's by `send`, which runs Go's `Do` on a goroutine and serves the broker until it answers, and a TLS configuration's by whichever of these uses it (the listener or client adopts it) or by the handshake itself (`tls_dial`, `tls_client`, `tls_server`, and the accept of a TLS listener, which runs the handshake there instead of on the first read). A hook that waits for an answer (`certificate_for`, `verify`, `on_proxy_connect`) blocks Go's goroutine until the task has run it; one that only reports (connection states, the error log, `on_shutdown`, HTTP/2's error counter, the key log) is queued, and `next` runs what is left before it answers that the server has shut down. A hook that traps traps the waiting task. A TLS state crosses as lists of parts (`stateParts`), the same for a connection, a request, a response and a trace event. `Response.upgrade` takes the connection with Go's `ResponseController.Hijack`, writes the status line and headers itself, and hands the handler a `Connection` that reads what Go's server had already buffered first. `Request.read` and `Response.read` run Go's `ReadRequest` and `ReadResponse` under a `bufio.Reader` over a reader that answers one byte per read, so no byte of the next message is taken. The reverse proxy sends once with the client's handle, following no redirect, and answers a `Body.Produce` that copies the upstream body, flushing per `flush_interval`. `http_host.Handle` (rt.HTTPHandle) holds the Go values a std struct wraps that have no handle type of their own: a ClientHello, a tracer, a read message, a multipart reader and its parts.

`internal/stdhttp/inventory.txt` lists every exported identifier of Go's HTTP packages with what Nomi does with it and the phase that lands it; `TestGoInventoryIsComplete` holds the file to the Go the tests run under and every covered row of a landed phase (`stdhttp.LandedPhase`) to a declaration std has.

## Logging and actors

`std/log`, `std/log_testing` and `std/actors` are written in Nomi alone,
with no `host fn`, and are lowered with each program (`programStdModules`).
A `Logger` is an opaque struct over a private `Sink`: text, JSON, discard,
and the recorder's, which writes each record as one JSON array (time in
nanoseconds, level, message, then `[key, kind, value]` per field) that
`log_testing.run` decodes back into `Record`s. An array keeps the fields'
order, which a decoded JSON object does not. Each record is one
`Writer.write_text`, and a failed write is dropped. `std/log` names a level
`Debug`, and a std file has no prelude, so it imports `debug.Debug` for the
synthesized `impl Debug` headers; the unused-import check counts such a
header as a use of an imported interface.

An actor is a task under the caller's supervisor that loops over
`Receiver.receive` on its inbox (64 messages unless `Actor.start` is given
another size), folding each message into its state, and ends when the
inbox is closed. `Actor.call` sends a message built around a one-slot reply
channel and waits with `Receiver.receive_within`, five seconds unless the
call gives a timeout. Both defaults sit before the trailing function, so a
call that leaves them out is filled as any call into another file is: the
callback goes to the last slot, and std/actors' accessor for the instance
fills the default (`slottedOperands`, `methodInstAccessors`).

## Processes

`std/process` and `std/process_testing` are lowered with each program.
Their crossings go through the internal module `std/process_host`
(`processes` class) to `internal/stdprocess`, whose one `start` takes the
program, its arguments, environment and directory, and a mode for each
stream: null, the running program's own, captured in memory, or piped.
`process.output` captures stdout and stderr, `process.run` sends them to
the program's own output (through `io.capture` when one is in force), and
`process.with_spawned` pipes all three, as `os.Pipe` files the child holds
directly, so its readers outlive the wait. `Child.wait` closes the piped
input and waits once; `with_spawned` defers `process_host.kill`, which
kills and reaps a child still running, so a body that returns, traps or is
cancelled leaves no process behind. A child is started under its frame's
context, so a cancelled task or a passed deadline kills it, and the wait
then unwinds as the cancellation does. A relative `dir` resolves against
the program's working directory, which `nomi test` puts at the module root
without changing the process's.

`process_testing.run` installs a recording fake on the frame
(`rt.EnterProcesses`, `process_host.run_on`, a VM intrinsic beside
`fs_host.run_on`), which tasks inherit as they inherit the file system.
`start` asks the frame first: under the fake it records the call and
answers from the first matching `Answer`, never the operating system.
`internal/stdprocess`'s tests run one program against the test binary as a
helper and against the fake, and require the same transcript.

A js/wasm or wasip1 build lacks the class (`vmhost.TargetLacks`); built
anyway, `start` answers `Unsupported`.

## Locale data

`std/locale`, `std/plural` and the locale-aware formatting and parsing read
CLDR data that `internal/cldrgen` writes into `internal/cldrfull`: plural
rules, parent locales, digits and currency codes in `root.cldr`, and each
locale's own data in `locales.cldr`, only what its parents do not already
give it.

The source is cldr-json 48.2, vendored in `internal/cldrgen/data`. The
cldr-core supplemental files are unchanged, except `currencyData.json`, cut
to what is read. The per-locale files are gathered into one file each, with
the keys cldrgen reads; some hold each locale's own values only, and cldrgen
resolves them along the parent chain. `go run ./internal/cldrgen/extract
<dir>` writes them all from the unpacked npm packages (cldr-core,
cldr-numbers-full, cldr-misc-full, cldr-dates-full, cldr-units-full).
`go generate ./internal/cldrfull` rewrites the blobs;
`TestGeneratedDataIsCurrent` fails while they are stale.

A program carries the locales its `nomi.toml` lists under `[i18n]` (every
locale without one), cut to the parts it reaches. `nomi build` writes that
bundle into the image, and `nomi run`, `nomi test` and the REPL give the
machine the same data. A built program whose date skeletons are all literals
carries each one's pattern resolved per listed locale in place of the
locales' available formats. A Go function that reads the data takes the
`*rt.Frame` first and finds it on the frame's context.

A locale whose chain reaches no carried locale (or only a browser build's
locale whose file is not loaded) is looked up as the source locale before
root, when the program lists it, and a ``Locale`…` `` literal of one is a
compile error.

Runners never link `cldrfull` (`TestRuntimeArtifactLinksNoFrontEnd`). x/text
is used only for BCP 47 syntax and matching and for Unicode normalization,
never inside `rt`. Its likely-subtag data is CLDR 32, older than the vendored
data, and it decides a few fallback steps (`zh-TW` to `zh-Hant`) and the
distances `Locale.negotiate` uses.

`Catalog.load` runs the compiler's own catalog checks while the program
runs. Each message's function carries a table of its parameters with the
types the checker resolved, and a running program finds the tables in the
functions it carries. A built image holds only the messages it reaches, and
`nomi run` walks the same reach, so both accept the same entries.

## Collation

`std/collation`'s `Collator` is a UCA collator over tables in the locale
data. The root collation is in `root.cldr`, and each locale's tailorings are
in its section. The Chinese and Korean ones, over 64 KB each, are in
`collation-cjk.cldr`, which only a bundle for listed locales reads, so only a
program that lists them carries them. Tailorings follow their own fallback
chain.

`internal/collation/collgen` builds both when the data is generated: the
root from `internal/cldrgen/data/collation/FractionalUCA_SHORT.txt`, and
each tailoring by compiling CLDR's rules with a port of ICU's
CollationBuilder. `go run ./internal/cldrgen/extract collation <dir>` writes
both from the unpacked CLDR source release.

`internal/collation`'s conformance tests run CLDR's `CollationTest` files
(vendored gzipped) against the root a program carries.
`TestCollationMatchesICU` compares every type with ICU when
`NOMI_ICU_ORACLE` names a build of `internal/cldrgen/testdata/icuoracle.c`
(macOS). It skips three known differences, where Nomi follows UTS #10 and
CLDR's conformance files rather than ICU4C. ICU4C's default root orders Han
by radical and stroke, and Nomi orders it by code point. ICU orders a
precomposed Turkish `â̧` apart from its NFD form, and Nomi treats the two
alike. ICU keeps U+0901 and U+0A01 after a variable character in shifted
mode, and UTS #10 makes them ignorable. A few CLDR rule sets (`aa`, `bal`,
`cu`, `dz`, `vo`, `wae` and others, and `cs`'s `digits-after`) are not in
ICU's data, so nothing compares them.

`String.uppercase_in`, `lowercase_in` and `fold_case` use tables that
`go generate ./internal/stdstrings` writes from the vendored Unicode
`SpecialCasing.txt` and `CaseFolding.txt` (`TestTablesAreCurrent`).

## Embedding

`github.com/nomi-language/nomi/vmhost` is the public Go API. The CLI, REPL,
tour wasm and FFI wrapper all go through it, so an embedder runs programs
exactly as `nomi run` does.

A plain user `host fn` or `host type` is answered by an embedder's host
table, keyed by the declaration's key. A project's crossing, a `go`
binding's included, is named under a project prefix, and a machine binds
every table but std/compiler's under those names. So a project file named
like a Go-backed std module (`regex.nomi` binding `compile`) never shares a
crossing with std's rows
(`TestHosts_AProjectFileNamedLikeAStdModuleRunsBesideIt`).

An embedder's own files are a `vmhost.FileSystem`, adapted to stdio's
`Backend`; `DiskFiles` and `NewMemoryFiles` are two.

## Go FFI wrapper

`nomi run`, `nomi check` and `nomi test` on a program with Go bindings
generate a cached Go wrapper that imports the bound Go packages and holds a
host adapter for each bound declaration. `nomi run` and `nomi test` then run
the program on the VM inside the wrapper, which answers a crossing into Go
through that adapter; `nomi check` stops after load and analysis. Pure-Nomi
programs and tests run in process with no wrapper.

A bound Go standard library package is read from the source of the toolchain
`go env GOROOT` names, with that toolchain's build constraints for this
platform, so a binding to `strings.ToUpper` is validated and adapted like one
to the project's own package. It needs no `go.mod`: a project with none gets a
wrapper that is its own module. The toolchain's version is part of the cached
wrapper's key whenever a binding names a standard library package.

A Go map crosses to Nomi in ascending key order. A Nomi `Map` keeps the order
its entries were inserted in, and Go iterates a map in a random order, so the
adapter sorts the entries before inserting them. A string, integer or float
key, or a named type over one, sorts by `cmp.Compare`, with NaN first. A bool
key sorts `false` first. A struct key compares its exported fields in
declaration order, each by the same rules, and a `time.Time` key or field by
`Time.Compare`. Any other key type (a pointer, an interface, a channel, an
array) is refused when the adapter is generated, since its only order is an
address or a dynamic type and would differ from run to run. A Nomi `Map`
passed to Go needs no order: a Go map has none.

A Go `io.Reader` or `io.ReadCloser` result becomes a `Reader` handle whose
source is the Go value, and an `io.Writer` or `io.WriteCloser` result a
`Writer` that hands it each write as it is made, leaving buffering to the Go
value. Either closes the Go value through its own `Close` when it has one. A
`Reader` or `Writer` passed to such a parameter reaches Go as an
`io.ReadCloser` or `io.WriteCloser` that calls the handle's own operations,
so Go reads from the handle's buffer and writes in order with the program,
under the handle's lock, on the frame of the binding's call. A Go value that
keeps the stream past the call reads or writes it later on that frame. The
match is by the declared Go type, so a function that answers a concrete type
(`*gzip.Reader`) needs a one-line Go function in the project that answers
the interface.

### The wrapper's module, its cache and the toolchain

The wrapper is generated under `os.UserCacheDir()/nomi/builds/<hash>/`.
`.nomi` edits need no rebuild; Go changes invalidate the cache key. The cache
is safe to delete. Tests must set `NOMI_FFIRUN_CACHE_ROOT`: the real cache is
refused under `go test`.

The wrapper's go.mod names the compiler module in one of two ways:

- A versioned `nomi` (its main module version is a tag, or a pseudo-version
  from `go install ...@version`, with no `+dirty`) requires
  `github.com/nomi-language/nomi` at that version, and Go fetches it. The
  cache key uses the version instead of hashing a tree.
- Any other build, including one from a checkout at an untagged commit,
  replaces the module with the tree it was built from.

`NOMI_COMPILER_SOURCE=<checkout>` forces the replace with that checkout in
either case.

A project's `go.mod` holds only its own module and dependencies. The wrapper
and `nomi build`'s FFI `nomi-vm` add the compiler's `require` and `replace`
themselves, overriding any the project wrote, and merge the compiler's
go.sum into theirs (`TestFFI_AProjectOutsideTheRepositoryRuns`). A versioned
`nomi` fetches the compiler module at its own version through `GOPROXY`, so
every release tag must be pushed for the proxy to serve it.
`TestReleaseArchive_RunsAGoBindingFromAFetchedModule` builds `nomi` as
`scripts/release.sh` does and runs a Go binding with no checkout and no `go`
on PATH.

A bound package of a required module is read from the Go module cache. One
that needs cgo is an error at its `gopkg`, since wrappers build with
`CGO_ENABLED=0`.

The `go` every build runs is `NOMI_GO`, else the one on PATH, else the
managed toolchain. That is the Go release `nomi` was built with, downloaded
from go.dev (`NOMI_GO_DOWNLOAD_URL`) into `<user cache>/nomi/go/<version>`
and checked against `internal/gotoolchain/pinned_gen.go`.
`NOMI_TOOLCHAIN_ROOT` replaces that directory; tests must set it, and the
real one is refused under `go test`. Only the CLI may download the
toolchain. After bumping the Go version, `go generate ./internal/gotoolchain`
pins the new archives; `scripts/release.sh` refuses a Go it does not pin.

## Modules

A project's dependencies are the modules its `go.mod` requires.
`internal/modcache` answers which it needs and where each is (a local
`replace`, or the Go module cache), reading only the disk. `internal/modfetch`
downloads missing modules through the module proxy protocol and the checksum
database with `golang.org/x/mod`, with no `go` command, and writes or edits
`go.mod` and `go.sum` for `nomi add`, `nomi fetch`, `nomi remove` and `nomi
update`. `nomi new` and `nomi init` write a project's first `go.mod`.

A Nomi user never edits `go.mod` or runs `go`, so an error or hint about a
missing requirement names the `nomi` command that fixes it, never a
directive to write. For a `gopkg` no go.mod provides, the checker and a
run's FFI validation give the same explanation: Go code beside the file in a
directory that is not a project names `nomi init`, and a missing dependency
names `nomi add`.

The CLI installs a fetcher, so `run`, `test`, `check` and `build` download a
Nomi module before the front end reads the cache. The language server
installs none and never downloads. Under `go test` a download refuses unless
`GOMODCACHE` is set; tests serve modules from a `file://` GOPROXY and a fake
checksum database, never the network.

## `nomi build`

`nomi build` writes one executable: the `nomi-vm` binary with the program's
linked IR image appended. It is not native code; it skips the front end at
startup. `--target goos/goarch` cross-compiles.

**Runner variants.** Every runner comes in two builds. The untagged one
links every std module's Go side; the one built with the `nomi_nonet` tag
(`nomi-vm-nonet`, `nomi-vm-reactor-nonet`) answers the network crossings
with an error instead, so it links no net/http, crypto/tls, crypto/x509
or net. Importing net/http keeps its whole client transport alive through
`DefaultTransport`, so leaving the package out is the only way to drop it:
the js/wasm and wasip1 runners go from 23.5 to 14.6 MB stripped, darwin's
from 12.6 to 7.9 MB. `go generate ./internal/stdlibadapters` writes the
split: `adapters_gen.go` holds every adapter that does not call
internal/stdhttp and `NetworkNames`, `adapters_net_gen.go` the ones that
do (built without the tag), and `adapters_nonet_gen.go` an error stub for
each (built with it), so `Bind` names the same crossings in both
(internal/hostgen/stdtable/netsplit.go). `nomi build` appends a program to
the smaller runner when it reaches none of `NetworkNames`
(`vmhost.Program.UsesNetwork`, over the crossings the pruned image keeps),
and `ffirun.Variant.NoNetwork` names, caches and finds it; a prebuilt
runner's `-tags` must match its name. A runner without the network handed
an image that reaches one refuses it before running anything
(`vmrunner/network.go`). A Go-FFI project's runner follows the same rule:
its wrapper, which builds the image, prints whether the program reaches a
network crossing (`ffirun.BuiltImage.NoNetwork`), and `ffirun.ProjectRunner`
builds the project's runner with `nomi_nonet` when it does not. The tag
drops only std's network adapters; the project's own Go is linked as
written.
`TestNoNetworkVMImportsNoNetworkCode` (cmd/nomi-vm) holds the packages out
of the tagged build on every target.

**Pruning.** The image holds only the functions the VM's own reachability
walk reaches from `main`, the boot and each `export fn`. It is the walk the
VM itself uses, so a call it did not foresee fails naming the callee, never
running something else. `TestIRImage_PrunedProgramsMatchTheCommittedRecords`
runs every corpus and tour program pruned against its record.

**File names.** The image names each source file as `go build -trimpath`
would: a stdlib module as `std/<file>.nomi`, a project file by its path from
the project root. It records the project root only for a program that
reaches `std/compiler`. A Go-FFI runner recognizes an entry that declares
its own bindings by that relative name.

**WebAssembly.** Nothing may follow a WebAssembly module's last section, so
a wasm target carries the image inside the module.

- A wasip1 runner (`nomi-vm` or `nomi-vm-reactor`) declares a 4 MiB bss
  array and an initialized slot holding a magic, the array's address and
  its size. `nomi build` finds the slot in the data segments (no symbols
  needed, so a stripped release runner works) and adds an active data
  segment at the array's address holding the image's length and the image,
  which the engine writes before Go starts. The array is bss below Go's
  heap, so it costs no file bytes, the allocator never reuses it, and the
  module needs no file access.
- A js/wasm module carries the image in a `nomi.image` custom section that
  nomi.js hands it.

**`--web`** writes a browser folder instead: the js/wasm module `app.wasm`,
`nomi.js` (Go's `wasm_exec.js` wrapped in a function, then the loader, which
starts itself as each run's module worker), `nomi.d.ts` with a typed `call`
per exported function, and the page, plus the author's static files from the
program's `web/` folder or `--static <dir>`. A program that lists its
locales embeds only `[i18n].web_embedded`'s and gets one
`locales/<tag>.nomil` per other listed locale, which `Locale.load` fetches
through nomi.js. The image's message tables are written without those
locales' texts.

`nomi run --web` serves the generated files from a temporary directory and
the static files from where they are, on localhost. It polls both, and
rebuilds on a source change or only reloads on a static one. The reload
script is added to each `.html` page only as it is served, never written.

**Exports.** A program with `export fn`s records each in the image with
std/json's converters instantiated at each parameter's and the result's
type, so a call crosses as JSON text. A runner boots an image once, runs its
`main`, and then runs one exported function at a time on that booted
program. nomi.js's `load` starts the worker in call mode.

**Go bindings.** A project with Go bindings gets its own runner for a
WebAssembly target as for any other; `--web`'s image, nomi.d.ts and locale
files come from the wrapper. A bound package that does not build for the
target is reported at its `gopkg` with the import chain. A Go binding
carries no capability class: Go's build decides.

**The wasip1 reactor.** `--target wasip1/wasm` of a program with an
`export fn` appends it to `cmd/nomi-vm-reactor` instead: the VM linked
`-buildmode=c-shared` as a WASI reactor that exports `_initialize` and the
generic call ABI `nomi_alloc`, `nomi_call`, `nomi_free`, `nomi_load` and
`nomi_main`. A program without one stays a command module. The ABI is
generic because Go fixes a module's export names when the runner is
compiled, and one runner serves every program, so a program's function
names travel as data rather than as per-function exports. A project with Go
bindings and an `export fn` gets the reactor variant of its own runner,
whose generated main imports the same `//go:wasmexport` functions from
`vmrunner/reactor` and passes its host table to them from `init`.

`nomi build-vm --reactor` copies out `nomi-vm-reactor` with nothing
embedded. Its image area's length is zero, so a runner compiles it once,
instantiates it per script and hands each instance one `.nomic` through
`nomi_load`: one program per instance, and a reactor with an embedded
program refuses it. Releases ship the reactor in the wasip1 archive beside
`nomi-vm`.

`nomi build-vm --reactor` also writes `nomi-wasi.js` and `nomi-wasi.d.ts`
beside the reactor: an ES module with no dependency that implements the WASI
preview1 imports the reactor needs (stdio, clocks, random, a blocking
`poll_oneoff` sleep, no preopens, `ENOSYS` for any import it does not know).
It offers `loadRuntime`, `load`/`call` and `run` over that ABI in browsers
and Node (`TestBuildCommand_ReactorWritesTheWasiLoader` drives it in Node).
`nomi build --target wasip1/wasm` writes the same two files beside a reactor
with an embedded program, whose `load()` and `run()` with no argument call
`nomi_load` with length 0. That boots the embedded program and answers its
exports, and a reactor with none refuses it.

**`--no-vm`** writes the image alone as a compiled program,
`<entry>.nomic`: a header with magic `NOMIC\0\0\0`, `ir.FormatVersion`,
flags, the writer's version and the length (Go code calls it an image
file). It needs no Go toolchain; Go-FFI projects are refused. It records
the program's exported functions, so a reactor can call them. A `nomi-vm`
with nothing appended runs a `.nomic` file by path or framed on stdin, and
`nomi run` runs one in process.

**Format version.** A field added to an IR node fails
`TestImage_EveryNodeFieldIsEncoded` until the encoder, decoder and
`ir.FormatVersion` carry it. The bump makes every `nomi-vm` refuse `.nomic`
files of the old format by name.

**Runners from a release.** Release archives ship `nomi-vm`, and `nomi
build` uses the one beside `nomi` only after checking that it comes from the
same commit. `--target` downloads, checksum-verifies and caches another
platform's runner from the same release. A wasm runner's commit is read
from its data section, since `debug/buildinfo` cannot read a WebAssembly
module.

## REPL

`nomi` with no arguments keeps one live VM and links each input into it as
a new program. It is lexically scoped across inputs, as GHCi is.

## Formatter

What `nomi fmt` does is in the tooling reference
([Formatting](tooling.md#formatting)). The sections below say why some of
its rules are built the way they are.

## Formatter: brace-bounded field constructs

Four tests guard the formatter. `TestFormatKeepsMeaning` and its fuzz target
hold every format to the program's meaning and to a second format changing
nothing ([`nomi fmt` keeps meaning](#nomi-fmt-keeps-meaning)), and
`TestKnownFormatGaps` pins each known break in `knownFormatGaps`
(`vmhost/format_gaps_test.go`). `TestTrackedNomiFilesAreFormatted`
(`internal/format`) fails for every tracked `.nomi` file that `nomi fmt`
would change, except those in its `unformattedAllowed` list.
`TestDocExamplesAreFormatted` (`vmhost`) does the same for every example in
the tour, `docs/` and std's doc comments.

Anonymous struct types, struct-shaped variant payloads and struct literals
share one surface syntax, so they share one width rule. The break decision
is made at the braces, not at the enclosing list, so a long parameter
annotation breaks its own anonymous struct rather than spilling the
parameter list, and `Map<String, List<#{...}>>` keeps the outer containers
inline. The one exception is an anonymous struct inside a function type's
parameter list (`(#{...}) -> R`): the whole signature including `-> R` is
fit-checked as one unit, so a too-long signature breaks at the parameter
list and the anonymous struct sits flat at the inner indent.

Fits are tail-aware: every group's flat-or-broken decision counts the rest
of the line that follows it. The rest of the line ends at the first place it
may break, and at the `{` of a construct kept multi-line, so `A{a} + B{`
measures the same whether `B{...}` was broken for width or kept broken from
the source. Trailing comments are invisible to fits.

A type keeps a multi-line shape when its fields span lines, as a `fn` body
keeps one ([Formatter: fixed layouts](#formatter-fixed-layouts)), so typing a
brace, pressing enter and saving does not collapse the layout just made. Literals use Prettier's rule for objects
instead ([below](#formatter-a-line-break-after-a-literals-opening-bracket)),
where deleting the one line break after the bracket joins the literal again.

The kept shape is local, so a call can hug it (`f(Point{`). The exception
is a surrounding argument or parameter list already written one element per
line, its first element below its opening bracket: it stays that way when it
holds an element that breaks whatever the width. Without that, a list
broken for width would hug its literal on the next format, and the layout
would not be stable. The same holds for an or-pattern written one
alternative per line, the short form of a block of one statement (`if`,
`else`), and a case arm's body below its `->`.

## Formatter: fixed layouts

Some layouts do not depend on width or on how the source was written:

- A `fn` body is always written across lines, `fn add(x: Int, y: Int) -> Int`
  then `x + y` on its own line, as gofmt, Prettier and rustfmt write one. An
  empty body stays `{}`. This holds for top-level functions, impl functions
  and interface defaults; an expression block such as `if c { x }` keeps its
  one-line form (*Formatter: one-line `if` and binding `else`*).
- A block of several statements puts each on its own line: `a = 1; b = 2`
  becomes two lines.
- A tail `return value` becomes `value`.
- A spread of a range in a literal is written in parentheses,
  `[0, ..(1..4)]`, so the two `..` do not run together.
- A parameter pattern whose head names its type loses a redundant
  annotation: `fn raw(Duration(x): Duration)` becomes `fn raw(Duration(x))`.
  A head with nothing after it keeps its annotation, since `fn f(Label)`
  would be a parameter with no name.
- A `#!` line is kept as written, without trailing whitespace. A blank line
  after it stays one blank line, and no blank line stays none.

## Formatter: one-line `if` and binding `else`

A one-line guard or binding `else` is limited only by the 100-column line,
its indentation and the rest of the line counted, so a run of guards reads
as a run of one-line statements. A plain `if`/`else` with two branches has a
second limit of 50 columns, as rustfmt's `single_line_if_else_max_width`
does. A form that holds
a forced break (a `case`, a block of several statements, a stacked pipe) is
never one line.

## Formatter: a line break after a literal's opening bracket

This is Prettier's rule for objects: the author joins the literal again by
deleting one line break. The parser records the opening bracket's line on
each literal, and the formatter compares it with the line its first element
starts on, so the rule needs no flag of its own. The broken form puts the
first element on the line below the bracket, so the next format finds the
same break and the layout is stable. A list with a spread, a pattern with
`..`, and every other bracketed form (call arguments, parameter lists, type
argument lists, pipes) are laid out by width.

A lambda body in braces follows the same rule. The parser builds the same
tree for `fn x -> { e }` and `fn x -> e` and records only the `{` line and
the first statement's line, so the braces the formatter adds stay on later
runs, even after the body shrinks to fit. A body that does not fit goes
below the parameters as a braced block, as rustfmt writes a closure,
wherever the lambda stands.

## Formatter: filled literal lists

Only lists of number literals (alone, after a unary `-`, or in parentheses
the formatter drops) are filled. Strings, codepoints and booleans are not,
since a list of those is usually read one element at a time, as messages or
the rows of a grid. The packing is greedy, measured in display columns, with
no alignment into columns, and it is decided when the list renders, from the
indent its elements start at. It therefore depends only on the syntax tree,
the list's position and whether the source breaks after the opening bracket.
The derive and impl conformance lists pack their entries the same way.

## Formatter: comparison and boolean operators

A comparison, `and` or `or` gets a group that breaks before the operator,
the right operand one indent in. The renderer breaks the outermost group
first, so a long comparison breaks before its operator before it breaks
inside a call on either side. A chain of the same `and` or `or` is one
group, so a chain that breaks puts each operator on its own line. No group
is made when either operand already spans lines, when the left operand is a
pipeline (its last stage is what the operator follows), or when the right
operand is a bracketed literal, which breaks inside its brackets.

## Formatter: compound depth

Each list, vector, set, map or struct literal or pattern adds a level. A
call, an enum pattern's payload, a tuple, an `as` or `or` pattern and
parentheses add none but pass their contents' depth through. Only the
literal or pattern that reaches three levels breaks for it; its elements are
laid out on their own merits, so a nested literal one level down can stay on
one line inside it.

## Formatter: comments

The parser places a comment inside a bracketed, comma-separated list on one
of its items as it reads the list, so every list form keeps comments the
same way. A list holding a comment is written one item per line with no
trailing comma where its syntax takes none (a lambda's parameters, type
arguments and parameters, a tuple or variant pattern, a list ending in a
spread, a call with one argument). Parentheses around an expression holding
a comment stay, the comment inside them. A lambda header whose only comments
each end the line of a parameter other than the last keeps its own layout.

Most other comments are trivia on the construct they sit in: a statement's
leading and trailing comments, the comments before a closing `}`, a struct
field's leading comments. A comment anywhere else (after a `with`
statement's `=`, after a spread's `..`, inside the parentheses around a
pattern) has no slot of its own, and the parser gives it to the nearest
node: inside the innermost node whose extent contains it, after the last
node that ends before it, else before the first node that starts after it.
The formatter writes a comment before a node on the line above it when the
node starts a line, and otherwise at the end of the line. A comment that
cannot end its line, because the line already ends in a comment or in an
opening bracket or `|`, goes on a line of its own above the next line of
code, where it reads back as the next item's comment.

## `or` patterns

Each name the alternatives of `A or B` bind is one symbol, defined at every
alternative's site, and the checker compares the alternatives' name sets and
types. Coverage expands an `or` into its alternatives in each column of the
constructor matrix.

The IR builder tests the alternatives in order against one value. An
alternative that matches copies each name into a join temporary and jumps to
the pattern's success block, where each name is bound once, so an arm's
guard and body are lowered once. A parameter's destructuring prologue cannot
branch, so for an `or` inside a parameter it holds the value, and the body
tests it before running.

## Captures

The parser reads one postfix-level operand after `&` and refuses a body that
is no call, parenthesized expression or literal. In a partial capture
(`&f(&1, x)`), the `&N`s of the call are the call's open arguments, so the
checker and the builder read it through their ordinary partial-application
paths. Any other capture is typed as a lambda whose parameters the expected
function type gives, with each `&N` read as parameter N. An `&N` outside
every capture is reported with a help that writes the capture from the nodes
around it.

The builder lowers a capture as the lambda it stands for, so nothing runs
when the capture is made. A partial capture's lambda takes the callee's
parameters at the open slots, with their defaults and names, and its body
is the call with the written arguments in place, written by name in
evaluation order when that is not their slots' order.

The formatter keeps a parenthesized body's parentheses, which it drops
elsewhere, since they change the meaning: `&(f(&1))` is no partial and
`&f(&1)` is one.

## Test harnesses

AGENTS.md's "Test" section holds the commands, the verification cadence and
the golden-file rules. This section describes each harness that holds the
implementation to a property, with its commands and environment variables.

### A program the front end accepts must lower

`go test ./vmhost -run '^$' -fuzz '^FuzzFrontEndAcceptsSoItLowers$' -fuzztime 5m`
mutates the corpus files, tour blocks, spec programs and generated programs
(`vmhost/lowering_gen_test.go`). It checks each as `nomi check` does,
without running it, and fails on one the front end accepts and the IR
builder declines, or on a compiler panic. Under plain `go test`,
`TestFrontEndAcceptsSoItLowers` checks the generated programs, and
`TestKnownLoweringGaps` pins each known decline in `knownLoweringGaps`
(`vmhost/lowering_gaps_test.go`) by a reproducer. The generator avoids those
shapes, so remove a gap and its avoidance together when it is fixed.

A test about how a blocked program is reported (`nomi run`, `nomi test`,
`nomi check`, the REPL, exports, the language server) blocks on a planted
decline: a call to `planted_decline`, which `irbuild.PlantDeclineForTest`
makes the builder decline. It never blocks on a real gap, which a fix would
close.

### Decline sites

`testdata/lowering-declines.txt` lists every place the builder can decline a
body, one line per site, with its class and a reproducer or reason. The
class says whether a suite reaches the site, it is dead because the checker
rejects what would reach it, it is a gap found and pinned, or it is
undecided. `internal/declinesites` finds the sites by reading
`internal/irbuild`'s source (its package comment gives the rule), and
`TestDeclineSitesAreListed` fails when a site is added or removed without the
file.

`NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/declinesites -run TestDeclineSitesAreListed -count=1`
rewrites the list and keeps each site's class and note; classify a new site
by hand. `NOMI_DECLINE_COVERAGE=1` instead runs the suites that hold
programs to lowering, with coverage of `internal/irbuild`, and records which
execute each site. That takes about ten minutes, including the whole irbuild
package: add `-timeout 0`, and `NOMI_DECLINE_PROFILES=<dir>` to keep the
profiles there or, alone, to reread them. An unreached site is a place to
look for a gap: write the program that reaches it, and pin it in
`knownLoweringGaps` if the front end accepts it.

### A program that lowers must run

`go test ./vmhost -run '^$' -fuzz '^FuzzLoweredProgramsRun$' -fuzztime 5m -parallel 4`
runs each input's `main`, or its test cases, with no input, its output in a
buffer and a step budget. A Nomi trap, a failed assertion and an `Err` from
main are the program's own outcomes. It fails on:

- a Go panic or a bytecode compiler panic;
- a machine limit reached mid-run;
- a loop-free program that uses its budget;
- two runs of a program with no clock, randomness or concurrency that
  differ, or such a program's `main` running differently from the image
  `nomi build` writes.

A program that may loop and uses its budget is skipped, and so is one whose
effects reach outside the machine (files, `std/compiler`, Go bindings).
`TestLoweredProgramsRun` runs the generated programs and every seed with a
`fn main` (about 13 seconds), and `TestKnownRunGaps` pins each known crash in
`knownRunGaps` (`vmhost/run_gaps_test.go`) by a reproducer.

### The feature × context matrix

The generator and the fuzzers find a gap by chance; the matrix
(`vmhost/feature_matrix_test.go`) looks for one on purpose. A feature is a
small construct with a known value (a literal, a declared type, a function
value, an existential, an `Iter` chain, a pattern, a call with named
arguments, a derived impl). A context is a place the value stands (a
binding, an argument, a `case` arm, a list element, a lambda, a `once`, a
task body, a test, a `defer`, a `with` write, another file).

`TestFeatureContextMatrix` (about 10 seconds) builds every feature in every
context, and a fixed twelfth of the triples (a fragile feature in two
contexts). Each program must be rejected with the text the matrix expects
for it, or lower and print the feature's value. `NOMI_MATRIX=full` checks
every triple (about a minute), `NOMI_MATRIX_ONLY=<text>` the cells whose
name holds the text, and `TestFeatureContextMatrixRotating` 60 more triples
a day. A new feature or context is one entry in `mxFeatures`, `mxWraps`,
`mxShells` or `mxPlacements`. A pair the type system makes invalid gets a
tag or an `mxCheckerRules` entry that says why. The matrix's findings are
pinned in `vmhost/lowering_gaps_matrix_test.go`.

### Debug machines run programs as plain ones do

`TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes`
(`internal/expectation`) runs every fourth corpus file and tour block, and
every deliberately failing fixture, on debug machines, and compares each
transcript with its committed record. `NOMI_DEBUG_IDENTICAL=full` runs them
all. `vmhost`'s `TestSpecProgramsOnADebugMachine` does the same for the
spec's programs.

### Load order does not change a program

`TestLoadOrderDoesNotChangeAProgram` (`vmhost`, about 6 seconds) loads a
sample of seeds and generated programs in two child processes, one in order
and one in reverse. It fails when a program's declines, unsupported
diagnostic or run transcript differ. `NOMI_ORDER_COUNT=100000` checks every
seed (about 90 seconds).

### Every expression has a type after checking

`analysis.UnresolvedExprs` lists each value expression of a checked file
with no type, a nil one, or an unsolved variable as its own type (one nested
in a type argument, as in `[]`'s `List<?1>`, is allowed). It runs in tests
only. `vmhost`'s lowering checks report it beside the builder's declines, so
`TestFrontEndAcceptsSoItLowers` and the lowering fuzz target name a checker
skip at its source. `TestSeedExprsAreTyped` and `TestStdlibExprsAreTyped`
hold the corpus, tour, spec and stdlib to it. A known skip is pinned in
`knownCheckerGaps` (`vmhost/checker_gaps_test.go`) with a reproducer that
`TestKnownCheckerGaps` checks; a fix removes its entry in the same change.

### `nomi fmt` keeps meaning

`TestFormatKeepsMeaning` (`vmhost`, about 4 seconds) formats every tracked
`.nomi` file, tour and spec block, corpus seed and generated program, and two
layout variants of each (respaced, broken across lines, commented,
parenthesized, renamed long). It fails when the output does not parse, its
syntax tree or comments differ, a second format changes it, or the front end
accepts one of source and output and rejects the other.
`go test ./vmhost -run '^$' -fuzz '^FuzzFormatKeepsMeaning$' -fuzztime 5m -parallel 4`
fuzzes the same check. `TestKnownFormatGaps` pins each known break in
`knownFormatGaps` (`vmhost/format_gaps_test.go`) by a reproducer that fails
once it is fixed.

### Incremental checking

An incremental check must answer what a full check answers.
`analysis.DocumentManager.Recheck` takes a checked program and an edit and
answers the program after the edit, which must be what a full check builds
from the edited texts (see [Language server analysis](#language-server-analysis)).
`analysis.DumpProgramForTest` renders a checked program as text, including
the diagnostics `nomi check` prints
(`TestCheckProgramPrintsWhatTheFrontEndPrints` holds that list to the front
end).

`TestIncrementalMatchesFull` (`internal/analysis`, about 13 seconds) applies
fixed edit sequences to small projects the test declares (layered imports,
message catalogs, dispatch, app types) and the multi-file corpus
directories. After each edit it compares the recheck's dump with a fresh
full check's, byte for byte. `TestIncrementalHarnessCatchesAStaleResult`
proves a stale recheck fails it.

`go test ./internal/analysis -run '^$' -fuzz '^FuzzIncrementalMatchesFull$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4`
mutates a project file and edits it the same way, and
`TestIncrementalMatchesFullSample` (about 2 seconds) runs a fixed sample of
those edits under plain `go test`. Its `messages` project holds catalogs on
disk: an edit step whose key ends in `.toml` writes that input and rechecks
with an input edit, and `TestOpenDocumentCatalogEditsMatchFreshBuilds` holds
open documents to fresh builds through the same edits.

### The language server survives typing

`TestLSPSurvivesTyping` (`internal/lsp`) types a fixed sample of files into
the server and fails on any panic it recovers. `NOMI_LSP_TYPING=full` types
every tracked `.nomi` file and tour block instead (about 7 minutes split
four ways with `NOMI_LSP_TYPING_SHARD=k/4`), and
`go test ./internal/lsp -run '^$' -fuzz '^FuzzLSPSurvivesEdits$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4`
mutates them. A known panic is pinned in `knownTypingGaps` with a
reproducer.

### Rotating sets

The fixed inputs above are the regression baseline. Beside them, tests
check a rotating set from a start seed that changes every UTC day, so each
day checks inputs no earlier run did:

- `TestFrontEndAcceptsSoItLowersRotating` checks 150 more generated programs;
- `TestLoweredProgramsRunRotating` runs 150 more;
- `TestFormatKeepsMeaningRotating` checks two more layout variants of every
  format input;
- `TestLSPSurvivesTypingRotating` types five more documents of at most 400
  lines;
- `TestIncrementalMatchesFullRotating` runs six more edit sequences;
- `TestFeatureContextMatrixRotating` checks 60 more matrix triples.

Each of the first five adds 2 to 4 seconds, the incremental one under one.
The log names the start seed. `NOMI_GEN_SEED=<n>` pins it and makes the run
repeatable, and `NOMI_GEN_COUNT=<n>` changes how many seeds the set holds. A
failure there says it came from the rotating set and prints its seed and a
command that checks exactly that program, variant (with
`NOMI_GEN_INPUT=<name>`) or document. AGENTS.md says how to treat such a
failure.

### Scale

`internal/projectgen` generates a multi-file project from a seed and a line
count: hub files most files import, unit files of 50 to 300 lines in
clusters with chains and import cycles, an entry and test files. Every
project checks with no diagnostic, runs, and passes its tests
(`TestGeneratedProjectsAreClean`, at about 1k and 10k lines). To open one in
an editor:

```
NOMI_GENPROJECT_DIR=/tmp/gen NOMI_GENPROJECT_LINES=50000 go test ./internal/projectgen -run TestWriteGeneratedProject -count=1 -v
```

`TestEditWorkStaysFlatAsAProjectGrows` (`internal/analysis`, under a
second) counts the files whose bodies each kind of edit re-checks at about
1k and 10k lines. A body-only edit re-checks the edited file alone, and a
signature edit the edited file and the files that import it. It also counts
the program-wide passes' visits to project files: a body-only edit's
reductions visit the edited file once, at every size. It pins, with a
comment, the work that still grows with the project: the full check after an
impl is added. It counts work rather than timing it, so it holds on any
machine.

The table of time and allocations per operation (check cold and warm,
`nomi check`, the language server's edits and lowering, `nomi run`, the heap
a kept program holds) at 1k, 10k and 50k lines and on the stdlib takes about
two minutes:

```
NOMI_SCALE_TABLE=1 go test ./internal/scalebench -run TestScaleTable -count=1 -v -timeout 0
```

`NOMI_SCALE_SIZES=1000,10000` picks the sizes. The same operations are Go
benchmarks for profiling one:
`go test ./internal/scalebench -run '^$' -bench 'Scale/50000/signature_edit_hub' -cpuprofile cpu.out`.

### CI

The header comment of `.github/workflows/test.yml` says which jobs run and
what CI leaves out (the tree-sitter tests and the VS Code smoke test). The
Windows jobs set `core.autocrlf` off so golden files keep LF.

### Golden files beyond the expectations

AGENTS.md lists the `testdata/expectations/*.expect` files, their
regeneration commands and the rules for changing a golden file. These are
the others.

**`vm-corpus-programs.txt` and `vm-corpus-reports.txt`** list the corpus
files `internal/vm`'s harnesses compare whole, as programs and as test
reports. The last regeneration command in AGENTS.md rewrites them. Every
tour and failure record must compare, so those populations have no list. A
corpus file leaving a list stopped comparing and needs a named cause. No Go
test pins a population count, and tests name a tour block by a substring of
its code (`tourBlock(t, "pipes.md", marker)` in `internal/vm`), never by its
`chapter:L<line>` id, so a moved line does not break them.

**`internal/dap/testdata/*.golden`** are scripted debug sessions. Each test
in `internal/dap/session_test.go` and `eval_test.go` plays a client against
the server in process and records every request, response and event, with
paths normalized. `NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/dap -count=1`
rewrites them; a moved transcript needs a named reason like any golden file.

**`vm-retained.txt`** lists every function the builder retains over the
corpus, with whether the VM ran it on synthesized arguments
(`TestIRRetainedPopulationRuns`). A function ran when the machine entered
its body and the run did not end in a machine refusal: a construct the
machine runs no code for, an undeclared local, a callee no module retained,
or a crossing with no binding. A trap, an `Err`, a `todo` or a limit is the
body's answer. A dispatched call whose receiver's type has no retained impl
counts as a missing callee, since the probe hands an interface-typed
parameter or field a value of a type the program retains an impl of. A
function not entered on any argument is in the `PROBE` bucket.

A counting machine also refuses, at a call from Go, a scalar argument of the
wrong kind, which is what lets `TestIRParamShapeAgreesWithTheVM` catch a
wrong recorded scalar shape. A machine without counting compiles no entry
counter (`TestEntries_AMachineWithoutCountingCompilesNoEnter`).

A new corpus file or a builder change moves the list. Regenerate it with
`NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^TestIRRetainedPopulationRuns$' -count=1`
(the whole-package regeneration in AGENTS.md also rewrites it) and name each
moved function's cause. A function that ran and no longer runs fails by
name.

**`vm-std-retained.txt`** is the same list for the cached std modules, keyed
by the file under `std/` and the function's index name
(`calendar.nomi:Date.Add<Days, Date>.add`), from
`TestIRRetainedStdPopulationRuns`. A change to `std/` or to how the builder
lowers it moves it. Regenerate with
`NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^TestIRRetainedStdPopulationRuns$' -count=1`.
`TestIRParamShapeAgreesWithTheVM` and its `ForStd` counterpart read their
expected `ran` counts from these two lists.

## Grammar checks

AGENTS.md's "Grammar and editor extensions" holds the checklist after a
grammar change. Two of its checks have rules worth knowing.

`go test ./internal/parser -run TestTreeSitterParsesWhatTheParserAccepts`
parses with tree-sitter every tracked `.nomi` file and every tour and spec
block the Go parser accepts, plus the line-break cases in `lineBreakInputs`.
It fails on each one with an ERROR or MISSING node, or with a statement list
(top level, block, `//!` prompt, case or else arms) whose statement count
differs from the Go parser's, unless `treeSitterParityGaps` pins it with a
reason. It also parses each malformed source in `rejectedInputs`, which the
Go parser must reject, and fails on one tree-sitter reads with no ERROR or
MISSING node. It runs the CLI through `scripts/tree-sitter.sh` and skips
when `npx` or a cached copy of the pinned CLI is missing.

`scripts/tree-sitter.sh test` checks the `// ^ capture` assertions in
`tree-sitter-nomi/test/highlight/*.nomi` against
`tree-sitter-nomi/queries/highlights.scm`. `make test-nvim` checks which of
`discards.nomi`'s tokens read as comments, and which of `delimiters.nomi`'s
read as `@type`, against the Neovim query generated from Zed's. Those
fixtures are tracked `.nomi` files, so they must parse, be formatted and
type-check (`TestWorkspaceFixtureDiagnosticsAreQuietWhenExpectationsMatch`
in `internal/lsp` analyzes them). A caret can only point at a column three
or more past the comment's indentation, `// <-` points at the indentation
itself, and an assertion separated from its line by a blank line checks the
blank line. `nomi fmt` indents a comment after a line ending in `{` into the
block.

## Editor development setup

AGENTS.md's "Grammar and editor extensions" holds the checklist after a
grammar change, the rule for publishing the grammar and the git hooks. This
section is the setup for running each editor against this checkout.

**Zed dev extension.** For a dev install Zed uses `editors/zed/grammars/nomi/`
(gitignored) as the grammar checkout. It requires that directory's `origin`
to equal `extension.toml`'s `repository`, tries `git fetch origin <rev>` (a
failure is ignored), then runs `git checkout <rev>`. Run
`scripts/sync-zed-grammar.sh` in any fresh clone or worktree, and after
grammar changes. It makes that directory a git repository with the
published origin whose `dev` branch holds this checkout's
`tree-sitter-nomi/`. When the pinned rev's files equal the local grammar,
`extension.toml` is left alone; otherwise the script points `rev` at the
local commit and says so. Do not commit that rev:
`scripts/sync-zed-grammar.sh --restore` puts the committed one back, and a
publish overwrites it. Then run "zed: install dev extension" and select the
`editors/zed/` directory itself. Install the LSP with
`go install ./cmd/nomi-lsp` (Zed launches `nomi-lsp` from PATH).

**Helix.** `make dev-helix` links your Helix config's Nomi queries to this
checkout and installs a `languages.toml` block whose grammar source is this
checkout's `tree-sitter-nomi/` path, so local grammar changes need no
publish. `make install-helix` copies the queries instead, for people who
only use Nomi.

**Neovim.** Load `editors/nvim` from this checkout (lazy.nvim `dir =`) and
run `make dev-nvim` to compile `editors/nvim/parser/nomi.so`.
`make install-nvim` copies a snapshot into Neovim's package directory, for
people who only use Nomi. Nothing under `plugin/` or `ftplugin/` may require
neotest or nvim-dap at load time. The test keys require neotest at keypress
only when it is installed, and the debug commands require nvim-dap at
keypress. `make test-nvim` runs the plugin's headless checks, with neotest,
nvim-dap, lazy.nvim and snacks.nvim stubbed; CI does not run them. See
`editors/nvim/README.md`.

**Local editors after a change.** `make dev-editors` installs `nomi` and
`nomi-lsp` from this checkout, runs the Neovim and Helix dev setups, syncs
Zed's grammar checkout, says when Zed's dev extension must be reinstalled,
and rebuilds and installs the VS Code extension when `code` is on PATH. The
`post-merge` hook runs the parts a merge into `main` needs. It acts only on
`main`, or on the branches listed in `git config nomi.editorBranches`
(space-separated, for example `main next` when work lands on a local branch
first), so agent worktrees, which share the clone's config, are unaffected.

**VS Code.** `make install-vscode` builds `editors/vscode/nomi.vsix` with
`vsce package` and installs it with `code --install-extension`; never
publish it. `make test-vscode` runs its unit tests, which include tokenizing
every `.nomi` file under `tests/` and `std/` with the grammar, and its
grammar assertions (`editors/vscode/test/grammar/syntax.nomi-test`).
`npm --prefix editors/vscode run test:smoke` runs the extension in a
downloaded VS Code against `nomi` and `nomi-lsp` built from the checkout.
Test at Cursor finds the enclosing test by scanning text, since VS Code has
no tree-sitter. See `editors/vscode/README.md`.

**bat.** `cp editors/bat/nomi.sublime-syntax "$(bat --config-dir)/syntaxes/" && bat cache --build`.

## Tour internals

AGENTS.md's "The tour and stdlib reference" holds the rules an author
follows: the words a ` ```nomi-run ` fence may carry, expect blocks, and how
the Examples pages are generated. This section is how blocks are run and
shown.

### Running a block

A block runs against an in-memory file system, never the disk. Each of its
`// FILE: <name>` sections that is not a Nomi source or `nomi.toml` is a
file it starts with, by its path from `/work`. The editor shows it as a
plain-text tab, and the doctest, the tour wasm and the expectation harnesses
all seed it the same way.

An `error` block's expect block is the same text the page's output panel
shows. tour.expect records a block that stops at run time with exit 1 and
its failure on stderr, as `nomi run` prints it, and `internal/vm`'s
whole-program harness excludes a record with a nonzero exit.

A file in a live example is editable only when an edit can change what it
prints. Go files, `go.mod`, `go.sum` and a `nomi.toml` whose only table is
`[module]` are read-only by kind. For a block on the in-memory file system,
a successful open of a data file's contents counts as a read for the
`read-only:` checks; a listing or metadata does not.

The tour's wasm reaches nothing outside its worker: std/fs goes to an
in-memory file system, and the stdlib has no networking module
(`cmd/nomi-wasm/browser_build_test.go`). Memory is capped at 1 GiB and call
depth at 1,000 under js/wasm. `tour/public/_headers` keeps a hash-pinned
`script-src`, which `csp-audit.mjs` checks against the built HTML.

### Recorded output and manual blocks

A page runs nothing when it loads. Until its first run a block's output
panel shows its expect block, over the files as the block holds them (a
tool's generated files as committed), and the page asks the worker only for
the analyzer's tokens; the reader's first edit runs the block, and a manual
block runs only on its Run button, its panel labelled "Recorded output.
Press Run to run it." until then. The build carries the expect text in the
page beside the block's code (`rehype-shiki.mjs`, empty for a block with no
expect block), except for a `no-run` block, which runs when it scrolls into
view, as a stdlib reference example does. The panel shows the record as a
run's output: an error block's last line is its failure, or its last two
when the last is `exit status N`, or all of it when its first line names a
file, line and column, which `TestTourDoctests` holds each error block to
(`recordedFailure`). The doctest and `TestTourWasmAnswersTheTourBlocks`
compare every block's output and tests' report with its expect block.

The worker lowers the standard library (`nomiWarm`, `vmhost.Warm`) before
its first run starts, inside the page's load allowance (`LOAD_BUDGET_MS`)
and outside the run's budget; tokens, hovers and the formatter need only
the analysis, so a page that is only read never lowers it.

`npm --prefix tour test` loads recorded, manual and unrecorded blocks into
headless Chrome with a fake worker and holds them to that (`tour/test/`; it
skips without Chrome or the staged `tour/public/nomi/`). `astro.config.mjs` passes a hash of
`rehype-shiki.mjs` as the plugin's option, so an edit to it clears Astro's
cache of rendered pages.

`TestTourWasmAnswersTheTourBlocks` times each block's run under Node and
logs the slowest. With `NOMI_TOUR_RUN_BUDGET=1`, which `make check-full`
sets, it fails one that takes more than half of its budget, since a browser
runs the wasm slower than Node. A run over that limit runs once more, and
the faster time counts. The deploy build does not set it, since its machine
is slower and its times say nothing about the program.

### Tools

The files a `tool:` step writes replace the block's, in its tabs too, and
what it prints comes before the run's output. When the module is a tour
module the block imports, whose Go side only the tour's wasm links, the
doctest only checks the block's words and expect block, and
`TestTourWasmAnswersTheTourBlocks` runs the tools and the block. When it is
a module the block holds (the block's own, as the Generating Code chapter's
cafe runs `tools/choices.nomi`, or one under a directory of its own, as its
`csvdata/`), the doctest runs the tools and the block too, and compares what
they print, the tests' report included, with the expect block. The tour
expectations record the block's run without its tools, over the generated
files the block holds.

### Projects of modules and sibling projects

A block with a `// FILE: go.mod` section is a project of modules: a root
module that requires and replaces one under it, such as a generator's
`csvdata/`. Its files are written under a directory and loaded from there,
as the same files on disk load, so module resolution and the files a tool
reads need nothing special. The doctest and harnesses use a temporary
directory; the browser uses `/tour` in the in-memory file system
`tour/src/lib/go-memfs.js` gives Go's `os` in the worker. Diagnostics name
files by their path from the project root.

A block with no go.mod at its root and one in two or more top-level
directories holds sibling projects (`rates/` and `csvdata/`, whose go.mod
`rates/go.mod` replaces with `../csvdata`), and every file is in one of
them. Its project is the top-level directory of its first file, since a
dependency's tool has a `fn main` too. The block's run, tests and `tool:`
steps happen in that directory, as a reader runs `nomi run`, `nomi test` and
`nomi tool` there, and a tool writes back only that project's files.
Diagnostics still name files by their path from the block's root
(`csvdata/csvdata.nomi`), as the tabs do.

The editor opens on a block's first file that is not a setup file
(`go.mod`, `go.sum`, `nomi.toml`); in a block of sibling projects, on its
project's first. In a block of modules or sibling projects, the entry rule
applies to the root module or the project. A block runs the tests of every
Nomi file that declares them, the entry's and then each other test file's
in block order, each loaded entered at its file, as `nomi test` runs a
project's.

### The live editor

A block of up to four files, none under a directory, picks files from a tab
strip; a larger one, or one with a file under a directory, from a file tree
beside the editor. Every runnable block has an "Open large" button that
moves the editor itself into a modal `<dialog>` and back, so its edits and
output carry over. In the large view a block's files can be split into two
groups of tabs, and the output can move below the code. A block that is a
solution and its test opens the first time, in a large view at least 1100px
wide, with the two side by side.

Highlighting, hovers and runs happen client-side with the same grammar,
queries, analyzer and VM the editor uses, compiled to wasm.

### Examples pages

The Examples pages are committed because the doctests and tour.expect read
every page under `src/content/docs`. The generator runs each exercise to
write its tests' report into the expect block.

### Go bindings in a block

A block with `go` bindings runs too. The tour links the Go its blocks bind
into the tour wasm (`internal/tourgo`), since a browser cannot build Go. A
block's Go file (`// FILE: urltools.go`) is
`internal/tourgo/<package>/<file>.go`, byte for byte
(`TestTourGoFilesAreTheLinkedSources`), shown in a read-only tab.
`go generate ./internal/tourgo` writes `linked_gen.go` from the blocks: the
adapters for each, and the one list of linked Go functions with the
declaration each was generated for. `TestGeneratedFileIsCurrent`
(`internal/tourgo/tourgogen`) fails while it is stale, so after editing such
a block's bindings or its Go, regenerate.

Every runner of tour blocks (`cmd/nomi-wasm`, the doctests, the expectation
and VM harnesses) rekeys the linked adapters to the edited program's keys and
refuses a binding the tour does not link, at the binding: ``this example's
Go code is fixed in the tour: `urltools.Foo` is not available``.

### Modules the tour links

A block may also import a module the tour links (today `sqlite`, the driver
in `lib/sql/sqlite`, and `sqlkit`, from `lib/sql/sqlkit`), with nothing in
the block that says where it comes from. Every runner attaches each such
module the block imports as a project that names modules (a go.mod
requiring it, its files under `tour_modules/<name>/`), and adds the module's
adapters, built for js only. The module's Nomi files reach the tour from a
generated Go file, since the compiler's module cannot embed a nested
module's files. Both generated files come from the module's directory under
`lib/`, by `go generate ./internal/tourgo`, and `TestGeneratedFileIsCurrent`
fails while either is stale.

The compiler's go.mod does not require the module. A Nomi module it required
would be a dependency of every program whose go.mod is the compiler's (the
corpus), linked into its wrapper, and the driver's module requires
modernc.org/sqlite. A replace in the compiler's go.mod would also break
`go install github.com/nomi-language/nomi/cmd/nomi@latest`.

Instead every js/wasm build of `cmd/nomi-wasm` runs in the Go workspace
`internal/tourgo/tour.work` (`GOWORK`; `scripts/build-tour-wasm.sh`,
`vmhost`'s rebuild and `internal/wasmsmoke` set it). The workspace puts the
module's directory beside the compiler's, so the generated adapters import
`lib/sql/sqlite/goside` from the tree. That package's js/wasm build (the
browser driver, which calls SQLite's official WebAssembly build through
`globalThis.sqlite3`) imports no modernc.org/sqlite. A js/wasm build without
the workspace fails at that import. The workspace's checksums are
`internal/tourgo/tour.work.sum`, which the go command writes as it needs
them (go.mod checksums only); commit it when it moves.
`TestTourWorkspaceUsesEveryModule` holds `tour.work` to the linked modules.

Outside the browser no binary links a tour module's Go side. The doctests
only compile a block that imports one, tour.expect has no record of it, and
`TestTourWasmAnswersTheTourBlocks` runs it on the staged wasm under Node,
held to its expect block. `TestTourWasmRunsTheSqliteDriversTests` runs the
driver's own `sqlite_test.nomi` there, and `TestLibModules` runs it natively.
The tour's worker loads SQLite's WebAssembly build (staged in
`public/nomi/sqlite/` by `scripts/build-tour-wasm.sh`, pinned by version and
checksum) the first time a run imports sqlite, before the run's budget
starts.
