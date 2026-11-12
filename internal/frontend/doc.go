// Package frontend is Nomi's front end: parse, inject the synthetic host
// declarations an FFI project mirrors from its Go bindings, expand keyword
// declarations, lower derives, synthesize derived and universal Debug
// impls, analyze the whole import graph, and check that every `host`
// declaration has something to answer it. It executes nothing.
//
// internal/irbuild's Analyze, vmhost and `nomi check` call it directly.
//
// The checker (internal/analysis) registers every type in one phase and
// walks bodies in the second. Names resolve whole-program, so imports may
// be cyclic and import order never matters. Most of what follows is
// implemented in internal/analysis and internal/parser, whose file comments
// hold the detail; this doc says how the parts meet.
//
// # Types from values
//
// An unannotated `once`, and a field or parameter written with a default
// and no type (`misses = 0`), take their value's type (analysis's
// default_type.go). Both are part of the file's interface, so the program's
// OnceTable infers every one in a phase before the first file's CheckTypes,
// each with its declaring file's checker: fields first, in rounds, then the
// `once`s. Until then the field's FieldDef, or the parameter's slot in its
// FuncType or interface MethodSig, holds pendingDefault, overwritten in
// place, so instances of a generic struct, which share its Fields, see the
// type. A parameter's symbol is defined where the body is walked, so its
// type is kept with the declarations (attachInferredParam) and written
// again on a body rebuild. The IR builder reads an inferred field's or
// parameter's type from its symbol (irbuild's fieldKind, paramKind); a std
// signature, built from syntax, reads it from the module's analysis
// (stdInferredParamKind). An interface keeps every field default, so a
// file checked against it infers the same type.
//
// # Type ascription
//
// `value as Type` is an ast.Ascription, parsed at `|>`'s precedence
// (analysis's ascription.go). The checker checks the value against the type
// as an annotated binding's value (checkNodeExpecting, then unifyInto). For
// a name, field read or number literal it first checks the value alone in
// a trial whose effects are undone; when that already gives the type, the
// node goes in FileAnalysis.RedundantAscriptions, which the language server
// reports as an unnecessary-code hint. A hover on `as` reads a hover-only
// symbol at the keyword. The IR builder lowers it as an annotated binding's
// value (annotatedValue); an ascription to an interface lowers as its
// value, which the reading position erases.
//
// # Default type arguments
//
// A type parameter's resolved default is kept on its TypeParam_ (Default),
// shared by every instance (analysis's default_type_args.go). A default
// that reaches, through type arguments, an instance of the unbuilt
// declaration leaving out a parameter whose default leads back would build
// a cycle of type arguments that every type walk follows forever, so
// rejectEndlessDefaults reports it and makes that default `Unit`. A cycle
// through several declarations closes at the build of the last, whichever
// file builds first. A nominal type's String leaves out trailing arguments
// equal to their defaults.
//
// The IR builder resolves annotations itself, so its typeOf first writes
// the left-out arguments in as the default's syntax
// (irbuild.withDefaultTypeArgs), finding the declaration by the reference
// the analyzer recorded at the name. A default means what it means where it
// was written: the declaring file's gen resolves it at module scope, with no
// type parameter or body block open (moduleTypeOf), and the using gen
// imports the kind (importKind) and answers the default's node with it
// (defaultArgKinds). A file using `Box` for `Box<T = Config>` need not
// import `Config`, and its own `Config` does not replace it.
//
// # Boot-only check
//
// CheckBootScope (spec §16 *Supervision*; analysis's boot_scope.go) is
// reported per file over a graph of the whole program's functions: every
// project function, and each std function the program reaches, whose calls
// are read once per process, since std's analysis never changes. A
// diagnostic names the shortest chain, the first call to a callee nearest
// `Supervisor.new` (bootDepths). No report reads a body: each file's bodies
// are read once per body part (FileBodies.generation) into a summary
// (bootSummaryOf), and calls name callees by *Symbol. The graph is built
// once per program and kept on the Program until a summary or the file list
// changes (programBootGraph); rebuilding it per file is quadratic. The
// check runs when a file's diagnostics are assembled, never from a cached
// check, so an edit that makes a function boot-only reaches its callers'
// files when the language server re-analyzes a changed file's importers.
//
// # Syntax the parser settles
//
//   - Shift. The lexer leaves `>>>` as three `>` tokens; the parser joins
//     them into the shift operator only after an operand, so
//     `List<Maybe<Box<Int>>>` closes one `>` at a time.
//   - Parentheses. One parse reads what parentheses hold: a top-level comma
//     makes a tuple (ast.TupleLit, ast.TuplePattern, or a tuple type, an
//     ast.FuncType with no Return), and one item alone is grouped
//     (parseParenExpr, parseSinglePatternInner, parseFuncTypeAnnotation). In
//     a type, a `->` after the `)` makes the list a function's parameters. A
//     lambda parameter's annotation is read as written and, when it is a
//     function type that what follows does not end a parameter after, again
//     with parenTypeNoArrow set, leaving the `->` to the lambda
//     (parseParamPattern); the tree-sitter grammar keeps both readings by a
//     declared conflict. A call's `(` must be on the line its callee ends
//     on, so a line starting with `(` after `f()!` starts a statement. `#(`
//     is hashTupleError, whose fix drops the `#`.
//   - Paths at a use (parser's paths.go). The lexer emits `::` as
//     COLONCOLON and the parser joins each run of words with `::` and no
//     spaces into one PATH token, so every position taking a short-name
//     qualifier takes a path. An import splits the PATH into ModulePath
//     segments; a `/` there is an error that spells the path with `::`. In
//     an expression a path is an *ast.FilePath, the Object of a
//     FieldAccess; in a type, pattern or literal head it is a QualifiedType
//     whose Module is its spelling (`db::schema`) and whose Path holds each
//     segment's position. Segments are ast.PathSegment values, not nodes, so
//     no walk reads one as a name. Internal module keys keep `/` (`std/io`,
//     cache and Origin keys); only source text and messages use `::`.
//
// # Directory imports
//
// Every parse ends with parser.NotePathUses, which sets each path-only
// import's PathUses: the paths its scope writes below the bound name
// (analysis's file_paths.go describes the binding). The parser cannot tell
// a file from a directory; resolution does. A loader returns a
// *DirectoryError for a path that names a directory and no file (this
// package's and stdcompiler's virtual files, a DocumentManager program's
// texts and a check against interfaces' file set say so for what they
// hold), and the builder asks the disk (DiskDirectory) for any other miss.
// A directory holding a `nomi.toml` is another module's. `import std`, and a
// module's or dependency's name alone with no file of that name, are their
// roots.
//
// defineDirectoryImport binds a SymbolDirectory whose DirectoryBinding holds
// the listing and, per path use, the file it reaches (resolveImportOrMiss,
// resolveStdlibImport). A nested import's entries shadow a file-level
// one's. An unresolvable use keeps its error in the binding, reported at
// each site.
//
// Discovery (importsOf) appends each import's reached paths, so it loads
// exactly the files a directory import reaches, and every artifact built
// from import paths (a check against interfaces' import closure, stored
// interfaces' imports, an incremental recheck's dependents) depends on
// those files and no other under the directory. PathUses is a field of the
// import, so a body edit that adds or drops a path changes the file's
// interface fingerprint (or a body's imports, for an import in a block) and
// is checked as an import change, never a body-only rebuild.
//
// walkPath records the reached file at a path's first and last segments and
// the directory between, for hover, go-to-definition and highlighting, and
// reports any other path in the words of spec §14 *Short names*, with a
// PathAtUseCode fix where an import binds the file or directory under
// another name; the checker drops its unknown-type error at a reported path
// (atPathError). bindShortName binds a path-only import's name: a second
// import of it, or a declaration holding it, is an error at the import,
// which markUnbound keeps out of the unused-import check
// (collidesWithImportedModule reports the order when the import came
// first). The language server completes a path one segment at a time from
// the binding's listing and the disk (pathSegmentCandidates; a path with no
// member never parses, so pathBeforeWord reads it from the text), and
// applies every PathAtUseCode fix in `source.fixAll`.
//
// # Keyword declarations and their inputs
//
// A top-level declaration that starts with a keyword and a name is read by
// one rule whatever the keyword. `phrase`, `noun` and `embed` are the only
// keywords Nomi knows; any other is an error. Nomi has no macros: a
// library that generates code ships a tool (`nomi tool`) that writes
// ordinary `.nomi` files.
//
// Every path that prepares a file expands these declarations
// (analysis.ExpandDeclarations, through [Checker.Loader] and the entry's
// preparation), so the checker, the language server and the builder see the
// same functions. A `phrase` becomes the function it declares, in place,
// with the declaration's parameter nodes and name position and a body in
// the synthesized band. A `noun` becomes the `once` of std/phrase's `Noun`
// it names. analysis's expansion.go describes inputs, positions, the span
// map and diagnostics in inputs.
//
// An expansion gets two hidden imports (`phrase$`, `phrase_format$`) no
// source can name. They are the only way into std's internals: std/phrase's
// private constructors and the internal modules std/phrase_format and
// std/money_format. A program that names one is told it is internal, docgen
// does not document them, and no diagnostic names a hidden alias (the
// registry's `phrase$.Noun` is never a "did you mean", SuggestDotted). This
// is why the IR builder lowers calls to a std module's private functions.
//
// A name a file does not bind that a public std module exports as a type,
// or that names a public std module, gets help that names its import
// (`import std::http.Method`; stdTypeImportHint, stdModuleImportHint,
// analysis's std_exports.go). A process builds only the std modules it
// reaches, so the answer is what each std module exports, which package std
// installs (analysis.SetStdExports): the std artifact's record of it, or,
// in a process with no artifact, the sources the first such diagnostic
// parses once. A
// name two or three modules export lists each import; four or more get none
// (`Wide` in TestStdImportHints). Internal modules and the prelude are
// never named. The language server's "Add import" fix (FindMissingImports)
// reads the same index (TestCodeAction_AddImport_IsTheImportTheHelpNames).
// A phrase parameter whose type names nothing gets that error alone:
// checkPhrase skips the text and wrapping checks, which would repeat it.
//
// An input's error is reported at the input: `nomi check` prints the
// input's text, the language server publishes on the input's URI.
// Agreement of nouns and phrases is checked over the whole program. A kept
// program records its inputs; a changed input is an edit to the files that
// read it with their text unchanged, so a catalog edit that keeps every
// phrase's signature rebuilds one file's bodies.
//
// # Embedded files
//
// An `embed` declaration (spec §18 *Embedded files*) follows the
// keyword-declaration rule, so the tree-sitter grammar, editor queries and
// formatter treat it as one, but the parser reads it into its own
// ast.EmbedDecl, with the body split into positioned ast.EmbedPatterns
// (parser's embed.go). analysis's embed.go describes the patterns, the
// expansion and missing bases, embed_paths.go the checked literal paths,
// and irbuild's irembed.go the lowering. How the stages meet:
//
//   - Where files are read. The front end resolves the patterns as it
//     expands keyword declarations. The site's EmbedFS is a tour block's
//     data files (analysis.MapEmbedFS, through Config.VirtualData, set by
//     vmhost.WithVirtualData and vmhost.LoadMarked), else the disk under the
//     project root (analysis.DiskEmbedFS; the entry's directory with no
//     nomi.toml), else the REPL's working directory (Config.EmbedRoot). The
//     resolver walks a pattern a name at a time with path.Match, so it sees
//     each match's directory entry (a symbolic link stays one).
//   - Missing bases. A pattern whose base does not exist records no error;
//     the front end reports nothing, and analysis.EmbedWarnings returns the
//     file's warnings (code `embed-missing`), which vmhost hands to
//     WithWarnings once a load has checked (vmhost.Program.Warnings; `nomi
//     run`, `check`, `test`, the FFI wrapper's modes and the REPL pass one;
//     `nomi check --format json` writes them with severity `warning`), and
//     the language server publishes them beside `dbg` and `todo`
//     (embedWarningDiagnostics). A build refuses them as errors where it
//     refuses a `todo` (vmhost's Program.missingEmbeds,
//     EmbedInfo.MissingErrors), for every target and the FFI wrapper's
//     builds. A program with a warning is not kept, and the base is a tree
//     of a kept record, so its appearing is a miss.
//   - No bytes held. ast.EmbeddedFiles holds each file's path, Source (the
//     path on disk, or the data file's key), size and SHA-256, computed by
//     streaming the file; no stage keeps an embedded file's bytes. The IR
//     builder lowers it to tuples of a path, an ir.ConstEmbedded handle and
//     a size; only a reached `once` is lowered, so pruning drops an unread
//     `embed`. std/embed is a program std module written in Nomi over a
//     `Map` of files and one of directories. A file's `Contents` is
//     `Stored{handle:, size:}` (or `Held(Bytes)` for trees its examples
//     build), so `paths`, `list`, `walk`, `metadata`, `exists` and `sub`
//     read no contents.
//   - Reading contents. An ir.ConstEmbedded carries an ir.EmbeddedRef
//     (source, size, sum), not bytes. When the `once` runs, the VM enters
//     the ref in the machine's rt.EmbeddedFiles table and the value is its
//     index, the handle. `Files.read` crosses into std/embed_host's `read`
//     (rt.EmbeddedRead), which asks the machine's rt.EmbeddedSource for the
//     bytes; nothing is cached. The source is the host's
//     (vm.Machine.WithEmbeddedSource): vm.DiskEmbedded by default (`nomi
//     run`, `nomi test`, the REPL), which reads the file each time and
//     refuses one whose size or sum differs from the ref's, so a changed
//     file is never served as the one compiled; vmhost's dataEmbedded for
//     an in-memory program (the tour), checked against the sum; vmrunner's
//     imageEmbedded for a built program, the image's files region found by
//     sum (ir.Image.Files).
//   - ETags. `Files.sum` crosses into std/embed_host's `sum`
//     (rt.EmbeddedSum), which returns the ref's sum and reads nothing.
//     std/http's `serve_embedded` sends it as the file's strong `ETag`
//     through `Response.content`'s `headers`, which `http_host.answer_content`
//     sets before Go's ServeContent runs, so Go handles `If-None-Match` and
//     `If-Range`. A precompressed sibling (`file.br`, `file.gz`) is an
//     ordinary file of the tree with its own sum and `ETag`.
//   - Literal paths against interfaces. A file's interface keeps an
//     expanded `once`'s value, so a check against interfaces reads the same
//     paths (TestEmbeddedPathsCheckTheSameAgainstInterfaces) and the missing
//     bases.
//   - The language server's sites set SkipEmbedContents: they find files
//     and sizes without reading them and record no input, so an edit to an
//     embedded file, or a new file under an embedded directory, rechecks
//     nothing until the declaring file is edited.
//
// # Test cases and their selection
//
// The case list `nomi test` runs is the front end's (tests.go):
// CollectTestCases walks the entry's declarations into TestCases, each
// named as the report prints it after `file :: ` (TestCase.FullName). A
// group's `subtests` block makes one case per row and test, named with the
// row's label (ast.GroupRowLabels, the copy irbuild's list shares), so a
// row's cases exist before anything runs. SelectTests narrows the list by
// TestOptions: `--line` (FilterTestCases), then `--run`, a regexp matched
// against FullName. The VM runs only the selected cases, and a case boots
// its own group, so a row none of whose cases is selected never boots. The
// rows of a `subtests` statement in a body are values the body computes,
// not cases, and run with their test. `nomi test` reads the same list from
// each file's source alone (SourceSelectsTestLine, SourceSelectsTests) to
// report a selection that names nothing and to skip a file `--run` selects
// nothing in; the FFI wrapper gets the pattern as an argument and selects
// with the same SelectTests.
package frontend
