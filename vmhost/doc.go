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
// lower, the diagnostic `nomi check` reports at its source.
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
// the handler of each backtick typed literal whose handler returns a
// `Result` (literals.go) and the `check` of each checked literal, one whose
// tag implements `CheckedLiteral` (checked_literals.go). An `Err` is a
// compile error at the literal; a `LiteralError` names a static and an
// offset, which this package maps back to the source. A handler or check
// that can reach something acting outside the machine is refused, an
// effectful check once at the check, so a compile never has effects.
//
// The checker types a checked literal as its `build`'s result and records
// it in analysis.FileAnalysis.CheckedLiterals. irbuild lowers it as a
// `build` call plus a check site, a lazy cell over `check` and the statics,
// in the irbuild.LiteralSite list backtick literals use (irchecked.go).
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
//   - More than maxRelowered files and a quarter of the program (a hub's
//     signature) checks the whole program instead, since each relinked
//     file's check and lowering link against every other file.
//   - When impls changed or a file came or went, the supply is rebuilt from
//     the entry built against every other file's interface, registering
//     every impl and checking no body (analysis.ProgramInterfaces.Supply).
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
