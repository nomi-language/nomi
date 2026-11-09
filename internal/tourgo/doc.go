// Package tourgo is the Go the tour's examples bind, linked into the tour's
// wasm (cmd/nomi-wasm) and into every test that runs the tour's blocks.
//
// Every runner of tour blocks (cmd/nomi-wasm, vmhost's TestTourDoctests and
// TestTourWasmAnswersTheTourBlocks, the expectation and VM harnesses) loads
// a block through Source and vmhost.LoadMarked and answers its Go through
// this package. How a block's files, data files, modules and sibling
// projects load is in vmhost.LoadMarked and vmhost.BlockFiles; how a
// `tool:` step runs is in vmhost.ToolMarked; how the page shows and runs a
// block is in tour/README.md.
//
// # Go bindings in a block
//
// A browser cannot compile Go, so a tour block's `go` bindings cannot get
// the wrapper `nomi run` builds for them (internal/ffirun). Instead, every
// Go package a tour block binds is compiled in ahead of time, with the
// adapters ffirun generates for the block's declarations
// (ffirun.LinkedAdapters). `go generate ./internal/tourgo` reads the tour's
// chapters, stages each ```nomi-run block that declares a `gopkg` as a
// project, and writes linked_gen.go: the adapters, and `linked`, the one
// list of the Go functions the tour links, each with the Nomi declaration
// its adapter was generated for. TestGeneratedFileIsCurrent
// (internal/tourgo/tourgogen) fails while it is stale.
//
// A block's Go file (`// FILE: urltools.go`) is shown read-only in its
// editor, and is this package's urltools/urltools.go, byte for byte
// (TestTourGoFilesAreTheLinkedSources): the generator compiles the file in
// this tree, and the page shows the same text.
//
// When a visitor edits a block, Hosts matches each `go` binding the edited
// program declares to a linked function by its Go package and name, and
// rekeys the adapter to the key the edited program crosses under, so a
// renamed or moved declaration still runs. A binding the tour did not link,
// or one whose declaration no longer has the types its adapter converts, is
// an error at the declaration (Problems): "this example's Go code is fixed
// in the tour: `urltools.Foo` is not available".
//
// # Modules the tour links
//
// A tour module is a Nomi module a tour block imports as a program on disk
// imports a module its go.mod requires, with nothing in the block that
// says where it comes from: the Databases chapter's `import sqlite` is the
// same line a program written by hand has. Modules lists them: the sqlite
// and postgres drivers, migrate and sqlkit, each from its directory under
// lib/. The tour links each one's Nomi sources (modules_sources_gen.go,
// since the compiler's module cannot embed a nested module's files) and,
// in the browser, the adapters of its `go` bindings (linked_modules_gen.go's
// linkedModules), generated as ffirun generates a wrapper's, built for js
// only, over the js/wasm build of its Go side. `go generate
// ./internal/tourgo` writes both files from the tree, and
// TestGeneratedFileIsCurrent fails while either is stale. The sqlite
// driver's js/wasm side calls SQLite's official WebAssembly build through
// globalThis.sqlite3, which scripts/build-tour-wasm.sh stages in
// tour/public/nomi/sqlite/, pinned by version and checksum, and which the
// tour's worker loads the first time a run imports sqlite, before the
// run's budget starts (run-worker.js). The postgres driver's js/wasm side
// has no pgx, and every call answers that Postgres cannot be reached.
//
// Source attaches the modules a block imports to its source, as the
// sections of a project that names modules (a go.mod that requires each
// one, its files under ModulesDir), which vmhost.LoadMarked writes and
// loads as it loads any such block; MarkedHosts then answers the modules'
// bindings beside the block's own.
//
// The compiler's go.mod requires no tour module. A Nomi module it required
// would be a dependency of every program whose go.mod is the compiler's
// (the corpus), linked into its wrapper, and the sqlite driver's module
// requires modernc.org/sqlite. A replace in the compiler's go.mod would
// also break `go install github.com/nomi-language/nomi/cmd/nomi@latest`.
//
// Instead every js/wasm build of cmd/nomi-wasm runs in the Go workspace
// GoWork (internal/tourgo/tour.work, set as GOWORK by
// scripts/build-tour-wasm.sh, vmhost's rebuild and internal/wasmsmoke),
// which puts each tour module's directory beside the compiler's module, so
// linked_modules_gen.go imports the module's Go package from the tree. The
// js/wasm build of that package imports no modernc.org/sqlite; a js/wasm
// build without the workspace fails at that import. The workspace's
// checksums are tour.work.sum, which the go command writes as it needs
// them; commit it when it moves. TestTourWorkspaceUsesEveryModule holds
// tour.work to the compiler's module and each of Modules.
//
// Outside the browser a tour module's Go side is linked only in a build
// with the nomi_tourmodules tag in that workspace, over the package's
// native build (the sqlite driver over modernc.org/sqlite); otherwise none
// is (modules_native.go), and Unlinked says so. vmhost's TestTourDoctests,
// whose own binary is built from the compiler's module and links none,
// builds its package's tests again with the tag in the workspace
// (`go test -c`) and runs TestTourDoctests in that binary over only the
// blocks that import a tour module (runTourModuleBlocks;
// NOMI_TOUR_MODULES_SHOWN marks the child). The child holds each block to
// its expect block with the same checks, its tools' run included, and under
// NOMI_REGENERATE_EXPECTATIONS=1 writes what each shows to a file the
// parent puts in the chapter with the rest. The build adds about 4 to 10
// seconds a run, more the first time, compiling modernc.org/sqlite and pgx.
// tour.expect has no record of such a block. TestTourWasmAnswersTheTourBlocks
// runs it on the staged wasm under Node, held to the same expect block;
// TestTourWasmRunsTheSqliteDriversTests runs the driver's own
// sqlite_test.nomi there, and cmd/nomi's TestLibModules runs it natively.
package tourgo
