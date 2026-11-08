# Nomi implementation notes

Why the implementation is built the way it is: the paths through the system
that no single file shows, the invariants that span packages, the outside
constraints the code obeys, and the tests that guard each rule. A language
user does not need any of this; the [spec](spec.md) says what the language
does. [`AGENTS.md`](../AGENTS.md) holds the rules every change follows and a
short map of the pipeline that links each subsystem here. What a function
does, and where something lives, is for grep and the code's own comments.

## Contents

**How the implementation is built.** [The engine and `nomi check`](#the-engine-and-nomi-check), [Kept programs](#kept-programs), [Front end](#front-end), [Keyword declarations and their inputs](#keyword-declarations-and-their-inputs), [Embedded files](#embedded-files), [HTML templates](#html-templates), [File interfaces](#file-interfaces), [Language server analysis](#language-server-analysis), [IR builder](#ir-builder), [VM](#vm), [Debugging and `nomi dap`](#debugging-and-nomi-dap), [rt](#rt), [Int: exact at any size](#int-exact-at-any-size), [Stdlib](#stdlib), [std and lib](#std-and-lib), [Host functions](#host-functions), [Why std uses `host` rather than `go`](#why-std-uses-host-rather-than-go), [CSV and XML](#csv-and-xml), [Passwords, signatures and JWTs](#passwords-signatures-and-jwts), [Files](#files), [Logging and actors](#logging-and-actors), [Background jobs](#background-jobs), [Mail](#mail), [Object storage](#object-storage), [Locale data](#locale-data), [Collation](#collation), [Embedding](#embedding), [Go FFI wrapper](#go-ffi-wrapper), [Modules](#modules), [`nomi build`](#nomi-build), [REPL](#repl).

**Formatter and syntax.** [Formatter: layout](tooling.md#formatting), [Formatter: brace-bounded field constructs](#formatter-brace-bounded-field-constructs), [Formatter: fixed layouts](#formatter-fixed-layouts), [Formatter: one-line `if` and binding `else`](#formatter-one-line-if-and-binding-else), [Formatter: a line break after a literal's opening bracket](#formatter-a-line-break-after-a-literals-opening-bracket), [Formatter: filled literal lists](#formatter-filled-literal-lists), [Formatter: comparison and boolean operators](#formatter-comparison-and-boolean-operators), [Formatter: compound depth](#formatter-compound-depth), [Formatter: comments](#formatter-comments), [`or` patterns](#or-patterns), [`if` without `else`](#if-without-else), [Releasing resources](#releasing-resources), [Captures](#captures).

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

A whole lowering from the analysis is held for the document (the last four
documents lowered; `vmhost/checklowering_perfile.go`). The next run of the
document, when its program holds the same files and at most four of them
changed text, lowers just those files from the analysis and links them with
the held lowering's objects (`irbuild.LowerFileAmong`), as `nomi check`
relinks a kept program. A changed file whose interface hash or unannotated
`once` values changed is lowered with every file that imports it, directly
or through others (`importersToo`, up to 16 files or a quarter of the
program), each after the files it imports. One whose impl headers name
other files than before is lowered with every file that sees one of its
old or new impls, whether or not it imports it: each project file the
header names is the file or one it imports (`seersOf`, over each file's
headers as `analysis.ImplHeaderFiles` reads them, held with the lowering).
The held
result is encoded as objects (`Result.Keep`) by the first such run, so a
whole lowering costs what it did. Whether a changed file's interface
changed, and whether its declarations moved, is read from its old and new
texts parsed afresh, since a recheck rewrites an edited file's bodies in
place, and its declarations are fingerprinted from its old text
(`KeptLowering.WithDecls`), so nothing made from a declaration whose text
changed is carried. A program whose held lowering declines a body or
refuses a construct is lowered whole, which words each blocker. At 50k
generated lines, the run after a body edit of the entry takes 0.38 s
against 0.85 s for a whole lowering (`internal/scalebench`'s `lsp lowering
after an entry body edit, file by file`); the entry imports most of the
program, so its own lowering declares most of it.
`TestLoweringPerFileMatchesTheSource` (`internal/lsp`) holds the runs file
by file to the text's.

Every load, `nomi check`'s included, runs one thing on the VM before the
program runs: the handler of each backtick typed literal whose handler
returns a `Result`, and the `check` of each checked literal (a tag that
implements `CheckedLiteral`). An `Err` is a compile error at the literal;
a checked literal's `LiteralError` names a static and an offset, which
vmhost maps back to the source (checked_literals.go). The checker types a
checked literal as its `build`'s result and records it in
`FileAnalysis.CheckedLiterals`; irbuild lowers it as a `build` call and
declares a check site, a lazy cell over `check` and the literal's statics,
in the same `LiteralSite` list backtick literals use (irchecked.go). An
untagged string literal whose expected type has a `CheckedLiteral` impl
is checked as a twin `TaggedString` the checker builds
(analysis/target_literal.go): `FileAnalysis.TargetLiterals` maps the
literal to the twin and `TargetLiteralBuilds` the twin to the `build` the
expected type chose, which irbuild reads in place of the tag's reference
(`literalDispatch`), lowering the literal as its twin; its site has an
empty `Tag` and the type in `Type`. The check site calls the impl's
`check_type` when it writes one, with the spelling of each of the
literal's type arguments (`kindSpelling` over the instance's
`genericArgs`), and its `check` otherwise. A generic type's handler
(`impl CheckedLiteral<String, Route<P>> for Route<P>`) is freshened per
literal in the checker (checked_literal_args.go) and lowered at the
literal's instance (`genericLiteralImpl`): the instance's impl item for a
program's type, an instance of the std template for std's. A
handler or check that can reach something acting outside the machine is
refused, an effectful check once at the check, so a compile never has
effects. The language server also evaluates double-quoted
typed literals with no `${...}` this way, for hovers and diagnostics.

## Kept programs

`nomi run`, `check`, `test` and `build` keep what they make of a program in
the artifact cache (`internal/progcache`, `vmhost/cache.go`), so the next
command over the same files reads it instead of checking and lowering the
program again. A file's program is analyzed in the mode its own tests
decide, so run, check and test share one record per entry file, the
lowering (`vmhost.Cache`); `nomi build` keeps its image for a target in a
record of its own (`cmd/nomi/build_cache.go`), and a build also writes the
lowering's record.

A record is kept only for a program with no Go bindings that loads
cleanly: no diagnostic, no declined body, and nothing its main, boot or
test cases reach that a machine cannot run (`runsClean`), so a hit says the
program checks and a kept program reports nothing a fresh load would word
differently. It holds the entry, the root, the phrase tables and one IR
image: what main and the boot reach (`vm.PruneImage`, as a build keeps)
for a program without tests, every module for a file with tests. Positions
keep their paths. A hit decodes the image and runs it in a `vmhost.Program`
whose analysis holds only the entry (parsed again for a file with tests,
whose cases are selected from its nodes); a test file's machine and
reporting are the ones a fresh load uses. A hit is looked up before
`ffirun.Prepare`, which parses the program to find Go bindings.

The key is the compiler's build ID, the kind (`load`, or `build` and its
target and flags), the entry's absolute path, and the `NOMI_` variables and
the few others a build reads (`GOMODCACHE`, `GOPATH`, `HOME`, `TZ`, `LANG`,
`LC_ALL`). The record names what the program was built from: trees walked
for what a build can read in them (`.nomi`, `.toml`, `go.mod`, `go.sum`;
hidden names and `node_modules` skipped), the project root's and each other
module's (a dependency's), each `embed` pattern's base with every file in
it (`EmbedInfo.Bases`: the folder or file the pattern's names spell before
its first wildcard, so `static` is static/ and `*.html` is the whole root), each module file and catalog outside them, and the `nomi.toml`,
`main.nomi` and `go.mod` above each root, which decide the root
(`analysis.ProjectRoot`). Each file is held by its size, times, inode and
SHA-256. A lookup walks the same trees: a file whose stat is the record's
and was last changed more than two seconds before the record's snapshot is
taken as it was, and any other is hashed, so a file rewritten with its own
text is still a hit and an edit in the same timestamp tick is not. A file
added or removed is a miss. Files a run writes in its project (a log, a
database) change nothing unless an `embed` pattern's base holds them.

A command takes the snapshot before it builds and writes the record only
when no snapshotted file's stat moved while it built; a tree it had not
predicted (a dependency's) is kept only when its files are older than two
seconds. So a record never names a file's later text beside a lowering of
earlier text. `NOMI_DEBUG_ARTIFACT_CACHE=1` reports each lookup and write
and why it missed or was not written.

At 50k generated lines (`projectgen`, 275 files), on an Apple M2 Max: `nomi
run` of an unchanged project takes 0.056 s against 1.97 s, `nomi check`
0.018 s against 2.0 s, `nomi test` of the project's 49 test files 0.40 s
against 3.0 s, and `nomi build` 0.12 s against 1.97 s. Hello world runs in
0.029 s against 0.11 s. A hit spends most of its time decoding the image
(7.6 ms for the 50k program's 1.3 MB). Any edit is a miss and costs a whole
check and lowering, about 50 ms more than with no cache for the snapshot,
the walk and the record, unless the program can be relinked from its file
records (below).

**File records.** Beside a program's record, a load that keeps a program
keeps a record of its files (`vmhost/filerecord.go`, kind `files`, under
`files/` in the artifact cache, opened from the program record's snapshot
with `progcache.Entry.Sibling`): each project file's interface as checking
against interfaces reads it (`analysis.StoredInterface`: encoded, with its
hash, its imports and its impl headers, resolved from the program's check),
the SHA-256 of its text, its facts for the program's reductions over bodies
(`analysis.FileFacts`, see [stored facts](#stored-facts)), its body summary
(`analysis.BodySummary`), whether it declares keyword declarations, the
program's lowering kept per file (`irbuild.KeptLowering`, see [lowering one
file](#lowering-one-file)), the supply side of the program's missing-impl
check (`analysis.ProgramSupply`) and the program's application types.
When the program's record misses because some of its sources changed, came
or went and nothing else did (`progcache.Entry.ChangedSources`: a `.nomi`
file added to or removed from a tree walked for sources, never one walked
for every file, an `embed`'s; no other input changed), `nomi run`, `check`,
`test` and `build` relink it instead of checking and lowering the whole
program:

- each changed or added file's new interface is extracted
  (`ProgramInterfaces.AddFile` over `analysis.NewStoredInterfaces`, with the
  front end's `Checker.StoredSource` reading sources from disk) and a
  removed one taken out. The program's files are then the entry and what
  it imports, read from the interfaces' imports (`ImportClosure`), and must
  be the kept ones less the removed with the added that are imported (an
  added file nothing imports is not the program's);
- the files checked again are the changed and added ones, every file that
  imports a file whose interface hash changed, directly or through others
  (`Importers`), and, when a file's impl headers changed or a file came or
  went, every file that can see one of its old or new impls (each project
  file its header names is the file or one it imports: the relevant impl
  set). None may declare keyword declarations, whose expansion reads more
  than the file. An edit that reaches more than 16 files and a quarter of
  the program (a hub's signature) checks the whole program instead: each
  file's check and lowering link against every other file, so many of them
  cost more than one whole check;
- each is checked against the interfaces of the files it reads, with no
  diagnostic of its own;
- the program's reductions over bodies (boot-only calls,
  application-field roots, whole-file imports, missing impls) run over the
  checked files' new facts and every other file's stored ones
  (`analysis.ReduceFacts`) and must find nothing, so a body edit that adds
  a call, names a function or demands an impl relinks. When impls changed
  or files came or went, the missing-impl supply is worked out again from
  the entry built against every other file's interface, which registers
  every impl and checks no body (`ProgramInterfaces.Supply`). A program
  with an application type keeps one more rule: its application-field
  roots follow calls into other files' bodies by a walk no file's facts
  reproduce, so the walk those roots take of each checked file's bodies
  must be the kept one and find nothing in them (`AppWalkSummary`, as the
  language server's edits from a record compare it, below);
- each is lowered on its own and linked with the kept objects of the
  others (`irbuild.LowerFileAmong`), a file after the files among them it
  imports, an added file into a new unit (`KeptLowering.AddUnit`) and a
  removed file's unit taken out (`RemoveUnit`). A file whose declarations
  moved is a miss when the object of a file not lowered again holds a
  position in it. A lowering that names an instance of another file's
  generic declaration no kept object holds (`FileMiss.Undefined`) is made
  again with that file read with its bodies (`ProgramInterfaces.SetBodies`),
  which lowers it as a linked library lowers what is asked of it, and the
  instance is kept beside the std instances (`Rekeep`).

The linked program must reach nothing a machine cannot run, as a kept
program must. Both records are then written for the new texts. Anything
else is a miss, and the command checks and lowers the whole program and
writes both records. `NOMI_DEBUG_ARTIFACT_CACHE=1` reports a relink
(`relinked 2 of 8 files`), a lowering again with another file's bodies, or
why there was no relink.

At 50k generated lines (221 files; Apple M2 Max), a built `nomi check` or
`nomi run` after an edit takes, against 2.6 s when the whole program is
checked and lowered again:

| edit | relinked | check |
|---|---|---|
| a leaf's body | 1 file | 0.32 s |
| a leaf's body, adding a call into another file | 1 file | 0.35 s |
| a leaf's body, asking for a new instance of a hub's generic function | 1 file, the hub with its bodies | 0.39 s |
| a leaf's signature | the leaf and the entry, which imports it | 0.80 s |
| an impl added to a leaf | the leaf and the entry, and the supply again | 1.07 s |
| a file added that a leaf imports | the file, the leaf and the entry, and the supply | 1.14 s |
| the hub's signature | none: it reaches 210 of 221 files | 2.59 s |

The entry imports every unit no other unit imports, so it is checked again
after any interface edit of a leaf, and its check against interfaces
registers most of the program. The facts made the files record 9.1 MB
from 6.8 MB, and a relink after a body edit 30 ms slower than one that
compared body summaries. A first run, which writes both records, takes
2.33 s against 2.49 s with no cache. `internal/scalebench`'s `nomi check,
after a ... (file records)` operations measure each edit in process.

### Stored facts

The program's reductions over bodies (the boot-only check, application-field
roots, whole-file imports and coherence's missing impls) read each file's
bodies through summaries keyed by AST and `*Symbol` pointers, which live as
long as one check. `analysis.FileFactsOf` reads the same of one file into a
`FileFacts` that names every declaration by declaration name (`DeclRef`: the
declaring file's module key and its `DeclarationNames` path, or `@line:col`
for a function that has none), so facts made by different checks and
processes fit together, and gob writes them. `ReduceFacts` runs the
reductions over the facts of every project file of a program, with the
program's missing-impl supply (`ProgramSupply`: the impl index's impls of
each type with the project files' own laid over them, and the identity
index) and the stdlib's base (`StdContextOf`), and returns what the passes
over the analyses return. A program built over the base keeps only what its
files change of the base's supply (`OverBase`: the impls and identity
entries they add, the pairs they make judgeable and those they make
unjudgeable), which the reduction lays over the base it reduces with. The
base holds the std modules built so far, which grow as a process reaches
modules, so a whole supply would depend on which modules the process that
wrote a record had built
(`TestStoredFactsSupplyIgnoresModulesBuiltLater`):

- boot-only: each file's functions with their calls outside lambdas and
  every project function they call, and the report's visits in order (each
  function's flags, and each test body). A call into std carries the
  callee's depth from `Supervisor.new` and its chain's steps, which are the
  same in every program, since std calls only std. A function is boot-only
  at one more than the least depth among its callees, and its chain's next
  step is its first call to a callee of that depth (`bootDepths`); the
  program's check uses the same rule, which made the chain a diagnostic
  names independent of map order;
- application-field roots: the file's own result, when the program
  declares no application type (`AppFacts.Exact`). With one, a root's walk
  reads other files' bodies (a factory's, an interface method's
  implementations), and the facts do not hold it;
- whole-file imports: the file's unused whole-file imports with the file
  each imports, the impl functions of other files its references reach,
  and its impl blocks for types it does not declare;
- missing impls: the file's own `ImplManifest`, positions in the file
  written with no file.

A file's facts read nothing of another file's bodies, so a file checked
against its imports' interfaces has the facts it has in the program's
check. A program not built over a stdlib base that holds every std file is
not reduced over facts. See [stored facts reduce as the program
does](#stored-facts-reduce-as-the-program-does) for the harness.

## Front end

The front end parses, injects synthetic host declarations, expands keyword
declarations, lowers derives, synthesizes universal `Debug`, and then
analyzes the whole program. The checker has two phases: one registers every
type, and the second walks bodies. Names resolve whole-program, so the
import graph may be cyclic and import order never matters.

**Types from values.** An unannotated `once`, and a field or parameter
written with a default and no type (`misses = 0`, `limit = 10`,
`internal/analysis/default_type.go`),
take the type of their value, which only checking the value tells. Both
are part of the file's interface, so the program's `OnceTable` infers
every one in one phase before the first file's `CheckTypes`, each with a
checker for its declaring file: the fields first, in rounds (a default that
reads a type still pending is tried again after the others), then the
`once`s. Until then a field's `FieldDef`, or the parameter's slot of its function's
`FuncType` (or interface `MethodSig`), holds `pendingDefault`, which the
phase overwrites in place; a parameter's symbol is defined where the body is
walked, so its type is kept with the declarations' (`attachInferredParam`)
and a rebuild of the bodies writes it again, so an instance of a generic struct, which shares
its declaration's `Fields`, sees the type too. The declaring file's
`CheckTypes` reports what the phase found (`checkInferredDefault`); a
block-local struct is inferred there. The IR builder reads an inferred
field's or parameter's type from its symbol (`irbuild.fieldKind`,
`paramKind`), as it reads an inferred lambda parameter's; a std signature,
which it builds from the module's syntax, reads such a parameter's type
from the module's analysis (`stdInferredParamKind`). An interface keeps every field default, so a
file checked against it infers the same type.

**Type ascription** (`internal/analysis/ascription.go`). `value as Type`
is an `ast.Ascription`, which the parser reads at `|>`'s precedence. The
checker checks the value against the type as an annotated binding's value
(`checkNodeExpecting`, then `unifyInto`), and for a name, a field read or a
number literal first checks it alone in a
trial whose effects are put back: when that gives the type already, the
node goes in `FileAnalysis.RedundantAscriptions`, which the language server
reports as an unnecessary-code hint, not an error. A hover on `as` reads a
hover-only symbol at the keyword. The IR builder lowers it as an annotated
binding's value (`annotatedValue`), and an ascription to an interface as
its value, which the position that reads it erases.

**Default type arguments** (`internal/analysis/default_type_args.go`). A
declaration's type parameter that names a default keeps its resolved type
on its `TypeParam_` (`Default`), which every instance shares through
`TypeParamDefs`. The resolver fills a use in a type position that leaves
arguments out (`Route` is `Route<Unit>`); a use resolved before the
declaration is built becomes an instance with the arguments written, and
building the declaration appends the defaults, read from the parameters
its shell keeps (`unbuiltDecl.params`). Completing such an instance with a
default that contains it would make a cycle of type arguments, which every
walk over types follows forever, so `rejectEndlessDefaults` looks for one
while the declaration's defaults are resolved: a default that reaches,
through type arguments, an instance of the unbuilt declaration that leaves
out a parameter whose default leads back. It reports the error there and
makes that default `Unit`, so no cycle is built. A cycle through several
declarations closes at the build of the last of them, whichever file builds
first. A nominal type's `String` leaves
out trailing arguments equal to their defaults. The IR builder resolves
annotations itself, so `typeOf` first writes the left-out arguments in as
the declaration's default syntax (`irbuild.withDefaultTypeArgs`), finding
the declaration by the reference the analyzer recorded at the name. A
default means what it means where the declaration wrote it, so the gen of
the declaring file resolves it at module scope, with no type parameter or
block of a body it is lowering open (`moduleTypeOf`), and the using gen
imports the kind (`importKind`) and answers the default's node with it
(`gen.defaultArgKinds`). A file that uses `Box` for `Box<T = Config>` need
not import `Config`, and its own `Config` does not replace it.

The boot-only check (`CheckBootScope`, spec §16 *Supervision*) reports per
file over a graph of the whole program's functions: every function of every
project file, and each std function the program reaches, whose calls are
read once per process (std's analysis never changes). A function is
boot-only through a call to `Supervisor.new` or to a boot-only function, to
a fixed point, and a diagnostic names the shortest chain: the first call to
a callee nearest `Supervisor.new` (`bootDepths`). A file reports the calls its roots make
(`main`, test bodies, lambdas, private functions nothing calls) to
boot-only functions, and the boot-only functions it names as values.

No report reads a body. Each file's bodies are read once per body part
(`FileBodies.generation`) into a summary the analysis keeps
(`bootSummaryOf`): per function and test body, its calls outside lambdas,
its lambdas' calls, the functions it names as values, and every function it
calls; per test group, the functions its `boot` calls. Calls name their
callee by `*Symbol`, and the callee's `FuncDef` is read from it when the
graph is built. The graph is built from the summaries once per program and
kept on the `Program` until a summary or the file list changes
(`programBootGraph`). Before, every file's report rebuilt the graph from
every body, which was quadratic: 83% of a 50k-line check. The check still
runs when a file's diagnostics are assembled, never from a cached check, so
an edit that makes a function boot-only reaches its callers' files when the
language server re-analyzes a changed file's importers.

The lexer leaves `>>>` as three `>` tokens. The parser joins them into the
shift operator only after an operand, so nested type arguments such as
`List<Maybe<Box<Int>>>` still close one `>` at a time.

**Parentheses.** One parse reads what parentheses hold: a comma at their top
level makes a tuple (`ast.TupleLit`, `ast.TuplePattern`, or a tuple type, an
`ast.FuncType` with no `Return`), and one item alone is grouped
(`parseParenExpr`, `parseSinglePatternInner`, `parseFuncTypeAnnotation`). In
a type, a `->` after the `)` makes the list a function's parameters. A
lambda parameter's annotation is read once as written and, when it is a
function type that what follows does not end a parameter after, again with
`parenTypeNoArrow` set, which leaves the `->` for the lambda
(`parseParamPattern`); the tree-sitter grammar keeps both readings alive
by a declared conflict. A call's `(` is on the line its callee ends on, so a
line that starts with `(` after `f()!`, which the lexer ends no statement
after, starts a statement. `#(` is `hashTupleError`, whose fix drops the `#`.

**Paths at a use.** The lexer emits `::` as `COLONCOLON`, and the parser's
constructors join each run of words with `::` between them and no space
around it into one `PATH` token (`parser/paths.go`), so every position that
takes a file's short name as a qualifier takes a path with one extra token
type. An import splits the `PATH` into its `ModulePath` segments; a `/`
there is the error that spells the path with `::`. In an expression a path
is an `*ast.FilePath`, the `Object` of a `FieldAccess`. In a type, pattern or
literal head it is a `QualifiedType` whose `Module` is its spelling
(`db::schema`, or `db::schema.Kind` with an owner) and whose `Path` holds
each segment's position; segments are `ast.PathSegment` values, not nodes,
so no walk reads one as a name. Internal module keys keep `/` (`std/io`,
the cache and `Origin` keys); only source text and messages use `::`.

**Directory imports** (`file_paths.go`, `directory_loader.go`). Every parse
ends with `parser.NotePathUses`, which sets each path-only import's
`PathUses`: the paths its scope (the file, or the block it heads) writes
below the name it binds, `users` and `reports::daily` for `import
views::admin` in a file that writes `admin::users.index` and
`admin::reports::daily.show`. The parser cannot tell a file from a
directory; resolution does. A loader returns a path that names a directory
and no file with a `*DirectoryError` listing it (the frontend's and
stdcompiler's virtual files, a `DocumentManager` program's texts and a
check against interfaces' file set say so for what they hold in memory),
and the builder asks the disk (`DiskDirectory`) for any other miss. A
directory holding a `nomi.toml` is another module's and not one. `import
std`, and a module's or a dependency's name alone with no file of that
name, are their roots. `defineDirectoryImport` binds a `SymbolDirectory`
whose `DirectoryBinding` holds the listing and, for each of `PathUses`, the
file it reaches: the directory's path with the use's segments after it,
resolved as an import of that file would be (`resolveImportOrMiss`,
`resolveStdlibImport`) and bound under the use's spelling (`admin::users`)
in the import's scope and module tables. No identifier spells that name,
so every pass after the builder that reads a qualifier by name and
position (`ast.QualifierIdent`) reads a path as it reads a short name, and
a nested import's entries shadow a file-level one's. A use the import
cannot resolve keeps its error in the binding, reported at each site.

`importsOf` (discovery) appends each import's reached paths to its own, so
discovery loads exactly the files a directory import reaches, and every
artifact built from a file's import paths (the import closure a check
against interfaces reads, stored interfaces' imports, the dependents an
incremental recheck visits) depends on those files and no other file
under the directory. Since `PathUses` is a field of the import, a body edit
that adds or drops a path changes the file's interface fingerprint, or a
body's imports for an import in a block, and is checked as an import
change, never as a body-only rebuild.

`walkPath` checks a path where it is written, for `FilePath` and for a
`QualifiedType` with a `Path`: its first segment must be the closest
binding of a directory import's name, and its rest one the binding reached.
It records the reached file at the first and last segments and the
directory at each between, for hover, go-to-definition and highlighting,
and reports any other path in the words of spec §14 *Short names*, with a
`PathAtUseCode` fix where an import binds the file or directory under
another name; the checker drops its unknown-type error at a reported path
(`atPathError`). `bindShortName` binds a path-only import's name, file or
directory: a second import of the name, or a declaration that holds it, is
an error at the import, which `markUnbound` keeps out of the unused-import
check (`collidesWithImportedModule` reports the order where the import was
defined first).

The language server completes a path at a use one segment at a time from
the binding's listing and the disk below it (`pathSegmentCandidates`; a
path without a member never parses, so `pathBeforeWord` reads it from the
text), goes to a reached file through the directory's path with the use's
segments after it, and applies every `PathAtUseCode` fix in its
`source.fixAll`.

## Keyword declarations and their inputs

A top-level declaration that starts with a keyword and a name
(``phrase cart_items(count: Int) `...` ``) is read by one rule whatever the
keyword. `phrase`, `noun` and `embed` ([Embedded files](#embedded-files)) are
the only keywords Nomi knows, and any other
is an error. Nomi has no macros: a library that generates code ships a tool
(`nomi tool`) that writes ordinary `.nomi` files.

Every path that prepares a file expands these declarations, so the checker,
the language server and the builder all see the same functions. A `phrase`
becomes the function it declares, in its place, with the declaration's
parameter nodes and its name's position, and a body in the synthesized band.
A `noun` becomes the `once` of std/phrase's `Noun` it names.

An expansion gets two hidden imports (`phrase$`, `phrase_format$`) that no
source can name. They are the only way in to std's internals: std/phrase's
private constructors and the internal modules `std/phrase_format` and
`std/money_format`. A program that names one is told it is internal, and
docgen does not document them. This is why the IR builder lowers a call to a
std module's private function. No diagnostic names a hidden alias: the
registry's `phrase$.Noun` is never a "did you mean" (`SuggestDotted`).

A name a file does not bind that a public std module exports as a type, or
that names a public std module, gets the help `` import `std::http.Method` ``
(`stdTypeImportHint`, `stdModuleImportHint`, `internal/analysis/std_exports.go`).
A process builds only the std modules it reaches, so the answer comes from
std's sources, which package std installs (`analysis.SetStdSources`) and the
first such diagnostic parses once. A name two or three modules export lists
each import; a name four or more export gets none, since that list is noise
(`Wide` in `TestStdImportHints`). Internal modules and the prelude are never
named. The language server's "Add import" quick fix
(`FindMissingImports`) reads the same index, so it offers the import the
help names (`TestCodeAction_AddImport_IsTheImportTheHelpNames`). A phrase
parameter whose type names nothing gets that error alone: `checkPhrase`
skips the phrase's text and wrapping checks, which would repeat it.

The expansion reads inputs, the catalogs, and records each with its text and
a span map back to the declaration and each catalog entry. An error in an
input is reported at the input: `nomi check` prints the input's text, and
the language server publishes it on the input's URI. Agreement of nouns and
phrases is checked over the whole program.

A kept program records its inputs. A changed input is an edit to the files
that read it, with their text unchanged, so a catalog edit that keeps every
phrase's signature rebuilds one file's bodies.

## Embedded files

`embed name `patterns`` (spec §18 *Embedded files*) is written by the
keyword-declaration rule, so the tree-sitter grammar, the editors'
queries and the formatter's layout are a keyword declaration's, but the
parser reads it into its own node, `ast.EmbedDecl`, splitting the body
into `ast.EmbedPattern`s with their positions (`parser/embed.go`).

The front end resolves the patterns as it expands keyword declarations
(`analysis/embed.go`): the site's `EmbedFS` lists directories and reads
files under the module root, and every pattern is read from the root,
whatever directory the declaring file is in, as import paths are. It is
the disk (`DiskEmbedFS`, rooted at the project root, or the entry's
directory with no nomi.toml), a tour block's data files
(`MapEmbedFS`, through `frontend.Config.VirtualData`, which
`vmhost.WithVirtualData` and `LoadMarked` set), or the REPL's working
directory (`Config.EmbedRoot`). The resolver walks a pattern a name at a
time with `path.Match`, so it sees each match's directory entry (a
symbolic link stays one), skips hidden names and other modules'
directories in a walk, and records an error per pattern.

A pattern that matches nothing because its base (`EmbedInfo.Bases`'s
rule, `patternBase`) does not exist, from the root or from the declaring
file's directory, records no error: its index goes in
`EmbedInfo.Missing`. The expansion passes the missing bases to std/embed's
constructor as a second argument, a list of string literals, which makes
each an empty directory of the tree, so `Files.sub` of it is an empty
tree. Nothing in the front end reports it: `analysis.EmbedWarnings`
returns a file's warnings (`EmbedInfo.Warnings`, code `embed-missing`),
which vmhost hands to `WithWarnings` once a load has checked the program
(`Program.Warnings`; `nomi run`, `check`, `test`, the FFI wrapper's modes
and the REPL pass one; `nomi check --format json` writes them as records
of severity `warning`), and the language server publishes them as
warnings beside `dbg` and `todo` (`embedWarningDiagnostics`). A build
refuses them as errors where it refuses a `todo`
(`Program.missingEmbeds` in `image`, `EmbedInfo.MissingErrors`), which
covers every target and the FFI wrapper's builds. A program with a
warning is not kept (`Program.keep`), so a hit never hides one; its
pattern's base is a tree of its record like any other, so the base
appearing is a miss.

The declaration expands into the `once` it names, as a `noun` does:
`once site: embed$.Files = embed$.from_entries(<EmbeddedFiles>, [<missing bases>])`, with a
hidden import of std/embed, whose private constructor the expansion
reaches. `ast.EmbeddedFiles` holds each file's path, its `Source` (the
path on disk, or the data file's key) and its size and SHA-256, which the
resolver computes by streaming the file through the hash; no stage keeps
an embedded file's bytes. The checker types it `List<(String, Int, Int)>`
and reports the pattern errors the expansion kept (`checkEmbed`);
`analysis.EmbedOf` returns what each pattern matched, which the language
server's hover and go to definition read. The IR builder lowers it to a
list of tuples of a `ConstString` path, an `ir.ConstEmbedded` handle and a
`ConstInt` size (`irembed.go`); only a reached `once`'s are lowered, so
pruning drops an `embed` nothing reads. std/embed is a program std module,
written in Nomi over a `Map` of files and one of directories; a file's
`Contents` is `Stored{handle, size}` (or `Held(Bytes)` for the trees its
examples build), so `paths`, `list`, `walk`, `metadata`, `exists` and
`sub` read no contents.

**Reading contents.** A `ConstEmbedded` carries an `ir.EmbeddedRef`
(source, size, sum), not bytes. When the `once` runs, the VM enters the
ref in the machine's `rt.EmbeddedFiles` table and the constant's value is
the ref's index there, its handle. `Files.read` crosses into
std/embed_host's `read` (`rt.EmbeddedRead`, an RtFuncs row), which finds
the table on the frame's context and asks the machine's
`rt.EmbeddedSource` for the ref's bytes. Nothing is cached: the `Bytes`
live as long as the program holds them. The source is the host's
(`vm.Machine.WithEmbeddedSource`):

- `vm.DiskEmbedded`, the default (`nomi run`, `nomi test`, the REPL):
  reads the file each time, one byte past its size through SHA-256, and
  refuses a file whose size or sum differs from the ref's with an error
  naming it, which `rt.EmbeddedRead` raises as a fault. A changed file is
  never served as the one compiled.
- vmhost's `dataEmbedded` for an in-memory program (the tour): the data
  file's own string, checked against the sum.
- vmrunner's `imageEmbedded` for a built program: the image's files
  region, found by sum (`ir.Image.Files`), [below](#nomi-build).

`Files.sum` crosses into std/embed_host's `sum` (`rt.EmbeddedSum`), which
returns the ref's sum from the table and reads nothing; the image already
holds it, so the format did not change for it. std/http's
`serve_embedded` sends it as the file's strong `ETag` through
`Response.content`'s `headers`, which `http_host.answer_content` sets on
the recorder before Go's `ServeContent` runs, so Go handles
`If-None-Match` and `If-Range` against it. A precompressed sibling
(`file.br`, `file.gz`) is a file of the tree like any other, with its own
sum, so its `ETag` differs from the original's with no extra record.

The kept program stamps each embedded file as a single file beside the
pattern bases' trees (`vmhost/cache.go`), since a tree's walk skips hidden
names and `node_modules`, which `all:` embeds: an edit to an embedded
dotfile misses the record instead of reaching the read-time check.

A `Files` call with a literal path is checked against the declaration's
files where the checker types the call (`checkCall`, and a pipe stage in
`checkBinary`; `analysis/embed_paths.go`). The callee is std::embed's
function when its reference resolves to a `Files` function over std/embed's
`Files`; the tree is read from the declaration's `once` value, its
`ast.EmbeddedFiles`, through the files argument's reference (`site`,
`web.site`), or a `Files.sub` of one with a literal directory under `!` or
`?`. A file's interface keeps an expanded `once`'s value, so a check
against interfaces reads the same paths
(`TestEmbeddedPathsCheckTheSameAgainstInterfaces`), and the missing bases
with them (the constructor's second argument): a literal at or under one
is not checked (`embedTree.unchecked`).

The language server's sites set `SkipEmbedContents`: it finds the files
and their sizes without reading them, and records no input, so an edit to
an embedded file, or a new file under an embedded directory, rechecks
nothing until the declaring file is edited.

## HTML templates

An `Html` literal (spec §18 *HTML templates*) expands too, but where its tag
resolves rather than per declaration. The builder's walk of a
`TaggedString` asks whether the tag names std/html's `Html`: the tag's
symbol's declaration node is the one std/html's analysis declares (or, in
std/html itself, the file's own `Html`). If so it reads the literal with
`internal/htmltemplate`, a strict HTML tokenizer and a context machine
modelled on Go's html/template, run over the static parts with one slot
between each two. Its errors become the file's type errors, placed at their
character: a part's first line starts after the opening quote or after the
previous slot's `}` (the slot expression's span), and later lines start at
the literal's `BodyCol`, the column a triple-quoted body starts at once its
indentation is stripped (the lexer's `Indent` on the opening token). A slot's
error is at its expression.

A literal that reads cleanly gets its expansion, `html$.assemble([...])`
with each slot a call of the std/html function its context chose
(`slot_text`, `slot_attribute`, `slot_url_start`, ...), whose parameter's
interface is the slot's type rule. The expansion is kept in an unexported
field of the `TaggedString` (`Template()`), so a walk of the tree still
visits the slot expressions once, under `Parts`. The builder walks the
expansion in the literal's place, the checker checks it, and the IR builder
lowers it. The added nodes are placed on characters of the literal no node
is placed on, the quotes and each slot's `${`, since resolutions are keyed by
position. Those positions go in `FileAnalysis.StandIns`, as a membership
test's `in` does: the text there does not spell the name, so semantic
tokens, rename, find-references and document highlights skip them
(`StandInReference`). A token there would be as long as the function's
name and paint over the slot's code. Element content takes a `ToHtml` or another `Display`, which one
function's parameter cannot say, so the checker checks each `slot_text`
argument first (`chooseHtmlTextSlots`): a type that is `Display` and not
`ToHtml`, and is not std's `Maybe`, `Result`, a collection or a tuple,
moves the call to `slot_text_display`, and the reference at the callee's
position with it. The same pass moves a slot whose function takes a
`Display` (`slot_attribute`, `slot_rcdata`, `slot_url_path`,
`slot_url_query`, `slot_url_fragment`) to its `_maybe` twin when the
argument is a `Maybe` of a `Display`, which writes the value as the slot
does or nothing for `None`; a `Maybe` of a `Maybe` there is an error.
`UrlValue` and `CssValue` reach the same rule through impls for `Maybe<T>`.
The argument's type is kept in `checkedSlots`, so checking
the call does not check the argument again. The checker rewrites an
argument-type error at a slot into what the slot takes, with help for its
context (`htmlSlotHelp`).

A file that imports std/html gains `import std::html as html$`
(`withHtmlImport`), so `html$` reaches std/html's private slot functions as
`phrase$` reaches std/phrase's; another std file gains it as `import html
as html$`, in std's spelling, and std/http's directory listing is a
literal. An aliased whole-module import moves the module's key in the
builder's module map to the alias, so the file's own `import html.Html`
then finds std/html among the scopes the file is built against
(`builder.stdlibScopes`, in `resolveStdlibImport`). Before that fallback, a
std file built as a program (a reference page's editor) loaded std/html
again from source, and with it fresh copies of std/io and the rest, and its
`io.Writer.write_text` resolved to a scope the IR builder did not know as
std's. A slot's value whose type the checker has not solved (a bare `None`
in `disabled=${None}`) is solved by the one impl of the slot's interface
whose receiver it fits (`unifyInterfaceAgainstConcrete`): an impl at an
instance of a generic type (`impl OptionalAttribute for Maybe<String>`)
records its receiver (`recordImplTypeArgsFromBlock`), which also refuses a
`Maybe<Int>` there. std/html is a program std module: its
types are the program's. Its escapers are host functions in the internal
`std/html_host`, over `rt/html.go`, which `internal/htmltemplate`'s fuzz
target `FuzzTemplateEscaping` also calls: a slot value cannot change a
rendered template's tags.

std/toml is a program std module as well, so its slot interface `ToToml`
is held and dispatched as a program interface is. A shared std body that
takes a std interface without an rt table as a value declines
(stdprogiface.go), which is why `Toml` cannot be shared, and why
std/compiler, which is, takes its manifest as a `String`. `Toml`'s check and build cross into the internal
`std/toml_host`, over internal/stdtoml: it follows the text far enough to
place each slot (a key part, a value, or refused in a string, a comment
or touching a token) and reads the text with a stand-in per slot with
github.com/BurntSushi/toml, mapping its error back to a static and offset. `ToToml` returns a `Toml.Value`, never text; `build` writes each slot's
value with `Toml.Value.text` in Nomi (strings and table keys through
`toml_host.quote`) and hands the texts to the host, which quotes any value
but a `String` where the slot stands for a key.

## File interfaces

A file's interface is what another file's check reads of it
(`analysis.FileInterface`, `internal/analysis/interface.go`): the file's
nodes as a build is given them (parsed, Go bindings injected, keyword
declarations expanded), with what no other file reads taken out. A
function's body becomes an empty block holding only the `break` and
`continue` its own scope holds, the one fact of a body another file's check
reads (`FuncIterSignals`). The bodies of an `impl Release` and an `impl
Commit` stay, since another file's release check reads the call each makes
([releasing resources](#releasing-resources)). A `once` written with a type
has its value replaced by `todo`; an unannotated one, and a `noun`'s, keep theirs, since
the type is inferred from the value. Test declarations and attached tests
go. Interface default bodies stay, since registering the interface reads
them (`TakesSelfApart`, `BuildsSelf`), and so do parameter and field
defaults. `ExtractInterface` also records the interface hash, a SHA-256 of
`interfaceFingerprint`.

The nodes are written by `internal/ast/astcodec`, whose encoder and decoder
per node type are generated from `internal/ast`'s types by
`internal/ast/astcodec/codecgen` (`go generate ./internal/ast/astcodec`;
`TestGeneratedCodecIsCurrent` fails until it is rerun after an AST change). A
pointer met twice is written once and then by number. The two unexported
fields the analysis sets on a checked tree (`TaggedString`'s template) and
an `Expansion`'s detail are not written; an interface never holds either.
`TestInterfaceCodecCoversEveryNode` fills every field of every node form and
requires it to survive. `FileInterface.Encode` adds a magic, a version
(`interfaceVersion`), the file's key, path and hash.

**Checking a file against interfaces** (`interface_check.go`). A
`SourceProgram` describes a program checked from source, and
`ProgramInterfaces` extracts and encodes its files' interfaces and checks
one file F at a time: F from its source, the files of F's import closure
from their interfaces decoded afresh, and of every other file only its
imports and the impl blocks F can see, those whose interface, receiver and
type arguments are each declared in F's closure, F or the stdlib (the
relevant impl set). Which files an impl's header names is the file's impl
headers (`ImplHeader`: the interface node that brings the impl, and the
project files its names are declared in), resolved by building that file
against its own closure's interfaces with no other file's impls, since no
impl changes what a name in a header means, or taken from the file's own
check against interfaces when it has had one. A derive's synthesized impl
records no reference, so its header's names are looked up in the file's
scope; before that, every file's derives counted as visible everywhere, and
a leaf's check at 50k lines read 215 files' impls instead of none (132 ms
against 11 ms). `TestImplHeadersFromInterfacesMatchTheProgram` holds the
headers to the program's check from source. No other file of the module is
read; a file of another module is read from source.
The build is handed what it cannot work out from those files
(`buildInputs`): extra discovery roots, the program's application types
declared in what it reads, and the diagnostics of the two checks that read
every file's declarations (entry placement and exported names). F other than
the program's entry is built from an entry with no declarations, so it is
checked as a non-entry file; one with no nodes is imported by that entry and
loaded on demand, as in the program. A file's own diagnostics are its
build's and `CheckTypes`', its `break` and `continue` placement, and for the
entry its iteration, concurrency and task-lifetime checks. The program's
reductions over bodies (application-field roots, whole-file imports,
coherence, boot-only calls, agreement) stay the program's.

The interfaces are registered through the sweeps like source, so a file's
check against them costs a registration of its closure. Decoding all of
std's 100 interfaces takes about 4 ms against 60 ms to parse std, and they
are 1.17 MB against 1.39 MB of source, comments and positions included. The
stdlib is built from its interfaces (see
[the stdlib from its interfaces](#the-stdlib-from-its-interfaces)). A
program's own files are checked from source, except that a kept program
whose files' bodies changed checks the changed files against the stored
interfaces of the rest and lowers them on their own
([file records](#kept-programs), [lowering one
file](#lowering-one-file)); a check's `FileCheck` carries the other files
its build read, which such a lowering declares. See
[checking against interfaces](#checking-against-interfaces) for the harness.
A REPL session's program grows by one file per input, checked against the
earlier files' interfaces kept in memory (`ProgramInterfaces.AddFile`; see
[REPL](#repl)).

## Language server analysis

An open document is analyzed as part of a whole program that the
`DocumentManager` keeps per project root and entry: the program of the first
entry candidate that reaches it, else its own.

An edit that leaves each edited file's interface fingerprint as it was
rebuilds only those files' bodies, in place. A `once`'s value counts as a
body; an unannotated one is inferred again and must keep its type (early
cutoff). Any other edit checks the edited files, and the files that import
them directly or through others, afresh, and keeps every other file's
analysis. An edit that changes the impls a file brings is checked the same
way when only those files can see each impl it added, removed or changed:
a file sees an impl when every project file its header names is the file
or one it imports, directly or through others (the relevant impl set, as
a check against interfaces reads it). An impl of the leaf's own type is
seen only by its importers, so adding one at 50k lines checks two files in
about 0.1 s instead of the whole program in 1.07 s. The whole program is
checked afresh instead when another file can see a changed impl, when a
kept file holds something of a rechecked one, or when the rechecked files
are every file.

A field of an anonymous struct is declared where its literal labels it or
where a type expression names it. A literal's label puts its file and
position in the type (`FieldDef.File`, `Line`, `Col`), and a field access,
a field accessor, a destructuring pattern or a struct update's label on the
value refers there; from another file the reference is a symbol naming that
file (`anonFieldRef`), which go-to-definition, references and rename read as
they read an imported declaration's. A type expression's field names no
file and is linked only within its own file. A use with neither stands at
itself, marked `Undeclared`, for hover alone. A label in an unannotated
`once`'s value sits in a body, so a body edit moves the labels of the types
the file's literals built with its other positions (`shiftDeclarations`),
and the early cutoff keeps the once's old type only when its labels stand
where inferring it again put them (`sameLiteralFields`); a label inside the
edit has no place to move to, and the files that read the once are checked
again.

The program-wide reductions over bodies (coherence, whole-file imports, a
rootless file's application-field roots, the boot-only check's summaries)
keep each file's part with the analysis it came from, so a body edit reads
only the edited files again. The boot-only check then rebuilds its program
graph once from the kept summaries and reports every file from it.

When a document closes, the program it is the entry of goes, unless it
is an entry candidate another document reads, and every program under its
root goes once no document there is open (`forgetClosed`): a server that
opened and closed many files kept a program for each.

A recheck rewrites installed analyses in place. So a reader of one holds
`DocumentManager.View`, and a recheck takes that lock exclusively until it
has installed its result.

**Closed files.** A request that needs a closed file's analysis
(references and rename read every file whose text names the symbol, the
closed files' diagnostics pass reads each file) takes it from a program
kept for an open document when that program holds the file as a build of
the file alone would make it (`keptClosed`, `internal/analysis/closed_kept.go`):
the first entry candidate that reaches the file is the program's entry,
every closed file and the entry still hold on disk the texts the program
read, each open document's analyzed text is the one it read, and no file
has a syntax error or reads an expansion input. Its diagnostics are those
the file would carry open (`documentErrors`). An open entry whose text is
not the one on disk (an unsaved edit), which a build of the file reads,
leaves the file to the program when the edit cannot reach it
(`entryEditReaches`): the two texts import the same files, bring the same
impls and declare no `export fn` or keyword declaration, the program has
no application type and no error of its own, the file does not import
the entry, directly or through others, and its analysis holds nothing of
the entry (`holdsFrom`, as a dependents recheck keeps a file). References
to a function 137 files call after an unsaved body edit of the entry took
104 s at 50k lines, each closed file built on its own, and take 0.12 s. Otherwise the file is built
on its own, as before, which builds the declarations of the whole program
it belongs to: at 50k lines references to a function 137 files call took
149 s, and take 0.18 s from the program. `TestClosedFilesFromProgramsMatchTheirOwnBuilds`
holds each closed file of every multi-file corpus project, as a request
reads it, to a build of the file alone with the same documents open,
after each of a sequence of edits to the open entry, saved and then
unsaved, and requires some closed file served from the program past an
unsaved edit (`ClosedPastUnsavedEntry`); what a derive synthesizes is left out, since a build of
the file alone checks less of it and no request reads it.

**Opening from a record.** Opening a document checks the program it
belongs to whole, about 1.1 s and 303 MB held at 50k lines. When the
program was checked before, by this server or an earlier one of the same
build, and its files are as they were, the manager opens it from a record
in the artifact cache instead (`internal/analysis/stored_program.go`,
progcache kind `lsp-program`, keyed by the entry's path, its root's tree
walked as kept programs' are): each project file's interface
(`StoredInterface`), the SHA-256 of its text, its facts for the program's
reductions over bodies (`FileFacts`, encoded), the walk the
application-field roots take of its bodies for a program with an
application type (`AppWalkSummary`), what references and rename read of its
analysis (`StoredRefs`, encoded), the program's application types and
missing-impl supply, the documents whose lowering found nothing, and the
entry's lowering kept per file (`irbuild.KeptLowering`, encoded) as the
server lowered it from those texts. Each open document
is checked against the interfaces of the files it reads
(`ProgramInterfaces.Check` over `NewStoredInterfaces`), and a closed file a
request needs (references, rename, the closed files' diagnostics) is
checked the same way when it is asked for, and cached as closed files'
analyses are. A record is written for a program that reads no file of
another module and has no Go bindings. It keeps what each file reports
(`storedFile.Report`: its syntax errors and repaired declarations, its
document's diagnostics as `documentErrors` assembles them, and what the
reductions over facts report in it, `storedReportsOf`), which the open
documents and closed files read; a program that reports what only the
program checked whole words (its impls' errors, a missing impl, a noun's
or phrase's disagreement, a catalog nothing reads, any diagnostic beside
an application type, or a reduction's diagnostic its file's document does
not hold) keeps none. At 50k lines a project with a typo in a leaf opens
from its record in 0.27 s, where it was checked whole in 1.08 s. A
document whose check against interfaces reports
something its record's file does not is checked in its program whole.
Go-binding projects keep no program at all: the server builds each of
their documents on its own (`buildOpen`), so there is no program to write
a record of.

An `embed`'s files are read at its expansion for their names and sizes
alone (`SkipEmbedContents`), and the server tracks none of them while a
program is open, from its record or not. A record of a program that
embeds files is written with each pattern's base walked for every file and
each embedded file stamped on its own, as a kept program's are, and the
snapshot that store compares against takes those trees before it reads
the embeds again (`EmbedInfo.sameOnDisk`), so a file that changed after
the program's check is one the store sees moved; a record is not opened
once a file under a base came, went or changed
(`TestOpenFromRecordReadsEmbeddedFiles`). The inputs the files' expansions read
(phrase catalogs) are named in the record with their texts' SHA-256, so a
record is not opened once one changed on disk, and a program opened from
its record is forgotten once one holds another text, an open catalog's
edit included (`TestOpenFromRecordReadsCatalogs`). The record is written
when the server's lowering of an open document, with its program kept and
every text on disk, finds nothing (`NoteLoweredClean`): after an open or a
save. The lowering of a document opened from a record takes its result
from the record when the record lists the document (`StoredLoweredClean`);
otherwise the entry is lowered from a kept lowering (below) and any other
document from its text, since a document opened from a record offers no
`EntryProgram`.

A file checked against interfaces reads the other files' declarations
without their attached tests and with a typed `once`'s value `todo`. A
program built so carries the other files' texts, and hover reads a
declaration's examples and value, and a `phrase`'s or `noun`'s texts by
locale, from its source, expanded as its check expands it
(`FileAnalysis.DeclarationSource`). It also sees only the impls the file
can see, so implementation, and references to an interface method, check
the document in its program whole first (`CheckWhole`).

**Edits in a program opened from its record** (`stored_edit.go`). The
program opened from its record has a state: each file's text, interface,
facts and body summary, and the missing-impl supply, the record's at
first. An analysis that sees a file hold another text takes the edit in
that state as `nomi check` relinks a kept program from its file records:
each changed file's interface is extracted again (a file whose interface
hash and unannotated `once` values are unchanged keeps its impl headers);
the program's files must be the ones it had; the changed files, the files
that import one whose interface changed and the files that see a changed
impl are checked against the others' interfaces, each with no keyword
declaration; and the reductions over bodies run over their new facts and
the stored ones, with the supply worked out again when impls changed
(`ReduceFacts`), skipped when the new facts differ from the stored ones in
positions alone and the state's reductions found nothing. A program with an
application type also requires the walk its application-field roots take
of each checked file's bodies to be its old one and to find nothing
(`AppWalkSummary`), since those roots follow calls into other files' bodies
by a walk no file's facts hold. The summary walks the file's bodies as
each root's and function's walk would, with the walker in a local mode:
each step that would leave the file (a call, a factory's body read for
what it hands back, an interface method's default body, a dispatch to
every impl of a method) is named instead of taken, and the file's roots,
the boots they run under, and the impls the file brings are named too.
The file's own nodes are named by declaration name or by their order
among its functions, never by position, so an edit that only moves code
keeps it, where the body summary it replaced changed with every moved
line; two texts with one summary take every root through the same reads
in the same order, so a program whose check found nothing finds nothing
again. An edit that leaves the edited document's interface as it was
rebuilds the bodies of its installed check in place (`stored_inplace.go`),
as a kept program's recheck rebuilds bodies: the declarations take the new
bodies and positions, every position the check's declarations hold moves
with the edit over the files the check read, the bodies are walked again,
and the check's own diagnostics are worked out again. A rebuild rewrites an
installed analysis, so it takes the view lock, and the build holds it until
its result is installed. An open document keeps its installed analysis
while no file its check read changed how its interface is written; one an
edit checks again must import a changed file, so that the edit's
propagation analyzes it again.

An open document an edit checks again because a file it reads changed
how its interface is written is checked from its installed check
(`ProgramInterfaces.CheckAfter`), as a kept program's dependents recheck
keeps the files that do not import an edited one: the check takes the
installed check's analyses of the files the edit cannot reach and builds
the others from their interfaces again. A file the edit can reach is an
edited one, one that imports a file built again, one whose analysis holds
something of a file built again (`holdsFrom`), one the check now reads
otherwise (whole where it read its impls alone, or other impls of it), and
one that can see an impl the edit added, removed or changed. The build
hands the kept analyses its own program, as a kept program's recheck
does, so it takes the view lock first and the edit's build holds it until
its result is installed; the installed check is dropped. What the earlier
program knew of the kept files' `once` bindings carries over, as in
`checkProgramFrom`. The entry checks every other file through its closure,
so after a leaf's signature edit it builds the leaf and the entry again,
not 220 files. An edit that changes an impl takes the missing-impl supply
from the entry's check when the entry's closure is every other file of
the program, as it is when the program is the entry's closure: that check
registers what the entry built against every interface registers
(`ProgramInterfaces.Supply`), which cost a second build of every file's
declarations. Reusing a file's analysis found that `primitiveOriginIndex`
took a `once` typed as a primitive (`pub once scale = 100`) for a
primitive the file declares: a fresh build asks before onces are inferred,
and a build that reuses a file's analysis after, so the reusing build gave
up the stdlib's base and the reductions over facts with it. It now passes
over values.

An edit that leaves something to report (a typo, a syntax error, a type
error in an importer) stays in the state. Each checked file's document
diagnostics are assembled as `documentErrors` assembles them from the
program checked whole (`storedDocumentErrors`): its build's and check's
errors, its concurrency, task-lifetime and duplicate test name checks over
its own nodes (the entry's own checks and front-end errors for the entry),
and what the reductions over facts report in it (`Reductions.of`: its
boot-only calls, application-field roots and unused whole-file imports) in
the places `documentErrors` puts them, then dropped inside a declaration
the parse repaired; its syntax errors and repaired declarations are its
text's. Whether each whole-file import is used is settled on the file's
analysis from the reductions, as `CheckImplImports` settles it. The state
keeps what each file reports (`storedProgram.reports`), and the open
documents and closed files read it. Three kinds of diagnostic are left to
the program checked whole, since no file's check words them as the program
does: the program's own errors about impls (collisions, the orphan rule,
which a check of one file sees only part of and the entry reports too),
missing impls (the entry reports every one, each file those its demands
find), and a reduction's diagnostic in a file the edit does not check
again, whose document would keep its old diagnostics. A program with an
application type, or an edit that changed an impl, also checks whole when
a checked file reports anything. While anything is reported the record is
not written again and the entry is lowered from its text. At 50k lines a
typo in a leaf's body takes 0.04 s from the record against 1.07 s when it
checked the program whole, and one in the entry's body 0.05 s against
1.56 s.

Anything else (a reduction that finds something the above leaves whole,
an edit that reaches more than 16 files and a quarter of the program, a
file that came or went) forgets the program opened from its record and
checks the program whole, as an open with no record does, and from then
on the program is kept and rechecked as before. The state lives in
memory: the record is written again only when the server's lowering finds
nothing and every file holds the state's text on disk.

**Lowering the entry** (`stored_lowering.go`, vmhost's
`checklowering_stored.go`). A document opened from a record offers no
`EntryProgram`, and a lowering of the entry's text checks and lowers the
whole program, about 2.1 s at 50k lines (a leaf's costs 25 ms, since it
reaches its closure alone). So the entry is lowered from a kept lowering:
the one this process made last for the entry's state, or else the
record's, with each file whose text changed since then lowered on its own
from its check (`irbuild.LowerFileAmong`, `vmhost.relowerOne`, shared with
the relink from file records), and the files that import a file whose
signature (interface hash and unannotated `once` values) changed lowered
again too, each after the files it imports, and, when a file's impl
headers name other files than before, every file that sees one of its old
or new impls (`StoredLoweringFile.Headers`), as the relink from file
records lowers them. A program that reaches what a machine cannot run or
a declined body, or more than 16 files and a quarter of the program,
lowers the text instead. At 50k lines the entry's lowering after an
impl added to a leaf takes 0.30 s from the record, where it lowered the
text in 1.86 s; from the analysis of a program checked whole the first
such run takes 0.81 s, as a whole lowering does, since it first encodes
the held whole lowering as objects. Its problems are then what `CheckLowering`
reports. The record's kept lowering is the server's whole lowering from
the program's analyses when it wrote the record (`analyzedlowering.Kept`).

**References from a record** (`stored_refs.go`). References and rename
read every closed file whose text names the symbol. A closed file of a
program opened from its record is served from its stored references
(each reference, punned field label and definition with the symbol there,
by name, source file, kind and position) while it holds the record's text
and no file a stored symbol is declared in has changed how its interface
is written; a file an edit checked again drops them. Otherwise it is
checked against interfaces as before.

Decided: the server's record and `nomi`'s file records stay two formats
under two keys. Each is keyed by its own binary's build ID, and a
development build of either changes it on every rebuild; and the file
record holds one lowering of the entry `nomi check` was given, where the
server lowers every open document as its own entry and keeps references
the CLI never reads. A release could key both by its VCS revision and
share the interfaces and facts.

At 50k generated lines (221 files; Apple M2 Max, `TestScaleOpen` and
`TestScaleEdits` with `NOMI_SCALE_LSP=1`; each edit the first after the
open, with the entry open, and the edited file when it is another):

| operation | checked whole | from the record |
|---|---|---|
| open the entry, first diagnostics | 1.08 s, 221 files built | 0.28 s, 1 file |
| heap held, the entry open | 353 MB | 182 MB |
| hover or definition into a closed file | 3 ms | 1 ms |
| open a second file (core/util) | 8 ms | 4 ms, 1 file |
| references to a function 137 files call | 0.14 s | 0.12 s, none checked |
| rename of it | 0.14 s | 0.11 s |
| a closed leaf's diagnostics | 1 ms | 10 ms, 1 file |
| the entry's body edit | 14 ms | 34 ms, in place |
| its lowering | 0.50 s | 0.28 s, 1 file from the kept lowering |
| a leaf's body edit | 19 ms | 8 ms, in place |
| a leaf's signature edit | 0.09 s, 2 files | 0.10 s, the leaf, and the entry from its installed check |
| an impl added to a leaf | 0.10 s, 2 files | 0.12 s, the two, and the supply from the entry's check |
| the hub's body edit | 9 ms | 3 ms |
| the hub's signature edit | 1.01 s, 210 files | 1.03 s, the program whole |
| a typo in a leaf's body | 0.03 s | 0.04 s, 1 diagnostic |
| a typo in the entry's body | 0.02 s | 0.05 s, 1 diagnostic |
| writing the record | 0.43 s | |

Before the record kept facts, the kept lowering and references, the first
edit of the entry's body took 1.05 s from the record, its lowering 2.08 s,
and references to that function 1.81 s, each file checked against
interfaces. Before the entry was checked again from its installed check, a
leaf's signature edit took 0.35 s and an impl added to it 0.60 s: the
entry's check against interfaces reads every other file's interface, about
0.27 s, and the supply built the entry against every interface once more.

The entry imports every unit no other unit imports, so its check reads
every other file's interface and holds their declarations: opening a leaf
reads its own closure, about 10 ms.

`TestIncrementalMatchesFull` and `FuzzIncrementalMatchesFull` hold a recheck
to a full check, dump for dump, with no kept analysis holding one a recheck
replaced (`StaleForTest`). `TestOpenDocumentEditsMatchFreshBuilds` holds each
document's diagnostics to an analysis of the file on its own. See
[incremental checking](#incremental-checking).

The server never applies an edit computed from an analysis older than the
text it edits. `didChange` stores the text and returns, and the document is
analyzed 50 ms after the last edit. Completion and signature help respond at
once from the last finished analysis, with positions mapped into it, since
waiting made menus arrive too late. Hover, definition, references, symbols,
semantic tokens and inlay hints wait up to 2 s for the current analysis.
Rename and code actions fail with `ContentModified` rather than respond from
an older one.

**Annotations a default decides** (`code_action_default_type.go`). On a
field or parameter written with a default, the `refactor.rewrite` actions
"Remove type annotation" and "Add type annotation" turn `limit: Int = 10`
into `limit = 10` and back. They come with no diagnostic or hint and are
not in `source.fixAll`. Removal asks the analysis what the default's type
is alone (`FileAnalysis.DefaultTypeAlone`): a checker for the file, under
the owner the inference phase uses, checks the default with no expected
type against a copy of the file's body tables (`FileBodies.clone`), so the
published analysis is not written. It is offered when that check reports
nothing and leaves no part unknown, when the type is the written one
(`SameDefaultType`: identical, with no `embeds` coercion, and printed
alike), and when the written text is that type as the file spells it
(`FileAnalysis.SpellType`, the spelling the annotation help writes), so
an alias such as `n: Count = 1` stays. Adding writes the field's or
parameter's symbol type in that spelling, which is the inlay hint's text
for the types a file names bare.

**MessageFormat 2 in the editor** (`phrase_tokens.go`, `phrase_hover.go`,
`code_action_plural.go`). No editor Nomi supports has an MF2 grammar for
the injection `tree-sitter-nomi/queries/injections.scm` asks for, so the
server colors MF2 itself. `mf2.Tokens` runs the MF2 parser with recording
on: each piece of syntax it consumes is a token, and a parse that stops at
a syntax error keeps the tokens read before it. Text a pattern writes is
no token, so the editor's string color stays. `phraseBodyTokens` places
each token of a `phrase` or `noun` body in the source with
`ast.KeywordDecl.BodyPosition`, which accounts for a multi-line body's
stripped indentation, and drops a token that would cross a line;
`catalogTokens` places a catalog string's tokens through
`catalog.Value.Offset` and skips a basic string with an escape, which
breaks the byte mapping. `encodeTokens` merges them with semtokens' identifier
tokens, drops any token that starts inside the one before it, and converts
columns to UTF-16. The token types: `keyword` for `.input`, `.local` and
`.match`; `parameter` for a `$name` that is a parameter
(a noun's `$count`), `variable` for a `.local`; `function` for a function,
namespace and all (`:nomi:noun` is one token, since many colorschemes leave
`namespace` unstyled); `property` for an option's or attribute's name;
`enumMember` for a key, `*` included, and a name literal, `number` and `string` for
number and quoted literals; `type` for a markup name; `operator` for
`{{`, `}}`, `{`, `}`, `=` and a markup's `/`. The tour's highlighter does
not use them.

Hover inside MF2 (`phrase_hover.go`) finds the `mf2.Tokens` token under the
cursor: in a body by mapping the position back through `BodyPosition`, in
a catalog through `catalog.Value.Body`. An `mf2Text` carries the text, its
declaration's signature (`analysis.PhraseSignature`, or
`mf2.NounSignature`) and its locale: the source locale for a body and a
catalog's `source`, the catalog's locale for its `text` and a noun's forms.
What functions and options do is one table, `mf2.FunctionDocOf`
(`internal/mf2/docs.go`), which `TestFunctionDocsMatchValidate` holds to
the functions and options `Validate` accepts. A variant key asks
`mf2.Selectors` for its column's selector (plural, with `Ordinal` and
`Integers`; gender; weekday; enum) and a plural category's numbers come
from `mf2.PluralExamples`, which scans 0 to 1000 under the locale's rules
and writes runs (`2–4, 22–24, 32–34, …`). Text a pattern writes has no
hover, so a catalog entry's own hover answers there.

**Keyword hover** (`internal/hoverdoc/keywords.go`, `hoverdoc.KeywordAt`,
called first by `textDocumentHover`). Every keyword's hover is a row of one
table, `keywordDocs`: the word, the context it stands in (`""` for its main
use), the construct as Nomi code, one to three sentences from the spec, and
the tour or spec page that teaches it. `KeywordAt` lexes the file only when
the word under the cursor is a reserved or contextual word
(`lexer.ReservedWords`, `lexer.ContextualWords`), then reads the context
from the neighbouring tokens and the nodes whose span holds the keyword:
`as` in an import, a `gopkg` or a pattern; `case`, `if` and `dbg` after a
`|>`; `else` of a binding (`ast.BindingElse` at the keyword); `or` in an
`ast.OrPattern`; `for` and `with` on a `derive` line; `type` after `host` or
before a `go` binding; `where self`. Some rows add what the place holds:
`return` the function it leaves and its result type (a lambda's from
`ExprTypes`), `break` and `continue` the call their callback is passed to,
`self` the interface or impl receiver, `pub` the declaration it publishes.
A contextual word is a keyword only where an AST node records its
construct at that position (a group's `clock`, `boot` and `setup`
positions, `ast.Subtests`, `ast.ConcurrentBlock`, `ast.KeywordDecl`, ...),
or where its tokens are its construct's (`host fn`, `open fn` in an
interface, `embeds` starting a line of an enum, `derive Name` starting a
line); elsewhere the hover falls through to the name's. Where the analysis
already hovers the token, a row says what to do with that hover: keep it
and add the link when it explains the keyword with its types (`if`,
`case`, `then`, `assert`, `impl`, a lambda's `fn`, the word operators),
ignore it for `self` in an interface, and otherwise keep it with the
row's text below (a declaration's `fn` under the function's hover). `TestKeywordDocsCoverTheLexer` fails when a reserved or contextual
word has no row for its main use, `TestContextualWordsAreListed`
(`internal/parser`) when the parser recognizes a lexeme that
`lexer.ContextualWords` lacks, and `TestKeywordHoverLinksResolve`
(`cmd/nomi-docgen`) when a row links a page or anchor the site does not
have, read from the pages the generators would write and the tour's
chapters (`siteAnchors`). The tour's own hover (`hoverdoc.At`, which the
wasm build's `nomiHover` and `nomiHoverIn` call) asks it first too, as
`textDocumentHover` does.

"Add plural forms" is `mf2.AddPluralForms`: it keeps the declarations,
adds `.input {$name :integer}` unless one declares the variable, adds
`.match`, and asks `mf2.Selectors` which keys the selector needs in the
locale, so the arms are those the checker would require (whole-number
categories: English `one`, Polish `one`, `few` and `many`), each with the
old pattern, then `*`. The server writes the result as a multi-line body
four spaces past the declaration, which `nomi fmt` leaves as it is, or in a
catalog as a multi-line literal string (`catalog.Literal`).

## IR builder

The builder lowers every body a program runs into an `ir.Func`: module and
impl functions, `once` initializers, test bodies, lambdas and stdlib bodies.
Generic functions, types and impls are instantiated per type-argument tuple
a program reaches. Generic stdlib bodies are instantiated per program, never
in the shared cache.

A tuple's items are read by ordinal name (`pair.first`). The list of
ordinals lives once in `rt` (`rt.TupleOrdinal`, rt/ordinal.go), which the
debugger's variable tree reads too; `internal/ast` forwards to it for the
front end, which already links `rt`. A struct may declare a field called
`first`, so the name alone does not say which read it is. The checker records each field access it typed as a
tuple's or a named tuple's item in `FileAnalysis.TupleItems`, with the
item's index, and the builder reads that slot, unwrapping a named tuple's
distinct first (`tupleItem`, irtuple.go). An accessor (`.second`) has no
record and decides by the kind it reads from. The parser reads `t.0` as the
error that writes `t.first` and goes on with the ordinal, so no later layer
sees a numeric field.

A std module in `programStdModules` is not in the shared cache. It is
declared with each program that imports it, as one more module of the
program, so its types are the program's types and one program has one
descriptor for each. Its interfaces are program interfaces, boxed and
dispatched by the VM's ordinary path. Its positions are stdlib positions, so
a trap inside it is reported at the program's call, as one inside a shared
module is. `TestProgramStdModulesQualify` holds each listed module to the
rule: no `host fn` or `host type`, no shared std module imports it (the
prelude, which only re-exports, may), and it checks and lowers with no
decline. The prelude's import of std/phrase's `Phrase` and `Noun` reaches
the module only from a file that names one of them, anywhere, a
parameter's type included, whether or not the file's nodes carry the
prelude's import statement (`importedStdModules`, `preludeListedNames`);
a `phrase` or `noun` declaration reaches it through its expansion's hidden
import. Otherwise every program would lower std/phrase and std/money, and
their checked `Currency` literals would keep any program from a kept
lowering (`Result.Keep`).

Their bodies are lowered once per process, all of the listed modules as one
program, and linked into each program by name (`programstdlink.go`). The
lowering is kept: its modules, the std instances its bodies asked for, its
typed literals and refusals, and the linkage name of every symbol it
defines. A program takes the part of it that the listed modules it reaches
make (`restrict`): their own modules, the std instances whose type
arguments are declared only in those modules (`stdInstances.needs`), and
their literal sites and refusals, so a program holds no type, impl or
refusal of a listed module it does not reach. It still builds its type
registry, file index and impl index over the modules it reaches, so its own
files name their types, signatures and impls as before, but their gens are
`linked`: emitModule skips their walk, a synthesized impl block takes the
kept lowering's verdict instead of being lowered again, and every table of
the program resolves a linkage name the kept lowering defines to the kept
symbol (`ir.Table.SetLinkage`). A linked gen still lowers what the program
asks of it that the kept lowering lacks, a generic function's instance at
one of the program's types, and that links beside the kept modules. A body
both define is kept once (`dropLinkedDuplicates`). One lowering serves
every set because the builder's declaration waves are whole-program: it
gives each listed module the others' symbols, and every program names a
declaration by one linkage name. The std bundle carries it (below), so a
process normally decodes it. A process that can neither decode it nor cache
it lowers its first such program's set with the program, and keeps the
lowering of all of them for its second. Not linked: a program whose entry
is a listed module, a shared module's cases that import listed modules, a
reference editor's edited module with its twins (below), and a program that
reaches a test-only fixture module.

For the HTTP chapter's first tour block, a program's lowering takes 16-17
ms once the listed modules are kept, against 101-107 ms when it lowered
std/http, std/http_testing, std/url, std/fs, std/net, std/tls and the rest
itself. What remains is the program's own file and the listed modules'
declarations: the file index's reference walk (`closeReferences`) over
every body, the type and signature waves, and the tail graphs. Lowering
every listed module takes about 240 ms. `kindmemo.go` memoizes the
retention predicates, `stdHostOriginKind` is an index, the lint describes
an instruction only for a violation, and a generic std instance's module
binding clones the earlier modules' index rather than refilling it.

### The std bundle

The shared stdlib lowering and the kept lowering of the listed modules are
the same in every process of one binary, so a process decodes them from a
std bundle (`stdbundle.go`, `stdbundlelisted.go`) instead of lowering them.
The bundle has two sections. The shared one is the shared modules' IR, one
ir image, and per module what lowering decided for each declaration: the
outcome fields of each `stdFunc` and `stdOnce` (whether a body lowered, its
refusal, its arity and default wrappers, its IR body) and the module's
unlowered table, recorded once every module is filed, so each outcome is
the one the whole stdlib gives it (filing another module's declaration of
one spelling may refuse it, `addOverload`). A process decodes the image and
the records once, and restores a module when the shared stdlib has built it
(`stdlibIndex.withModules`, which `stdlibLowering` runs when `std.Shared`
has grown): it builds the module's view as `buildStdlibIndex` does, from
std's declarations (built from interfaces, no body checked), restores the
module's outcome in place of lowering it (`restoreStdModule`), and files
every restored module again in the bundle's order, the lowering's, in a new
index, so a gen holding the old one reads it unchanged. Filing a part of
the stdlib marks no declaration the whole would not, and the index is the
one a lowering of those modules would file. Every module's IR is the
image's, which a program links whole. The listed
section is the kept lowering, its modules one image whose symbols name the
shared modules' by linkage name, decoded against the process's shared
lowering with an `ir.Linker` when a program first reaches a listed module.
Linking applies the link-once rule: two std instance modules of the kept
lowering that define one instance keep one body.

A binary built with the `nomi_stdbundle` tag embeds a bundle that
`internal/irbuild/stdbundlegen` lowers from the same tree just before the
build and supplies with `go build -overlay`, with the stdlib's interfaces
and facts beside it
([the stdlib from its interfaces](#the-stdlib-from-its-interfaces)), so no
bundle file is ever in the source tree, and a build with the tag and no
overlay does not compile.
`scripts/build-tour-wasm.sh` and `scripts/release.sh` build that way, so the
tour and released binaries never lower std. Any other binary looks in the
artifact cache (`internal/artifactcache`: `os.UserCacheDir()/nomi/artifacts/std/`,
or `$NOMI_ARTIFACT_CACHE`) under a name made from its Go build ID. A
process that finds no bundle lowers the shared modules and writes the
shared section; one whose bundle has no listed section lowers the listed
modules when a program first reaches one and writes the bundle again with
both. Writes go through a temporary file renamed into place, and a write
prunes the bundles no process has used for a day. Under `go test`, and in a
child a test starts (`artifactcache` sets the variable for the test
process), the cache is under the system temp directory.

A bundle is checked before it is used: its own version and
`ir.FormatVersion`, a digest of the embedded std sources (`std.Digest`), a
digest of the host function registry the lowering reads, and the fixtures
flag must be the process's, each section's CRC must hold, it must name each
module the stdlib lowers once, and every declaration and `once` it names
must be the one the process's view holds at that place, which a module's
restore checks. A module that does not restore later in a process is
lowered (in a test it panics: the digest says the bundle is this stdlib's).
`decodeStdBundle`, which the tests use, restores every module and checks
the bundle's order too. A cached bundle that fails is ignored and written again, a
listed section that fails costs only itself, and an embedded bundle that
fails is reported on stderr, since the build that embedded it is broken.
Positions name std files `std/<file>` in the bundle and the process's
`SourcePath` once decoded. `NOMI_DEBUG_STD_BUNDLE=1` reports on stderr why a
bundle was not used.

Natively, decoding the shared section and building the index takes about 5
ms against 40-45 ms of lowering, and decoding the listed section 22-27 ms
against 240 ms. Under Node as js/wasm the shared lowering takes 175-295 ms
and its decoding 17-19 ms (34 ms on first use), and the listed lowering
1.3-1.6 s against 85-170 ms. A built `nomi` runs hello world in 0.36 s
against 0.40 s, and the HTTP chapter's first tour block in 0.41 s against
0.52 s. The first run of a binary with an empty cache pays the lowering,
and for a program that reaches a listed module about 0.3 s more to lower
all of them.

### Linkage names

A function's identity in memory is its `*ir.Symbol`, interned per gen in an
`ir.Table`. A symbol another module can name also carries a linkage name
(`link.go`) built from the analyzer's declaration names
(`analysis.DeclarationNames`: `parse`, `Invoice.total`, `impl Display for
Invoice.to_string`, `once rates`, `parse/step` for a function inside a
body) qualified by the declaring module: `std/http.get`,
`billing/invoice.impl Display for Invoice[...].to_string`. A generic
instance adds its type arguments, spelled by `portableArgsKey`, which names
every declaration by its linkage path (a program's from the names its
lowering holds while it runs, `holdDeclKeys`), and a process-wide std
interface (`stdIfaceDefs`, whose decl is a node of the process's own) by
its spec's module and name, so `impl CheckedLiteral<Display, Url> for Url`
names its functions alike in the std bundle a process reads and the
process that wrote it; an impl
function adds its block's receiver and the substitution it was built
under; a default accessor its slot; an interface method a program interns
for a std interface (`stdIfaceMethodToken`) is the program's, not a
module's. The name depends on the declaration and not on the lowering, so
two lowerings of one module name its declarations alike, which is what lets
a program import the kept lowering's symbols. A symbol only its own module
names (a local, a field's projection, a type's symbol, whose run-time
identity is its spelling) has no linkage name.

The image codec writes each symbol's linkage name, so a module encoded on
its own (`ir.EncodeObject`) is an object: `ir.Linker` decodes objects and
resolves every linkage name to one symbol across them, and a name two
objects define is linked once when the two bodies encode identically.
`ir.CheckLinkage` reports a symbol two modules name with no linkage name,
and two symbols that share one with different code. `NOMI_LINK_OBJECTS=1`,
or `irbuild.LinkThroughObjects` in a test, makes every lowering encode each
of its modules (the program's, the kept and shared std modules, its std
instances) as an object and link them again before the VM sees them
(`objects.go`); see [Objects link to the same
program](#objects-link-to-the-same-program).

A def's run-time spelling is qualified by its declaring file, read from the
declaration names, so a std instance's gen, which holds its caller's kinds,
spells fs's `Entry` as `fs.Entry` and not as the instance's module.

A listed module's reference editor checks the module from edited source, and
the listed modules its editor imports are lowered beside it from the shared
analysis (std/storage_testing beside an edited std/storage). Those name the
shared copy's declarations, so each is resolved to the edited module's
declaration of the same kind and name (`implFuncTwins`): its types in the
type registry, and its module scope and file-level functions in the file
index (`indexEditedStdTwins`), so `storage.check_key` in storage_testing
calls the edited module's function.

A call into another file that leaves a defaulted parameter out fills it
where the call is when every name the default mentions means the same
there, and otherwise calls an accessor the declaring file builds, one per
defaulted parameter, taking the parameters before it (siblingdefault.go).
A file builds them for each `pub fn`, and for each private `fn` when its
partner test, the one file that may call it, is in the program. A
monomorphic impl's `pub` members get theirs as the impl is emitted, a
generic member with its own type parameters per instance
(`methodInstAccessors`), and an impl function of a generic type's instance
(`Job.new` from a file that never imports the `Duration` its `timeout`
default names) on request: the declaring gen queues the request and builds
the accessors under the instance's frame in the late-instance flush
(instimplaccessor.go). A concrete operand of an interface-typed parameter
of a type-qualified call enters by erasure before its slot is matched
(`qualEraseSlotted`).

A `Regex<C>`'s capture type C is phantom. The builder lowers every
`Regex<X>` as one host handle, and a std function whose type parameters
appear only inside `Regex<...>` is not generic to it. `String.captures`
builds the C value from a match's groups by C's shape alone, through a VM
intrinsic, so no std body is instantiated per capture type.

Every IR temporary has a stored value type, and a retained graph must pass
`ir.Lint`. The shared stdlib lowering skips lint outside `go test`, since it
is the same in every process of a binary; `TestStdlibLowersTheSameWithoutLint`
requires that lowering, and a std bundle of it decoded, to pass lint and to
retain what the linted one retains.

A body the builder declines is not retained, and a program that reaches one
fails before its first effect. `nomi run` prints the diagnostic `nomi check`
gives, at the expression the builder stopped at and worded for the author
("this call to `f` is not supported yet, so `fn g` cannot run"). `nomi test`
reports the case as `BLOCKED` with that diagnostic. The builder's own
decline reason is shown only with `NOMI_DEBUG_LOWERING=1`, which adds it to
each such diagnostic as a hint.

The shared stdlib lowering records no decline of a std body: a std function
that declines is marked unlowered, and a program's call to it declines at
the call, which is code the program's author wrote. A stdlib module's own
cases (`nomi test` and `nomi check` on `std/<module>.nomi`) are std's
author's code, so there `vmhost` follows such a call into std
(`stdBlocker`): `irbuild.StdlibDeclines` lowers the callee's module afresh
with a census that keeps each body's decline, and the report moves to that
body's decline, through each further call to a std function that did not
lower, and names the innermost construct in its own file. A program-std
module's declines already reach the program's hooks; a diagnostic in a
stdlib file is worded from that module's nodes (`stdModuleAt`). Before a
module's embedded cases are built, `frontend.StdlibErrors` reports every
check error the shared analysis recorded, in any module, as a program's
check does, so a case that reaches a std body that does not check fails
with the error rather than blocking on the body's missing lowering.

A dispatched call reaches every retained implementation of its method. One
with none blocks the program only when some module implements the method
without a dispatch entry (`ir.Module.ImplementUnretained`: the body was
declined, or the type's values do not dispatch). A method nothing
implements is not reported: no value of an implementing type exists, so
the call runs over none (`Handler.kind` over an empty `List<Handler>`), and
were it reached it would stop with the VM's dispatch miss.

The builder's own type is `kind` (a tag plus declaration pointers, spelled
in Nomi by `nomi()`), and a value is an `expr`: its IR temporary and its
kind. Do not add Go text or Go names to the builder.

`irRetainedStructKind` and `irRetainedEnumKind` keep each typeDef's last
result, stamped with a process-wide epoch (`kindmemo.go`). A function that
writes an existing typeDef's fields, variants, slots, layout or
lowerability opens a `changingTypes` bracket around the writes: the memo is
off while one is open, and opening or closing one invalidates every stored
result. A function that builds a new typeDef and writes nothing else opens a
`constructingType` bracket, which turns the memo off without invalidating.
Under `go test` every memoized result is recomputed and compared, so a write
outside a bracket fails the first test that reads the stale result;
`TestKindMemo_*` count the walks one lowering makes and plant such a write.

### Lowering one file

A whole program's lowering can be kept (`Result.Keep`, perfile.go): each
module as an object, its std instances as objects, and what lowering one
of its files again needs to know of the program and cannot read: the
listed std modules it links, the registry's program-wide sets (the
instances some file holds erased and the enums some file boxes, both
spelled rather than keyed by pointer), the synthesized impls its
speculation refused, whether the entry starts an app, the linkage names
some module names and none defines, each file's declines and refusals,
and a fingerprint of every top-level declaration of each file (its
encoded nodes, positions included).

`LowerFile` lowers one file F from F's check against its imports'
interfaces (`ProgramInterfaces.Check`, whose `FileCheck` now carries the
build's other files), and links it with the kept objects of every other
module into the program a whole lowering makes. The program it lowers is
F first, then the files F's check read, declared as a whole lowering
declares them. Each of those is `fromObjects`: nothing of it is lowered,
not even what F asks of it, since its nodes are its interface's, and every
table resolves a linkage name the kept objects name to their symbol, as a
program resolves the listed std modules it links. F is first but is not
the entry unless it is, so its gen builds no boot, main failure or test
table, and whether the program starts an app is the kept result. The
registry's sets start as the kept ones, so F records the dispatch entries
other files' boxing asks of it.

What F's module held in the whole lowering that F's own bodies do not
make is carried from its kept object (`carryKept`): a function whose
linkage name continues the name of a top-level declaration of F whose
fingerprint is unchanged (an instance of F's generic function another file
asked for, a field default's accessor), and the dispatch entries to what F
now holds. `LowerFile` returns a `*FileMiss`, and its caller lowers the
whole program, whenever the result could differ from a whole lowering: F's
declines or refusals are not the whole lowering's; another file's are not
among them; F holds erased an instance or boxes an enum no file did; a new
module names a linkage name no module defines and the whole lowering did
not name; or F's kept module holds something that cannot be carried.
`Rekeep` replaces F's object and fingerprints in the kept lowering with
the new ones, for the next file's lowering.

`TestLowerFileMatchesTheWholeLowering` lowers every file of three
generated projects this way and requires each module to be the whole
lowering's part by part (`ir.Parts`: each function and cell encoded on its
own, each entry by the names it holds, so order and a type symbol made
twice do not count), the linked program to define every name the whole
one does, and no file to miss. See [files lower on their own as in the
program](#files-lower-on-their-own-as-in-the-program) for the harness over
every recorded program.

`LowerFileAmong` lowers one of several files a relink lowers again after
one edit (see [kept programs](#kept-programs)). A file whose positions
moved is not a miss for the object of another file that is lowered again
too. What the file's old object held from a declaration that changed or
went (the default accessor of a parameter it no longer has, an impl it no
longer declares, an instance of a generic function whose body changed) is
dropped rather than a miss, unless a kept object of a file not lowered
again, or of one already lowered again, or a kept instance, names it. A
file the check read with its bodies is lowered as a linked library is, so
the file can ask it for an instance no kept object holds, and `Rekeep`
keeps that module's functions beside the std instances.

`LowerSessionFile` (session.go) is the REPL's form of it: a session's newest
file lowered against the modules the earlier files lowered, kept in memory,
with what a lowering of one file of a whole program returns a miss for
taken as it comes ([REPL](#repl)).

### Instantiation and dispatch

A generic function, type or impl is specialized to each set of concrete type
arguments a program uses it at. Inside a body, `T` is a known type, and
`T.format(x)` calls that type's implementation directly. A bounded generic
function's declaration lowers no body of its own (`funcDecl`): every call,
function value and other file's call names an instance.

Interface calls on existentials dispatch at run time. An interface-typed
parameter, field or collection element erases its concrete type (spec §4).
Values carry their type identity, and the call selects the implementation by
the receiver's identity. What a program may do with a value of interface type
is the spec's §12 *Interface values*.

A std interface held as a value is lowered as a program's own is
(`internal/irbuild/stdprogiface.go`). Each file that names one builds a def
from std's declaration, and a generic one is instantiated per type arguments
as a program template is. The signatures are std's source: while they
resolve, `srcFA` returns std's file, so a position-keyed lookup
(`Maybe<String>` in `Matcher.find_in`, `Fragment<I>` in `Literal`) reads what
the checker recorded there, and a type expression naming no substituted type
parameter resolves in the declaring module's scope, as a std function's
signature does (`stdTypeKind`), so `ToJson`'s `Json` needs no import and
stays std's in a file that declares its own `Json`. A type expression neither
resolves is looked up by name in this file's tables only when its head name
means the same declaration here as in std's file (`stdSrcShadowed`); else that
function is a signature gap of its own. A file that binds a prelude enum's
name to its own type (`struct Fragment`) keeps std's declaration as a
by-position anchor (`preludeAnchorsOf`), so `literals.Fragment<String>` there
is still std's. Its dispatch symbols are the program's,
interned in the first file's table, so a value boxed in one file dispatches
to an impl recorded in another. A program type's impl is recorded as any impl
is. A std type's value enters as a program interface's would: a builtin
scalar or a std struct or enum unboxed, its run-time value naming its type,
and a std container or leaf (`List<Int>`, `Range`, `Regex`, `Bytes`) boxed
under its type's name (`irBoxPlan`). A tuple or anonymous record entering a
std interface (`h: Hashable = (1, 2)`) is boxed the same way
(`irStructuralBoxes`); its `Hashable.hash` thunk is the derive mix a direct
call lowers (`tupleHashOf`, irtuplehash.go), and its Display is the
structural rendering a container's is (`irDisplayErasedContainer`,
`vm.displayStructural`). Either way each function's dispatch
entry is a thunk the program builds, once per file, type and function: a
function of the method's operands whose body is the call `Iface.method(...)`,
planned as a rendering's `Display.to_string(v)` is, so std's impl is whatever
a direct call with that type reaches (a container family's operation, an
instance of std's generic body, a retained body or a host function). Where the
file binds the interface's bare name to something else, the thunk calls the
std function whose impl header names the interface's declaration. A thunk is
built with the gen's refusals and the decline bookkeeping set aside
(`stdProgIsolated`), so a failed one leaves the boxing site's refusal. Its
nodes sit at line 1, column 1, where no reference is recorded.
`Hashable.hash` of a value held as `Hashable` dispatches through the table
like any other function, so it returns what the call on the concrete value
returns (`TestStdProgIface_InterfaceCallsAnswerAsTheDirectCall`). A Map key
or Set element of interface type, and `Hashable.hash` of a value held as
some other interface, hash with `vm.hash_value` (vm/keys.go), which has
only to agree with `==`.
`Display`, `Comparable` and `Debug` keep their `stdIfaceSpecs` rows, `Struct`
and `Iter` their own representations, and the stdlib lowered once for the
process builds no such def, so a shared std body taking one declines.

A declared struct, enum or distinct type with no `Equatable` or `Hashable`
impl of its own meets a `where T: Equatable` or `where T: Hashable` bound
by its equality (spec §5 *Equality bounds*). The checker decides it in
`structuralEqualityConformance` (analysis/structural_bounds.go) over
`noEqualityPartWith`, the one definition of what `==` accepts, at each
place a bound is checked (`typeImplementsInterface`, the impl-bound match
`typeSatisfiesInterfaceForImplMatch`), and records no demand for the pair.
An existential position (`h: Hashable`) still takes only an implementor
(`partMeetsInterfaceValue`). A derive's field check asks the same of each
component from its declaration (`deriveComponentHasEquality`), since types
are not resolved when derives are checked. In the builder, `meetsByEquality`
establishes that no file and nothing in std writes or derives the impl
(`noImplOf`, `declaresImpl`); `Equatable.equal` on such a type then lowers
as `==` (`compareOperands`) and `Hashable.hash` as `vm.hash_value`, the hash
a Map key of it takes (irscalaroperator.go). `declaresImpl` counts a generic
type's impls from its template, since they are registered per instance and
never in `implOrder`. A std instance gen (`Maybe.hash<Point>`) has no
registry of its own and asks its program's (`programReg`), so a derived
impl wins there too.

An interface default is lowered in the file that declares it, once per
implementing type, with `self` bound to that type (`gen.defaultSelf`), so its
names (a private function of that file, its imports, its `once` values) are
that file's wherever the implementing block is. Each implementing block gets
one function per default it does not write (`inheritDefaults`), so a
type-qualified call, the dispatch table, a dictionary and a function value
all name the block's function as they name a written one. A block in the
declaring file lowers the body as that function. A block in another file
makes the function one tail call of the declaring file's instance at its
receiver (`irImplForwardDefault`, ifacedefault.go), which the declaring gen
queues and builds in its own scope (`flushIfaceDefaults`, late instances
included). In the instance, a call of the interface's own function at the
receiver, bare or qualified (`area(s)`, `Shape.describe(s)`), is the
implementing block's function (`defaultInstImplPlan`), so an `open` override
the block writes is the one called and another default is its forwarding
function. A `self(n)` pattern names the receiver directly (`selfHeadDef`),
since the declaring file may have no name for it. The instance is keyed by
the default's declaration, the receiver and the type arguments it is lowered
at (the interface's and a generic default's own, a generic default being
instantiated per call in the implementing block first, methodinst.go), and
links by the interface function's linkage name with them
(`shape.Shape.describe<Square>`). A call in another file that leaves out
a parameter default of an interface function, whether it reaches an
implementing block's function, an erased receiver's dispatch or a
named-argument call, fills it as a sibling file's `fn` default is
(ifaceslotdefault.go): at the call, under the declaring file's names
(`lowerNamesFrom`), when every name it mentions resolves to the same
declaration in both files (`defaultNamesAgree`), and otherwise by calling
an accessor the declaring file builds on request, keyed by the interface
function, the slot and the kinds of the parameters up to it, and linked
by the function's linkage name with those kinds and the slot.

A default a std interface declares with a Nomi body (`Discrete.steps_between`)
is std's code, whose names resolve in its declaring module. A std module is
lowered once per process, so its default is a template over `self`,
instantiated per receiver kind and program as a generic std function is (stdifacedefault.go,
stdinst.go): a gen of the declaring module's retained view builds it with
`self` bound to the receiver (`gen.defaultSelf`), and a bare call of the
interface's own function in the body (`compare(b, a)`) is that function at
the receiver (`stdIfaceOwnCall`), which reaches the receiver's impl wherever
it is: std's, the program's, or a derived one. A call reaches the template
when the checker resolved it to the interface's function and no route that
finds a written impl function answered it (`stdIfaceDefaultCall`, the last arm
of `qualCallLowered`): `Day.steps_between(a, b)`, `Discrete.steps_between(a,
b)`, and either inside a generic body once its argument is known. So an
`open` override an impl writes is found first, and is the impl's own function
(`stdIfaceOverride` admits it beside a `stdIfaceSpecs` row, whose table holds
the row's functions alone). A row's anchor ignores the interface's Nomi
defaults (`stdIfaceSpec.matches`), so `Comparable.compare` keeps its table
when Comparable gains defaults; a default on an erased receiver declines. The
instance's linkage name is the interface function's with its receiver
(`std/discrete.Discrete.steps_between<Day>`). A std body that calls a default
is not retained by the shared lowering, which builds no instances, and a
program builds it as an instance with no arguments (`stdUnretainedMonoBody`),
a call that leaves out a parameter with a literal default included
(`stdUnretainedShortCall`). A default that is no literal
(`max_fraction: Int = Int.max(Decimal.scale(d), min_fraction)` in
`Decimal.format`) is Nomi of the function's module, so the instance's gen
builds the function's arity wrappers and default accessors beside its body,
as the shared lowering builds a retained function's, and the call is planned
against them (`stdUnretainedDefaultedCall`). They link by the names the
shared lowering would give them (`Decimal.format arity 3`), which it never
defines for a body it does not retain: `irStdArityRetain` and
`emitStdSlotDefaults` each build only beside the body. The shared lowering builds no
instance of a default even at a receiver only std declares (`Int.min`), so
it never defines a name a program's own `Int.min` call defines too. Range's and
Iter's own protocols (`known_count`, `is_infinite`, `steps_between` as a
Range's step count) keep their lowering in iter.go and irrange.go: the VM
drives them with the impl's functions as values and answers an inherited
default statically, and `Iter` is generic, which the template does not cover.

### Type argument conversion

A std collection converts to wider type arguments (spec §5 *Type argument
conversion*) only where the conversion costs nothing, so an ordinary call
never converts a list element by element behind the reader's back. What
one element's conversion costs decides it:

| Element conversion | Work per element |
|---|---|
| the same type | none |
| `Int`, `Float`, `Bool`, `String`, a declared struct, a declared enum with no `embeds` variant or a declared distinct type into an interface it implements | none: the existential is the value, and a dispatch reads the value's own type name (`erasesUnboxed`) |
| a function into one whose parameters and result convert as above | none: a call passes and returns the same values |
| a std container, a `Maybe` or `Result`, a std leaf (`Decimal`, `Bytes`), an `embeds` enum, or a tuple into a std interface, into an interface | a box (`irBoxPlan`) |
| an embedded type into its enum | a record around it (`irembedsrebuild.go`) |
| a function that needs its argument or result converted another way | an adapter closure (`funcWiden`) |
| a collection into an `Iter` | a view |

The checker admits the conversions of the first three rows under a List,
Vector, Set, Map (keys and values), Maybe or Result, nested and through a
tuple's elements and a record's fields (`analysis.unifyAssignArg`,
`carriesOwnType`; `unifyAssignFree` inside a function type there), and
refuses the others with help that names the explicit form. The builder
returns such a value with one `ir.Copy` typed at the wider kind
(`relabels`, `irCopyRelabel`, irrelabel.go), in `coerceEmpty` before any
rebuild is tried and in `qualSignature`, so a std or sibling function's
argument passes as it is. `ir.ValType.Accepts` accepts the copy: two
instances of Maybe or of Result (`sameCovariantEnum`), and a function
parameter either way round.

A record relabelled this way may hold an `Int` in a word slot where its new
type says a reference. The VM reads a field through the record's own
descriptor (`frame.writeField`, `opProjF`, `opProjP`), and equality and
hashing read each slot by its own layout (`rt.Keys.recordsEqual`,
`hashSlot`), so it reads, compares and hashes as a record built at the
wider type. `TestRelabel_ConvertsNoElement` counts it: converting a list of
10,000 Ints, structs, functions or lists, or a map of 10,000 entries, enters
one function body (the converting function's) and returns the very list it
was given, where `Iter.map(xs, keep) |> Iter.to_list()` enters 10,001 and
builds a new one.

A declared generic type's type arguments stay exact. Its instance's
descriptor names them (`rt.TypeDesc.Inst`, `main.Box<Int>`), and a
dispatch on an erased instance (`vm.implOf`) and an erased Display
rendering select by that name, so a relabelled `Box<Int>` held as a
`Box<Display>` would reach `Box<Int>`'s impls, which need not exist.
Converting one for free would take impls recorded for every instance a
conversion reaches, and a checker rule that `Box<Display>`'s impls imply
`Box<Pt>`'s, which fails today: an impl's `where T: Equatable` holds for
any interface value, and need not for `Pt`.

Two conversions the checker admitted before this rule still do work per
element: an embedded type entering its enum under a type argument (a
`List<Circle>` as a `List<Shape>`), and a collection of collections
entering a collection of `Iter`s.

Where nothing says which way values flow (two typed collections joined with
`+` or `-`, two typed spreads, the branches of an `if` or `case` with no
expected type, the elements of an unannotated list), their types must be
one, and the error carries a help and a fix naming the annotation that makes
them one (`analysis/annotation_help.go`). The type is found by walking the
two types together: where they differ, a type alias the file can name whose
type both parts convert to (the one the enclosing signature names wins a
tie; an unbroken tie suggests nothing), else the walk goes into a List, Map,
tuple or function result, else the part the other converts to; failing
that, one side's whole type. The candidate is spelled with the names the
file writes (`http.Handler` under `import std::http`, `spellType`),
resolved as an annotation is, and kept only when the value checks against it
as a binding's value would (`annotationTrialPasses`, a trial check whose
errors and recorded types are put back). For `+` and spreads the values
that change must read a binding with no annotation, and that binding is
the one annotated.

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
hashing and rendering otherwise see through the box. A std type that is no
container and enters std's `Display` or `Debug` without its own erased
rendering (`d: Display = 'c'`, a `Duration`, a `Date`, a `Byte` as `Debug`)
is boxed too: the `Display` box names a thunk whose body is the direct call
`Display.to_string(v)`, so a host function's impl is reached as a direct
call reaches it, and `Debug` renders the value inside through std's Debug
body (`irStdNamedBoxPlan`).

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
machine whose result a replay cannot compute (input, the clock, randomness,
timers, files, std/compiler's hosts) with its result and statement. Going
back abandons the run and replays the program from its start on a fresh
machine, returning each crossing from the recording and checking that it is
the same call at the same statement. There are no checkpoints, so going back
from statement N runs every statement before the target again. A read that
an `io.capture` or `io.replay` returns runs again instead of being recorded,
so the capture's transcript replays too. A replay cannot reproduce how tasks
interleave, so step back turns off at the first task, `concurrent`,
supervisor or channel.

The variables tree is `rt.Inspect`, built from a value's representation
and running no Nomi code. It names a struct's fields by name and a tuple's
items by the ordinals that read them (`first` through `twentieth`); a named
tuple shows its tuple's items as its own children, as `span.first` reads
them. An item past the twentieth has no ordinal and is `[20]`, its
zero-based index in brackets as a List's elements are named, so it never
reads as a field. A watch or hover that is a variable or a path of names on
one (`p.x`, `nested.second.first`) follows those children with nothing
compiled (`readPath`, internal/dap/evaluate.go); any other text, `pair.0`
included, is compiled as below, and the compiler accepts or rejects it as
Nomi does.

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

A frame's Go context carries what the machine gives every frame: its locale
data, phrase tables, streams, files and input. A supervised task's frame
takes those values from the frame that spawned it and its cancellation from
the supervisor, so it outlives the request that spawned it and still reads
the program's locale data rather than `cldrdata.Default`'s, which a runner
leaves empty (`TestSupervisorTaskReadsTheSpawnersContextValues`,
`TestLocaleImage_ASupervisedTaskReadsTheProgramsLocales`).

## Int: exact at any size

An `Int` holds a whole number of any size, and its arithmetic is exact. The
representation keeps the common case, a value that fits in 64 bits, as cheap
as a plain 64-bit integer.

**In `rt`.** A value that fits int64 is an `int64` everywhere: a `Value`, a
VM word register, a record's word slot. A value past int64's range is a
`*rt.BigInt`, an immutable `math/big` integer. There is one canonical form:
every operation returns an int64 when the value fits, so `big - big` that
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
path kept out of the loop, which computes the exact result. Multiplication detects a
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
`Int.wrapping_add`, `wrapping_sub` and `wrapping_mul` return the low 64 bits
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
  it holds; nomi.d.ts types an Int as `number | bigint`, and `nomi types`,
  for JSON a frontend reads with `JSON.parse`, as `number`.
- A Range's count protocol inside rt counts in int64; a range of more
  elements than that declines there, while `known_count` returns the exact
  count.

## Stdlib

`std/` is one Nomi module embedded into the binaries. Each process builds it
from its modules' interfaces, a module at a time as its programs reach them,
and checks a module's bodies only when something reads them (below). Its
lowering, the shared modules and one lowering of all the modules in
`programStdModules`, which each program declares and links by name, is
decoded from a std bundle the binary embeds or caches, a module at a time
too, and lowered only by a process that has neither (see
[IR builder](#ir-builder) and [The std bundle](#the-std-bundle)).

The analysis also works out the stdlib's share of a program's impl index and
coherence facts once per stdlib a process has built. A build folds only its
own files and lays them over that base, which no build writes. The
`TestStdBase_*` tests in `internal/analysis` and `vmhost` hold it to a build
that folds every file.

Stdlib files import each other with bare paths (`import iter.Iter`); code
outside writes `std/iter`.

`std/_fixtures/<name>.nomi` files are test-only modules
(`std/_fixtures/<name>`). They are in the stdlib only of a test binary run
with `NOMI_STD_FIXTURES=1`, so a test that needs one runs itself again in a
child process with it set. They are always lowered per program.

### The stdlib from its interfaces

`std.Shared`, the stdlib every program in a process is checked and
lowered against, is built from each module's interface
([file interfaces](#file-interfaces)), whose bodies are stubs: the sweeps
run over the interfaces (`analysis.BuildStdDeclarations`, which keeps each
module's declarations as a project build keeps a project file's, defers
the unused checks and infers the unannotated `once`s), and no body is
checked. `std.Load` still analyzes the stdlib from source; the stdlib
reference, `nomi test` of an edited std file and a few tests use it.
std/compiler's `check`, `hover` and `run` check their source against
`std.Shared`, as a program is checked.

What std's bodies tell a program's check is the stdlib's facts
(`analysis.StdFacts`, `std_facts.go`), worked out from `std.Load` and
installed over the modules built from interfaces: each module's
diagnostics, the conformance demands its bodies and attached tests add to
its declarations' (its `ImplManifest`, which `StdBase` and the whole-file
import check fold), and each boot-only std function's first boot-only
call, which the boot-only check reads instead of the function's body
(`stdBootFns`). std's functions call only std's, so which are boot-only is
the same in every program. Positions in the facts name a module, not a
path. A stub body keeps the `break` and `continue` its function's own scope
holds, which is all a program's check reads of another file's body.

A module's bodies are grafted back from its source (`analysis.GraftBodies`,
which puts each function body, annotated `once` value and attached test
back into the interface's nodes, so every pointer into the declarations
holds) and walked and checked in place, with its test declarations, when
something first reads them (`StdLib.Bodies`, `CheckDeferredBodies`): the
module's lowering when no std bundle has it, an instance of one of its
generic functions, a program that lowers a listed module, the module's
cases (`StdLib.TestDecls`), and an editor's hover, which shows a
declaration's attached tests (`StdLib.AttachedTests`). The IR builder asks
through `stdBodies` before it reads a body. A check of hello world's
instances checks a handful of modules this way, a few milliseconds each.
Body checks write the module's own analysis; the module scope's symbols
stay as they are, and only the prelude's scope, every program file's
parent, is marked shared, since no build parents a scope under another
std module's.

The interfaces and facts are one artifact (`std/artifact.go`): a head
naming its format version, `std.Digest` and the fixtures setting, a CRC-32,
each module's interface and parse errors, and the facts. A binary built
with the `nomi_stdbundle` tag embeds it (`std/stdiface.bin`, written by
`internal/irbuild/stdbundlegen` beside the std bundle and supplied with
`go build -overlay`); any other binary keeps it in the artifact cache
under its build ID (`<cache>/std/<hash>.iface`). A process that finds
neither parses the stdlib, extracts the interfaces, runs `std.Load` for
the facts, builds from the interfaces as a later process does and writes
the artifact. An artifact that does not decode or build is made again.
`NOMI_DEBUG_STD_BUNDLE=1` reports why one was not used.

Building every module this way takes about 53 ms, against 345 ms to analyze
it from source. The first run of a new binary pays about 0.8 s.

### The stdlib a module at a time

`std.Shared` builds only the modules something has reached
(`std/shared.go`, `stdCore`): at first the prelude, std/assertions and
std/io with every module they import (`stdBaseModules`, 47 of 101
modules), then each module a build reaches with every module it imports,
in one build per program that reaches any. What a module imports is the
artifact's index, written when the interfaces are extracted
(`analysis.StdModuleImports`): its imports at any depth, its bodies' and
attached tests' included, since checking its bodies resolves them all.
std/assertions is in the base because every test, `?` in a test and
`assert` carries its `AssertionFailure`, which a file declaring tests does
not import, and the first build that holds it installs it for the process
(`analysis.TypeAssertionFailure`). std/io adds one module, and nearly every
program imports it; a build of more modules costs about 3 ms over those
already built, for the program-wide indexes it folds over them.

A build against the shared stdlib extends it before it builds anything
(`analysis.StdSource`, installed by `std.Shared`): discovery asks for each
stdlib module a walked file imports, at the top level or in a block, and
for std/json when a file declares an `export fn`, whose arguments and
result cross as JSON; a build with no loader asks for the entry's imports.
The build then runs over the largest stdlib built so far, so its base is
the one the next build forms. A module is built against those already
built (`analysis.BuildStdDeclarationsOver`), its imports of them resolving
to their analyses as a program's do, with their scopes as the program's
`StdlibModuleScopes`. Its facts are installed then (`InstallModules`), and
the `StdBase` of the larger stdlib is formed when a build first asks for it
(`StdSource.Base`), so a run forms one.

Each extension is a new `StdLib`, a snapshot sharing the smaller one's
analyses: a caller holding one keeps a consistent view while another
goroutine extends the stdlib, and asks `Latest` for the larger one after a
build (the front end does after its build, before telling a shared std
module from its own files). `Ensure` builds named modules; `All` (and
`AllBodies`) builds every module, which the language server, `nomi mcp`
and std/compiler use, since they read the whole stdlib.
`NOMI_STD_WHOLE=1` builds every module in one build at the first call, as
before; the tests below compare a run on the lazy stdlib with a run on it.

Two rules make a part of the stdlib behave as the whole does. A program can
reach no declaration of a module it does not import, directly or through
what it imports, and an impl lives in its interface's or its type's module,
one either imports, or a base module, so a program's impl index holds every
impl it could select (`TestShared_ImplsAreBuiltWithWhatTheyImplement`);
std is one Nomi module, so the orphan rule alone would allow an impl
anywhere. And a module built later is the module built at once
(`TestShared_LazyModulesAreEagerModules`), except that an annotation's
reference to another module's declaration holds a copy of that
declaration's symbol, typed when the module it names was built earlier,
which the eager build's order leaves untyped.

On an Apple M2 Max a built `nomi` runs hello world with no program record
in about 44 ms, against 119 ms when every module was built: the std setup
(the artifact read, the base modules' build, their facts and the base)
takes about 18 ms of it against 73 ms, the bundle's decode and the base
modules' restore 5 ms, and the program record 5 ms, now that a machine
decodes its CLDR data only when a frame first reads it
(`cldrdata.NewLazyStore`). The HTTP chapter's first block, which reaches
the listed modules and so builds 96 of the 101 modules in three builds,
runs in 232 ms against 245 ms,
and a check of a generated 50k-line project in 2.26 s against 2.38 s.
Under Node the tour's worker starts (`go.run`) in 194 ms against 480 ms and
warms (`nomiWarm`) in 92 ms against 128 ms; its first HTTP block takes
1.14 s against 0.89 s, since it builds the modules the warm-up no longer
did, and 1.46 s against 1.58 s from the worker's start.

What remains of the 18 ms is mostly the base build's fresh memory (about
11 MB, whose page faults the Go runtime takes in `madvise`) and the sweeps
over the 47 modules' declarations; decoding interfaces straight into
symbols and types would cut both.

## std and lib

std holds what Go's standard library can back with no third-party code:
`std/fs`, `std/sql`'s vocabulary, later `std/http` over `net/http`. `lib/`
holds first-party modules that need third-party Go code or are opinionated
layers over std: database drivers (`lib/sql/sqlite`, over
modernc.org/sqlite, and `lib/sql/postgres`, over pgx), `lib/sql/migrate`
(the migration runner sqlkit's `migrate` calls), `lib/sql/sqlkit`,
`lib/markdown`, over goldmark, `lib/image`, over golang.org/x/image,
`lib/password_hashers`, over golang.org/x/crypto, `lib/jobs/postgres` and `lib/storage/s3`, Nomi alone over std, later an
HTTP router or framework.

`lib/markdown` renders with goldmark's CommonMark and GFM extensions and
keeps its safe defaults (raw HTML omitted, dangerous URLs emptied), which is
what lets `markdown.render` return its output through `Html.trusted`. Its Go
side is one function bound with `go`; rendering to memory cannot fail.

`lib/image` decodes with Go's `image/jpeg` and `image/png` and x/image's
`webp`, choosing the decoder from the data's leading bytes, and resizes
with x/image's `draw.CatmullRom`. x/image has no Lanczos kernel; on a
2048 by 1536 photo, Catmull-Rom's 800 by 600 and 267 by 200 downscales
are within 46 and 43 dB PSNR of disintegration/imaging's Lanczos, which
is below what an eye sees, so the module needs no third-party Go beyond
x/image. `decode` reads the header first and refuses an image declaring
more than its `max_pixels` before decoding, since Go's decoders allocate
for the declared size. A Go `*Image` is never changed after it is made,
and a failed call returns a shared empty one beside its failure, so no nil
handle reaches Nomi. No call takes a context: the decoders and scalers
cannot stop partway, and the pixel limit bounds each call. A call does
not hold up other tasks; four decode, fit and encode passes on that photo
take about 0.55 s in one task and 0.2 s in four.

The postgres driver's Go side (`lib/sql/postgres/goside`) keeps the
transaction bookkeeping the SQLite driver does, per transaction instead of
per connection: a `Session` is one Nomi `Conn` (the pool or connection, or a
transaction or savepoint at a depth with a token), and a pool's transaction
holds a connection acquired for it until it ends. Every function that
reaches the server takes the task's context first (spec §19 *The task's
context*), and connections are configured to cancel a statement on the
server when it ends (`keepOnCancel`), since pgx's default breaks the socket
and the connection with it. `postgres.scratch()` runs one throwaway cluster
per process under `sh`, which stops it and removes its directory when its
standard input, a pipe the process holds, closes at exit; the driver's tests
and the conformance suite run on it.

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
generated-code check and the drivers' Go tests. It first runs
`go mod download` in each driver, which needs the network when the Go module
cache lacks modernc.org/sqlite or pgx. Its postgres runs skip without
`initdb` and `postgres` on PATH and on Windows. CI's lib job vets and tests
their Go side. By hand: `go test -C lib/sql/sqlite ./...` and
`go test -C lib/sql/postgres ./...`, and `go vet` the same way in each lib
module. A release carries the Go side of the lib modules with `go`
bindings prebuilt ([Prebuilt lib runners](#prebuilt-lib-runners)), so
`TestLibModules` also checks that `librunners/` is what
`go run ./internal/ffirun/libgen` generates and vets it. The tour links a lib module's
browser build through a workspace (see [Tour internals](#tour-internals)).

## Host functions

A stdlib `host fn` is implemented either by a VM intrinsic (tasks, channels,
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
`stdenum.go`, `opaque.go` or `stdstruct.go`. An interface such a body
takes as a value needs a `stdIfaceSpecs` row and an rt dispatch table; a
program holding one needs neither (see [Instantiation and
dispatch](#instantiation-and-dispatch)). Without them, every call whose
signature names the type is reported as not supported yet.

The shared std modules are lowered once, in import order, and a body that
calls into a module lowered after its own is refused. So a foundational
module that names a later one's type (std/int naming `Locale`) passes the
value to its host function rather than calling the later module's functions
on it (`"${locale}"` would call its `Display`). `typeOnlyImportModules`
keeps such an import from reordering the modules. In a shared module, a
function's parameter cannot be an `Iter<T>` unless it is generic, and a
generic function's defaults cannot be left out at a call.

A program std module whose Go half holds state across calls keeps it in a
handle: a `host type` in its internal `_host` module, an rt struct over
`any` (rt/formats.go, rt/processes.go), a row in `stdHostSpecs`, `rtHeld`
and `irStreamKinds`, and the module in `alsoImplements` when the Go
package is named for the public module (`internal/stdcsv` is
`std/csv_host`'s); the `_host` module goes in `internalStdModules`
(internal/analysis), so programs cannot import it and the reference
leaves it out. An rt-shaped host function returns one value, so a
crossing that has several to give returns a `List<Int>` or splits into
one call per value.

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

## CSV and XML

`std/csv` and `std/xml` are Nomi lowered with each program, over the
internal `std/csv_host` and `std/xml_host` (`internal/stdcsv`,
`internal/stdxml`, over Go's encoding/csv and encoding/xml). Reading goes
through a handle, `rt.CsvRecords` or `rt.XmlTokens`, over a Go reader
that reads the program's `Reader` (`internal/lineinput`), so a string, a
file and a stream are one path: `csv.decode(text)` is `csv.records` over
`Reader.from_text`. The input drops a leading UTF-8 byte order mark and
keeps the lines read since the oldest one an error may still name, so
Go's byte columns become code point columns, as std/json reports them.
Each crossing sets the input's frame before it reads, since a Reader is
read on the crossing's frame; a read that unwinds goes through Go's
parser as a panic, and the handle is not used again.

The CSV reader replaces Go's error texts with its own. Writing a record
is Go's `csv.Writer`, one record per crossing, except a record of one
empty field, which Go writes as an empty line a reader skips; it is
written `""`.

The XML reader reads Go's raw tokens and does the namespace work itself,
so a `Name` keeps the prefix the document wrote beside its namespace. It
checks what raw tokens leave out: an end tag closes the open element,
one root and no text outside it, a declared prefix, no attribute or
declaration twice, the XML declaration first. Go's strict decoder knows
only the five predefined entities and character references and reads no
DTD, and the reader refuses a DOCTYPE unless `allow_doctype`, so an XXE
document fails before its first entity. The writer is Nomi: it tracks
the bindings in scope, declares a namespace an element's or attribute's
name is in where it is not bound (an unprefixed attribute in a namespace
gets the first free `nsN`), and escapes text and attribute values in Go
(`escape_text`, `escape_attribute`, pure crossings), replacing a
character XML 1.0 cannot hold with U+FFFD.

## Files

`std/fs`, `std/path` and `std/fs_testing` are lowered with each program.
std/fs's crossings go through the internal module `std/fs_host` (`files`
class) to a `Backend` in `internal/stdio`. There are two backends: the
operating system's, one implementation for the whole disk and for an
`*os.Root` with every path written with `/` on every system, and an
in-memory one, which resolves symlinks a name at a time.

std/path cleans, joins and takes a path's last name and directory with
Go's `path` package, through the internal module `std/path_host`
(rt/path.go, pure, so a typed literal's handler may reach it). The rest
is Nomi over those: `extension` and `stem`, which differ from Go's `Ext`
(no leading dot, none for `.bashrc`), `is_absolute`, which also takes a
drive, `relative`, which Go's `path` lacks, and `matches`, whose `**` and
hidden-name rule Go's `Match` does not have.

Each `rt.Frame` carries the backend it reaches: the machine's, else the
build's (the disk natively, one in-memory file system on js/wasm). `nomi
test`, in process and in a Go-FFI wrapper, gives a test file the disk with
its working directory at its module root. `fs_testing.run` and `fs.within`
replace the backend for their body, and tasks inherit it as they inherit
`io.capture`.

`fs_testing.conformance` holds the in-memory backend to the disk; std's
tests run it against both. A test that touches the disk does it in a
directory from `fs.make_temp_dir`, with `defer fs.remove_all_quietly(dir)`,
or a Go `t.TempDir()`.

## HTTP

`std/url` and `std/http` are lowered with each program (`programStdModules`), so their structs stand in public signatures; their Go crossings are the internal modules `std/url_host` (rt/url.go, pure) and `std/http_host` (internal/stdhttp). A `Url` crosses as a list of its 14 parts. A request crosses as its parts too: method, URL text, headers as alternating names and values, the body as bytes or a `Reader`; `http_host.send` returns an `Exchange` handle (rt.HTTPExchange) whose accessors read the response, or an `http.Error` variant's name and fields. A `Client` holds a `Client` handle (rt.HTTPClient) over Go's `http.Client`, which owns the pool; `http.default_client()` is one Go client for the process. `send` carries the `network` class, which wasip1 lacks. On js/wasm the transport keeps Go's `fetch` path by setting no dial function (`transport_js.go`).

`http_testing.run` is three crossings the VM implements (internal/vm/network.go): `run_on` installs the `answer` function on the frame (rt.EnterNetwork), `answer_on` calls it for a request and records the request, and `recorded_on` hands back the requests. The builder lowers them as it lowers `run_on` for files (`answerOn`, `recordedOn` in ircapture.go).

A server is Go's `http.Server` (internal/stdhttp/server.go). Go calls its handler on a goroutine per request; that handler hands the request to the program over a channel (`http_host.next`) and waits until the program has responded (`http_host.finish`), so no Nomi code runs on Go's goroutines. `http.serve` loops on `next` in the calling task and spawns each request on the supervisor it was given. The request task runs its handler through `http_host.request_on`, a VM crossing (`networkRequestHost`, lowered by `requestOn` in ircapture.go): the handler gets the request's context, the active context with the server's `request_timeout` as a deadline, on a frame whose app `Context` field reads it and which stops when the client goes away (`rt.EnterRequest`, which ends the frame's Go context when the request's does). Every write to the `ResponseWriter` holds the request's lock, and once Go's handler has returned a write fails rather than touch it. A request whose handler wrote nothing gets a 500 from the deferred `finish`; a request whose task a supervisor restarts finds the response already sent and returns. `Body.Produce` writes through an rt `Writer` whose sink flushes to the client on each write it passes on. `http_host.listen` carries the `listen` class, which js/wasm and wasip1 lack.

`Router` builds Go's `ServeMux` over its patterns (`http_host.new_mux`), each with a handler that only records which pattern matched and its wildcards' values; `http_host.route` runs the mux on a recorder and returns the match, or what the mux wrote itself (a redirect, a 405, a 404). So pattern syntax, precedence, conflicts and path cleaning are Go's own. `Route<P>` is a checked literal whose `check` adds two rules in Nomi before Go's check (`http_host.check_pattern`): a method is one of `Method`'s named variants, and a brace stands only in a whole-segment wildcard; its `check_type` holds the wildcards to `P`'s fields. `Route.parse` and `Route.with_method` (which skips the method rule) apply the same rules at run time, the fields through std/parse's `ParseFields.field_names`. The checker types a route literal with no expected type from its wildcards (analysis/checked_literal_args.go) and holds `P`'s fields to `Parse` and `Display` there. `Route.to` and `Route.to_with` make an `Entry`, a pattern and a `(Request) -> Maybe<Response>`; `to_with`'s reads the path values through `parse.read_fields`, and `None` is a 404. `ParseFields` at an anonymous struct has no impl: the checker admits it when each field implements `Parse` and `Display` (structural_parse_fields.go), and irbuild lowers its three functions at the record kind an instance binds (irparsefields.go), each field's `parse` by its kind; at `Unit` irbuild writes them too, since no receiver selects std's impl. `Response.redirect` and `Response.content` run Go's `Redirect` and `ServeContent` on a recorder the same way; `http.serve_file` reads through std/fs and hands the bytes to `ServeContent`. Cookies cross as lists of their fields, and a `CookieJar` is Go's cookiejar as a handle (rt.HTTPJar).

A listener registers a drain hook (`rt.OnDrain`) when it binds, so when the program starts draining its supervisors the server runs Go's `Server.Shutdown` before any grace period: it stops accepting, the requests it took finish, and `serve` returns, which ends the drain. With `Redirects.Decide` the Go client follows no redirect and std/http's `send_following` follows them on the calling task, since Go would run the callback on its own goroutine. A multipart form crosses as a flat `List<Bytes>` (`http_host.parse_multipart`, `encode_multipart`), parsed and written by Go's `mime/multipart` over bytes already read; `Body.Multipart`'s boundary is a digest of its parts, so its `Content-Type` and its bytes, computed separately, agree. The form readers read a body through `read_capped`, in Nomi, which copies at most one byte past the limit into `Writer.collect` and leaves the rest of the stream unread; the limit is the request's `body_limit` (set by `Request.limit_body`, which also wraps the stream in Go's `MaxBytesReader` semantics, and applied to every request by `Server.max_body` in `answer`), else 10 MB for a URL-encoded form and 32 MB for a multipart one. A read that the `MaxBytesReader` cuts off exactly at `body_limit` is reported as `Error.BodyTooLarge`. Every middleware (`timeout`, `cross_origin_protection`, `cors`, `log_requests`, `security_headers`, `request_id` and the rest) is a Nomi function that takes the handler first and returns a handler; only `compress` crosses to Go, for compress/gzip (internal/stdhttp/compress.go). It picks the body's form: a whole body goes through `http_host.gzip_bytes`, a `Body.Stream` through `gzip_reader`, which compresses on read and flushes the compressor after each read of the source so a slow stream is not held back, and a `Body.Produce` writes into `gzip_writer`, whose `rt.WriteSink.Flush` hook flushes the compressor and then the writer under it at each `Writer.flush`. `http_testing.decompressed` reads a response back through `http_host.gunzip`. `method_override` reads a URL-encoded `POST` body through `read_capped` and hands the handler a request whose body is those bytes, so the handler reads the form again. `cross_origin_protection` builds one `Router` per bypass pattern, since overlapping patterns are fine there and one router refuses them, and asks `CrossOrigin.check`, which runs Go's `CrossOriginProtection.Check` (`http_host.cross_origin_check`) over the trusted origins and the Nomi request's method, `Host` and headers. It does not hand Go the request the server received (`Request.incoming`): a middleware before it may have changed the headers, and the check must read what the handler holds. The other questions Go answers about a request's parts cross the same way, as plain strings: `Headers.write_to` (`Header.WriteSubset`), `Protocol.parse` (`ParseHTTPVersion`), `Request.basic_auth` and `Request.add_cookie`.

`std/tls` and `std/net` are lowered with each program too, and their crossings sit in `std/http_host` beside HTTP's, implemented in internal/stdhttp (tls.go, netconn.go), so the HTTP, TLS and TCP Go sides share one adapter package. A `ClientConfig` or `ServerConfig` is a Nomi value; each use builds Go's `*tls.Config` from its parts (`http_host.tls_config`, then `tls_with_certificate` per certificate) and holds it as a `TlsConfig` handle for that use. A `Certificate` is a handle over Go's `tls.Certificate`, its key never read back. A `Connection` is a handle (rt.NetConn) over a `net.Conn`, TCP or `*tls.Conn`, with one rt `Reader` and one unbuffered `Writer` made when it opens; a `context.AfterFunc` on the context of the task that dialed sets a past deadline when it ends, so a cancelled task's reads and writes stop. A listener (rt.NetListener) accepts on a goroutine into a channel, as the HTTP server hands requests over, so `Listener.accept` can stop for the waiting task's cancellation or an accept deadline; it registers a drain hook that closes it. `net.Error` sorts Go's errors with `errors.Is` against the system's error numbers (not matched on Windows). `http.serve_here` serves requests as tasks of the enclosing `concurrent` block, which `http_testing.with_server` uses with a certificate `http_host.test_certificate` makes; `http.reverse_proxy` is Nomi over the client's send. `dial_with`, `tls_dial` and `lookup` carry the `connect` class, which js/wasm and wasip1 lack: a browser offers a program only `fetch`, so there each returns the no-sockets error (`noSocketsMessage`).

Go calls a program's hooks on goroutines of its own, where no Nomi code may run. A broker (internal/stdhttp/broker.go) carries each call to the Nomi task waiting in the crossing whose work set it off, which runs it on its own frame: a server's broker is served by `http_host.next`, a client's by `send`, which runs Go's `Do` on a goroutine and serves the broker until it returns, and a TLS configuration's by whichever of these uses it (the listener or client adopts it) or by the handshake itself (`tls_dial`, `tls_client`, `tls_server`, and the accept of a TLS listener, which runs the handshake there instead of on the first read). A hook that waits for a result (`certificate_for`, `verify`, `on_proxy_connect`) blocks Go's goroutine until the task has run it; one that only reports (connection states, the error log, `on_shutdown`, HTTP/2's error counter, the key log) is queued, and `next` runs what is left before it reports that the server has shut down. A hook that traps traps the waiting task. A TLS state crosses as lists of parts (`stateParts`), the same for a connection, a request, a response and a trace event. `Response.upgrade` takes the connection with Go's `ResponseController.Hijack`, writes the status line and headers itself, and hands the handler a `Connection` that reads what Go's server had already buffered first. `Request.read` and `Response.read` run Go's `ReadRequest` and `ReadResponse` under a `bufio.Reader` over a reader that returns one byte per read, so no byte of the next message is taken. The reverse proxy sends once with the client's handle, following no redirect, and returns a `Body.Produce` that copies the upstream body, flushing per `flush_interval`. `http_host.Handle` (rt.HTTPHandle) holds the Go values a std struct wraps that have no handle type of their own: a ClientHello, a tracer, a read message, a multipart reader and its parts.

`std/websocket` is Nomi over eleven crossings in `std/http_host` (internal/stdhttp/websocket.go), and the protocol is internal/websocket, which uses only Go's standard library: Go has no WebSocket package. Framing, masking, the UTF-8 check, ping replies and the limit are byte work per frame that is cheap in Go and slow in the VM, and the serialized writes need a Go mutex, so the protocol is in Go rather than in Nomi over `Connection`'s streams. `websocket.accept` checks the handshake in Nomi and returns `Response.upgrade`, so middleware sees an ordinary `101` response and the connection runs in `respond`, on the request's task, after the middleware has returned; `compress` already returns a `1xx` response untouched. `ws_open` wraps the hijacked `net.Conn` (reads go through Go's server buffer first) and clears the deadlines Go's server left on it. `connect` dials with std/net or std/tls and `ws_connect` runs the client's handshake in Go on the raw `net.Conn`, reading the response through its own `bufio.Reader`, which the connection keeps, so no frame behind the `101` is lost and the `Connection`'s `Reader` is never read. A `Conn` (a `Handle`) has one reader at a time (`rmu`) and one write lock (`wmu`) that every frame takes, a pong and a ping-interval ping included, so a broadcast from an actor and the handler's own sends interleave whole frames. A blocking call stops when the calling task's context ends, by a past deadline set from `context.AfterFunc`. `receive` returns a message as `Bytes` with a first byte of 1 for text or 2 for binary, so one crossing returns either. A close frame is echoed and the connection closed at once; `close` sends one and reads until the peer's or five seconds, or waits for the task already reading to see it. A protocol error sends the close code RFC 6455 names (1002, 1007, 1009) and closes. Each connection registers a drain hook that starts closing with 1001. `http_testing.open_websocket` runs the handler's `on_connection` on a task over `ws_pipe`, an in-memory pair that buffers what is written, since `net.Pipe`'s unbuffered ends deadlock when both send a close frame at once. internal/websocket's tests feed hand-built frames to a server (Autobahn's sections 1 to 7 and 9, written as a table), and `TestWebSocket_EndToEnd` (internal/stdhttp) runs wss through the middleware stack on 127.0.0.1.

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

## Background jobs

`std/jobs` and `std/jobs_testing` are written in Nomi alone and lowered
with each program. A `Job<A>` erases to a `Handler` through `impl Handler
for Job<A> where A: FromJson`, so a worker holds a `List<Handler>` of jobs
at several argument types and keys it by kind in a `Map`; no `Handler` or
`Queue` function returns `self` or takes two `self` operands, so both are
dispatched on interface values. `jobs.enqueue<A>` is a free function and
writes through `Queue.insert` on the caller's `Conn`.

`run_one` runs the handler in a task and a second task that waits on
`Task.outcome` and sends it on a channel, so the worker's `select` over that
channel and `Arm.after(timeout)` sees a trap at once and a timeout before
the handler ends; on a timeout it calls `Task.cancel`, which reaches a Go
call the handler is in through the FFI context rule. A library cannot set
`App.context`, so the timeout is the worker's.

`Worker.new` creates two supervisors, so it runs while boot builds the app
value: `loops` (`.Transient`, a grace period two seconds longer than the
jobs') holds the claim loop and the queue's watcher, and `runs` holds the
jobs, with `max_running`. Handlers come in `Worker.start`, in `main`,
because boot runs what it references and a handler reads app fields. The
claim loop takes a slot (a buffered channel holding `max_running` units),
claims one job, spawns it under `runs`, and charges the rate's token bucket
(a theoretical arrival time, GCRA); an empty claim returns the slot, runs
`rescue` and `prune`, and waits on the wake channel or the poll. The wake
channel is closed on drain, so the loop then flushes `runs` for the grace
period, by which time `runs` has cancelled what is left, and sends
`release` for its worker id. A running job's lease is renewed every third
of it by a task beside the handler.

A unique key is resolved in Nomi before it reaches a queue:
`Unique.key_for` writes the kind, the key and, with a period, the
period's start as one JSON array, so no two lists of parts write one key,
and `NewJob` carries that string and the states it is held in. A backend
compares strings only. Priority orders claims before due time.

Schedules run in the worker's `loops` supervisor, beside the claim loop,
when `Worker.start` is given any: the loop calls `enqueue_due` for each
schedule, then waits on a stop channel closed on drain or until the
earliest next tick. `enqueue_due` reads the schedule's last recorded tick
(`Queue.last_tick`), finds the latest tick at or before now, and hands its
job to `Queue.record_tick`, which records the tick and inserts the job in
one transaction. The job's unique key is the schedule's name and the tick,
held across every state, so a second process that read the record before
the first wrote it stores a duplicate that the key turns away; the record
only spares the insert. With no record a scheduler records now and
enqueues nothing, which tells a new schedule from one that was down. An
interval's ticks are whole multiples since the epoch, so processes agree
without talking. A cron tick is found on Int wall times (month, day, hour,
minute, each step jumping to the next start that could match, with Vixie
cron's either-day rule) and then resolved through std::calendar: a fold
takes `Disambiguation.Earlier` and the search goes on past a time not after
the start, so the second occurrence never ticks; a gap resolves to the
transition, found by bisecting seconds between the `Earlier` and `Later`
readings for the first one whose offset changed. The latest tick at or
before now comes from a window before now that doubles until a tick falls
in it, then a walk forward. `Cron` is a `CheckedLiteral` that refuses any
slot, so its `build` cannot fail; parsing is the same `Cron.parse` at check
and run time.

`lib/jobs/postgres` is Nomi over `lib/sql/postgres`. Its statements match
renew, settle and release on id, attempt and worker, so a settle after a
rescue changes nothing; the table name option rewrites `nomi_jobs` in each
statement's text. The watcher relays `Listener.forward` into a `select`
with a stop channel closed on drain, so it ends when shutdown starts rather
than after the grace period. A unique key is a partial unique index over
the rows that hold one (`unique_states = 'kept' OR state IN ('available',
'running')`), and an insert is `ON CONFLICT ... DO NOTHING` followed, when
it stored nothing, by a select of the holder; the insert waits for an open
transaction that holds the key, so the select sees its outcome. Ticks are
rows of `<table>_schedules`, upserted with `GREATEST`. `install` also runs
`schema_upgrade.sql`'s `ADD COLUMN IF NOT EXISTS` statements, so it brings
an older table up to date. `std/jobs_testing.conformance` is the suite a
backend runs; `Inline` passes it without the rollback rule, and
`lib/jobs/postgres` passes all of it on `postgres.scratch()`.

## Mail

`std/mail` and `std/mail_testing` are written in Nomi and lowered with
each program. Two encodings are Go's, through the internal module
`std/mail_host` (rt/mail.go, pure): quoted-printable bodies are written by
`mime/quotedprintable`, and the `Date` header is `time`'s `RFC1123Z` in
UTC. There is no Go SMTP code: the SMTP client speaks the protocol over std/net's `Connection`, upgrades with
`tls.client` for STARTTLS or dials with `tls.dial` for TLS from the first
byte, and reads replies with `Reader.read_line`. So sending mail inherits
std/net's `connect` class (`nomi build` checks the target with no row of
its own), its context rule (a cancelled task's reads and writes stop), and
the network runner, since such a program reaches net crossings. Go's
net/smtp was not used: it is frozen, takes no context and has no implicit
TLS. `SmtpConfig.timeout` is set as the connection's deadline before each
command and each reply. One send is one connection.

`mail.render` is the one encoder; `Smtp`, `Memory` and any other backend
call it, and it checks the message before anything connects. Its output
is 7-bit: RFC 2047 encoded words (base64, cut between graphemes) for a
subject, display name or header value that is not printable ASCII or holds
`=?`, quoted-printable bodies, base64 attachments, CRLF line ends, header
lines folded before spaces at 78 characters. Boundaries are fixed strings
starting `=_`, which neither quoted-printable nor base64 can write, so the
output is deterministic for a given Message-ID and date. Encoded words
stay in Nomi because they are cut between graphemes, where Go's
`mime.BEncoding` cuts between runes.

`mail_testing.with_smtp_server` serves one connection at a time on a task
of its `concurrent` block, with `http_host.test_certificate` for STARTTLS.
`conformance` runs against `Memory` and against `mail.smtp` on that server
in std's `//!` cases. `vmhost/mail_smtp_test.go` runs a Nomi program
against a fake server written in Go (STARTTLS with AUTH PLAIN and LOGIN,
TLS from the first byte, no TLS) and reads what arrives with Go's
net/mail, mime and mime/multipart, so the headers, encoded words and
multipart structure are checked by parsers that share nothing with them.

## Sessions

`std/sessions` is written in Nomi and lowered with each program; its two
crossings sit in `std/http_host`: `seal` and `open_sealed`, AES-256-GCM
over Go's crypto/aes and crypto/cipher in rt (rt/seal.go), with a random
12-byte nonce before the ciphertext. A cookie's value is that, in unpadded
base64url, over a JSON object: `c` and `s`, the Unix seconds the session
began and was last written, `f`, the flash messages for the next request,
and `v`, the app's value through its `ToJson`, absent for none. The
additional data is `nomi session 1 <cookie name>`, so a value sealed for
one cookie does not open as another and a change of format is a change of
label. `SessionKey.derive` is the secret's HMAC-SHA256 under a fixed
label. `open` tries `key`, then each of `previous_keys`; anything that
does not open, decode or parse, or is past `max_age` or `idle_timeout`,
reads as no session. `with_session` writes nothing when the response
already sets the cookie (`Response.cookies`); otherwise it deletes a
cookie that did not open, and rewrites one whose flash was read or whose
last-seen time is a minute old under an idle timeout. `write` replaces
any `Set-Cookie` of the cookie's name, so the last write wins. There is
no server-side store: a later one would keep an id in the same sealed
cookie and the value in the store, behind the same `Session` and `write`.

## Passwords, signatures and JWTs

`std/passwords`, `std/signatures` and `std/jwt` are written in Nomi and
lowered with each program. `passwords`' one crossing is
`hash.pbkdf2_sha256` (Go's crypto/pbkdf2 in rt/digest.go), with its salt
from `random.secure_bytes`. A stored hash is a PHC string (`Phc`), and
`verify` picks the first `PasswordHasher` in its list whose `reads`
accepts the string's algorithm id, so a module adds an algorithm by
implementing the interface: `lib/password_hashers` adds Argon2id and
bcrypt over golang.org/x/crypto, writing Argon2id's PHC string with
std's `Phc` and passing bcrypt's own `$2a$` encoding through. Each
hasher compares with `hash.equal_in_constant_time`.

`signatures`' crossings sit in the internal `std/signatures_host`, whose Go
side is internal/stdhttp/signatures.go. A key crosses as DER, a private
key as PKCS #8 and a public key as PKIX, which the opaque `PrivateKey`
and `PublicKey` hold beside their kind; each crossing parses it again,
which for RSA is the larger part of a verify's cost, as it is of a
sign's. The encodings are crypto/x509's, and crypto/x509 links net, so
the functions are in stdhttp, the package the adapter generator moves to
the network runner (`alsoImplements` in internal/hostgen/stdtable): a
program that never touches a key keeps the runner without crypto/x509
(`cmd/nomi-vm`'s deps test). ECDSA's `EcdsaSha256` signature is r and s
as 32 bytes each, converted from Go's ASN.1 form; `EcdsaSha256Der` is
Go's form as it is. `jwt` builds on both: HS256 is `hash.hmac_sha256`,
the others `signatures.sign_with` and `verify_with`, and a key holds the
one algorithm it was made for, which a token's `alg` must name.

## Object storage

`std/storage` and `std/storage_testing` are written in Nomi alone and
lowered with each program. `Store` is an interface no function of which
returns `self` or takes two `self` operands, so it is dispatched on
interface values, and `storage.local` and `s3.store` each return a
`Store` over a private struct, so boot code picks a backend with an `if` whose branches are both `Store`. Every backend calls
`check_key` before anything else, and `check_content_type` in `put`; the
key grammar (no empty segment, no segment starting with `.`, no control
character and no `\`) is what keeps the local store inside its root, with
no path cleaning. Every other character is a key's, as S3 allows, so the
local store writes each segment's name on disk with `%`, the characters
Windows refuses in a name, a trailing `.` or space, and a device name's
first letter as `%XX` (`file_names`); escaping `%` keeps the mapping
one-to-one. `storage.join_key` escapes each segment for a public URL,
since `Url.join_path` reads its segments as already escaped and drops a
join whose `%` is not an escape. lib/storage/s3 signs and sends the path
with every byte outside `A-Z a-z 0-9 - _ . ~` escaped, and its fake server
in the conformance test refuses a request whose escaped path differs from
that encoding.

The local store goes through std/fs, so `fs_testing.run` sandboxes it. A
`put` writes the content type to `<root>/.nomi-storage/types/<key>` and
then the bytes to `<root>/<key>`, each with `WriteMode.ReplaceAtomically`,
whose temporary file starts with `.`, so no key names it. A read of a key
whose path is a directory, or runs through a file, is `NotFound`; a file
with no type file is typed by `http.detect_content_type`. `storage.handler`
responds through `Response.content`, which runs Go's `ServeContent` for
ranges and conditional requests, and then sets the stored type on a 200 or
a single-range 206.

`Store.list` pages by one rule, `storage.page`: the entries under the
prefix in key order, each key that holds the delimiter after the prefix
folded into its common prefix, deduplicated, then those after the
continuation, `limit` of them; the continuation is the last key or
prefix shown. Keys under one prefix are contiguous in that order, so a
continuation that is a prefix skips every key under it. `Memory` and the
local store list with it; the local store walks its root, leaves out
every path with a segment starting with `.` (the types directory, a
put's temporary files), and reads each name's `%XX` escapes back. S3's
`ListObjectsV2` pages on the server, whose continuation token is opaque.

`Memory` keeps a `Map<String, Object>` in a `Channel<State>` of capacity
one, as `jobs_testing.Inline` does. `storage_testing.conformance` runs each
rule on a fresh store from `open`; `Memory` passes it, and the corpus runs
the local store through it in a sandbox and in a temporary directory. There
is no rule that keys `a` and `a/b` coexist, which S3 allows and a directory
tree cannot.

`lib/storage/s3` is Nomi over std/http with no Go code. Its signer follows
AWS's SigV4 for S3: one URI encoding of the path (no double encoding and no
`.` normalization, unlike other AWS services), the query re-encoded and
sorted, every header the request carries signed with `host`, and the body's
SHA-256 or `UNSIGNED-PAYLOAD`. It builds a request's `Url` with the encoded
path in `raw_path` when it differs, so what Go sends is what was signed.
std/url's escapers are Go's, which differ from SigV4's (`$` kept, a space
as `+`), so the module has its own `uri_encode`. S3's error documents and
`ListObjectsV2` pages are read with std/xml; a listing's query is written
with the same `uri_encode` the signer applies. Each attempt is signed afresh,
and a call that ends `Unavailable` (a transport failure or a 429 or 5xx)
sleeps 100 ms, then 400 ms, with up to half again from the clock's
nanoseconds, before trying again. A library cannot read the context in
force, so a retry does not check the deadline itself: a passed deadline
unwinds the task. Its tests sign AWS's three published examples, and
respond to requests with std/http_testing, including a fake server over a
`Memory` that runs the conformance suite; `lib/storage/minio` runs it
against a real MinIO, and `TestLibModules` skips that project without
`minio` on PATH.

## Processes

`std/process` and `std/process_testing` are lowered with each program.
Their crossings go through the internal module `std/process_host`
(`processes` class) to `internal/stdprocess`, whose one `start` takes the
program, its arguments, environment and directory, and a mode for each
stream: null, the running program's own, captured in memory, or piped.
`process.output` captures stdout and stderr, `process.run` sends them to
the program's own output (through `io.capture` when one is in force), and
`process.spawn` pipes all three, as `os.Pipe` files the child holds
directly, so its readers outlive the wait. `Child.wait` closes the piped
input and waits once; `Child.kill`, which the caller defers, is
`process_host.kill`, which kills and reaps a child still running and does
nothing after a wait, so a function that returns, traps or is cancelled
leaves no process behind. A child is started under its frame's
context, so a cancelled task or a passed deadline kills it, and the wait
then unwinds as the cancellation does. A relative `dir` resolves against
the program's working directory, which `nomi test` puts at the module root
without changing the process's.

`process_testing.run` installs a recording fake on the frame
(`rt.EnterProcesses`, `process_host.run_on`, a VM intrinsic beside
`fs_host.run_on`), which tasks inherit as they inherit the file system.
`start` asks the frame first: under the fake it records the call and
responds from the first matching `Answer`, never the operating system.
`internal/stdprocess`'s tests run one program against the test binary as a
helper and against the fake, and require the same transcript.

A js/wasm or wasip1 build lacks the class (`vmhost.TargetLacks`); built
anyway, `start` returns `Unsupported`.

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
runs. Each phrase's function carries a table of its parameters with the
types the checker resolved, and a running program finds the tables in the
functions it carries. A built image holds only the phrases it reaches, and
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

A plain user `host fn` or `host type` is implemented by an embedder's host
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
the program on the VM inside the wrapper, which carries a crossing into Go
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
match is by the declared Go type, so a function that returns a concrete type
(`*gzip.Reader`) needs a one-line Go function in the project that returns
the interface.

The generator reads a Go type's exported fields only: an unexported
field's type is recorded as an unread placeholder (`fieldType` in
gotypes.go, and gotypesimport.go for a type-checked package), since no
value crosses through it. A bound handle's internals may then name types the
reader does not read, such as pgxpool's `*puddle.Pool[*connResource]`.

A leading `context.Context` parameter is left out of the match
(`withoutTaskContext`, validate.go) and of the adapter's arity, and the
adapter passes `fr.Context()` there (internal/hostgen's `isGoContext`, beside
the `*rt.Frame` parameter std's hosts take). That context is the frame's: the
ambient deadline `rt.EnterDeadline` derived at the last `with App.context`
rebind, ending when the task is cancelled. A `context.Context` at any other
position is refused, by validation at the binding and by the generator if one
reaches it. `tests/18-ffi-and-dynamic/context_ffi_app` holds a Go call to the
deadline.

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

### Prebuilt lib runners

A release runs, tests and builds a project whose Go bindings are all the
first-party `lib/` modules' with no Go (`internal/ffirun/libprebuilt.go`).
`go run ./internal/ffirun/libgen` generates the wrapper and the runner a
project of every lib/ module with Go bindings would build
(`ffirun.GenerateLibMains`, over `libModules`: image, markdown,
sql/postgres, sql/sqlite, sql/sqlkit) into `librunners/nomi-lib` and
`librunners/nomi-vm-lib`. `librunners/` is a nested module that requires
the compiler and those modules by directory, and its generated sources are
committed. They name each declaring file by its module's path and its path
in it (`github.com/nomi-language/nomi/lib/sql/sqlite/sqlite.nomi`), never
the generating machine's, and the wrapper's entry-scoped keys match a
target in the module cache by that suffix once each `@version` is dropped
(the template's `Relocated` blocks). `TestLibModules` fails while they are
stale, so changing a lib/ module's `go` bindings, or the wrapper template,
means rerunning libgen.

`scripts/release.sh` builds both from `librunners/`, nomi-lib with the std
bundle, for the five native platforms (nomi-vm-lib alone for `js/wasm`)
into a lib archive per platform, `nomi_<version>_<goos>_<goarch>_lib.tar.gz`,
and stamps `nomi` with `ffirun.LibVersion`, the pseudo-version of the
release's commit. Prepare serves a program from nomi-lib in place of a
generated wrapper when every binding's declaring file is in a module of
`libModules` whose module cache directory is at `LibVersion`
(`libBindingsMiss`), and skips validation, which ran when the sources were
generated. nomi-lib is looked up beside `nomi`, then in the release
download cache, then downloaded from the release's lib archive
(`findLibWrapper`, gated by `ffirun.AllowLibDownload`, which only the CLI
calls); `ProjectRunner` finds nomi-vm-lib the same way for a native or
`js/wasm` target (`Variant.Lib`). Each must carry `nomi`'s VCS stamp, as
nomi-vm must. Anything else takes the generated-wrapper path, and a
missing toolchain or compiler source there names the first binding that
could not be served (`needsGoError`): a binding of the project's own, a
lib/ module at another version (with `nomi update`), one replaced with a
directory, or a target or variant no lib runner ships for.

Decisions, with the numbers they rest on (darwin/arm64, stripped):

- A separate archive. nomi-vm-lib is 21.8 MB (8.0 MB gzipped) against
  nomi-vm's 13.4 MB (5.1 MB), and nomi-lib with the std bundle 43.9 MB
  (15.8 MB), so shipping them in the main archive would double every
  download for the users who need neither. A pure-Nomi program still gets
  the plain nomi-vm.
- No nomi_nonet lib runner. pgx links net and crypto/tls whatever the tag,
  so it saves 2.4 MB natively (19.5 MB); it would save 9 MB on `js/wasm`
  (28.7 to 19.6 MB), which a later change could ship.
- No wasip1 lib runner and no reactor: modernc.org/sqlite does not build
  for wasip1, so such a build falls back to a project runner built with Go.
  No `std/compiler` variant either, as for the plain runner.
- A source build keeps generating its own wrapper: `LibVersion` is empty
  for every build release.sh does not make, so nothing changes for a
  checkout or a `go install`ed `nomi`.
- A release `nomi` keeps lib/ modules at `LibVersion`: `nomi add` of a lib/
  module with no version takes it, and `nomi update` moves every lib/
  module to it (`modfetch.Options.LibVersion`); `@latest` still takes the
  latest commit.

`TestLibRelease_*` (cmd/nomi) builds `nomi` with both stamps, nomi-lib and
nomi-vm-lib once per test process, serves the lib/ modules from a file
proxy at two versions, and runs, tests, checks and builds a project of
sqlite, sqlkit, migrate, markdown and image with no `go` on PATH and an
empty toolchain cache, downloads the lib archive from a local release, and
checks the refusals of a project binding its own package and of one at the
other version.

## Modules

A project's dependencies are the modules its `go.mod` requires.
`internal/modcache` returns which it needs and where each is (a local
`replace`, or the Go module cache), reading only the disk. `internal/modfetch`
downloads missing modules through the module proxy protocol and the checksum
database with `golang.org/x/mod`, with no `go` command, and writes or edits
`go.mod` and `go.sum` for `nomi add`, `nomi fetch`, `nomi remove` and `nomi
update`. `nomi new` and `nomi init` write a project's first `go.mod`.

As in Go, only the project's `go.mod` is the main module's: a dependency's
`go.mod` contributes its requires and never its replaces, which name
directories of the dependency's own checkout (sqlkit's `../migrate`). A
tool of a dependency (`nomi tool sqlkit generate`) is a program rooted in
the dependency's directory, so `nomi tool` sets `NOMI_MAIN_MODULE` to the
project's root, and while it is set a graph rooted at a module the
project needs is the project's (`modcache.MainModule`, which `Walk`,
`Load`'s fetch and `ffirun.Prepare` all ask). The tool then reads its
sibling modules at the versions the project lists, which `nomi add` and
`nomi update` keep on one commit for the lib/ modules. It is an
environment variable so the FFI wrapper the tool runs in reads the same
graph; such a wrapper builds in the project's Go module and is cached
apart from the project's own (`prepareModule`).
`TestLibRelease_ToolOfAModuleFromTheCache` runs sqlkit's generator from
the module cache on the prebuilt lib wrapper and in a wrapper built with
Go.

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
(`nomi-vm-nonet`, `nomi-vm-reactor-nonet`) fails the network crossings
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
its own bindings by that relative name. An embedded file's source is
renamed the same way.

**Image layout and embedded files.** An image is its code, what
`ir.EncodeImage` writes and `ir.DecodeImage` reads, then its files region:
the contents of each file the code's `ConstEmbedded`s name, once per
SHA-256, back to back. The code says how long it is in its header
(`ir.ImageCodeLen`) and ends with the region's table, `ir.Image.Files`
(sum, size, offset). `EncodeImage` writes only the code, so `BuildImage`,
the build cache (`keptBuild`) and an FFI wrapper's image hold no file's
bytes. Whoever writes an output appends the region from the files on disk
(`vmhost.WriteFilesRegion`), streaming each and refusing one whose size or
sum changed; the sources come from the program's own lowering, whose
`ConstEmbedded`s name the absolute paths (`Program.EmbeddedSources`,
which a wrapper writes beside its image as `sources`). A native
executable's trailer, a `.nomic`'s header and a js/wasm module's
`nomi.image` section count the region in the image's length, and each is
written head first (`vmrunner.ImageFileHead`, `WasmImageSectionHead`) so
the region streams. A wasip1 module's image goes into its 4 MiB image area
whole.

A runner reads the code and leaves the region where it is
(`vmrunner/files.go`): in its executable or `.nomic` file, which it keeps
open and maps read-only (`mmap_unix.go`, `mmap_windows.go`; elsewhere a
`ReadAt` per read), so a read is a view of the mapping that the operating
system pages in and drops; in memory it already holds (a wasip1 module's
image area, a `.nomic` on stdin, a reactor's `nomi_load`), as a view or,
for bytes the caller still owns (`Run`, `Start`, `Boot`), a copy; or, in a
page, in the `nomiImage` Uint8Array, whose code alone is copied into Go
and whose files are copied out a file at a time. Starting reads the same
bytes however much a program embeds: `TestOpenImageAt_StartingReadsTheCodeAndAFileOnlyWhenRead`
counts them through the reader an image is opened over (25 KB embedding
64 MiB, where the whole image is 67 MB), and the run that reads the small
file reads its bytes and none of the big one's.

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
through nomi.js. The image's phrase tables are written without those
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
`nomi_load` with length 0. That boots the embedded program and returns its
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
module. A project of lib/ modules at the release's commit is appended to
nomi-vm-lib from the target's lib archive, found and checked the same way
([Prebuilt lib runners](#prebuilt-lib-runners)).

### TypeScript declarations

`internal/webtypes` maps Nomi types to the TypeScript of the JSON their
`ToJson` writes, for both nomi.d.ts (`nomi build --web`) and `nomi types`.
Both use one `Writer`, which differs only in `Ints`: nomi.d.ts writes an
Int as `number | bigint`, which nomi.js hands JavaScript, and `nomi types`
as `number`, which `JSON.parse` yields.

The writer reads the checked program, not the source: `AddProgram` indexes
each module's type declarations with the analysis type its scope holds,
and a field's type is the checker's, so a type named through an import,
a directory path or an alias resolves as the checker resolved it. A
declaration is keyed by (file, name), since an origin names a file only
within one program and every entry's origin is `<entry>`; each program
keeps its own origin-to-file map. `nomi types` checks each program root of
the named files as `nomi check` does (`programRoots`) and adds every
program to one writer.

Writing has two passes. The first visits the roots (the named files' public
types, or the exported functions' signatures) and records each declaration
they reach, in the order reached. `Seal` then names them: a Nomi name taken
by two declarations, or a reserved one (`Record`, and nomi.d.ts's own
types), is qualified by the fewest trailing path elements of each file that
tell them apart. The second pass writes the references and the
declarations under those names. A derived impl's JSON keys come from
`analysis.DerivedJSONKeys`, the rule the derive synthesis writes them by.
A std type maps by name through a table of what std's impls write; a type
whose `ToJson` is hand-written is declared `unknown`.

## REPL

`nomi` with no arguments keeps one live VM and links each input into it.
It is lexically scoped across inputs, as GHCi is (`cmd/nomi/repl_vm.go`
says how each kind of definition carries).

**Inputs as files.** Each input is one file of the session's program,
`repl_in<N>` (a key with no directory, so an `embed` pattern reads the
working directory). Its file imports by name, from the earlier input that
made it, each function, `once`, type, interface or embed in scope that the
input's text names, and imports whole each earlier input with an inherent
impl of a type it imports, since the type's members are found through
it. A session value it names is declared again in the file as a `once`
read from the session's store. Its own declarations get a generated `pub`
line above them, so a later input can import them. Versioning falls out of
the files: a redefinition is declared in its own input's file, and an
earlier file keeps importing what it imported, so nothing is renamed.

The file is checked against the interfaces of the earlier files
(`vmhost.Session.CheckFile` and `LoadFile`, `vmhost/session_files.go`):
one `analysis.ProgramInterfaces` lives for the session, made by
`NewStoredInterfaces` over `frontend.Checker.SessionSource`, and grows a
file at a time with `AddFile`; `SetEntry` makes each new file the entry
while it is checked, and `RemoveFile` takes out the file of an input that
failed. Each file's impl headers are resolved once, by its own check, and
kept while the file is in the session. `frontend.Checker.CheckSessionFile`
reports the file's diagnostics as a check of the program from source
reports the entry's; the reductions over every file's bodies are not run
again, since each earlier file passed them. A session's program
(`SourceProgram.Session`) differs from a project in what one input could
do to an earlier one's type when the inputs were one file: an inherent
impl may extend a type another of its files declares, and a `derive` may
name one, lowered from that file's declaration (`analysis.LowerDerivesWith`,
`ImportedTypeDecl`) with the derive itself as the impl's origin, so the
impl's header is the input's. Each new file is the entry while it is
checked, so its own boot's application type joins the ones earlier boots
returned (`Session.Commit` adds them to the program's `AppTypeNames`).

A `fn boot` runs with every input after the one that declares it, as it
did when each input was a program of every live declaration. The machine
keeps the boot it ran when a new entry declares none
(`vm.Machine.Extend`); a later input's file imports the boot's file whole,
its `main` is checked as running under that boot
(`analysis.CheckAppRootsUnder`, `Session.SetBoot`), and its lowering reads
application fields as an entry with a boot does (`SessionObjects.Boots`).

The file is lowered on its own (`irbuild.LowerSessionFile`) against the
modules the earlier files lowered, which the session keeps in memory
(`irbuild.SessionObjects`) and the machine already links: every earlier
file its check read is `fromObjects`, and the new file's names resolve to
the earlier modules' symbols by linkage name, as `LowerFile` resolves kept
objects. Two things `LowerFile` returns a miss for are taken as they come.
An earlier file that declares anything generic is read with its bodies
grafted onto its interface (`ProgramInterfaces.SetBodies`,
`analysis.GraftBodies`) and checked, and its gen is `linked` rather than
`fromObjects`, so it lowers the instances the new file asks for and no
earlier lowering made, into a module of its own. The registry's
program-wide sets (instances held erased, enums boxed, refused synthesized
impls) grow by addition: nothing before the new file is lowered again.
The new file's modules are linked into the live machine with
`vm.Machine.Extend`, and committed to the session's objects once the input
ran (`Session.Commit`).

A redefined impl is taken out of its earlier file's text
(`Session.ReplaceFile`, its lines blanked) before the input is checked, so
no check sees two, and put back if the input fails. Once the input ran,
each of the old impl's functions is redirected to the new one's
(`Session.Supersede`, `vm.Machine.Redirect`, matched by linkage name with
the file's key changed), so code an earlier input compiled calls the new
impl, as it did when every input re-lowered every earlier declaration.

Types are versioned as functions are. A type redeclared with a different
declaration is declared in the input's file, and the earlier type stays in
its own: every earlier file keeps importing it, so earlier values,
functions, onces, types and impls keep it, and a later input's file
imports the name from the new one. A value's or once's type is kept as
source with each type an input declared spelled through its file
(`repl_in1.Point`; `vmhost.Checked.LocalType`'s qualifier,
`analysis.TypeTextQualifying`), and a file that reads the value imports
that file whole, so the spelling names the old type whatever the name
means by then. An impl records the file of the version of its type it was
written for (`replDecl.typeFile`): it stays with that version, and a later
impl of the same interface replaces it only for the same version. When a
diagnostic names both versions, the checker's rule for two types of one
name qualifies each by its file (`expected repl_in1.Point, got
repl_in5.Point`), and the REPL prints that as `Point (input 1)`, the input
numbered as its file is.

Two compiler gaps showed once inputs were files, and are fixed for every
program: a call to a function-valued `once` another file declares and this
one imports by name was declined (`TestIROnceSibling_ACallThroughAnImportedFunctionValuedOnce`),
and the impl header of `impl Named for Box<T>`, a generic receiver with no
`<T>` clause, named its own file through `T`, so a check against
interfaces saw the impl only in files that imported it
(`TestAGenericReceiversImplIsSeenWithoutItsFile`).

A derive and a written impl of one interface for one type share the
REPL's key for a declaration, so a later one of either replaces an earlier
one. Every type has a Debug impl the front end synthesizes in its file, so
a later input's Debug impl, written or derived, is a second one in the
program. The lowering ranks it as the analyzer's coherence check does: a
synthesized universal Debug (`ast.FuncDef.AutoSynth`) yields to a derived
one as well as a written one (`writtenImplMembers`), and once the input
ran the synthesized impl's functions are redirected to the new impl's, as
a redefined impl's are. An impl in a third file, neither the type's nor
the caller's, is found for a std generic instance by the type's
declaration (`implForDecl`), as `Json.encode_as(r)` needs for a later
input's `derive ToJson for R`.

Every top-level declaration the parser makes is a declaration of the
input's file, never a statement of its `main` (`replDeclOf`;
`TestReplVM_EveryTopLevelDeclarationIsADeclaration` parses one of each
node type). A `phrase` or `noun` (`ast.KeywordDecl`) and an `embed`
share the key `value <name>` and are imported by name, as a type is; the
front end expands each in the input's file. The name joins the input's
function names, so it shadows an earlier function, once or value of that
name for later inputs, and a later function of its name shadows it. The
session has no project root, so `[i18n]` is its default: source locale
`en`, no catalogs. `lexer.IsComplete` reads an input whose last token is a
raw or triple-quoted string still open as one that continues.

A file imports only the names its text names, so an undefined name's
`did you mean` hint would offer only those. The REPL hands the session
every name in force (`Session.SetScopeNames`, `SourceProgram.ScopeNames`),
and the entry's check offers them beside its own scope's
(`FileDecls.ScopeNames`: a capitalized one as a type, through the
registry's `Names`, and any other as a value).

**Cost.** `TestReplVM_AnInputsCostDoesNotGrowWithTheSession` counts what
each input's checks build (top-level declarations of its file and the
files they read) and its lowering makes (functions), from
`Session.Cost`, and requires the hundredth input of a session to cost what
an input of the same shape early in it did. `NOMI_REPL_TIMING=500 go test
./cmd/nomi -run TestReplVM_WallTimePerInput -v` prints wall time per
input. On an Apple M2 Max, for inputs that each declare a function and a
value and call the first input's function (median of nine inputs): input
10 takes 2.4 ms, input 100 2.5 ms and input 500 2.9 ms. When each input
was a program of every live declaration's source, they took 2.4 ms, 9.1 ms
and 42.6 ms. The first input takes about 0.8 s either way, for the
stdlib.

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
  is another parameter: one written as its type alone, which the parser
  gives `ast.Param.TypeOnly` (name `_`, the head as its annotation) and the
  formatter writes back as the type alone.
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

## `if` without `else`

An `if` with no `else`, or an `else if` chain with no final `else`, whose
branches have a value of a type T other than Unit is `Maybe<T>`. The checker
(`analysis/elseless_if.go`) checks the chain's branches as an `if` with an
`else` does, each link returning its branches' own type, against the
expected Maybe's payload or, with no Maybe expected, against Unit as a
statement's are. The joined type T makes the head `Maybe<T>` unless it is
Unit or Infallible (every branch leaves). The head is recorded in
`FileAnalysis.ElselessIfs` with its Maybe type; the links are not. A
mismatch whose value is such an `if` gets a help naming it
(`elselessIfHint`), at a function's result, a `return`, an annotation, an
argument and a non-final statement.

The IR builder reads the record (`irbuild/irelseless.go`). `ifRegion`
lowers a recorded head's arms with `irFuncSig.elseless` set: each arm lowers
its value at the payload's kind and wraps it in `Some`, the missing `else`
writes `None`, and an `else if` link continues the region. A nested `if` or
`case` in an arm is lowered as a value of its own and wrapped whole, so a
nested `if` without `else` gives `Some(None)`, not a flattened `None`. Where
the region's result is not a Maybe (an `Html` slot's `ToHtml`), the `if` is
lowered as a Maybe value that then enters the result. A pipe's `if` stage
is lowered from a copy of its node, which `gen.splicedIfs` maps back to the
recorded one. A test body's statement `if` discards its value, so its arms
stay test statements.

`nomi fmt` strips a tail `return value` only where the value is the body's:
a branch of an `if` with no final `else` keeps its value returns, since its
value is wrapped.

## Releasing resources

The release rules (spec §9 *Releasing resources*) are a check over one body
at a time, run by the checker at the end of `checkFunc` and of a test's body
(`analysis/resources.go`, `checkReleases`), once every expression has its
type. A lambda met in the body is checked as a body of its own; a nested
function is checked by its own `checkFunc`.

A type is a Release type when the impl index holds an `impl Release` whose
receiver is that declaration: the index is keyed by the receiver's name, so
the impl found must carry the type's qualified receiver (std/http_host's
`Conn` is not std/sql's). What the errors and the quick fix write comes from
the body of the impl's `release` and `commit`, each one call
(`resourceCall`): `Writer.discard(w)` gives `Writer.discard`, and a bare
`close_quietly(conn)` in std/sql is written `sql.close_quietly` outside it.
So another file can read those bodies, a file's interface keeps the bodies
of its `impl Release` and `impl Commit` blocks (`stripBodies`), as it keeps
interface defaults; every other function's body is stripped.

`collectAcquisitions` walks the body for bindings whose name has a Release
type and whose value, with its `?`, `!` and parentheses taken off, is an
acquiring call or pipe (`acquired`): its type is a Release type or a
`Result` holding one, with no identifier or field access of a Release type
in it outside a lambda, and it is no constructor call. A `Reader` or
`Writer` outside a `Result` does not count (`isStreamType`, by std/io's
declarations). `case` arms and `if` patterns over such a call bind them
too. `checkDiscarded` reports an acquiring call bound to a discard name, or
the last statement of a block whose value nothing takes, as the
`discarded-resource` error. `markDropped` finds those blocks: a test's
body, a body whose result is `Unit`, a lambda the checker recorded in
`unitCallbacks` (`checkLambdaExpecting` with a `Unit` result), and the
value blocks of a statement whose value is dropped. A name with no `defer` among its block's later
statements whose call takes it, and that `handedOn` does not find returned,
yielded, stored in a literal or constructor, or rebound, is the
`unreleased-resource` error. A Commit type's deferred usual release starts a
`commitFlow` at the statement after the `defer`: it follows the statements
in order, branching at `if` and `case` and joining the states of the
branches that go on (committed only when each committed), and records each
normal return reached uncommitted (a `return` or a tail value that is not
`Err` or `None`, `break`, `continue`, and the end of the binding's block) as
the `uncommitted-resource` error. A call of the commit or of the release
with the name settles the path, as does one inside a lambda. The flow is
the unreachable-code rule's shape (`alwaysExits`) carried with a state; the
analyzer has no control-flow graph to reuse.

Each error carries a `parser.Fix`, which the language server offers as a
quick fix: "Release `w`" inserts the `defer` on the line after the binding
and, for a Commit type, the commit before each return the flow finds
uncommitted; "Release the `Reader`" binds a dropped acquisition to a name
the body does not write (`reader`, else `reader2`), then does the same;
"Commit `w`" inserts the commit before its return.

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
close. A test of a stdlib module's cases plants the decline on a call std
itself makes (`withPlantedDeclineOf` in `cmd/nomi`, `nanos_in_range` in
std/duration.nomi), in a child that keeps no std bundle, so std is lowered
with the plant in place.

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

### Objects link to the same program

`TestObjectsLinkToTheSameProgram` (`internal/expectation`) runs the corpus,
the tour's blocks and the failure fixtures with every lowering linked
through objects (`irbuild.LinkThroughObjects`, [IR
builder](#linkage-names)): each module is encoded on its own, the objects
are decoded with one `ir.Linker`, and each transcript must equal its record
byte for byte. Before encoding, `ir.CheckLinkage` fails the lowering when a
symbol two modules name has no linkage name or two symbols share one with
different code. By default it runs every eighth corpus program and tour
block and every failure fixture (about 20 seconds); `NOMI_OBJECTS_LINK=full`
runs all of them. `make check-full`'s `objects-link` step runs it in full,
and its `objects-irbuild` step runs `TestIRGolden_RecordsMatchTheVM` under
`NOMI_LINK_OBJECTS=1`: every program `irbuild.expect` records, which is
every program an irbuild test compares with the VM, linked through objects
and held to its record. The `TestLink_*` tests in
`internal/ir` hold the linker's rules, and `TestLinkedStd_*` in
`internal/irbuild` hold that two programs reaching listed std modules link
one kept lowering of them.

### Files lower on their own as in the program

`TestFilesLowerOnTheirOwnAsInTheProgram` (`internal/expectation`) runs the
same programs with every lowering kept and each of its project files
lowered again on its own ([lowering one file](#lowering-one-file),
`irbuild.RelowerEachFile`, or `NOMI_LINK_OBJECTS=perfile` for any test).
Each file's module must be the whole lowering's part by part, the program
it links must define every name the whole one does and name no function by
a symbol no module defines while one defines its name, and the run of the
program the first file other than the entry links (the entry's, for a
program of one file) must equal the record byte for byte. A miss is
allowed; `NOMI_PERFILE_REPORT=1` writes each on stderr. Over the whole
corpus, the tour and the failure fixtures (`NOMI_OBJECTS_LINK=full`, about
110 seconds), one file of 1,085 programs misses: it refuses one
`existential identity` construct where the whole lowering refused two.
The harness found that a std instance F asks for again was defined twice
under one symbol, which the VM reads as ambiguous and no body
(`dropLinkedDuplicates` now runs against the kept objects too), and that
a program's type was spelled by its node in linkage names, which no other
lowering can reproduce (`writeDeclKey`). `make check-full`'s
`objects-perfile` step runs it in full.

### The std bundle decodes to the lowering

Every process under `go test` runs on a decoded std bundle: one that lowers
the stdlib encodes the lowering and decodes it before using it
(`loadStdlibIndex`, `keptListedStd`), so every test and every expectation
suite runs on decoded std. `TestStdBundle_DecodesToTheLowering`
(`internal/irbuild`) has a fresh process, whose std sits at another path
(`NOMI_STD_PATH`), write a bundle; decoded in the test's process, its shared
modules must equal the test process's own lowering byte for byte as an ir
image, and its index must print the same by reflection (every view,
declaration, `once` and table, each IR function named by its module and
place), so a field added to `stdFunc` that lowering sets and the bundle
does not carry fails it. `TestStdBundle_ListedDecodesToTheLowering` holds
the listed section the same way, against the test process's lowering of
every listed module linked by name. `TestStdlibLowersTheSameWithoutLint`
runs over a decoded bundle too. `TestStdBundle_RejectsAStaleOrCorruptBundle`
requires a truncated or changed bundle, one made for another version,
stdlib, host registry or fixtures setting, and one whose modules or
declarations do not match the process's to be refused, and
`TestStdBundle_TheCacheRewritesABundleItCannotUse` that a corrupt cached one
is lowered past and written again. `internal/artifactcache`'s tests hold
that a test never writes the user's cache.

### The stdlib from its interfaces is `std.Load`'s

`TestShared_ModulesAreLoadsModules` (`std`) makes the interfaces artifact,
decodes it and builds the stdlib from it as a later process does. Before
any body is checked, every module must declare what `std.Load`'s declares
(`analysis.DumpDeclarationsForTest`: module scope symbols and types, impls,
type methods and demands, the facts' included), and the stdlib's errors
must be `Load`'s. Then each module's bodies are checked (`Bodies`) and its
whole analysis must dump as `Load`'s (`DumpFileForTest`), with one
difference allowed: an `import` in an attached test binds a copy of the
type it names, which `Load`, walking bodies before it types declarations,
leaves untyped and `Bodies` types. `TestStdArtifact_RefusesAStaleOrCorruptOne`
requires a truncated or changed artifact, or one of another version or
stdlib, to be refused. Every other test in the repository runs on the
stdlib from interfaces, and a test that runs a built `nomi` runs it on a
cached or fresh artifact, which is where a fact a program's check reads
from a body that no body check had yet put in place (once types, before
`BuildStdDeclarations` inferred them) showed.

### A stdlib built a module at a time behaves as the whole one

`TestShared_LazyModulesAreEagerModules` (`std`) builds the stdlib from the
artifact's base and adds every module alone, in three shuffled orders, and
holds each module to the one built with every module at once: its
declarations (`DumpDeclarationsForTest`) before any body is checked, its
whole analysis once `Bodies` has checked them, and the stdlib's
diagnostics. `TestShared_ImplsAreBuiltWithWhatTheyImplement` holds the impl
placement the laziness relies on ([the stdlib a module at a
time](#the-stdlib-a-module-at-a-time)).
`TestStdLazyRunsAsWholeSample` and `TestStdLazyRunsAsWholeRotating`
(`cmd/nomi`) run `nomi check` and `nomi run`, or `nomi test` for a file
that declares tests, on corpus and stdlib files in fresh processes, on the
lazy stdlib and under `NOMI_STD_WHOLE=1`, and require the same output and
exit: the sample holds files that reach the stdlib in ways an import does
not say (an `export fn`, a boot, a phrase catalog, embedded files, a std
module's own cases), the rotating set 12 files of `tests/` and `std/` a
day. `internal/expectation`'s suites run in one process on the lazy
stdlib, which grows as the corpus runs, and match their records.
`internal/irbuild`'s tests ask about the whole stdlib (its spec tables, its
lowering's index), so its `TestMain` builds every module first (`std.All`).

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

An incremental check must return what a full check returns.
`analysis.DocumentManager.Recheck` takes a checked program and an edit and
returns the program after the edit, which must be what a full check builds
from the edited texts (see [Language server analysis](#language-server-analysis)).
`analysis.DumpProgramForTest` renders a checked program as text, including
the diagnostics `nomi check` prints
(`TestCheckProgramPrintsWhatTheFrontEndPrints` holds that list to the front
end).

`TestIncrementalMatchesFull` (`internal/analysis`, about 13 seconds) applies
fixed edit sequences to small projects the test declares (layered imports,
phrase catalogs, dispatch, app types) and the multi-file corpus
directories. After each edit it compares the recheck's dump with a fresh
full check's, byte for byte. `TestIncrementalHarnessCatchesAStaleResult`
proves a stale recheck fails it.

`go test ./internal/analysis -run '^$' -fuzz '^FuzzIncrementalMatchesFull$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4`
mutates a project file and edits it the same way, and
`TestIncrementalMatchesFullSample` (about 2 seconds) runs a fixed sample of
those edits under plain `go test`. Its `phrases` project holds catalogs on
disk: an edit step whose key ends in `.toml` writes that input and rechecks
with an input edit, and `TestOpenDocumentCatalogEditsMatchFreshBuilds` holds
open documents to fresh builds through the same edits.

### Checking against interfaces

A file checked against interfaces must return what a check of the whole
program from source returns for it (see [file interfaces](#file-interfaces)).
`DocumentManager.CheckFromInterfacesForTest` checks every project file of a
checked program against the interfaces of the files it reads, written and
read back, and returns the program those checks make: each file's analysis,
nodes and own diagnostics from its check, the program's reductions from the
check from source (`CheckedProgram.factsFrom`). `TestCheckFromInterfacesMatchesFull`
(`internal/analysis`, about 40 seconds) compares its dump with the full
check's, byte for byte, over the incremental harness's projects and edit
sequences and every multi-file corpus directory; `FuzzCheckFromInterfacesMatchesFull`
mutates and edits files as `FuzzIncrementalMatchesFull` does, and
`TestCheckFromInterfacesMatchesFullSample` and `...Rotating` run a fixed and
a daily set under plain `go test`.

`NOMI_CHECK_FROM_INTERFACES=1` makes the front end check every project file
of each program again against interfaces after it checks the program, and
fail the program with the first file whose own diagnostics differ. The
expectation suites run under it in `scripts/check-full.sh`, one step per
suite (`interfaces-corpus`, `-stdlib`, `-tour`, `-failure`), so every
program they record holds the two to one result. Each still compares its
golden file byte for byte, so the script's package step skips the four
suites rather than run them a second time without the check.

The harness found that two files declaring a type and an interface of the
same names, each with an impl, were confused: the bound check kept one
interface origin per receiver and interface name, and a call's dispatch and
an `==`'s impl were dropped as ambiguous
(`TestSameNamedDeclarationsInTwoFilesStayApart`). It also found the
program's check typing `Box.inspect`, in a file that neither declares nor
imports a `Box`, through another file's impl by name; a bare type name the
file binds to nothing now names nothing
(`TestUndefinedTypeOwnerReadsNoOtherFile`), and resolving `List.size`, in a
file with its own `impl Sized for List<Int>`, with another file's impl of
another `Sized`: the impl index keyed by receiver and name keeps one, so a
file's own impl now wins, for the receiver its impl header names (under a
block import of std's `Duration`, a file's own `Duration.seconds` does not
resolve; `TestOwnerDecl_BlockImportRebindsAStdType`). An empty file's interface holds
no nodes rather than an empty list, so a check against interfaces passes
over it as discovery passes over the empty source. A difference the harness
knows and has not fixed is pinned in `knownInterfaceGaps` by a reproducer
(`TestKnownInterfaceGaps`), and the harness passes over a difference one
explains.

### Opening from a record

A program the language server opens from its record must return what the
program checked whole returns (see [language server
analysis](#language-server-analysis)). `TestOpenFromRecordMatchesAFullCheck`
(`internal/lsp`, about 15 seconds) writes the record of every multi-file
corpus project and a generated one, opens every file in a server that
reads the record and in one with the artifact cache off, and compares at
every identifier the responses to hover, definition, type definition,
document highlight, references, prepare-rename and rename, completion,
signature help, call hierarchy and implementation, and each document's
published diagnostics, symbols, semantic tokens, inlay hints, code lenses
and links. `TestOpenFromRecordMatchesAFullCheckDumps` (`internal/analysis`)
compares the installed analyses dump for dump, over each corpus project
also with a typo and then a syntax error in its entry's first body; most
of those, and the project that embeds files, must be recorded. And
`go test ./internal/analysis -run '^$' -fuzz '^FuzzOpenFromRecordMatchesFull$' -fuzztime 5m -parallel 4`
replaces one file's text with the input after the record is written, on
disk before the files are opened (the record must not be used) or in the
open document after, and compares again.
`TestServerOpensAProgramFromItsRecord` drives two servers over the protocol:
the first writes the record after its lowering, and the second opens from
it, building one file, takes its lowering's result from the record, and
takes its first edit in the program opened from the record.

`TestOpenFromRecordEditsMatchAFullCheck` (`internal/analysis`, about 28
seconds) writes the record of the texts
before each step of the incremental harness's edit sequences (orderProject,
layeredProject, rateProject, `genericEdits` over orderProject's files and a
body edit of every file of every multi-file corpus project), opens every
file from it and in a program checked whole, makes the step in the open
document and compares every document dump for dump. Each corpus file's
steps include a typo and a syntax error in its first body, which leave
something to report. The project with an application type
(`app_field_permission`) also moves a read by lines, which must be taken,
adds a read the entry's main reaches, adds a factory whose lambda reads
the application type, and adds a test with no boot that reads it. A step must be taken in the program opened from the
record unless it changes an impl, the program has an application type, or
the manager names a diagnostic only the program checked whole words (the
program's impl errors, a missing impl, a reduction's diagnostic in a file
the step does not check again); a body-only step must rebuild the
installed analysis in place; some step that leaves something to report
must be taken; some open document must be checked from its installed
check (`ChecksAfterEdits`); and some edit must take the missing-impl
supply from the entry's check, each such supply equal to the one worked
out from every file's interface (`CheckSuppliesForTest`). The fuzz target's open-document edits go through
the same path.
`TestOpenFromRecordEditsMatchAFullCheck` (`internal/lsp`, about 210
seconds) makes each of projectgen's edit kinds and its undoing in a server
that opened a generated project from its record, with its entry, hub and a
leaf open, and in one that checked it whole, and compares every request at
every identifier but those that check the document whole (implementation,
and references to an interface method), references and rename over the
closed files among them; most edits must be taken from the record, and
projectgen's typos (`TypoLeaf`, `TypoEntry`) and their undoing each must.
`TestOpenFromRecordMatchesAFullCheck` requires a closed file's references
to have been served from a record. `TestStoredLoweringMatchesTheText`
and `...Generated` (`internal/lsp`) lower the entry after edits from the
record's kept lowering and require the diagnostics a lowering of the text
reports, and a lowering file by file after each body edit.
`TestRelinkChecksTheImportersOfAOnceWhoseTypeChanged` (`cmd/nomi`) found
that the relink from file records, the server's lowering file by file and
the record's edits read an unannotated `once` whose value changed as an
edit that changes no interface, since the interface hash leaves `once`
values out: a fresh check reported the importer's type error and the
relinked `nomi check` printed `ok`. They now check and lower the importers
again (`analysis.InferredOncesChanged`).
`TestClosedFilesFromProgramsMatchTheirOwnBuilds` holds a closed file read
from a kept program to a build of the file alone.

The harness found that two of the language server's lookups by position
took the first of two names covering the cursor in map order: document
highlight's occurrence index, which now takes the innermost reference as
`FileAnalysis.TokenAt` does, and rename's `nameAt`, which now orders its
candidates.

### Kept programs run as fresh ones

A command that reads a kept program must print what it prints when it
checks and lowers the files afresh ([Kept programs](#kept-programs)).

`TestCachedRunsMatchTheRecords` (`internal/expectation`, about 17 seconds)
runs every sixteenth corpus program and every failure fixture twice with
the cache in a directory of its own: the first run keeps the program, the
second runs what was kept, and both transcripts must equal the record
byte for byte; every program the first kept, the second must read
(`progcache.Counts`). `NOMI_CACHED_RUNS=full` runs the whole corpus (about
200 seconds).

`FuzzCachedBuildMatchesFresh` (`cmd/nomi`) writes a project to disk and
edits one file as `FuzzIncrementalMatchesFull` does (the fuzzer's text,
then a deleted stretch, a typed character, a pasted block and the original
back), and after each edit runs `nomi check` and `nomi run`, or `nomi test`
for a test entry, twice with the cache and once with the programs it keeps
set aside, as a built `nomi`. All three must print the same, and the second
must read a kept program whenever the first read or kept one. Its projects
are a generated one of about 600 lines and every multi-file corpus
directory. `TestCachedBuildMatchesFreshSample` (about 15 seconds) runs
`projectgen`'s edit kinds, made and undone, and the fuzzer's edits of two
corpus entries; `TestCachedBuildMatchesFreshRotating` runs two more edit
sequences a day.

The commands relink a program from its file records after most edits, so
the same comparisons hold relinking to a fresh check and lowering.
`TestCachedBuildMatchesFreshSample` runs each of projectgen's edit kinds
and a file added that a leaf imports, each made and undone, and fails
unless a command relinked after each of them but the hub's signature edit
(it prints the cache's reasons when one did not); the fuzz target runs the
same edits of the generated project before the fuzzer's. A fresh command
sets the file records aside too.
`TestChangedSourcesNamesSourcesAddedAndRemoved` (`internal/progcache`)
holds which files may come and go.

`TestArtifactCacheRebuildsWhatItCannotRead` truncates, empties and changes
a kept record and puts another program's in its place, and requires the
next command to print what a fresh one does and write the record again;
`TestFileRecordsRebuildWhatTheyCannotRead` does the same to a program's
file record after a leaf's body changed, and requires the command not to
relink from it, and the next edit to relink from the record it wrote;
`TestArtifactCacheTwoProcessesRace` starts two commands over one program
and an empty cache at once. `internal/progcache`'s tests hold the record's
rules one by one: each kind of input change, a file rewritten with its own
text, a same-sized edit with its time put back, a file changed while the
build ran, the environment in the key, pruning, and racing writers (run
them with `-race`).

Under `go test` a process keeps no program unless the test points
`NOMI_ARTIFACT_CACHE` at a directory of its own
(`artifactcache.ProgramRoot`), so every other test loads afresh. A `nomi`
a test starts is not a test process and keeps programs under the directory
the test gave it, or the one `artifactcache` chose for the test binary;
`cmd/nomi`'s tests share one per test binary, so its CLI tests run on the
cache as a user's commands do.

### Stored facts reduce as the program does

`TestStoredReductionsMatchTheProgram` (`internal/analysis`, about 40
seconds) checks a program and requires each project file's facts read from
the program's check to equal, encoded, its facts read from its check
against interfaces, and `ReduceFacts` over the facts, written and read
back, to report what the program's passes report (`StoredFactsForTest`),
diagnostic for diagnostic, over the incremental harness's projects and edit
sequences, a project that draws each reduction's diagnostic (a boot-only
chain through two files and into std, a function value, an unused
whole-file import), and every multi-file corpus project with its entry
edited. It fails when some reduction reported nothing over the whole run.
`FuzzStoredReductionsMatchTheProgram` mutates and edits as
`FuzzIncrementalMatchesFull` does, and `...Sample` and `...Rotating` run a
fixed and a daily set under plain `go test`. `TestCachedBuildMatchesFreshSample`
requires a command after each of projectgen's body edits, the one that adds
a call into another file among them, to relink.

### The language server survives typing

`TestLSPSurvivesTyping` (`internal/lsp`) types a fixed sample of files into
the server and fails on any panic it recovers. `NOMI_LSP_TYPING=full` types
every tracked `.nomi` file and tour block instead (about 7 minutes split
four ways with `NOMI_LSP_TYPING_SHARD=k/4`), and
`go test ./internal/lsp -run '^$' -fuzz '^FuzzLSPSurvivesEdits$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4`
mutates them. A known panic is pinned in `knownTypingGaps` with a
reproducer.

A fuzz worker that died with exit status 2 and no output was a fatal
concurrent map access: checking a std module's bodies (`std.StdLib.Bodies`,
which the first lowering of a process, an instance of a generic std
function and hover's examples ask for) writes into the shared stdlib's
nodes, analyses and scopes, while the document manager's analyses read
them on other goroutines. The seeds replayed under `-race`
(`NOMI_LSP_TYPING=full go test -race ./internal/lsp -run FuzzLSPSurvivesEdits`)
reported 36 races, every one through `Bodies`. The server now checks every
std module's bodies when it starts (`StdLib.AllBodies`, about 0.3 s once
per process), so no later call writes. Setting `NOMI_ARTIFACT_CACHE` to a
directory of its own makes a run write and open program records
(`internal/analysis/stored_program.go`) as a server does.

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
- `TestCheckFromInterfacesMatchesFullRotating` checks four more edit
  sequences against interfaces;
- `TestCachedBuildMatchesFreshRotating` runs two more edit sequences through
  a built `nomi` with and without kept programs;
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
signature edit, or an impl added to a leaf for its own type, the edited
file and the files that import it. It also counts
the program-wide passes' visits to project files: a body-only edit's
reductions visit the edited file once, at every size. It counts the bodies
the boot-only check reads and the program graphs it builds
(`BootWorkForTest`): on every edit it reads only the bodies of the files the
edit checked, each once, and builds one graph. It counts work rather than
timing it, so it holds on any machine.

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

What opening a project costs the language server, and the requests a
reader makes next (hover and definition into a closed file, references
and rename of a function most files call, a closed file's diagnostics,
the first edit), checked whole and opened from its record, with the files
each built and the heap held, at about 50k lines (`NOMI_SCALE_LINES` picks
another size):

```
NOMI_SCALE_LSP=1 go test ./internal/lsp -run TestScaleOpen -count=1 -v -timeout 0
```

The first edit of each of projectgen's kinds, checked whole and opened
from its record: the edit's analysis, its propagation to the open
importers, the edited document's lowering and references to a hub
function, with the files each built (`NOMI_SCALE_EDITS` picks kinds by
name):

```
NOMI_SCALE_LSP=1 go test ./internal/lsp -run TestScaleEdits -count=1 -v -timeout 0
```

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
body's result. A dispatched call whose receiver's type has no retained impl
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

`tree-sitter-nomi/queries/injections.scm` hands a tagged literal's body to
the tag's language as one document with its `${...}` slots left out: each
pattern matches the literal's children as one repetition, so one match
captures every body chunk, and Zed, Helix and Neovim parse the content
nodes of one match together. Zed also groups by `@injection.host`. Neither
`injection.combined` (every literal in the file as one document) nor a
whole-node capture without `injection.include-children` (Zed injects the
whole node; Neovim cuts out only named children) works across the editors.
`TestTreeSitterInjectsASlottedLiteralAsOneDocument` (`internal/parser`)
holds the one-match shape.

An HTML node that spans a slot, such as `href="/posts/${id}/edit"`'s
value, keeps the slot's characters inside its range, and Neovim reads a
node's text for a predicate from the buffer over that range.
nvim-treesitter's html injections inject JavaScript into any attribute
value whose text matches `%${` (lit-html), so in Neovim every attribute
value with a slot became JavaScript. The Neovim ftplugin creates a Nomi
buffer's parser with an html injection query that leaves those patterns out
(`editors/nvim/lua/nomi/injections.lua`); HTML files keep theirs. `make
test-nvim` holds it, when the machine has an html parser.

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
`read-only:` checks; a listing or metadata does not. A data file the
program embeds counts as read (`Program.EmbeddedFiles`), since an edit to
it changes the program.

The tour's wasm reaches nothing outside its worker: std/fs goes to an
in-memory file system, and the stdlib has no networking module
(`cmd/nomi-wasm/browser_build_test.go`). Memory is capped at 1 GiB and call
depth at 1,000 under js/wasm. The worker starts the wasm with
`GODEBUG=gcstoptheworld=1`, and so do vmhost's node drivers. Under js/wasm
the Go runtime has one thread and no preemption, so the background mark
worker runs only when a run yields, which a run never does, and a
collection's mark progresses only through allocation assists. The runtime's
GC CPU limiter turns assists off when the collector's share of wall time
passes half, which a busy or throttled machine causes; the heap then grew
without bound (`baffling-birthdays.md`'s block reached the cap under
`make check-full` and at background priority, and stays at 164 MB with the
setting). A stop-the-world collection marks at its trigger. `tour/public/_headers` keeps a hash-pinned
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

### Stdlib reference examples

`cmd/nomi-docgen` writes each `//!` prompt on a public entry as a live
editor (`<pre data-nomi-stdlib-module><code class="language-nomi-test">`),
which the worker runs through `nomiRunStdlibTest` (`vmhost.StdlibReference`)
when it scrolls into view, unless the prompt reaches a capability class the
browser lacks (`promptClassifier`). Docgen loads each module's lowered
tests once (`vmhost.LoadEmbeddedStdlib`) and walks from the prompt's own
case (`Program.TestCapabilityUses`), the walk `nomi build --target
js/wasm` refuses a program by, for the classes in `vmhost.TargetLacks("js",
"wasm")` (`connect`, `listen`, `processes`). An HTTP request (`network`) is
not among them, since the browser build sends it through fetch.

The walk counts what a prompt could reach, which is wider than what it
does: a call through an interface reaches every impl (`mail_testing.rules`
reaches the SMTP mailer through `Mailer.send`), and a crossing a fake
handles on the frame still counts (`process.run`'s prompt runs under
`process_testing.run`). So docgen runs a flagged prompt once, natively, as
its editor runs, and keeps only the classes whose calls went on to the
operating system: `internal/osreach` counts them, noted where internal/stdprocess
starts a process and internal/stdhttp listens, dials or looks a name up. A
run that fails keeps every class the walk found. A prompt left with a class
is written as a plain `language-nomi` block carrying
`data-nomi-browser-lacks`, with a line under it saying why and that `nomi
test` runs it; the client only highlights it, as it does a `compile-only`
tour block. A new crossing of a class the browser lacks must call
`osreach.Note` where it reaches the operating system; one that does not
leaves a prompt that reaches it live, and
`TestTourWasmAnswersTheReferenceEditors` fails on that prompt.

reference.expect records each prompt the page does not run with `[not run
in the browser: <classes>]` and still runs it natively.
`TestTourWasmAnswersTheReferenceEditors` (vmhost) runs every prompt on the
generated pages through the staged wasm under node, requires each live
editor to run clean, and logs each prompt the page does not run that ran
clean anyway (`net.lookup_port`, which Go's built-in table of service
ports resolves without a resolver).

An editor's edit is its test: the module's declarations are the embedded
ones, which the process has checked and lowered. So `vmhost.StdlibReference`
parses the test alone, at its lines, and checks it in the module's scope of
the shared analysis (`frontend.Checker.CheckStdlibTest`,
`analysis.FileAnalysis.CheckAddedTest`): the walk and the checks run over
the test only, into a copy of the analysis's body part, and the test's scope
leaves the module scope again once the case has run. The case is lowered
against the module's lowering (`irbuild.GenerateStdlibReferenceIR`): a
shared module's case against its cached view, and a listed module's, or a
shared module's case that imports one, in a program whose modules all link
the kept lowering (`Program.linkAllStd`). An edit that is not one test
alone is checked and lowered with the module's whole source
(`LoadStdlibSource`), as `nomi test` checks an edited std file. Natively a
calendar editor takes about 5 ms, against 58 ms when each one checked
std/calendar and lowered its 202 prompts; under node 20-125 ms, against
0.2-3 s, and all 837 prompts 32-42 s on a loaded machine, against 700 s.

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
