// Package analysis resolves names and checks types for Nomi programs, and
// keeps the checked programs the language server reads.
//
// A file's check is a [FileAnalysis]: its symbols, definitions and
// references, every expression's type (ExprTypes) and its diagnostics. A
// whole program checked from its texts is a [CheckedProgram]
// ([DocumentManager.CheckProgram]). A file can also be checked against the
// interfaces of the files it reads ([ProgramInterfaces.Check]), and must
// answer what the check of the whole program answers for it. The
// [DocumentManager] keeps checked programs across edits for the language
// server (internal/lsp package doc, "Requests").
//
// # Programs the document manager keeps
//
// An open document is analyzed as part of a whole program that the
// [DocumentManager] keeps per project root and entry: the program of the
// first entry candidate that reaches it, else its own. A recheck rewrites
// installed analyses in place, so a reader holds [DocumentManager.View],
// and every recheck, in-place rebuild and [ProgramInterfaces.CheckAfter]
// takes that lock exclusively until its result is installed. When a
// document closes, the program it is the entry of goes unless it is an
// entry candidate another document reads, and every program under a root
// goes once no document there is open (forgetClosed).
//
// # Rechecks
//
// [DocumentManager.Recheck] takes a checked program and an edit and returns
// what a full check builds from the edited texts.
//
// An edit that leaves each edited file's interface fingerprint unchanged
// rebuilds only those files' bodies, in place. A `once`'s value counts as a
// body; an unannotated one is inferred again and must keep its type (early
// cutoff). Any other edit checks the edited files and their transitive
// importers afresh and keeps every other file's analysis.
//
// An edit that changes a file's impls is checked the same way when only
// those files can see each impl it added, removed or changed. A file sees
// an impl when every project file its header names is the file or one it
// imports transitively (the relevant impl set, as a check against
// interfaces reads it), so an impl of a leaf's own type is seen only by its
// importers. The whole program is checked afresh when another file can see
// a changed impl, when a kept file holds something of a rechecked one, or
// when the rechecked files are every file.
//
// The program-wide reductions over bodies (coherence, whole-file imports,
// a rootless file's application-field roots, the boot-only check's
// summaries) keep each file's part with its analysis, so a body edit reads
// only the edited files again. The boot-only check rebuilds its program
// graph once from the kept summaries.
//
// # Anonymous struct fields
//
// A field of an anonymous struct is declared where its literal labels it
// or a type expression names it, and the type records that file and
// position ([FieldDef] File, Line and Col; [ResolveTypeExpr] takes the
// FileAnalysis it is written in and records the field's symbol in its
// References, typeFieldFile). Accesses, accessors, destructuring patterns
// and struct-update labels refer there. From another file the reference is
// a symbol naming that file (anonFieldRef), read as an imported
// declaration's. A value whose type a signature or annotation declares has
// that type, not its body literal's, so `routes.make(1).label` reaches
// `label` in `make`'s return type. A stdlib type's field names no file and
// links only within its file; a use with neither is marked Undeclared, for
// hover alone.
//
// A label in an unannotated `once`'s value is in a body: a body edit moves
// it with the file's other positions (shiftDeclarations), the early cutoff
// holds only when the labels stand where inference put them
// (sameLiteralFields), and a label inside the edit makes the once's readers
// check again.
//
// # Closed files
//
// References and rename read every file whose text names the symbol, and
// the closed files' diagnostics pass reads each file. A closed file's
// analysis comes from a program kept for an open document when that
// program holds the file as a build of the file alone would (keptClosed,
// closed_kept.go): the first entry candidate reaching the file is the
// program's entry, closed files and the entry hold on disk the texts the
// program read, each open document's analyzed text is current, and no file
// has a syntax error or reads an expansion input. Its diagnostics are those
// it would carry open (documentErrors).
//
// An open entry with an unsaved edit still serves the file when the edit
// cannot reach it (entryEditReaches): both texts import the same files,
// bring the same impls and declare no `export fn` or keyword declaration,
// the program has no application type and no error of its own, the file
// does not import the entry transitively, and its analysis holds nothing of
// the entry (holdsFrom). Otherwise the file is built alone, which builds
// its whole program's declarations: at 50k lines, references to a function
// 137 files call take over 100 s that way against about 0.15 s from the
// program. What a derive synthesizes is left out of a served file, since a
// build alone checks less of it and no request reads it.
//
// # Opening from a record
//
// Opening a document checks its program whole. When this server or an
// earlier one of the same build checked it before and its files are
// unchanged, the manager opens it from a record in the artifact cache
// (stored_program.go, progcache kind `lsp-program`, keyed by the entry's
// path, the root's tree walked as kept programs' are).
//
// Per file the record holds its interface ([StoredInterface]), its text's
// SHA-256, its facts for the reductions ([FileFacts]), for a program with an
// application type its application-field roots' walk ([AppWalkSummary]),
// its references for references and rename ([StoredRefs]), and what it
// reports (storedFile.Report: syntax errors, repaired declarations,
// document diagnostics as documentErrors assembles them, and the
// reductions' reports, storedReportsOf). Per program it holds the
// application types, the missing-impl supply, the documents whose lowering
// found nothing, and the entry's lowering kept per file
// (irbuild.KeptLowering).
//
// Each open document is checked against the interfaces of the files it
// reads ([ProgramInterfaces.Check] over [NewStoredInterfaces]), as is a
// closed file a request needs, cached as closed files' analyses are. A
// document whose check reports something its record's file does not is
// checked in its program whole.
//
//   - A record is written only for a program that reads no file of another
//     module and has no Go bindings. Go-binding projects keep no program;
//     each document is built alone (buildOpen).
//   - A program that reports what only the whole check words (its impls'
//     errors, a missing impl, a noun's or phrase's disagreement, a catalog
//     nothing reads, any diagnostic beside an application type, a
//     reduction's diagnostic its document does not hold) keeps no reports.
//   - An `embed`'s files are read for names and sizes alone
//     (ExpansionSite.SkipEmbedContents) and the server tracks none of them.
//     A record of a program that embeds walks each pattern's base and
//     stamps each embedded file. The store's snapshot takes those trees
//     before reading the embeds again (EmbedInfo.sameOnDisk), so the record
//     is not opened once a file under a base came, went or changed.
//   - Expansion inputs (phrase catalogs) are named with their texts'
//     SHA-256: the record is not opened once one changed on disk, and an
//     opened program is forgotten once one holds another text, an open
//     catalog's edit included.
//   - The record is written when the server's lowering of an open document,
//     with its program kept and every text on disk, finds nothing
//     ([DocumentManager.NoteLoweredClean]): after an open or a save, and
//     again only when that holds and every file is the state's text on
//     disk.
//
// A file checked against interfaces reads other files' declarations without
// their attached tests and with a typed `once`'s value `todo`. Hover reads
// a declaration's examples and value, and a `phrase`'s or `noun`'s texts by
// locale, from its source, expanded as its check expands it
// ([FileAnalysis.DeclarationSource]). The file sees only the impls it can
// see, so implementation, and references to an interface method, check the
// document in its program whole first ([DocumentManager.CheckWhole]).
//
// # Edits in a program opened from its record
//
// A program opened from its record stays opened from it across edits, as
// `nomi check` relinks a kept program from its file records. The steps
// (advanceStored) are in the comment at the top of stored_edit.go. With an
// application type, each checked file's [AppWalkSummary] must equal its old
// one and find nothing, since those roots follow calls into other files'
// bodies. The summary walks the file's bodies in a local mode: each step
// that would leave the file (a call, a factory's body read for what it
// hands back, an interface's default body, a dispatch to every impl) is
// named instead of taken, and the file's roots, their boots and its impls
// are named too. Nodes are named by declaration name or order, never
// position, so moving code keeps the summary, and two texts with one
// summary take every root through the same reads.
//
// An edit that leaves the edited document's interface unchanged rebuilds
// its installed check's bodies in place (stored_inplace.go): new bodies and
// positions, every held position moved with the edit, bodies walked and the
// check's own diagnostics worked out again. An open document keeps its
// installed analysis while no file its check read changed how its interface
// is written. One that did is checked from its installed check
// ([ProgramInterfaces.CheckAfter]), which keeps the analyses of files the
// edit cannot reach and builds the others from interfaces. A file the edit
// can reach is an edited one, one importing a file built again, one holding
// something of one (holdsFrom), one the check now reads otherwise (whole
// where it read its impls alone, or other impls), and one that can see a
// changed impl. Knowledge of the kept files' `once` bindings carries over,
// as in checkProgramFrom. So after a leaf's signature edit the entry builds
// the leaf and itself again, not the whole closure. An impl edit takes the
// supply from the entry's check ([ProgramInterfaces.Supply]) when the
// entry's closure is every other file. primitiveOriginIndex passes over
// values, so a reused file's `pub once scale = 100` is not taken for a
// primitive the file declares.
//
// An edit that leaves something to report stays in the state. Each checked
// file's diagnostics are assembled as documentErrors would from the whole
// program (storedDocumentErrors): its build's and check's errors, its
// concurrency, task-lifetime and duplicate test name checks (the entry's
// own checks and front-end errors for the entry), and the reductions'
// reports in it (Reductions.of: boot-only calls, application-field roots,
// unused whole-file imports, as CheckImplImports settles them), dropped
// inside a declaration the parse repaired. The state keeps them
// (storedProgram.reports). Left to the whole check: the program's impl
// errors (collisions, the orphan rule), missing impls, and a reduction's
// diagnostic in a file the edit does not check. A program with an
// application type, or an impl edit, also checks whole when a checked file
// reports anything. While anything is reported the record is not rewritten
// and the entry lowers from its text. Anything else (such a reduction, an
// edit reaching more than maxStoredRechecked files and a quarter of the
// program, a file that came or went) forgets the opened program and checks
// the program whole, kept and rechecked as usual from then on.
//
// # Lowering the entry
//
// A document opened from a record offers no EntryProgram, and lowering the
// entry's text lowers the whole program (stored_lowering.go, and vmhost's
// checklowering_stored.go). So it takes its result from the record when the
// record lists the document ([DocumentManager.StoredLoweredClean]), and
// otherwise lowers from a kept lowering: the one this process made last for
// the state, else the record's (the server's whole lowering when it wrote
// the record, analyzedlowering.Kept).
//
// Each changed file is lowered alone from its check (irbuild.LowerFileAmong
// through vmhost's relowerOne, shared with the relink from file records),
// then the importers of a file whose signature (interface hash and
// unannotated `once` values, [InferredOncesChanged]) changed, each after its
// imports, and, when a file's impl headers name other files, every file
// that sees one of its old or new impls (StoredLoweringFile.Headers). A
// program reaching what a machine cannot run, a declined body, or more than
// vmhost's maxRelowered (16) files and a quarter of the program lowers the
// text. Problems are then
// what vmhost's CheckLowering reports. Any other document lowers from its
// text. From a program checked whole, the first such run first encodes the
// held lowering as objects.
//
// # References from a record
//
// A closed file is served from its stored references (stored_refs.go: each
// reference, punned label and definition with the symbol there, by name,
// source file, kind and position) while it holds the record's text and no
// file a stored symbol is declared in has changed how its interface is
// written. Otherwise it is checked against interfaces.
//
// The server's record and `nomi`'s file records (vmhost/filerecord.go) are
// two formats under two keys by decision. Each is keyed by its own binary's
// build ID, which a development build changes on every rebuild, and the
// file record holds one lowering of `nomi check`'s entry, where the server
// lowers every open document as its own entry and keeps references the CLI
// never reads. A release could key both by VCS revision and share
// interfaces and facts.
//
// # Costs at scale
//
// At 50k generated lines (221 files; Apple M2 Max, TestScaleOpen and
// TestScaleEdits in internal/lsp with NOMI_SCALE_LSP=1; each edit the first
// after the open, with the entry open, and the edited file when it is
// another):
//
//	operation                          checked whole        from the record
//	open the entry, first diagnostics  1.08 s, 221 files    0.28 s, 1 file
//	heap held, the entry open          353 MB               182 MB
//	hover or definition, closed file   3 ms                 1 ms
//	open a second file (core/util)     8 ms                 4 ms, 1 file
//	references, a function 137 call    0.14 s               0.12 s, none checked
//	rename of it                       0.14 s               0.11 s
//	a closed leaf's diagnostics        1 ms                 10 ms, 1 file
//	the entry's body edit              14 ms                34 ms, in place
//	its lowering                       0.50 s               0.28 s, 1 file
//	a leaf's body edit                 19 ms                8 ms, in place
//	a leaf's signature edit            0.09 s, 2 files      0.10 s, leaf, entry via CheckAfter
//	an impl added to a leaf            0.10 s, 2 files      0.12 s, the two, supply from the entry
//	the hub's body edit                9 ms                 3 ms
//	the hub's signature edit           1.01 s, 210 files    1.03 s, the program whole
//	a typo in a leaf's body            0.03 s               0.04 s, 1 diagnostic
//	a typo in the entry's body         0.02 s               0.05 s, 1 diagnostic
//	writing the record                 0.43 s
//
// The entry imports every unit no other unit imports, so its check reads
// every other file's interface; opening a leaf reads its own closure.
//
// # A bare todo
//
// `todo` is a contextual keyword (spec §2 *Keywords*). The parser reads
// `todo "reason"` as an ast.Todo and a bare `todo` as an identifier, since
// only resolution knows whether a binding named `todo` is in scope. Where
// none is (a module named `todo` is no value, so it does not count), the
// builder records the identifier in FileAnalysis.TodoPlaceholders with a
// Todo at its position, and FileAnalysis.AsTodo answers that Todo for it:
// the checker checks it in the identifier's place, recording its types on
// both, and the IR builder's placeholder sites, Todos and the hover consult
// AsTodo. The parsed tree is never rewritten, so a file the language server
// reuses unparsed, or two checks share, has each check's meaning
// (TestTodo_ChecksSharingATreeEachResolveIt runs them at once under -race).
//
// # Or patterns
//
// Each name the alternatives of `A or B` bind is one symbol, defined at
// every alternative's site, and the checker compares the alternatives'
// name sets and types (or_pattern.go, checkOrPattern). Coverage expands an
// `or` into its alternatives in each column of the constructor matrix
// (expandOrPatterns).
//
// The IR builder (internal/irbuild, irorpattern.go) tests the alternatives
// in order against one value. A matching alternative copies each name into
// a join temporary and jumps to the pattern's success block, where each
// name is bound once, so an arm's guard and body are lowered once. A
// parameter's destructuring prologue cannot branch, so for an `or` inside a
// parameter it holds the value and the body tests it before running.
//
// # If without else
//
// An `if` with no `else`, or an `else if` chain with no final `else`, whose
// branches have a value of a type T other than Unit is `Maybe<T>`. The
// checker (elseless_if.go) checks the branches as an `if` with an `else`
// does, each link returning its branches' own type, against the expected
// Maybe's payload or, with no Maybe expected, against Unit as statements
// are. The joined type makes the head `Maybe<T>` unless T is Unit or
// Infallible (every branch leaves). The head, not its links, is recorded in
// FileAnalysis.ElselessIfs with its Maybe type. A mismatch whose value is
// such an `if` gets a help naming it (elselessIfHint) at a function's
// result, a `return`, an annotation, an argument and a non-final statement.
//
// The IR builder reads the record (internal/irbuild, irelseless.go).
// ifRegion lowers a recorded head's arms with irFuncSig.elseless set: each
// arm lowers its value at the payload's kind and wraps it in `Some`, the
// missing `else` writes `None`, and an `else if` link continues the region.
// A nested `if` or `case` in an arm is lowered as its own value and wrapped
// whole, so a nested `if` without `else` gives `Some(None)`, not `None`.
// Where the region's result is not a Maybe (an `Html` slot's `ToHtml`), the
// `if` is lowered as a Maybe value that then enters the result. A pipe's
// `if` stage is lowered from a copy of its node, which gen.splicedIfs maps
// back to the recorded one. A test body's statement `if` discards its
// value, so its arms stay test statements.
//
// `nomi fmt` strips a tail `return value` only where the value is the
// body's: a branch of an `if` with no final `else` keeps its value returns,
// since its value is wrapped.
//
// # Releasing resources
//
// The release rules (spec §9 *Releasing resources*) are a check over one
// body at a time, run at the end of checkFunc and of a test's body
// (resources.go, checkReleases), once every expression has its type. A
// lambda in the body is checked as a body of its own; a nested function by
// its own checkFunc.
//
// A type is a Release type when the impl index holds an `impl Release`
// whose receiver is that declaration. The index is keyed by receiver name,
// so the impl must carry the type's qualified receiver (std/http_host's
// `Conn` is not std/sql's). The errors and the quick fix take their text
// from the bodies of the impl's `release` and `commit`, each one call
// (resourceCall): `Writer.discard(w)` gives `Writer.discard`, and a bare
// `close_quietly(conn)` in std/sql is written `sql.close_quietly` outside
// it. So that another file can read those bodies, a file's interface keeps
// the bodies of its `impl Release` and `impl Commit` blocks (stripBodies in
// interface.go), as it keeps interface defaults; every other body is
// stripped.
//
// collectAcquisitions walks the body for bindings whose name has a Release
// type and whose value, without its `?`, `!` and parentheses, is an
// acquiring call or pipe (acquired): its type is a Release type or a
// `Result` holding one, it contains no identifier or field access of a
// Release type outside a lambda, and it is no constructor call. A `Reader`
// or `Writer` outside a `Result` does not count (isStreamType, by std/io's
// declarations). `case` arms and `if` patterns over such a call bind too.
//
//   - `discarded-resource` (checkDiscarded): an acquiring call bound to a
//     discard name, or the last statement of a block whose value nothing
//     takes. markDropped finds those blocks: a test's body, a body whose
//     result is `Unit`, a lambda recorded in unitCallbacks
//     (checkLambdaExpecting with a `Unit` result), and the value blocks of a
//     statement whose value is dropped.
//   - `unreleased-resource`: a name with no `defer` among its block's later
//     statements whose call takes it, and that handedOn does not find
//     returned, yielded, stored in a literal or constructor, or rebound. A
//     `defer on error` (ast.Defer.OnError) does not count as the `defer`;
//     with one and no hand-on the message says it is released only on
//     failure.
//   - `leaks-on-return` (pathFlow with handOn, leakError): a name with no
//     such `defer` that handedOn finds handed on somewhere starts a
//     pathFlow at the statement after the binding, and every normal
//     return it reaches unsettled is an error there. A path is settled
//     by a statement handsOnAt counts (a binding, literal or constructor
//     holding the name, including one in a lambda), by a return or a
//     taken block value that is the name (carries, blockValue), or by a
//     call of the release or the commit. The fix inserts the release
//     before a return that starts its line.
//   - `leaks-on-error` (checkErrorExits): in a body that can propagate, the
//     first `?` or `return` of `Err` or `None` (firstErrorExit, outside
//     lambdas) in the statements between the binding and the first `defer`
//     or `defer on error` taking the name, or, with neither, the first
//     statement that hands it on (handsOnIn, or the last one yielding it).
//     With no deferred release its fix inserts `defer on error <release>`
//     after the binding; with one after the exit the message says to move
//     it up.
//   - `defer-on-error-cannot-fail` (checkDefer): a `defer on error` in a
//     named function whose result is neither a Result nor a Maybe.
//   - `uncommitted-resource`: a Commit type's deferred usual release starts
//     a pathFlow at the statement after the `defer`. It follows
//     statements in order, branching at `if` and `case` and joining the
//     states of branches that go on (settled only when each settled),
//     and reports each normal return reached unsettled: a `return` or
//     tail value that is not `Err` or `None`, `break`, `continue`, and the
//     end of the binding's block. A call of the commit or the release with
//     the name settles the path, including one inside a lambda. The flow is
//     the unreachable-code rule's shape (alwaysExits) carried with a state,
//     since the analyzer has no control-flow graph; `leaks-on-return` runs
//     the same flow.
//   - `released-value-escapes` (defer_escape.go, checkDeferredEscapes):
//     every `defer` in the body but a `defer on error`, acquisition or
//     not, whose call takes a
//     name or field path of a Release type. The path's name and fields
//     (releasePath) are tainted; the later statements of the `defer`'s
//     block are scanned in order, a binding whose value carries a tainted
//     path taints its own name, and the first `return` (outside a lambda)
//     or the block's value, when markDropped did not drop it, that carries
//     one is reported at the `defer`. A path carries when it is a prefix
//     of a tainted one (`db` for `db.conn`), or extends one with a value
//     of a Release type; constructors, literals, `if`, `case`, blocks and
//     a lambda's references carry what they hold. Other calls are uses,
//     as for the hand-on rule.
//
// Each error carries a parser.Fix, which the language server offers as a
// quick fix. "Release `w`" inserts the `defer` on the line after the
// binding and, for a Commit type, the commit before each return the flow
// finds uncommitted. "Release the `Reader`" binds a dropped acquisition to
// a name the body does not write (`reader`, else `reader2`), then does the
// same. "Commit `w`" inserts the commit before its return.
//
// # Captures
//
// The parser reads one postfix-level operand after `&` and refuses a body
// that is no call, parenthesized expression or literal. In a partial
// capture (`&f(&1, x)`, ast.Capture.PartialCall), the call's `&N`s are its
// open arguments, so the checker and the builder read it through their
// ordinary partial-application paths. Any other capture is typed as a
// lambda whose parameters the expected function type gives, each `&N` read
// as parameter N (captures.go, checkCapture). An `&N` outside every capture
// is reported with a help that writes the capture from the nodes around it.
//
// The builder lowers a capture as the lambda it stands for (internal/irbuild,
// capture.go), so nothing runs when the capture is made. A partial
// capture's lambda takes the callee's parameters at the open slots, with
// their defaults and names, and its body is the call with the written
// arguments in place, written by name in evaluation order when that differs
// from their slots' order.
//
// The formatter keeps a parenthesized body's parentheses, which it drops
// elsewhere, since they change the meaning: `&(f(&1))` is no partial and
// `&f(&1)` is one.
//
// # Advice
//
// Advice is a diagnostic about a program that checks and means the same
// either way, so it is never an error and the checker does not report it.
// A redundant `value as Type` is recorded during the check
// (FileAnalysis.RedundantAscriptions). A type parameter that style rule 52
// writes as an interface value is found after it by InterfaceValueHints
// (interface_value_hint.go), from the file's nodes and analysis: a
// function's (or an inherent impl function's) type parameter that its
// signature names once, as one parameter's whole type, under one interface
// bound whose functions the body calls only as an interface value can
// (`T.name(value)`, nothing returning or taking a second `self`), with the
// parameter only passed, piped or interpolated, and no call in the file
// passing the function explicit type arguments. A call in another file is
// not looked for, which would read the whole program on each edit; such a
// call is redundant and fails at the call after the fix. Each hint carries
// a parser.Fix that drops the type parameter and its bound, writes the
// parameter's type as the bound and each `T.` owner as the interface.
//
// The language server publishes advice as hints (internal/lsp
// diagnostics.go) and offers each fix as a quick fix. `nomi check` writes
// the interface-value hints as warnings that fail no file
// (vmhost.Program.Advice), from Check only, so `nomi run` and `nomi test`
// say nothing of them; a kept program holds its advice, so a check that
// reads it reports what a fresh one does. Neither reports advice about a
// std file.
//
// # The stdlib's state
//
// Building the stdlib's base modules from their interfaces (std/shared.go)
// runs every sweep over about fifty modules' declarations. Its result is
// the same in every process of one binary, so std keeps it as bytes beside
// its artifact and a later process reads it back whole
// ([EncodeStdState], [DecodeStdState], state_codec.go) instead of running
// the sweeps and forming the [StdBase] again.
//
// What is written is stdState: each module's [FileAnalysis], the nodes its
// build checked and the interface nodes it was given, the base, the
// errors, stdBootWhys' entries for the modules' functions (installing the
// facts stores them) and bodyGenerations. Everything they reach is written,
// every field of every type, exported or not: the builders a file keeps for
// rebuilding its bodies, the program, the scopes, symbols and types, the
// type registries and the node-keyed maps. The encoder and decoder of each
// type are generated from the package's types by stategen
// (state_codec_gen.go; `go generate ./internal/analysis`), so a field
// added anywhere the state reaches is written once the generator runs, and
// TestGeneratedStateCodecIsCurrent fails until it does. The generator
// refuses a field it cannot write unless it lists it: a function field is a
// hook (state_codec.go: a builder's loader, which the decoder is given; a
// default's setter, which the finished inference never calls again; the
// program's errors closure, whose answer for the stdlib is its Errors; the
// once table's inference, written as whether it ran), a field a finished
// build leaves empty, or one the stdlib's build never sets.
//
// Each file's kept declarations (what rebuilding its bodies reads: the
// builder, the declarations' share of the references and definitions) are
// written after the rest, and a process reads them only when something
// first asks for a file's (FileDecls.keptDecls, stateRest), which only
// checking a std module's bodies does. They go on with the decoder the
// state was read with, so they share its objects.
//
// Identity is kept. A pointer to a struct, a map and a slice that is not
// empty are written once and then by their number, keyed by address and
// type (a struct and its first field share an address) and a slice's
// length too, so what the build shares the decoded state shares. A package-level pointer, map or Type, as the package started
// (stateGlobals, the singleton primitive types, the fallback Bool and
// Ordering, pendingDefault), is written by its place in that table and
// read back as the process's own. AST nodes go through
// internal/ast/astcodec in the same stream, sharing its pointer table, so a
// map keyed by a FuncDef keys the decoded node the decoded nodes hold.
// Strings are written once each, in a table, and a path under the
// directory the stdlib was found in is written relative to it, so a state
// reads back wherever a process finds the stdlib.
//
// A build installs the canonical std types for the process
// (installCanonicalStdTypes), and the state points at whichever Bool the
// registries saw. So only a process whose base build installed them, its
// first build, writes a state ([EncodeStdState] refuses otherwise), and
// reading one installs them from the decoded modules, as that build did.
//
// The format is bound to the code that wrote it: the generated codec
// writes the fields in declaration order with no names. [StdStateFormat]
// names that code: the format's version and digests of the generated
// codec and of internal/ast/astcodec's, which stategen and codecgen stamp
// into the files they write (stateCodecDigest, astcodec.CodecDigest). std
// keys a cached state by the binary's build ID and its artifact's checksum,
// and an embedded one, which a process of internal/irbuild/stdbundlegen
// wrote for the binary that embeds it, by the artifact's head and checksum
// and the format; it checks its own CRC-32, and builds the base modules
// with the sweeps when a state is missing, stale or does not decode. A
// path is written with slashes, so a state written on one system reads on
// another. The
// stdlib's tests hold a state read back to the build it was written from,
// field by field and pointer by pointer ([CheckStdStateRoundTrip],
// TestStdState_ReadsBackAsTheBuild), and a process's stdlib read from a
// state to the one analyzed from source (TestShared_ModulesAreLoadsModules).
package analysis
