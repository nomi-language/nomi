// Package vmhost is Nomi's embedding API: a Go program loads a Nomi program,
// runs it, calls its functions and runs its tests on the VM.
//
//	p, err := vmhost.Load("policy.nomi", vmhost.WithHosts(myHosts))
//	quote, err := p.Call(ctx, "price", vmhost.Fields{"weight": 12.5, "distance": 800})
//
// Loading runs the shared front end (internal/frontend), lowers the program
// to IR (internal/irbuild's GenerateIR) and opens the VM (internal/vm) over it.
// `nomi run`, `nomi test`, `nomi check`, the REPL, the tour and an FFI
// project's wrapper all run through this package, so an embedder runs a
// program exactly as `nomi run` does.
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
// It is a public package because a generated FFI wrapper is a separate Go
// module, and a separate module can import only the public packages of this
// one.
//
// # Embedding
//
// Go functions a program calls are host tables (WithHosts) of hostadapt.Func
// values over the VM's own Values: an FFI wrapper generates one from the
// project's `go` bindings, and an embedder writes one for the `host fn`s and
// `host type`s its program declares, keyed by the declaration's key
// (frontend.HostKey). A machine binds std/compiler's table first and every
// other table under its keys' project names (hostadapt.ProjectTable,
// hostadapt.ProjectName), so a project file named like a Go-backed std
// module (`regex.nomi` binding `compile`) never shares a crossing with
// std's row (TestHosts_AProjectFileNamedLikeAStdModuleRunsBesideIt).
//
// A program's files are a [FileSystem] (WithFileSystem), adapted to
// internal/stdio's Backend. [DiskFiles] and [NewMemoryFiles] are the two
// Nomi builds in.
//
// # Holding a program to a list
//
// A host that runs code it did not write, such as a pricing rule a user
// uploads, loads it with [WithCapabilities] and the names of what it may
// reach outside the machine, the names nomi.toml's std lists use
// (`std::io`, `std::startup`) and `host:<key>` for the host's own
// functions, and with [WithGo] and the modules whose code may bind Go, as
// nomi.toml's `go` names them. With no names the program may reach
// nothing:
//
//	p, err := vmhost.LoadSource("rule", src, vmhost.WithCapabilities())
//	rate, err := p.Call(ctx, "discount", vmhost.Fields{"items": 12, "loyal": true})
//
// The load walks what every function a host can run reaches (main and the
// boot, every function the entry declares, since Call may call any, and
// the test cases), as the build's walk does, and refuses the program with
// a [*CapabilityError] before anything runs: the diagnostics `nomi check`
// prints, and each call as data (its name, its site, the chain from a
// root with the kind of each step). Run, Test and Call hold the program
// again from their own roots, to the host's list and the project's
// nomi.toml, which both apply. A program loaded without the option is
// held to nomi.toml's lists where it runs: Run, Test, Call and the image a
// build makes. LoadMarked and ToolMarked, which load a block for the
// tour's runner, hold it at the load from what `nomi check` holds, so a
// block over its lists fails as one that does not compile
// (TestCapabilityLists_EveryEntryPointRefuses). Cases holds nothing: Test
// holds before it, and a block's program was held when it loaded.
// Evaluate refuses every effect already.
//
// One rule holds a call to a table of lists (capability.Allows, through
// notAllowedBy): a `go` binding by the module whose files hold it, which
// the table's go must list (the project by its own module path,
// projectModule), and any other call by its name, which the table's std
// must hold. nomi.toml's lists, when the project has a [capabilities]
// table, hold each dependency's calls to three tables: the project's, the
// project's entry for the module (a std list, which says nothing of Go),
// and the [capabilities] table in the module's own nomi.toml (moduleAt
// reads it, once per module), whose entry for the module itself, or std
// when it has none, declares its own code, and whose go declares that the
// module binds Go
// by listing its own path and never grants it. The project's own calls are
// held to two: the project's, and its entry under [capabilities.modules]
// by its own module path, which notAllowedBy consults as it does a
// dependency's and which holds no test case (std and std_in_tests hold
// those). A call over several is reported once, over the module's own list
// first
// (CapabilityNotDeclaredCode, whose help puts the fix in that module),
// then the project's entry, then the project's lists
// (OutsideCallsNotAllowed). The dependency's own std names are checked by
// the code that checks the project's, at its own nomi.toml, and
// require_module_lists is checked there too, over the modules whose files
// the program holds (requiredModules). A host's lists are never a
// dependency's: a program held only by WithCapabilities and WithGo reads
// no dependency's table. With WithGo alone the host holds only Go
// bindings (hostHolds).
//
// A *CapabilityError keeps every call in Calls and Diagnostics; its text
// (Shown, which Unwrap answers too) gives the first call of each name over
// each list, with a help line counting the rest, unless AllCallsEnv is
// set (`--all-calls`). `nomi check --format json` and the language server
// read every call.
//
// Reached and Program.Reach answer what a program reaches, from the roots
// `nomi check` holds (Walk per root set), with the module each call is
// charged to; `nomi capabilities` prints it. Reach.Tightest is the
// smallest table the walks pass (tightestLists, beside notAllowedBy and
// held to it by TestTightestLists_PassAndEveryNameIsNeeded: every call
// passes, and taking out any one name refuses one), so a table it writes
// is one the check passes. Reached loads as Check does, a `go` binding
// taken as provided, so a program with Go bindings is read without its
// wrapper.
//
// What the list does not confine is in WithCapabilities' doc: the files
// WithFileSystem hands the program, the host's own functions, what a Go
// binding does inside, a nested std::compiler run, and time and memory.
//
// The rule that names an outside call and the check of a call against a
// list are internal/capability's, which vmrunner calls too: a host that
// loads compiled programs (vmrunner.WithCapabilities, the wasip1 reactor's
// nomi_load_with) holds them with the same names, walk and words, over the
// decoded image. vmhost adds what only it can read: a `go` binding's
// package from source, which names the call for a reader, the module its
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
// Only a program with no Go bindings that loads cleanly is kept: no
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
// reporting a fresh load uses. The CLI looks the record up before
// ffirun.Prepare, which parses the program to find Go bindings.
//
// The record names what the program was built from (Program.inputs): the
// project root's tree and each dependency module's, each `embed` pattern's
// base as a tree of every file (analysis.EmbedInfo.Bases), each embedded
// file on its own, since a tree walk skips the hidden names and
// node_modules an `all:` pattern embeds, and each module file and catalog
// outside those trees.
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
//     (Result.Rekeep).
//   - A body edit of a generic declaration drops the instances of it its
//     file's object held for other files. When kept objects of files not
//     lowered again name them (irbuild.FileMiss.Dropped), the relink starts
//     again with one such file per instance added, lowered after the edited
//     file and reading it with its bodies, so it asks for the instance again
//     and the instance goes back into the edited file's object. The other
//     files that name an instance are not lowered again: their objects name
//     it by its linkage name, which the new instance keeps. The relink is a
//     miss if a dropped instance is not defined after the last file
//     (Result.Undefined). At 5k generated lines an edit of core/util's
//     `first_or`, which seven files call at Int, lowers two files and runs
//     in about 0.18 s, against 0.45 s whole.
//
// The entry imports every unit no other unit imports, so any leaf interface
// edit rechecks it, and its check registers most of the program. At 50k
// lines a leaf body edit relinks in about 0.3 s and a leaf signature or impl
// edit in about 1 s, against 2.6 s whole; an edit that cannot be relinked
// costs a whole check and lowering plus about 50 ms. internal/scalebench's
// `nomi check, after a ... (file records)` operations measure each edit in
// process. `NOMI_DEBUG_ARTIFACT_CACHE=1` reports a relink (`relinked 2 of 8
// files`), a relowering with another file's bodies, or why there was none.
// cmd/nomi's artifact_cache_test.go and internal/expectation's
// TestCachedRunsMatchTheRecords hold a kept or relinked program to a fresh
// one.
package vmhost
