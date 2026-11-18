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
// a rootless file's application-field roots, the boot-only check's and the
// once-cycle check's summaries) keep each file's part with its analysis, so
// a body edit reads only the edited files again. The boot-only and
// once-cycle checks each rebuild their program graph once from the kept
// summaries.
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
// reports in it (Reductions.of: boot-only calls, cyclic `once` bindings,
// application-field roots, unused whole-file imports, as CheckImplImports
// settles them), dropped inside a declaration the parse repaired. The state keeps them
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
// # Generic calls and the expected type
//
// A generic call's type arguments come from its arguments first (spec §5
// *Type arguments from the arguments*). checkGenericCall and checkPipe
// still solve what the call's expected type would give, but hold that
// seed back and hand each argument, before checking it, only the seed's
// solutions for the type parameters its slot names that it cannot type
// itself (a dot-leading variant, an empty literal, an unannotated lambda's
// parameters) and the earlier arguments left open; a pipe's head takes it
// by the same rule (pipeHeadExpected). A type parameter the result holds
// at an invariant position (spec §5 *Covariant conversion*) takes the seed
// ahead of every argument, since a type argument there never converts
// (exactParams). The call is recorded at the instance the arguments
// solved, and its result meets the expected type as any value does: a
// Coercion record at the call where it enters an interface, an IterViews
// record where an Iter is relabelled, a Conversions record where it
// converts below its top. The rules and their reasons are in
// expected_seed.go.
//
// # Elaboration
//
// The IR builder lowers what the checker decided: each expression's type,
// each call's callee, where its arguments go and, for a generic callee,
// the type arguments it is
// instantiated at. [VerifyElaboration] checks a checked file against that
// invariant, reading only the checker's records (ExprTypes, TypeExprTypes,
// ExpectedTypes, References with each reference's CallType and DispatchImpl,
// Calls, Memberships, Operators, CallDefaults, HoleDisplays, UnwrapFailures, each TypedLiteral's Failure, Coercions, IterViews, Conversions, ImplTypeArgSets, each reference's CallInterface, the `?` markers), and returns each place a record is
// missing or open, by construct (call, expression, implicit operation) and
// category. Its doc comment defines "closed": no unsolved inference
// variable at any depth and no type parameter but the enclosing
// declaration's own. Derive-synthesized bodies and the functions a
// `phrase` or `noun` expands into are reported apart, by the declaration
// around them.
//
// Those bodies sit in the synthesized band (derive_synthesis.go,
// [IsSynthesizedLine]) so that no position of theirs aliases source, and
// the checker records them in the same maps as written code: ExprTypes,
// ExpectedTypes, PatternTypes, References and CallType. Their synthesized
// code names the protocol interfaces, `Ordering` and the prelude variants
// whatever the file binds; where the file binds none of them (a std
// module), the checker records the std declaration as the file's own
// reference would be (synthSupportType, synthPreludeVariant). The
// language server never shows a band record: a request's position is
// never in the band, and each reader that walks a whole map (references,
// rename, highlights, the completion index, a served file's stored
// references) skips the band.
//
// An impl block whose header writes no type arguments for a generic
// interface (`impl Gets for Box`) has a record of the ones it implements
// (ImplTypeArgs, read through [ImplBlockTypeArgs]), solved by the type
// builder from the items' signatures against the interface's, with each
// parameter no item names marked Open. The IR builder instantiates the
// block's interface from it, in the frame of the receiver's instance.
//
// A call of an interface function the checker selected no impl for (no
// DispatchImpl: a receiver of a type parameter or an interface value, or
// an inherited default reached through a type) records, on its reference, the interface instance it goes through
// (call_interfaces.go, Symbol.CallInterface): the bound for `C.holds(c,
// x)` under `C: Holds<T>`, recorded as the call is typed, and otherwise
// the interface at the type arguments its signature is instantiated at,
// recorded once the file's bodies are checked. Where a type implements a
// generic interface at several type arguments, the IR builder takes the
// block at that instance's arguments (internal/irbuild, instimpl.go).
//
// The language lets an inference variable stay unsolved where no value of
// its type is made (undetermined.go: `[]`'s element, `Ok(1)`'s error
// type). Once every body in the file is checked, the checker closes each
// such variable (closed_vars.go): it records one closed type in the
// variable's Closed field, which every record mentioning the variable
// shares, so an argument and the callee it is passed to are built at one
// instance. The rule is Int, or Unit when Int does not meet an interface
// bound the variable was checked against and Unit does. Closed is
// for lowering alone: checking and every display read Resolved, so the
// variable still prints as `?N`, and a later unification that solves it
// wins. The IR builder reads it through [ResolveClosed] and [Elaborate],
// and through its own projection of a variable; it fills no hole itself.
// The verifier counts a closed variable as closed.
//
// Each call has a record of where its arguments go (resolved_call.go,
// Calls, a [ResolvedCall]): for each of the callee's parameters, the
// written argument, the piped value, the default or the partial's `&N`
// that fills it, and the written arguments in the order they are
// evaluated, which is the order they are written (spec §8 *Evaluation
// order*). One function routes arguments to parameters (placeArgs), for a
// direct call, a pipe stage, which is a call with the piped value placed
// first, and a partial application, which is a call with holes; the
// direct, generic and pipe checks record what it answered
// (recordPlacement). A form that takes its arguments in order (a
// constructor, an operator function, `testing.check`) is recorded so,
// through the same function, where the form is checked
// (recordPositional). A `then` or `tap` stage's call of its function has
// its record under the stage. The record classifies the callee too
// (callee.go, [Callee]): a declared `fn`, a `host fn`, an impl's
// function, an interface function dispatched where it runs, a function
// value (naming the declared
// function it copies, if it copies one), a variant, a type's constructor
// or a keyword form, each by the checker's symbol and declaration node.
// A std function's record also carries its identity (StdFunc): its
// module, the type or interface it is declared under, the interface of
// the impl block that declares it with that interface's type arguments,
// and its name. It names one declaration of its module, so two parses of
// the module (the shared analysis's and one checked from its text) agree
// on each function, and two impls of one interface at different type
// arguments do not share one. A module checked from its text reaches the
// shared parse of itself too (an attached test's `import std::hex` in
// std/hex), which its program keeps beside the edited one for this
// (Program.reachedFiles).
// A call of an interface's function whose receiver's type is known where
// it is checked names the impl block it reaches (Callee.Impl,
// impl_reached.go) and the function that runs there: the block's `fn`, a
// derive's synthesized one, its `host fn`, or the interface's default the
// block inherits. The block is the one of the receiver's type and the
// interface (FileAnalysis.ImplBlocks, each block with its resolved
// receiver and its interface's origin, so a program's `Error` and
// std/json's, or a file's `DateParts` and std/calendar's, are told apart;
// the universal or derived Debug block of a std file that does not import
// Debug is registered under std's own, compilerKnownSynthTypeOf),
// chosen among several by the receiver's type arguments, or the one the
// checker selected (impl_selection.go). Every call of an interface's
// function also records how it is answered (Callee.Form, callForm), in
// the terms an operator through the interface records it
// (OperatorUse.Form) and by the same rules: OperatorByImpl where it
// reaches a block; OperatorStructural where the receiver's shape answers
// it (a tuple, an anonymous struct, a collection, a function value:
// shapeUse) or the receiver is a declared type with no block of the
// interface that meets it all the same (the Equatable and Hashable of a
// type with no impl of its own, a block-local type's Debug, a struct's
// Struct: noImplUse); OperatorDispatched on an interface value; and
// OperatorPerInstance through a type parameter. An operator asks the
// same two rules with the call's block resolver between them
// (interfaceUseFor, implMethodFor, implBlockFor), so `a == b` and
// `Equatable.equal(a, b)` are answered alike. A block is a type's when
// its receiver is the type's declaration (implBlocksOf): a type declared
// inside a body, which no impl can name, has none, whatever a std module
// declares under its name. TestElaborationGate holds a call on a receiver
// of a known type to a block or a Form (ElabImplUnrecorded), and an
// operator's recorded impl to one of its operand's type's blocks
// (ElabOperatorForeignImpl).
// A generic body is checked once, so a call in it through a type
// parameter (`T.area(x)`, `Shape.area(x)` with `x: T`, `T.from_json(j)`)
// records the interface's function, For the type parameter, and no
// block. FileAnalysis.CalleeAt answers that record at an instance's type
// arguments: For substituted and, where it is a known type, the block and
// function a call written at that type records. It asks the resolver
// reachedImpl asks (interfaceImplAt), with the receiver's type as an
// argument. An operator over a type parameter (OperatorPerInstance) is
// answered at an instance the same way (OperatorUseAt, through
// interfaceUseFor, the resolver operatorUseOf asks). For is the type
// parameter of a function, of an impl block (written `impl<B>`, or named
// by its receiver alone, `A` in `impl Make for Box<A>`, whose symbol the
// type builder gives the block's parameter: attachReceiverTypeParams) or of
// a generic type in its own block. A generic interface's qualified call
// (`Contains.contains(xs, x)`), whose `self` no argument solves, is for
// the type of the argument at its `self` parameter, `List<T>` included,
// and a bare call of an interface's own function in one of its defaults
// (`compare(b, a)` in Comparable's `min`) is for the default's `self`,
// which an instance of the default binds: both recorded as the
// reference's `self` type argument (recordCallSelf). A membership test
// `x in c` is the call `Contains.contains(c, x)` (in.go,
// FileAnalysis.Memberships), recorded as that written call is.
// A check classifies the calls it recorded (settleCallees), so a test
// checked against a file afterwards (CheckAddedTest), whose copy shares
// the file's records, leaves them as the file's check made them: a `fn`
// declared inside one of the file's bodies stays a function value.
// One function follows a callee back to the function behind it
// (calleeOf); the names a call may write, the defaults it may leave out
// and the classification all read its answer. The record says what the call means and
// nothing of how a machine holds a value, and the IR builder places a
// call's arguments from it alone (internal/irbuild package doc, "Calls").
//
// A call that leaves defaulted parameters out has a record of what fills
// them (call_defaults.go, CallDefaults): the declaration whose defaults
// they are, its file and the slots, made from the call's placement, so a
// pipe's subject and a trailing
// callback count as it counts them. A call through a local copy of a
// declared function names the function the copy was made from, and a call
// of an interface function the checker resolved to an impl takes, per
// slot, the impl function's default where it declares one and the
// interface's where it does not: the record names each slot's declaration
// (DefaultedCall.From, settleCallDefaults). A declared function named as
// a value where fewer parameters are expected records the slots its
// call fills (DefaultedRefs). The IR builder fills omitted parameters
// from these records alone.
//
// Each interpolation hole has a record of how its value renders by
// Display (hole_display.go, HoleDisplays), made once the file's variables
// are closed: a String is its own text; an Int, Float, Bool or Decimal and
// a value with no part that has an impl (a tuple, anonymous struct, List,
// Map or Vector of those scalars and Strings, or an empty collection) are
// the machine's rendering; a tuple or anonymous struct with another part
// renders each part by its own record; a declared type or a std container
// renders through its `impl Display`, which the record names with its
// file; an interface value renders through the impl of the value it
// holds. A hole of a type parameter, or of the `self` of an interface
// default (an InterfaceType marked DefaultSelf, which is the implementor
// and not an interface value), records only that: at an instance whose
// types are known, DisplayAt answers the record a value of the
// substituted type has, and the IR builder renders the `self` of a
// default by the same rule over the instance's kind. DisplayCallee is
// what a call of a record's impl function reaches, which the builder
// calls it by. The error a typed literal is refused with, E of a fixed
// literal's handler's `Result<T, E>` or a checked literal's check's
// LiteralError (TypedLiteral.Failure), and the error a `!` over a Result
// shows when it traps (UnwrapFailures) have the record a hole of that
// type has, made with the holes' (recordDisplays).
//
// Each binary operator over a value that is not a primitive, and each
// prefix `~~~` over one, has a record of what it means (operator_uses.go,
// Operators), made once the file's variables are closed: the impl it calls
// (a binary operator's OperatorImpls record, or for a derive-synthesized
// body the same record made for it there; `~~~`'s one BitwiseNot impl of
// its operand's type), a
// comparison by shape (a tuple, anonymous struct or collection, or a
// declared type no file writes or derives an Equatable for), a comparison
// of two interface values by their run-time types, or, over a type
// parameter, the interface of its bound, which the IR builder resolves at
// each instance. A declared type whose impls the index cannot narrow to
// one is left unrecorded, and the verifier reports it.
//
// An expression of a concrete type checked against an interface type
// becomes an interface value, and has a record of it (coercions.go,
// Coercions): the interface with its type arguments, the value's type and
// the impl block the checker selects for the pair (implCandidates). A value
// entering an interface conjunction (`Priced and Costed`, a
// ConjunctionType, its members normalized by NewConjunction) has one record
// naming the conjunction and an impl block per member; a conjunction
// widening to fewer members is no coercion and has none. The
// records are made once the file's bodies are checked and its variables
// closed, from the types the checker settled. Two such expressions are no
// coercion and have none: the receiver of a call of an interface function
// (`Hashable.hash(1.5)`, a pipe's subject in that slot, the collection of
// `x in c`), which the call passes to the impl it reaches, and a source at
// an `Iter<T>` position, which the callee receives as a view over the
// source (`ir.IterView`), not as an interface value. The IR builder makes
// a written expression's interface value from its record alone
// (internal/irbuild, irboxed.go).
//
// A source at an `Iter<T>` position has a record of the view instead
// (iter_views.go, IterViews), made in the same pass: the source's type,
// the element it pushes, and which walk the view is: std's List, String,
// Bytes, Range, Vector, Set, Map or Maybe, the type's own `impl Iter`,
// whose block the record names (implCandidates, or the type's one impl
// of Iter where a where clause bounds it inside a generic body), or a
// type parameter bounded by Iter, which each instance resolves. A
// receiver of `Iter.each_while` or `Iter.known_count` has one too. A value
// of type `Iter<T>` is a sequence already and has none. The IR builder
// builds each view from its record (internal/irbuild, iterview.go).
//
// An expression whose type converts to the expected one below its top (a
// `Maybe<List<Int>>` where a `Maybe<Display>` is expected, a `Box<Circle>`
// where a `Box<Shape>` is; spec §5 *Covariant conversion*) has a record
// of the conversion (conversions.go, Conversions), made in the same pass:
// its type, the expected type, and each part that enters an interface
// with the impl the checker selects for it, walked by each type
// parameter's variance (variance.go, TypeParamVariances: read off the
// declaration's fields, solved to a fixed point over the declarations
// that name one another, and cached by the declaration's first type
// parameter), and what converting it costs (Cost, conversionCost). The IR
// builder relabels the value where Cost says it costs nothing and
// otherwise rebuilds it, boxing each part by the record's impl
// (internal/irbuild, irrelabel.go, irembedsrebuild.go, irboxed.go); the
// hover reports the same Cost.
//
// vmhost's TestElaborationGate runs the verifier over the corpus, the
// stdlib, the tour, the spec and internal/irbuild's fixtures and fails on
// any call or expression without a closed record, derived bodies included,
// and on each implicit operation the checker records (gatedImplicit: a
// call's argument placement, a
// call leaving defaulted parameters out, an interpolation hole, an
// operator, a value entering an interface, a source entering an `Iter<T>`,
// a value converting below its top,
// an impl block of a generic interface whose header writes no type
// arguments, a call of an interface function dispatched where it runs).
// TestElaborationCensus runs it over the same programs, or over one project
// directory read in place, and prints counts by category and construct
// with examples, the implicit operations included:
//
//	NOMI_ELAB_CENSUS=1 go test ./vmhost -run TestElaborationCensus -count=1 -v
//	NOMI_ELAB_DIR=<project> go test ./vmhost -run TestElaborationCensus -count=1 -v
//
// NOMI_ELAB_OUT=<file> writes every violation, one per line.
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
// An `if` with no `else`, or an `else if` chain with no final `else`, is a
// statement, of type Unit: its branches are Unit, leave, or end in a
// statement whose value a block drops (an `assert`, `refute` or `dbg`,
// which a link answers as Unit). The checker (elseless_if.go) checks the
// branches as an `if` with an `else` does, each link returning its
// branches' own type, against the expected type when it is a Maybe and
// against Unit otherwise, as statements are. Any other joined type is an
// error at the first branch whose value is not Unit, and so is a chain
// whose branches do not join. The error names the type and carries a
// parser.Fix, the language server's quick fix: where a statement drops the
// value (checker.statementIf, or a Unit position) it writes `_ = ` before
// the value, in block form when the `if` is one line; otherwise, for a
// Maybe branch, it adds `else None`, after the last branch of a one-line
// `if` and as an `else` branch before a block's `end`. Any other branch
// gets help to add an `else` and no fix.
//
// The IR builder lowers every `if` without `else` as Unit (internal/irbuild,
// irelseless.go, unitElselessSig): its arms are lowered against Unit, the
// missing `else` writes Unit, and an arm ending in an `assert` or a `dbg`
// runs it and writes Unit. A test body's statement `if` discards its value,
// so its arms stay test statements.
//
// `nomi fmt` strips a tail `return value` only where the value is the
// body's: a branch of an `if` with no final `else` keeps its value returns,
// since that `if` has no value.
//
// # Cyclic once bindings
//
// A `once` whose initializer reaches the `once` itself is an error at its
// declaration (spec §6 *`once` Bindings*; once_cycle.go,
// CheckOnceCycles). The check runs beside the boot-only check, after every
// file is checked, and is built the same way: each file's bodies are read
// once per body part into a summary of what each `once` initializer and
// each function body reads and calls, the program's graph is built from
// the summaries of its own files (std's are left out: std imports no module
// of the program, so no cycle through a program's `once` passes through
// it), and each file reports the `once` bindings it declares that lie on a
// cycle, with the shortest chain back. A program opened from its record
// reduces the same graph from each file's OnceFacts.
//
// The graph is precise where the checker resolved the target and silent
// elsewhere, so the check has no false positives from dispatch: an arc is a
// read of a `once` (a call of a function-valued one included), a call the
// checker resolved to one function (by name, `Type.f`, an interface
// function on a concrete receiver through DispatchImpl, a pipe stage or
// `tap` naming a function), or the body of a lambda a `then` or `tap`
// stage, or `(fn -> ...)()`, applies where it is written. A lambda,
// capture or nested `fn` anywhere else adds nothing, since it runs only
// when called; so do a call through a function value, an interface value
// or a type parameter, an operator's implementation and a default
// argument. Every branch counts, as in Go's initialization-cycle rule. The
// run-time check (rt.OnceCell) still traps a cycle the graph does not see.
// An unannotated `once` whose value reads its own type is left to type
// inference's error (OnceTable.cycles).
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
// so the impl must carry the type's qualified receiver (std/http/host's
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
//     the unreachable-code rule's shape (leavingRegion) carried with a state,
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
// # Diagnostic codes
//
// A diagnostic is a TypeError: a position, a Message, help lines (Hints),
// related locations, and three fields for whatever acts on it rather than
// shows it. Code names its kind ("missing-field"). Args are the values the
// message names that a reader of the code needs, in the order the code's
// constant documents (the field's name). Fix is the edit that repairs it
// (parser.Fix), built where the error is built, by the code that knows
// what the source should say.
//
// Nothing recognizes a diagnostic by its message. The language server's
// quick fixes read Code and Args, or offer the Fix as it is; a later pass
// of the checker that revises an error it reported (an unknown type that
// is an inherent block's type parameter, an argument mismatch that is an
// HTML slot's) finds it by Code; and where one function needs to know
// what another just reported, the other returns it
// (reportGenericArgMismatch's result) or is told what to write
// (implCalleeLabel names a membership test's callee `in` in the message
// itself). So a message, a help line and a fix's title may be reworded
// freely. The one comparison of messages left is of an error with itself:
// alreadyReported drops a second report of the error a site already made.
//
// A code is an exported constant, `<Kind>Code`, in the file that builds
// its error; an error no consumer acts on has none. A new code needs an
// explanation in internal/mcp's explanations
// (TestEveryDiagnosticCodeIsExplained). The codecs carry Args with Code:
// the generated state codec and the std facts (std_facts.go).
// internal/docscheck's TestNoCodeRecognizesADiagnosticByItsMessage fails
// when non-test Go code tests message text, with the few exceptions
// listed there by reason.
//
// # Advice
//
// Advice is a diagnostic about a program that checks and means the same
// either way, so it is never an error and the checker does not report it.
// A redundant `value as Type` (FileAnalysis.RedundantAscriptions), an
// operand that restates the current value of the field it writes
// (FileAnalysis.RestatedPaths), and a call whose Maybe the value's
// refinement answers plainly, `List.first(names)` for a NonEmptyList
// `names` (FileAnalysis.PlainCounterparts, plain_counterpart.go), are
// recorded during the check, and FileHints reads them as hints, for the
// language server and `nomi check --hints` alike. The last carries a fix
// when a `!` unwraps the call: it names the refinement's function and
// drops the `!`. The counterpart is the refinement's function of the same
// name and arity whose result is no Maybe, so `Iter.reduce`, which takes
// a seed `NonEmptyList.reduce` does not, has none.
//
// A type parameter that style rule 52 writes as an interface value is
// found after the check by InterfaceValueHints (interface_value_hint.go),
// from the file's nodes and analysis: a
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
// the interface-value hints as warnings that fail no file, and FileHints'
// hints only under `--hints` (vmhost.Program.Advice, vmhost.WithHints),
// from Check only, so `nomi run` and `nomi test` say nothing of them; a kept program holds its advice, so a check that
// reads it reports what a fresh one does. Neither reports advice about a
// std file.
//
// # Warnings
//
// A warning is a diagnostic about a program that checks and runs and
// likely does not do what its writer meant, so, unlike advice, every way
// of running it reports it. There are two. An `embed` pattern whose base
// does not exist is one (EmbedWarnings, embed.go). A quoted or
// triple-quoted string that writes `$name` where a value named `name` is
// in scope is the other (DollarWarnings, dollar_interpolation.go): the
// lexer records each `$name` as it reads a string's text
// (token.DollarRef), the parser keeps them on the literal (ast.DollarRef),
// the builder, which holds the scope at each literal, records the ones
// that name a value in FileAnalysis.DollarRefs, and DollarWarnings reports
// each such literal, with the Fix that writes `${name}`. A binding or parameter whose only use is such a `$name` is
// the unused-binding error, which then says the string is text. vmhost
// writes a program's warnings where WithWarnings says
// (vmhost.Program.Warnings), for `nomi run`, `check`, `test` and the REPL,
// and the language server publishes them as warnings.
//
// # Walks over a type
//
// What a [Type] is built from is read in one place (type_walk.go).
// typeChildren answers the types one level inside a type: an element, a
// key and value, a tuple's elements, an anonymous struct's fields, a
// function type's parameters and result, type arguments, a conjunction's
// members, a Partial's target, a solved inference variable's solution.
// withTypeChildren builds the same type from other parts. walkType visits
// every type inside a type, typeHolds asks whether one of them answers a
// question, and mapType rebuilds a type with a function applied to its
// parts. A function that looks for something inside a type or replaces
// part of one is a visit or a mapping over these, and names only the kinds
// it treats specially: a kind it must stay out of is a visit that answers
// false for it, with the reason beside it. A new kind of Type gets a case
// in typeChildrenIn and withTypeChildren, which
// TestTypeChildrenNamesEveryKind requires, and every walk covers it.
//
// A declared type's own structure is not among the parts: a struct's
// fields, an enum's variants, an interface's functions, a distinct type's
// Inner and MoreParents. A recursive declaration reaches itself through
// them, so a walk that needs one reads it by name (containsTypeVar and a
// distinct type's Inner, distinctCycle).
//
// Two types are walked in step through pairedTypeChildren, which answers
// the parts of each, position by position, when the two have the same
// shape (sameTypeShape: the same kind, the same declaration, as many
// parts, an anonymous struct's fields by name) and nothing when they do
// not. A function that pairs two types structurally is a recursion over
// those pairs and names only the kinds it treats specially, a function
// type's parameters above all, which a value enters the other way:
// matchTypeParams, embedsDowncastIn, refinementJoinShape, differsByEmbeds,
// typesMayOverlap. sameTypeShape has a case for every kind
// (TestTypeChildrenNamesEveryKind).
//
// Unification, equality and conversion (unifyIn, typesEqualIn,
// conversionCost, convParts) are written out per kind and do not go
// through these: what they do at a position depends on the kind around it
// and on the mode they run in. So are variance and exactParams,
// substitution, which rebuilds a declared type's fields, and the functions
// that spell a type.
//
// # Type expressions' types
//
// Every type a program writes (an `ast.TypeExpr`: a parameter's or a
// binding's annotation, a field's type, a type argument at a call, an impl
// header, a `where` bound, an alias's target, a subtype's parent, the
// type of an `as`) is resolved through resolveTypeExpr, which records the
// type for the node, and for each node nested in it, in
// FileAnalysis.TypeExprTypes: one map write per node resolved. The type
// is the one the position gives it, so inside a generic declaration it
// names the declaration's own parameters (`List<T>`), and an alias's use
// records its target. A resolution that fails records the node with no
// type; one that succeeds later replaces it, and a failure never replaces
// a type. The IR builder reads a generic instance's type arguments here
// where an annotation spells the instance (internal/irbuild package doc,
// "Instantiation and dispatch").
//
// The table is part of FileBodies and is written only while the file is
// checked, as ExprTypes is. Type expressions in declarations are resolved
// before the bodies are walked or while BuildTypes types the declarations
// (typingDecls); that share is kept (keptDecls.typeExprs) and starts the
// table again whenever the bodies are rebuilt, since a recheck grafts the
// new bodies into the old declaration nodes. A body's are recorded again
// when the body is checked again. A file built from its interface and never
// checked (another file of a check against interfaces, which irbuild
// declares to lower one file alone or a REPL input) holds that share
// alone, so every type expression the lowering of a declaration reads is
// resolved there, an impl block's receiver included (buildImplBlockTypes).
// The stdlib's state writes the table with
// the rest of each file's analysis. [VerifyElaboration] holds every type
// expression of a checked file to a record, apart from the heads of
// literals and patterns, which name a variant or the type the literal's
// ExprTypes records, a decorator's arguments, which a derive lowers to the
// impl block it writes, and an impl item's ImplIface, which is its block's
// Interface node. The universal Debug the builder writes for a type a
// block declares is never checked, since no impl can name such a type;
// [UniversalDebugTypes] gives its type expressions their types from the
// checker's type for the declaration, and its patterns' heads their
// variants.
//
// # Patterns' types
//
// checkPattern, which every pattern goes through in every position (a
// `case` arm, a binding, a parameter, a lambda parameter, `if P = e`, `is`,
// a subtests row, and each pattern nested in one), records in
// FileAnalysis.PatternTypes the type of the value the pattern matches,
// and a distinct destructure statement (`JobId(n) = job`) records its
// value's. A pattern whose head names a type (`Point{x, y}`, `JobId(n)`,
// `self(n)`) needs no record of the declaration beside it: the checker
// requires the head to name the value's own type, so the type is the
// declaration. A head that names a variant (`Some`, `Shape.Circle`,
// `.Rect`, an aliased `I[x, ..]`) records the variant's declared name in
// FileAnalysis.PatternVariants, keyed by the head, the type expression
// TypeExprTypes leaves out; keyed so, a copy of the pattern that keeps
// its head keeps the record. A parameter's pattern is typed with the
// declarations (paramPatternType), so that share is kept
// (keptDecls.patternTypes) and survives a rebuild of the bodies, as
// type expressions' records do. [VerifyElaboration] holds every pattern
// node to a PatternTypes record and every variant-naming head to a
// PatternVariants record, apart from the `_` arm of a `case` with no
// subject, which matches no value; a literal pattern is a value of its own
// type and is not held. Nothing is recorded about how a pattern is
// lowered.
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
//
// # Subtypes and validated types
//
// A subtype (spec §4 *Subtypes*; refinement.go) is a `subtype`
// declaration, `subtype Port Int when fn p -> p >= 1 and p <= 65535`, a
// DistinctType with Subtype set (refinementOf answers it; the Go names
// keep "subtype"). Its rule, when it writes one, is DistinctType.Rule,
// as written. A struct with a `when` rule is a validated struct
// (StructType.Rule). A `type` with a rule is a validated type, and a
// `type` whose inner type is a subtype is built the same checked way;
// checkedDistinctOf answers both. Neither converts to anything; each
// enters only through a literal, `try_from` or `unchecked`
// (checkedCtorArg), and its literal's checks are its inner type's then
// its own rule (RefinementChecks). A subtype needs a rule anywhere in its
// chain, and one of a single parent needs its own
// (checkSubtypeHasARule): otherwise it is its parent under another name,
// which is a typealias.
//
// `A and B` of subtypes is an intersection (NewSubtypeIntersection): a
// DistinctType with Subtype and Intersection set, whose parents are its
// members, held sorted in TypeArgs so substitution and identity treat
// them as type arguments. A subtype with no rule of its own, an
// intersection among them, has exactly its parents' values, so a value
// converts into it when it converts to every parent (intoRulelessSubtype,
// beside the widening up in unify.go and refinesInto); this is the one
// conversion down. checkIntersections reports an interface beside
// subtypes that their base does not implement.
//
// A generic typealias is an AliasTemplate in the registry: a use
// instantiates its target (instantiateAlias), and checkAliasBounds
// reports a use whose argument breaks the alias's `where`.
//
// A validated type, and a `type` over a subtype, implements std's
// `TryFrom` of its base, a generic one at its parameters: the front end
// writes its `try_from` as that impl's (tryFromImpl in
// refinement_synthesis.go), and a hand-written impl of that one source
// type is an error (reportSuppliedTryFrom). A literal of such a type is
// checked through a `T.try_from(v)` call typed in the literal's file
// (refinementCheckCall), which reaches the impl.
//
// Whether a `type` with no rule of its own is over a subtype is a fact
// about names, which the front end writes its functions before any
// resolve: it writes the impl and the block with `unchecked` for every
// `type` whose inner type is written as a name that could be one
// (couldNameSubtype), and withholds both (ast.ImplBlock.Withhold), so
// until they are released they are empty inherent blocks no pass sees
// anything in. The annotation pass releases them (releaseWithheldRefinement)
// when the inner type resolves, through the file's own references and an
// alias's in its declaring file, to a subtype or an intersection with one;
// it stubs them then, so a dropped block never had a symbol, a completion
// or a TryFrom impl. Each build decides again, and a block a build of the
// same nodes kept loses its symbols when a later one drops it. A parent a
// declaration names through a typealias is checked through the target's
// `try_from`: the checker resolves the generated call's owner through the
// alias, and the IR builder calls the function by the declaration the
// checker resolved it to, which this file may have no name for
// (resolvedOwnerPlan).
//
// A subtype's value is a value of the types it refines, its
// parents: DistinctType.Inner, the first, and MoreParents, the rest of
// `subtype PositiveInt NonNegativeInt and NonZeroInt when ...`. It converts up
// to each parent, each of their ancestors and the base every parent
// reaches, implicitly and inside containers, by the unifier's assign modes
// (unify.go), which try each parent in turn. Two subtypes of one parent
// never convert to each other, and nothing converts down.
//
// Where values meet with no type written (an `if`'s or `case`'s branches,
// a collection literal's elements, a lambda's results, a range's
// endpoints), a subtype beside an ancestor of it, or beside another
// subtype they share an ancestor with, joins as the nearest shared
// ancestor, at the top and at each position a value converts at
// (refinementJoin, nearestSharedAncestor; branchesJoin tries it first when
// no type is expected), and a range with no type written ranges over the
// base (checkRangeLit). A later arm or element is checked against the
// earlier ones' type with its subtypes erased (earlierArmsExpected,
// checkLitElem), so a literal there is its base's, until the values have
// joined: then the literals among them, in any position, are the nearest
// std numeric subtype at or above what the other values join to whose
// rules each literal satisfies, decided in the checker, since those rules
// are known (narrowLiteralJoin, numeric_subtypes.go). A numeric value has
// a range (spec §4 *Value ranges*; ranges.go): an interval with a bit for
// "off zero", from literals, its type's std subtypes and literal-bound Int
// rules, comparisons with constants, exact arithmetic (+, -, * and unary -
// on Int, Decimal and Duration nanoseconds; `+` of non-negative Floats)
// and a few std results (callRange), and the elements of a range
// literal, `Iter.from`, a collection literal or an adapter chain over
// them for the parameter of a callback a std Iter function passes them
// to (element_ranges.go: noted with its source as the call is checked,
// seeded when the callback is; a callback whose parameter is a subtype
// that range proves takes the type the position passes, by one judgement
// for a lambda's annotated parameter, a partial capture's slot and a
// function passed by name, elemProves). As each expression is checked,
// rangeTyped records its range and types it as the narrowest std numeric
// subtype the range proves when the range rests on something other than
// literals (typeForRange), so `1 + 2` stays an Int; checkNodeExpecting
// converts any value whose range proves the subtype expected
// (provenRefinement), a literal-bound Int subtype over Int such as Int64
// included. Bindings are immutable, so a binding's range (bindRange)
// holds wherever it is read, with no fixed point for loops or recursion;
// nothing relates two bindings. An unannotated `once` takes the subtype
// its value's range proves as its type (onceRangeType). Every other
// operation on a subtype is its base's. A type parameter bound
// to a subtype widens the same way when a later argument is an
// ancestor (widenToRefinementAncestor). A generic call, direct or piped,
// solves its arguments through argSolving, which checks a literal
// argument against what earlier arguments solved with subtypes
// erased, or against the std numeric subtype they solved when the literal
// satisfies it (a numeric literal argument is checked after the arguments
// that solve its parameter on their own, deferredNumericLiteral, so the
// order is irrelevant), undoes a widening an earlier argument no longer fits as a
// mismatch at the later one (widenedPast: a function taking the
// subtype, or taking an embedded type a later value of its enum
// widened the parameter to, as widenToEnum does; the function would be
// called with values it was not written for), checks a collection
// literal whose elements are an ancestor or the enum of what earlier
// arguments solved against the widened type, under the same rule
// (widenedForLiteral: it checks the literal once with those parameters
// open to learn its type, dropping what that check reports), and
// solves a parameter a lambda argument's result names (Iter.reduce's
// accumulator) to its base, since the lambda computes its next value.
// Std's lengths, counts, sizes and indices are NonNegativeInts this way
// without a `try_from` where they meet a literal. Inside a function type
// the widening reaches the expected side too, where a function argument's
// result meets a parameter an earlier value solved: `Iter.iterate(p,
// next)` with p a PositiveInt and next an `(Int) -> Int` solves T to Int.
//
// A condition narrows a parameter or local binding in the code it guards
// (spec §4 *Narrowing*; subtype_narrowing.go): condFacts reads
// comparisons with a constant (an expression whose range is one value),
// `Iter.is_empty` and comparisons with an empty literal, through `not`,
// `and` and `or`, into a range or non-emptiness per binding, and
// narrowSubtypes meets the binding's range with it in checker.symRanges
// and records the subtype the range proves in checker.narrowed, which
// every read of the binding takes (checkIdent), so ExprTypes, hover and
// the IR builder see it. The regions are the branches of an `if`, the
// right operand of `and` and `or`, a `case` arm's body after its guard or,
// in a case with no subject, after its condition and the earlier ones,
// and the rest of a block after an `if` that leaves or an assertion
// (factsAfter). The same map holds a case arm's `embeds` narrowing
// (narrowing.go), which the IR builder lowers as a projection; a subtype
// narrowing is a static fact, since the value is its base at run time. A
// binding of a user's subtype keeps its type, though its range narrows.
//
// Every parent
// reaches one base (checkRefinementParents), so RefinementBase follows
// Inner; a parent that reaches the subtype itself is a declaration
// cycle the type builder leaves out (distinctCycle). RefinementChecks
// orders the rules a value satisfies, each once, a parent's ancestors
// before it, the parents as written, the subtype last: the order a
// literal's compile-time check reports the first that fails in, with the
// path to it (RefinementStep.Via).
//
// A generic subtype, `subtype NonEmptyList<T> List<T> when ...`, holds
// its parents in terms of its own type parameters, and an instance's
// parents and base are those at its type arguments (RefinementParents,
// RefinementBase, both through DistinctInner), so a NonEmptyList<Int>
// converts up to List<Int>. Its arguments convert as its parents hold them
// (variance.go reads a subtype's every parent, std's among them), and a
// conversion of one converts its base (conversions.go, refinementRunTime).
// A collection literal where one is expected at open type arguments solves
// them from its elements (distinctLiteralType). A `where` on the
// declaration bounds the parameters as a struct's does. A struct
// subtype takes no type parameters.
//
// The right operand of `/`, `%`, `<<<` and `>>>` may be a subtype in an
// impl (operandSubtypeInterfaces): std's `impl Divide<NonZeroInt, Int> for
// Int` and `impl ShiftLeft<NonNegativeInt, Int> for Int`. The impl is
// selected by the operand's base (subtypeOperandFits), and the operand is
// then held to the subtype by its type or its range (checkSubtypeOperand,
// operand_subtype.go), so a checked program never divides by zero or
// shifts by a negative count; the VM's checks are a backstop.
//
// A subtype implements no interface of its own: it has its base's impls,
// and at run time it is its base's value (irbuild erases it). So every
// record the checker makes of a value's run-time shape reads a distinct
// subtype as its base: the Iter view a source becomes (iterViewOf), a
// spread's head (checkSpreadHeads), and whether a value enters an
// interface as itself (carriesOwnType, entersAsItself). The impls a call
// solves a bound's open type arguments from are the base's too
// (solveFromWhereImpls), so `Iter.mean` over NonNegativeInts solves its result
// from Int's `Averageable<Float>`.
//
// A validated struct is a struct whose values satisfy the rule. It
// converts to nothing; its rule only constrains construction.
//
// A value enters a subtype, a validated type or a validated struct in
// four ways:
//
//   - a literal of the base where the subtype is expected (a typed
//     literal with no `${...}` among them, `Duration"15m"`), or as the
//     argument of its call form (`Port(8080)`), a struct literal whose
//     fields are literals, or a collection literal whose elements are,
//     through nested collection and tuple literals, where a subtype of
//     a collection, or of a subtype of one, is expected or as the
//     argument of the call form, or a range literal whose endpoints are
//     literals where a subtype of Range is expected (checkRangeLit)
//     (checkCollectionRefinementLit, literalInnerOf, refinementCtorArg).
//     A literal headed by a collection's name (`Vector[x]`, `Set[x]`)
//     builds a subtype of that collection as the unnamed one does
//     (shapeHeadSubtype). A literal of NonEmptyList, NonEmptyVector,
//     NonEmptySet or NonEmptyMap themselves needs no literal parts when
//     it writes an element or spreads a value of a non-empty type
//     (literalHoldsSomething): its shape proves the rule, so it is not
//     recorded and no rule runs on it. Otherwise the rule is
//     evaluated when the program is compiled (vmhost's checkRefinements
//     over irbuild's RefinementSites). A chain that reaches no base, its
//     parent naming no type or a declaration cycle, is reported where it
//     is declared, and a literal of it is checked as itself, since the
//     chain's end is the subtype again (checkRefinementLiteral);
//     A literal of a generic subtype is checked through its
//     `try_from` at the literal's type arguments: the checker types the
//     call `Name.try_from(v)` in the literal's file
//     (refinementCheckCall, RefinementStep.Check), its nodes at the
//     literal's line and below-zero columns no token has, and v,
//     RefinementLiteralName, the literal's value. Its owner is recorded as
//     the subtype's declaration (refinementDeclSymbol), and
//     receiverOriginForReceiver takes that owner's origin from the record,
//     so the file need bind no name to the subtype, and a type of its own
//     by that name changes nothing; an error the call reports, a type
//     argument that breaks a bound, is reported at the literal. Its declaration types
//     the same call with each parameter a value of its bounds' interface,
//     which meets them whatever implements them, and again at the first
//     witness type its bounds admit (Int, String, Float, Bool, Unit)
//     (recordRefinementRuleCheck, FileAnalysis.RefinementRuleChecks), so
//     vmhost refuses a rule with effects whether or not a literal of it
//     is checked;
//   - `T.try_from(x)`, a `Maybe<T>`, generated for every subtype and checked
//     `type` (synthesizeRefinements; a checked `type`'s is its TryFrom
//     impl's, whose parameter keeps the interface's name `value` and binds
//     the argument under a fresh one when the rule reads a `value`), which checks each parent's `try_from` in
//     the order written and then its own rule on the first parent's value, each
//     call of a rule an ast.RefinementRule, a Bool that is False when the
//     rule traps. A generic subtype's `try_from` and `unchecked` declare
//     its type parameters and bounds as their own (refinementHead), so a
//     call instantiates them as std's functions on a generic type are;
//   - `T.unchecked(x)`, which checks nothing, allowed only in the file
//     that declares T;
//   - an impl or function that returns T, whose body built it one of the
//     ways above.
package analysis
