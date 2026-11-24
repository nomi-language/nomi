// Package tourmodules is the modules the tour's examples import beside std,
// linked into the tour's wasm (cmd/nomi-wasm) and into every test that
// runs the tour's blocks.
//
// Every runner of tour blocks (cmd/nomi-wasm, vmhost's TestTourDoctests and
// TestTourWasmAnswersTheTourBlocks, the expectation and VM harnesses) loads
// a block through Source and vmhost.LoadMarked. How a block's files, data
// files, modules and sibling projects load is in vmhost.LoadMarked and
// vmhost.BlockFiles; how a `tool:` step runs is in vmhost.ToolMarked; how
// the page shows and runs a block is in tour/README.md.
//
// # Modules the tour links
//
// A tour module is a Nomi module a tour block imports as a program on disk
// imports a module its nomi.toml's [dependencies] names, with nothing in
// the block that says where it comes from: the Databases chapter's `import
// sqlite` is the same line a program written by hand has. Modules lists
// them: the sqlite and postgres drivers, migrate and sqlkit, each from its
// directory under lib/. Each is Nomi alone, so every runner, the browser's
// and a test binary's alike, runs it as it runs the block.
//
// The tour links each one's Nomi sources (modules_sources_gen.go, since the
// compiler's module cannot embed a nested module's files). The files a
// module's `embed` declarations name (the sqlite driver's engine, a
// WebAssembly module) are in modules_sources_gen.go too, in base64
// (ModuleEmbedded finds them as the front end resolves the patterns), and
// Source writes each as a `// FILE: <path> (base64)` section, so a binary
// file reaches the block's project byte for byte. `go generate
// ./internal/tourmodules` writes the file from the tree, and
// TestGeneratedFileIsCurrent (internal/tourmodules/tourmodulesgen) fails while it is
// stale. The postgres driver runs over std::net, which has no sockets in
// the browser, so there every call answers that Postgres cannot be
// reached.
//
// Source attaches the modules a block imports to its source, as the
// sections of a project that names modules: each module the block imports,
// and each tour module those name, under ModulesDir/<name>, and a
// [dependencies] table that names each one by its directory there
// (`sqlite = { dir = "./tour_modules/sqlite" }`). A block with a nomi.toml
// section of its own gains the table at the end of that section; one
// without gets a root nomi.toml that names the module `tour` and its
// entry. A block that names modules of its own already (virtualproject's
// namesModules) gets nothing. A tour module's own nomi.toml names another
// by its directory beside it (sqlkit's `migrate = { dir = "../migrate" }`),
// as in this repository's lib/, so it reaches the same directory under
// ModulesDir. vmhost.LoadMarked writes and loads the result as it loads
// any such block. TestTourWasmAnswersTheTourBlocks runs such a block
// on the staged wasm under Node, held to its expect block;
// TestTourWasmRunsTheSqliteDriversTests runs the driver's own
// sqlite_test.nomi there, and cmd/nomi's TestLibModules runs it natively.
package tourmodules
