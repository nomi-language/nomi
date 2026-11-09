// Command nomi is the Nomi CLI: run, check, test, build, fmt, the module
// commands, `nomi tool`, `nomi types`, the debug adapter, the MCP server and,
// with no arguments, the REPL. docs/tooling.md documents its commands and
// flags for users. This comment says how `nomi build` and the REPL are put
// together; build.go, build_web.go, run_web.go, build_vm.go and repl_vm.go
// open with what each command does.
//
// # nomi build
//
// `nomi build` writes one executable: the nomi-vm binary with the program's
// linked IR image appended (build.go). It is not native code; it skips the
// front end at startup. `--target goos/goarch` cross-compiles. What a runner
// does with the image, the runner variants without the network, the image's
// layout and embedded files, the WebAssembly forms, the wasip1 reactor,
// .nomic files and runners from a release are in the vmrunner package doc.
// A program with Go bindings gets its own runner and has its image lowered
// in its FFI wrapper (internal/ffirun package doc).
//
// `--web` writes a browser folder (build_web.go): the js/wasm module
// app.wasm, nomi.js (Go's wasm_exec.js wrapped in a function, then the
// loader, which starts itself as each run's module worker), nomi.d.ts with
// a typed `call` per exported function (internal/webtypes), the page, and
// the author's static files. A program that lists its locales embeds only
// `[i18n].web_embedded`'s, gets one locales/<tag>.nomil per other listed
// locale (fetched by Locale.load through nomi.js), and its image's phrase
// tables omit those locales' texts. nomi.js's load starts the worker in
// call mode, which runs one exported function at a time on the booted
// program. A project with Go bindings gets its own runner for a
// WebAssembly target too; `--web`'s image, nomi.d.ts and locale files come
// from the wrapper. A bound package that does not build for the target is
// reported at its `gopkg` with the import chain. A Go binding carries no
// capability class: Go's build decides.
//
// `nomi run --web` (run_web.go) serves the generated files from a
// temporary directory and the static files in place, on localhost. It polls
// both: a source change rebuilds, a static change only reloads. The reload
// script is added to each .html page as it is served, never written.
//
// # REPL
//
// `nomi` with no arguments keeps one live VM and links each input into it,
// lexically scoped across inputs as GHCi is. repl_vm.go's opening comment
// says what an input's file holds and how each kind of definition carries;
// this section says how the file is checked, lowered and linked.
//
// Each input is one file of the session's program, `repl_in<N>`, a key with
// no directory, so an `embed` pattern reads the working directory. The
// session has no project root, so `[i18n]` is its default: source locale
// `en`, no catalogs. lexer.IsComplete treats an input ending inside an open
// raw or triple-quoted string as one that continues.
//
// Checking. The file is checked against the earlier files' interfaces
// (vmhost.Session.CheckFile and LoadFile, vmhost/session_files.go). One
// analysis.ProgramInterfaces, made by analysis.NewStoredInterfaces over
// frontend.Checker.SessionSource, lives for the session and grows by
// AddFile; SetEntry makes each new file the entry while it is checked, and
// RemoveFile drops the file of a failed input. Each file's impl headers are
// resolved once, by its own check, and kept.
// frontend.Checker.CheckSessionFile reports the file's diagnostics as a
// from-source check reports the entry's; the reductions over every file's
// bodies are not rerun, since earlier files passed them. A session's
// program (analysis.SourceProgram.Session) differs from a project in two
// things one input could do to an earlier one's type: an inherent impl may
// extend a type another of its files declares, and a `derive` may name
// one, lowered from that file's declaration (analysis.LowerDerivesWith,
// ImportedTypeDecl) with the derive as the impl's origin. Each new file's
// boot application type joins earlier boots' (Session.Commit adds them to
// AppTypeNames).
//
// Boots. A `fn boot` runs with every input after the one that declares it.
// The machine keeps its boot when a new entry declares none
// (vm.Machine.Extend); a later file imports the boot's file whole, its
// `main` is checked as running under that boot
// (analysis.CheckAppRootsUnder, Session.SetBoot), and its lowering reads
// application fields as an entry with a boot does
// (irbuild.SessionObjects.Boots).
//
// Lowering. The file is lowered on its own (irbuild.LowerSessionFile)
// against the earlier files' modules, which the session keeps in memory
// (irbuild.SessionObjects) and the machine already links: every earlier
// file its check read is fromObjects, and the new file's names resolve to
// their symbols by linkage name, as irbuild.LowerFile resolves kept
// objects. An earlier file that declares anything generic is read with its
// bodies grafted onto its interface (ProgramInterfaces.SetBodies,
// analysis.GraftBodies) and checked, and its gen is linked rather than
// fromObjects, so it lowers the instances the new file needs that no
// earlier lowering made, into a module of its own. The registry's
// program-wide sets (instances held erased, enums boxed, refused
// synthesized impls) only grow: nothing before the new file is lowered
// again. The new modules are linked into the machine with vm.Machine.Extend
// and committed to the session's objects once the input ran
// (Session.Commit). TestIROnceSibling_ACallThroughAnImportedFunctionValuedOnce
// (internal/irbuild) and TestAGenericReceiversImplIsSeenWithoutItsFile
// (internal/analysis) hold two cases this shape exercises in every program:
// a call through a function-valued `once` imported by name, and the impl
// header of `impl Named for Box<T>` (a generic receiver with no `<T>`
// clause) being seen without importing its file.
//
// Redefinitions. A redefined impl is taken out of its earlier file's text
// (Session.ReplaceFile, its lines blanked) before the input is checked, so
// no check sees two, and put back if the input fails. Once the input ran,
// each old impl function is redirected to the new one (Session.Supersede,
// vm.Machine.Redirect, matched by linkage name with the file key changed),
// so code earlier inputs compiled calls the new impl. A value's or once's
// type is kept as source with each REPL-declared type spelled through its
// file (vmhost.Checked.LocalType's qualifier, analysis.TypeTextQualifying).
// The front end synthesizes a Debug impl for every type in its file, so a
// later input's Debug impl, written or derived, is a second one in the
// program; the lowering ranks them as the coherence check does: a
// synthesized universal Debug (ast.FuncDef.AutoSynth) yields to a derived
// or written one (irbuild's writtenImplMembers), and once the input ran its
// functions are redirected as a redefined impl's are. An impl in a third
// file (neither the type's nor the caller's) is found for a std generic
// instance by the type's declaration (irbuild's implForDecl), as
// `Json.encode_as(r)` needs for a later input's `derive ToJson for R`.
//
// Declarations and names. Every top-level declaration the parser makes is a
// declaration of the input's file, never a statement of its `main`
// (replDeclOf; TestReplVM_EveryTopLevelDeclarationIsADeclaration parses one
// of each node type). A `phrase` or `noun` (ast.KeywordDecl) and an `embed`
// share the key `value <name>` and are imported by name, as a type is; the
// front end expands each in the input's file. The name joins the input's
// function names, so it shadows an earlier function, once or value of that
// name, and a later function shadows it. A file imports only the names its
// text names, so the REPL hands the session every name in force
// (Session.SetScopeNames, SourceProgram.ScopeNames), and the entry's check
// offers them in a "did you mean" hint beside its own scope's
// (analysis.FileDecls.ScopeNames: a capitalized one as a type, through the
// registry's Names, any other as a value).
//
// Cost. TestReplVM_AnInputsCostDoesNotGrowWithTheSession counts what each
// input's checks build (top-level declarations of its file and the files
// they read) and its lowering makes (functions), from Session.Cost, and
// requires the hundredth input to cost what an early input of the same
// shape did. TestReplVM_WallTimePerInput prints wall time per input:
//
//	NOMI_REPL_TIMING=500 go test ./cmd/nomi -run TestReplVM_WallTimePerInput -v
//
// On an Apple M2 Max, for inputs that each declare a function and a value
// and call the first input's function, input 10 takes 2.4 ms, input 100
// 2.5 ms and input 500 2.9 ms (median of nine). The first input takes about
// 0.8 s, for the stdlib.
package main
