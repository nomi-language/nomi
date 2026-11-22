// Package vmhost is Nomi's embedding API: a Go program loads a Nomi program,
// runs it, calls its functions and runs its tests on the VM.
//
//	p, err := vmhost.Load("policy.nomi", vmhost.WithHosts(myHosts))
//	quote, err := p.Call(ctx, "price", vmhost.Fields{"weight": 12.5, "distance": 800})
//
// Loading runs the shared front end (internal/frontend), lowers the program
// to IR (internal/irbuild's GenerateIR) and opens the VM (internal/vm) over it.
// `nomi run`, `nomi test`, `nomi check`, the REPL and the tour all run
// through this package, so an embedder runs a program exactly as `nomi run`
// does.
//
// A program that reaches a function the VM cannot run fails with a *Blocked
// before the program's first effect: for each body the compiler could not
// lower, the diagnostic `nomi check` reports at its source. The diagnostic
// leads to the cause rather than to the call that reached it: a body
// several calls below a root names the calls in a hint (`main` →
// `run_prompt` → `run` → `evaluate`, from vm.Unretained.Chain), and a body
// that stopped at a call to a function whose signature the compiler refused
// is reported at that signature's type, with the type the refusal comes
// from (irbuild.Decline.Signature, signatureDiagnostic).
//
// It is a public package because an embedder is a separate Go module, and a
// separate module can import only the public packages of this one.
//
// # Embedding
//
// Nomi source binds no Go code. A Go program that embeds Nomi supplies the
// functions its program declares as host tables (WithHosts) of
// hostadapt.Func values over the VM's own Values: one for the `host fn`s and
// `host type`s its program declares, keyed by the declaration's key
// (frontend.HostKey): its name in the entry file, and in another file the
// file's path within its module without `.nomi`, `.` and the name
// (`helpers/util.greet`), under the dependency's name for a dependency's
// file (`lib/sub/thing.ping`). irbuild keys the crossing by the same
// function, from the checker's FileAnalysis.Origin. A machine binds std/compiler's table first and every
// other table under its keys' project names (hostadapt.ProjectTable,
// hostadapt.ProjectName), so a project file named like a Go-backed std
// module (`regex.nomi` binding `compile`) never shares a crossing with
// std's row (TestHosts_AProjectFileNamedLikeAStdModuleRunsBesideIt).
//
// A table is called once, when the machine binds it, with the machine's
// hostadapt.Env. Each Func then receives the calling activation's *rt.Frame
// and its operands, every parameter filled: a `host fn`'s defaulted
// parameter is evaluated in Nomi before the call crosses. A function value
// among the operands is called back with Env.Call on that frame, which runs
// it on the same machine and answers its result. A closure that traps
// answers the trap as Env.Call's error, and a Func that returns that error
// fails the call that crossed into it, as a trap in Nomi code does. A
// closure's effects count where the closure is made, so a host's list must
// name them though only the host calls it. The frame's Go context
// (rt.Frame.Context) carries the deadline of the Context in force (`with
// App.context = Context.with_timeout(...)`) and ends when the task is
// cancelled, by Task.cancel or by the context passed to Call. A Func that
// waits selects on it. When it ends, the Func either answers a value, and
// the task goes on with it, or calls rt.CancelIfDone(fr), which stops the
// task as a cancelled std wait does. A Call's own activation is stopped as
// a task is (rt.NewHostFrame): when the context passed to Call ends, the
// call unwinds and answers that context's error, context.Canceled or
// context.DeadlineExceeded. hosttable_test.go holds each of these.
//
// A host that is not Go answers a program's `host fn`s in JSON instead,
// through the WASI reactor's one import, nomi.host_call (vmrunner's
// WithHostCalls; the tooling reference's "Calling exported functions"):
// each argument is encoded by its type's ToJson and the answer decoded by
// the result's FromJson, std/json's conversions an `export fn`'s arguments
// and result cross by. The checker records which `host fn`s have that form
// (analysis.FileBodies.HostJSONForms), irbuild instantiates their
// conversions (irbuild.HostFn), and BuildExportsImage carries them
// (ir.HostFn). A function value, an opaque `host type` or a stream has no
// JSON form, so such a function is answered only from Go. A build for such a
// host loads with WithLoaderHosts, which admits a `host fn` no table
// answers.
//
// A program's files are a [FileSystem] (WithFileSystem), adapted to
// internal/stdio's Backend. [DiskFiles] and [NewMemoryFiles] are the two
// Nomi builds in.
//
// # Holding a program to a list
//
// A host that runs code it did not write, such as a pricing rule a user
// uploads, loads it with [WithEffects] and the names of the effects
// it may have, the names nomi.toml's std lists use
// (`std::io`, `std::startup`) and `host:<key>` for the host's own
// functions. With no names the program may have no effects:
//
//	p, err := vmhost.LoadSource("rule", src, vmhost.WithEffects())
//	rate, err := p.Call(ctx, "discount", vmhost.Fields{"items": 12, "loyal": true})
//
// The load walks what every function a host can run reaches (main and the
// boot, every function the entry declares, since Call may call any, and
// the test cases), as the build's walk does, and refuses the program with
// a [*EffectError] before anything runs: the diagnostics `nomi check`
// prints, and each call as data (its name, its site, the chain from a
// root with the kind of each step). Run, Test, Cases and Call hold the
// program again from their own roots, to the host's list and the
// project's nomi.toml, which both apply. A program loaded without the
// option is held to nomi.toml's lists where it runs: Run, Test, Cases,
// Call and the image a build makes. Test reports a refusal as the file
// failing; Cases, which answers data, answers each selected case failed
// with the error and runs none. LoadMarked and ToolMarked, which load a
// block for the tour's runner, hold it at the load from what `nomi check`
// holds, so a block over its lists fails as one that does not compile
// (TestEffectLists_EveryEntryPointRefuses). Evaluate refuses every
// effect already.
//
// A list names every effect but a pure call, a held one, which acts
// only through a value its operand already holds and is named where that
// value was made (internal/vm's effects.go): making a channel is
// `std::channels`, and sending on one is nothing; a dispatch into a std
// implementation of a type only std code makes is named where std makes
// the value, so `storage.local` is `std::storage` and `Store.put` on its
// store is nothing; and a `dbg`. A `dbg`
// prints and is an effect the walk reports, named `dbg` (effectName), but
// every list allows it (effects.Listed), so no list needs an entry for it
// and the tightest lists hold none: `nomi build` refuses a program that
// holds one, so none ships. A hover shows it with nothing about the lists
// (effects.ReachedLine), and `nomi effects --tree` shows it as needing no
// entry.
//
// One rule holds a call to a list (effects.Allows, through
// standingsOf, which notAllowedBy filters to the refusals): by its name,
// which the list must hold. nomi.toml has two tables, each with a table of
// entries under it, and each holds the calls written in the files it
// names; an entry is a std list that narrows its table for one key's
// files. standingsOf picks the table and its entries by the module whose
// file holds the call, and decides both the same way. [effects.self]
// holds the project's own files: its std, with std_in_tests for the
// tests, and the entry for the file's module under
// [effects.self.modules], keyed by the file's import path (importOf).
// [effects.deps] holds the files of every module the program's code comes
// from other than the project: its std, a ceiling when the table sets
// one, with std_in_tests for the tests, and the project's entry for the
// module under [effects.deps.modules]. When the project has
// [effects.deps], a dependency's calls are held too to the
// [effects.self] in its own nomi.toml (moduleAt reads it, once per
// module), whose std and entries declare its own code. A table the
// project leaves out holds nothing. A call over several lists is reported once,
// over the module's own list first (EffectNotDeclaredCode, whose help
// puts the fix in that module), then the entry, then the table the entry
// is under (EffectCallsNotAllowed). The dependency's own std names are
// checked by the code that checks the project's, at its own nomi.toml. A
// key under [effects.self.modules] must name a module of the project
// (projectModules), and entries_required is checked in each table over the
// modules whose files the program holds (ownModules, requiredModules). A
// host's list holds the whole program and is never a dependency's: a
// program held only by WithEffects reads no dependency's table.
//
// The roots `nomi check` holds to the lists (checkedRoots) are main and
// the boot, the exported functions, the project's public functions and
// the test cases. A public function is one another module can call
// (publicFuncs): a `pub fn` outside an `internal/` directory and a test
// file, at the top of a file or in an inherent impl, and each function of
// an interface's impl for a `pub` type that is not `opaque`, which
// another module can build. The walk takes them as late roots
// (vm.ReachedEffects): after main's walk, so a function main reaches keeps
// main's chain, and then from the bodies a value of each type their code
// makes may run (vm's madeBodies), since such a value can leave through a
// public function. So a library with no main is held to its own
// [effects.self] by everything it publishes, and std_in_tests holds what
// only its tests reach. A dependency's public functions are not roots:
// its calls count where the program reaches them. A public generic
// function is a root through its abstract body as well as its instances
// (irbuild.Result.Abstract): the body with each type parameter held as its
// bounds' interface, lowered only for this walk into modules nothing runs,
// so a use of a bound, a dispatch or, where no value of the type selects
// the implementation, a call through the bound (ir.NewBoundCall), reaches
// every implementation the program retains, a generic function it calls at
// its type parameters is read the same way, and the answer holds for every
// type a caller gives it. The functions of an impl block over a generic
// type, and each generic function of a block, are roots the same way. One whose abstract body does not lower is an
// effects-not-determined error under [effects.self] at its name
// (undeterminedDiagnostics), which `nomi effects` prints too
// (Reach.Undetermined). A relinked program (filerecord.go) and a lowering
// of one file build no abstract bodies, so a program whose [effects.self]
// holds generic code of its own is never relinked: a whole load answers
// it after every edit. Run holds main,
// the boot and the exports, and a build's pruning, a test's reach and
// the target check keep their own roots (reachRoots).
//
// A refused call in a dependency's files is reported at the call, and its
// diagnostic's Related holds each place the project's own code leads
// there (leadsTo): each edge out of a function in the project's files into
// one that can reach the call, found by the walk's own backward pass
// (vm.EffectWalk.Leads, the pass that answers Reaches), at the callee or
// value it names, worded from the project's side with the edge's kind as
// a chain step is ("your code has the effect std::http in
// lib.example/prices here: `main` calls `total`"). A call in the project's files has none.
// A *EffectError keeps every call in Calls and Diagnostics; its text
// (Shown, which Unwrap answers too) gives the first call of each name over
// each list, with a help line counting the rest, and of each call's
// related places the first, counting the rest, unless AllCallsEnv is set
// (`--all-calls`). `nomi check --format json` and the language server
// read every call and every place. CheckLowering, the language server's lowering, walks
// from the roots `nomi check` holds once, lists or not, and answers each
// call it reaches beside the problems (reachedCalls), with the line a
// hover on it shows (effects.ReachedLine). CheckJSON writes `nomi
// check --format json`'s records.
//
// Reached and Program.Reach answer the effects a program has, from the roots
// `nomi check` holds (Walk per root set), with the module each call is
// charged to, and the problems `nomi check` finds in the lists
// themselves (ListProblems, effectListProblems); `nomi effects`
// prints it. Reach.Tightest is the
// smallest pair of tables the walks pass (tightestLists, beside
// notAllowedBy and held to it by TestTightestLists_PassAndEveryNameIsNeeded:
// every call passes, and taking out any one name, or turning a flag false,
// refuses one), so the tables it writes are ones the check passes. Reached
// loads as Check does. What `nomi effects` and the language server's nomi.toml
// say is one set of answers on Reach: ReachedAll adds the reach of each
// program frontend.CheckedFiles names, Why and WriteWhy are `--why`,
// Trees is `--tree`, each root's calls down to each effect, read from
// the edges the same walk recorded (vm.EffectWalk.Tree), each call
// standing as the check holds it, HeldChanges the differences `--check`
// holds a table to, and
// ModuleReaches and WriteModuleReach the effects each dependency has, as
// `nomi add` prints them. reachedCalls is what an editor's hover says of
// the effects code has, read from editorWalks: the walks `nomi check`
// holds to the lists, then the same walk from every function of the
// program's lowering they do not reach (Roots.Besides), whose lines fill
// only the places theirs leave empty, as do the lines of what only the
// public functions reach (vm.EffectWalk.FromLate), so a library open
// beside a program that calls it reads as the program says. Nothing else reads that walk: the
// lists and `nomi effects` stay rooted where `nomi check` roots them, and
// a build's pruning and a test's reach where the program runs. It gives each effect call's line ("Has effects: `std::fs`."), a read of a std `once` whose initializer has effects among them (`io.stdout`, shown on the name it reads, shownSpan), and, from the same walks, the lines of
// each function that can reach one, at its name and its calls, and of
// each `once` of the program whose initializer can, at its name and its
// reads (reachedFuncs, over vm.EffectWalk.Reaches), then the line of the app
// fields each function uses, which no list holds (vm.EffectWalk.Uses,
// effects.UsesLine). The same walks say what can act
// (vm.EffectWalk.Acts and ActCalls): a function or a call that can reach
// a host function its declaration does not mark pure, by the test that
// decides whether a test case is pure, held crossings included. A function
// or call that can act and reaches no effect call has the line that it acts
// on what it is passed (effects.HeldLine), and a call through an interface
// only some of whose implementations can act the line that it may
// (effects.MayLine), which stands alone at that call. A line is Marked,
// and an editor draws its token bold, at each call that can act (a
// `dbg` among them, at its keyword, shownSpan) and at
// the name of each function, not a once, that can: a read of a once, a
// function named as a value and a once's name keep their lines unmarked,
// and so does a call the builder made where the source names a function as
// a value, which has no call syntax (actingCalls). ReachedMarked is
// reachedCalls for a tour block, whose hover in the browser ends with the
// same lines as the editor's.
// Program.StdEffects answers, for a std module LoadEmbeddedStdlib
// loaded, which of its functions a call to has an effect, and which of
// its `once` values a read of, and by what name, by the same walk started
// from each function and `once` initializer the module's lowering holds
// (vm.StdEffectCrossings), each generic function's
// abstract body (irbuild.StdAbstractBodies, or for a module lowered with
// each program the program's own, Result.Abstract), and each `host fn`'s
// crossing, and which functions the walk answers for at all: the stdlib
// reference marks them. A function every call of which the builder lowers
// to an operation of the machine (irbuild.StdLowersToMachine: `Iter.map`,
// `Set.insert`) is answered by the operation, with no effect of its own. A
// generic function whose abstract body declines is answered only when the
// module's tests instantiate it.
//
// What the list does not confine is in WithEffects' doc: the files
// WithFileSystem hands the program, the host's own functions, what a Go
// binding does inside, a nested std::compiler run, and time and memory.
//
// The rule that names an effect and the check of a call against a
// list are internal/effects's, which vmrunner calls too: a host that
// loads compiled programs (vmrunner.WithEffects, the wasip1 reactor's
// nomi_load_with) holds them with the same names, walk and words, over the
// decoded image. vmhost adds what only it can read: the module a call's
// file is in, the std modules a name may be, and nomi.toml's lists.
//
// # The engine and nomi check
//
// `nomi check` lowers the program as `nomi run` does, and a test file's
// cases as `nomi test` does, and reports each body a run would refuse as an
// error at its source ([CheckLowering]). A check that only type-checked
// would pass programs that then fail to run.
//
// The language server runs the same check in the background, on open, on
// save and after a burst of edits (internal/lsp's lowering.go says when and
// why). When the document's analysis holds the program `nomi check` would
// build, the check lowers from that analysis (checkLoweringAnalyzed)
// instead of reading the files again, several times faster;
// TestLoweringFromAnalysisMatchesTheSource (internal/lsp) holds it to a run
// from the text.
//
// A lowering from the analysis is kept per file for the last
// maxAnalyzedKept documents (checklowering_perfile.go). The next run of a
// document whose program holds the same files, with at most
// maxAnalyzedRelowered of them changed, lowers only the files the edit
// reaches and links them with the kept objects (irbuild.LowerFileAmong),
// as `nomi check` relinks a kept program. Each file is lowered after the
// files it imports. The set grows by two rules:
//
//   - a file whose interface hash or unannotated `once` values changed
//     brings every file that imports it, directly or not (importersToo);
//   - a file whose impl headers name other files than before brings every
//     file that sees one of its old or new impls, imported or not (seersOf,
//     over the headers analysis.ImplHeaderFiles reads, kept with the
//     lowering).
//
// More than maxRelowered files and a quarter of the program lowers the
// whole program instead. Interface and declaration changes are read from
// the old and new texts parsed afresh, since a recheck rewrites an edited
// file's bodies in place, and the old declarations are fingerprinted
// (irbuild.KeptLowering.WithDecls) so nothing made from a changed
// declaration is carried. A kept lowering that declines a body or refuses a
// construct is not used: the whole lowering words each blocker.
// internal/scalebench's `lsp lowering after an entry body edit, file by
// file` measures it, and TestLoweringPerFileMatchesTheSource (internal/lsp)
// holds these runs to the text's. A program the server opened from its
// record relowers its entry the same way (checklowering_stored.go).
//
// # Literals checked at compile time
//
// Every load, `nomi check`'s included, runs on the VM, before the program,
// the handler of each typed literal without `${...}` slots, in any quotes
// (literals.go), and the `check` of each checked literal, one whose
// tag implements `CheckedLiteral` (checked_literals.go). An `Err` is a
// compile error at the literal; a `LiteralError` names a static and an
// offset, which this package maps back to the source. A handler or check
// that can reach something acting outside the machine is refused, an
// effectful check once at the check, so a compile never has effects.
//
// The checker types a checked literal as its `build`'s result and records
// it in analysis.FileAnalysis.CheckedLiterals. irbuild lowers it as a
// `build` call plus a check site, a lazy cell over `check` and the statics,
// in the irbuild.LiteralSite list literals without slots use (irchecked.go).
//
//   - An untagged string literal whose expected type has a `CheckedLiteral`
//     impl is checked as a twin `TaggedString` (analysis/target_literal.go):
//     FileAnalysis.TargetLiterals maps literal to twin and
//     TargetLiteralBuilds twin to the chosen `build`, which irbuild reads in
//     place of the tag's reference (literalDispatch). Its site has an empty
//     Tag and the type in Type.
//   - The site calls the impl's `check_type` when it has one, with each type
//     argument's spelling (kindSpelling), and `check` otherwise.
//   - A generic type's handler (`impl CheckedLiteral<String, Route<P>> for
//     Route<P>`) is freshened per literal in the checker
//     (checked_literal_args.go) and lowered at the literal's instance
//     (genericLiteralImpl): the instance's impl item for a program type, an
//     instance of the std template for std's.
//
// The language server also evaluates double-quoted typed literals with no
// `${...}` this way, for hovers and diagnostics.
//
// A literal the checker typed as a subtype (spec §4 *Subtypes*)
// is checked here too (refinements.go): irbuild lists each one as a
// RefinementSite with the `try_from` of every subtype on its chain, and
// checkRefinements calls each on the literal, a validated struct's on the
// record its literal fields build. A `None` is the error `-1.0 is not a
// NonNegativeFloat: <rule> is false` at the literal, and a trap in a rule
// is an error carrying its message. No literal passes unchecked: one whose
// check irbuild could not make (RefinementSite.Unchecked) or whose
// `try_from` no linked module defines is an `internal:` error at the
// literal (UncheckedRefinementCode), and one whose check reaches what the
// machine cannot run is an error there that names it. Each declared rule
// (irbuild.Result.RefinementRules) is refused when it can reach an effect,
// whether or not a literal is checked against it, and a literal of a
// subtype so refused is not reported again. A generic subtype's rule
// is walked through its `try_from` at its bounds' interfaces, whose calls
// dispatch to every implementation the program retains, at a witness
// type, at every instance the linked program has, and with every function
// of every impl of its bounds' interfaces, concrete or an instance of a
// generic one: one of them is the argument's whatever type it is, so an
// effect in any is an error at the `when` that names it (Loud.to_string
// calls io.print).
//
// std/toml's `Toml` is a checked literal over a program std module. Its
// check and build cross into the internal std/toml/host, over
// internal/stdtoml: it follows the text far enough to place each slot (a
// key part, a value, or refused in a string, a comment or touching a
// token), reads the text with a stand-in per slot with
// github.com/BurntSushi/toml, and maps its error back to a static and
// offset. `ToToml` returns a `Toml.Value`, never text; `build` writes each
// slot with `Toml.Value.text` in Nomi (strings and table keys through
// `std::toml::host.quote`). A shared std body that takes a std interface with no
// rt table as a value declines (irbuild's stdprogiface.go), which is why
// `Toml` cannot be shared and why std/compiler, which is shared, takes its
// manifest as a `String`.
//
// # Kept programs
//
// `nomi run`, `check`, `test` and `build` keep what they make of a program
// in the artifact cache, so the next command over the same files reads it
// instead of checking and lowering again. internal/progcache holds the
// record: its key, the files it was built from and when a lookup trusts
// them. This package decides what goes in it ([Cache], cache.go). A file's
// program is analyzed in the mode its own tests decide, so run, check and
// test share one record per entry file. `nomi build` keeps its image per
// target in its own record (cmd/nomi's build_cache.go, [OpenCacheFor]) and
// also writes the lowering's record.
//
// Only a program that loads cleanly is kept: no
// diagnostic, no warning, no declined body, and nothing its main, boot or
// test cases reach that a machine cannot run (Program.runsClean). So a hit
// says the program checks, and a kept program reports nothing a fresh load
// would word differently. A program with advice (Program.Advice) is kept,
// and the record holds the advice, which a check that reads the record
// writes as a fresh check does. The record holds the entry, the root, the
// phrase tables, the advice and one IR image (cacheImage): what main and
// boot reach
// (vm.PruneImage) for a program without tests, every module for a file
// with tests. Positions keep their paths. A hit decodes the image into a
// [Program] whose analysis holds only the entry (parsed again for a test
// file, whose cases are selected from its nodes), with the machine and
// reporting a fresh load uses. The CLI looks the record up before it loads
// the program.
//
// The record names what the program was built from (Program.inputs): the
// project root's tree and each dependency module's, walked for every
// `.nomi` file by presence and for the text of every other file a build
// reads there (manifests, catalogs, go.mod: sourceTree); each of the
// program's own files with its text; each `embed` pattern's base as a tree
// of every file (analysis.EmbedInfo.Bases); each embedded file on its own,
// since a tree walk skips the hidden names and node_modules an `all:`
// pattern embeds; and each catalog outside those trees. A build reads a
// source's text only by loading it, so an edit to a file the program does
// not load, another test file of its project, leaves the record a hit. A
// source that comes or goes is a miss, since it can change what an import
// names (a file import where a directory import was, spec §14 *Directory
// imports*) or what the catalog check reports.
//
// At 50k generated lines (internal/projectgen, Apple M2 Max) a hit takes
// 0.02 to 0.4 s against 2 to 3 s fresh, mostly decoding the image.
//
// # File records
//
// Beside the program's record, a load keeps a file record (filerecord.go,
// kind "files", opened from the program record's snapshot with
// progcache.Entry.Sibling): per project file its interface
// (analysis.StoredInterface), its text's SHA-256, its facts
// (analysis.FileFacts; analysis/stored_facts.go describes them and the
// reductions over them), its body summary and whether it declares keyword
// declarations; per program the lowering kept per file
// (irbuild.KeptLowering), the missing-impl supply (analysis.ProgramSupply)
// and the application types; and per file its advice, which depends on its
// own signatures and bodies alone, so a relink works out the advice of the
// files it checks again and keeps the others'. When the program record
// misses only because `.nomi` sources changed, came or went
// (progcache.Entry.ChangedSources), the command relinks: it checks and
// lowers only the files the edit reaches, against the others' stored
// interfaces and kept objects.
// filerecord.go's file comment lists the steps. These hold beyond them:
//
//   - The changes must be the program's: a file whose text changed must be
//     one of its sources; a source that came is a miss unless a changed
//     file imports it, since a kept interface's imports name only the
//     files that were there and an import that named a directory names a
//     file once it is there; and when only sources the program does not
//     load went, there is nothing to check and lower again, which is a
//     miss too.
//   - Each file checked again must report nothing (analysis.FileCheck.Clean):
//     neither its own diagnostics nor the errors its build works out about
//     the program's impls (a second type-owned impl block, colliding
//     impls, the orphan rules), which a whole check reports with the
//     entry's. An edit that adds a declaration breaking one of those is a
//     miss, and the whole check reports it.
//   - More than maxRelowered files and a quarter of the program (a hub's
//     signature) checks the whole program instead, since each relinked
//     file's check and lowering link against every other file.
//   - When impls changed or a file came or went, the supply is rebuilt from
//     the entry built against every other file's interface, registering
//     every impl and checking no body (analysis.ProgramInterfaces.Supply).
//   - The kept lowering lists the typed literals the program checks at
//     load (irbuild.KeptLiteral: each site, with its cell named by its
//     index in its file's object), so a relink checks every one again over
//     the linked program, the relowered files' and the others'. A literal
//     whose text the edit did not touch can still fail when its handler's
//     body changed; a failure is a miss, and the whole load reports it. A
//     literal's value is not kept, since one need not encode (a compiled
//     `Regex`).
//   - An added file gets a new unit (KeptLowering.AddUnit), a removed one's
//     unit goes (RemoveUnit). A file whose declarations moved is a miss when
//     an object not lowered again holds a position in it.
//   - A lowering that needs an instance of another file's generic
//     declaration no kept object holds (irbuild.FileMiss.Undefined) is
//     redone with that file's bodies (ProgramInterfaces.SetBodies), and the
//     instance is kept in that file's object, where a whole lowering puts it
//     (Result.Rekeep), one made for it if the file had none: every
//     function made from a project file's declaration is in that file's
//     object, so the file's next lowering drops it when its body changes.
//   - A body edit of a generic declaration drops the instances of it its
//     file's object held for other files. When kept objects of files not
//     lowered again name them (irbuild.FileMiss.Dropped), the relink starts
//     again with one such file per instance added, lowered after the edited
//     file and reading it with its bodies, so it asks for the instance again
//     and the instance goes back into the edited file's object. The other
//     files that name an instance are not lowered again: their objects name
//     it by its linkage name, which the new instance keeps. The relink is a
//     miss if a dropped instance is not defined after the last file
//     (Result.Undefined). A dropped instance a dispatch entry of the
//     edited file's object reached (an instance of a generic impl another
//     file boxes into an interface) is asked for by holding its type
//     erased, which names nothing, and is a miss. At 5k generated lines an
//     edit of core/util's `first_or`, which seven files call at Int, lowers
//     two files and runs in about 0.18 s, against 0.45 s whole.
//
// The entry imports every unit no other unit imports, so any leaf interface
// edit rechecks it, and its check registers most of the program. At 50k
// lines a leaf body edit relinks in about 0.3 s and a leaf signature or impl
// edit in about 1 s, against 2.6 s whole; an edit that cannot be relinked
// costs a whole check and lowering plus about 50 ms. internal/scalebench's
// `nomi check, after a ... (file records)` operations measure each edit in
// process. `NOMI_DEBUG_ARTIFACT_CACHE=1` reports a relink (`relinked 2 of 8
// files`), a relowering with another file's bodies, or why there was none.
// A panic while relinking is a bug in Nomi: the command checks the whole
// program instead and writes the panic to stderr, which the cache's
// harness compares with a run that keeps nothing.
// cmd/nomi's artifact_cache_test.go and internal/expectation's
// TestCachedRunsMatchTheRecords hold a kept or relinked program to a fresh
// one, and artifact_cache_sources_test.go a project of several test files
// after an edit, and a file added where a directory import was. They test
// with every case run (RunAll), so a stored pass answers none.
//
// # Stored passes
//
// `nomi test` does not run a case again when nothing it can run changed
// since it passed (testpass.go). Test walks each case from its body, its
// group's boot and the boot's startup (vm.Machine.ReachCase, the walk that
// finds what a case cannot run), and gets the effects its code can have and
// its code as parts: each function the walk reaches, keyed by its linkage
// name, with its sum without positions (ir.BodySum) and the lines of its
// assertions whose failure the program reads as a value (vm.CodePart). The
// case's fingerprint (caseFingerprint) is a SHA-256 over a scheme version,
// the compiler's identity (artifactcache.CompilerID, which a new build of
// nomi, its std and its VM changes), the case's name, its group's virtual
// clock, the case's time limit, the program's locales and the parts. Each
// part guards a class of edit: a function's body, its signature and the
// layouts of the types it holds are in its sum; a callee, an impl a
// dispatch may select, a Display, Debug or key body a rendering or a
// comparison may select and a `once`'s initializer are parts of their own
// when the walk reaches them, so one that comes, goes or changes moves the
// fingerprint. A body with no linkage name (the case's, a lambda, a cell's
// initializer) is keyed by its name and file, and a lambda's code is in
// the sum of each function that makes it too. The same walk selects the
// cases `nomi test --reaching` runs (Program.SelectReaching): those whose
// walk reached a function a source line is in (vm.FuncsAt). Such a run
// loads every file.
//
// When a case passes, Test stores its fingerprint, its parts, the cases it
// counted and whether it has effects, in a record of kind "passes" beside
// the program's (progcache.Entry.Sibling, with no files of its own, so it
// is read whatever changed and kept, keyed and pruned as the program's
// record is; its header holds passScheme, and one of another scheme reads
// as none). The next Test compares. A case whose fingerprint matches is not run, and is
// reported passed with the cases its pass counted (CaseResult.Decided,
// rt.DecidedUnchanged), when it has no effects: no crossing its declaration
// does not mark pure (Machine.Effects' sense: output and `dbg`, input, the
// clock, entropy, files, the network, processes, tasks, channels, Go). Its
// result is then a function of its code alone. An app field it reads is no
// effect: a test reads one only under its group's `boot`, which publishes
// the app for each case and is in the case's walk. RunChangedOrEffects, the
// default, runs a case with effects whatever its fingerprint; RunChanged
// (`--changed`) skips it too, since its code cannot have changed though what
// it reads outside may have; RunAll (`--all`) runs every case and stores
// what passed. Only a pass is stored: a case that fails, is blocked or times
// out loses its pass. A debugged program and Cases use none.
//
// A file's cases share a machine and its `once` cells, so a skipped case
// must not change what the others see. A pure case reads only cells whose
// initializers are pure, since an initializer's effects would be in its
// walk. A pure initializer computes the same value whenever it is forced
// and writes nothing, so a case after a skipped one that reads the cell
// forces it itself and sees the value it would have read; only the time it
// spends forcing it counts against its own limit.
//
// A file whose every selected case would be decided unchanged is not
// loaded: Cache.TestUnchanged reports its cases from the passes record
// alone, through the reporting Test uses (reportCases), with the names and
// lines of a parse of the entry file (frontend.SelectTests) and the counts
// the passes hold. Decoding the kept program's image is most of what a
// loaded file whose cases are all skipped costs. The record says which
// program its passes were decided on: the sum of the kept program's record
// (progcache.Entry.Sum) a program decoded from it carries, and the time
// limit. A pass is verified when Test decided it, unchanged or passed, on
// that program under that limit; any other run leaves its fingerprint and
// clears the mark. A file is answered when the kept program is a hit with
// that sum, the limit is the run's, and each selected case has a verified
// pass the policy lets go unrun (pure, or any under RunChanged). That is
// each case's fingerprint matching by construction: the program is the
// bytes it was decided on, so its walk answers the same parts; the
// compiler keys both records and the scheme heads the passes; the locales
// are nomi.toml's, which the hit says has not changed. A program loaded or
// relinked marks nothing, so a file is answered from the second run after
// its program last changed. The effects lists, which Test checks first,
// are nomi.toml's over the same program, and a kept program is one they
// allow. In a test binary TestUnchanged also loads the file and decides
// each case (verifyUnchanged), and panics when one is not unchanged with
// the counts it answered.
//
// A run with no cache runs every case, so the counts and exit status a CI
// run with an empty cache reports are what they would be without stored
// passes. `NOMI_DEBUG_ARTIFACT_CACHE=1` says why each case ran: no stored
// pass, its effects, or the first function whose code changed, came or went;
// and for each file, that it was answered from its passes record, or why
// not. On the corpus a warm run answers 332 of its 466 test files from
// their records; 128 have a case with effects. cmd/nomi's TestSkippedCasesWouldPass holds a skipping run
// to one that runs every case, over corpus and generated projects and a set
// of edits, each also run through the files answered from their records.
package vmhost
