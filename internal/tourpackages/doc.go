// Package tourpackages is the packages the tour's examples import beside std,
// linked into the tour's wasm (cmd/nomi-wasm) and into every test that
// runs the tour's blocks.
//
// Every runner of tour blocks (cmd/nomi-wasm, vmhost's TestTourDoctests and
// TestTourWasmAnswersTheTourBlocks, the expectation and VM harnesses) loads
// a block through Source and vmhost.LoadMarked. How a block's files, data
// files, packages and sibling projects load is in vmhost.LoadMarked and
// vmhost.BlockFiles; how a `tool:` step runs is in vmhost.ToolMarked; how
// the page shows and runs a block is in tour/README.md.
//
// # Packages the tour links
//
// A tour package is a package a tour block imports as a program on disk
// imports a package its nomi.toml's [dependencies] names, with nothing in
// the block that says where it comes from: the Databases chapter's `import
// sqlite` is the same line a program written by hand has. Packages lists
// them: the sqlite and postgres drivers, migrate and sqlkit, each from its
// directory under lib/. Each is Nomi alone, so every runner, the browser's
// and a test binary's alike, runs it as it runs the block.
//
// The tour links each one's Nomi sources (packages_sources_gen.go, since
// go:embed reaches no file outside this Go package's directory). The files a
// package's `embed` declarations name (the sqlite driver's engine, a
// WebAssembly module) are in packages_sources_gen.go too, in base64
// (PackageEmbedded finds them as the front end resolves the patterns), and
// Source writes each as a `// FILE: <path> (base64)` section, so a binary
// file reaches the block's project byte for byte. `go generate
// ./internal/tourpackages` writes the file from the tree, and
// TestGeneratedFileIsCurrent (internal/tourpackages/tourpackagesgen) fails while it is
// stale. The postgres driver runs over std::net, which has no sockets in
// the browser, so there every call answers that Postgres cannot be
// reached.
//
// Source attaches the packages a block imports to its source, as the
// sections of a project that names packages: each package the block imports,
// and each tour package those name, under PackagesDir/<name>, and a
// [dependencies] table that names each one by its directory there
// (`sqlite = { dir = "./tour_packages/sqlite" }`). A block with a nomi.toml
// section of its own gains the table at the end of that section; one
// without gets a root nomi.toml that names the package `tour` and its
// entry. A block that names packages of its own already (virtualproject's
// namesPackages) gets nothing. A tour package's own nomi.toml names another
// by its directory beside it (sqlkit's `migrate = { dir = "../migrate" }`),
// as in this repository's lib/, so it reaches the same directory under
// PackagesDir. vmhost.LoadMarked writes and loads the result as it loads
// any such block. TestTourWasmAnswersTheTourBlocks runs such a block
// on the staged wasm under Node, held to its expect block;
// TestTourWasmRunsTheSqliteDriversTests runs the driver's own
// sqlite_test.nomi there, and cmd/nomi's TestLibModules runs it natively.
package tourpackages
