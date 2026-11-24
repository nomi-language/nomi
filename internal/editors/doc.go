// Package editors is `nomi editor`: it writes each editor's Nomi support
// where that editor reads it, from files `nomi` carries, so a user needs
// the one `nomi` file and no clone of the repository, and the editor's
// files always match the compiler's version.
//
// # What it carries
//
// The files are the repository's own sources, embedded with no second
// copy: editors/helix (package helix: languages.toml and the queries),
// editors/nvim (package nvim: the plugin), editors/zed (package zed: the
// extension's manifest, languages and debug adapter schema) and
// tree-sitter-nomi (package grammar: the generated C and the grammar's
// WebAssembly build). Each is a package in the directory that owns the
// files, since `//go:embed` reads nothing above its package's directory,
// so a plain `go build` or `go install` of cmd/nomi carries them with no
// generation step. The generated C is most of it, about 14 MB, and the
// grammar's wasm 2 MB. TestEmbeddedFilesAreTheSources holds each embedded
// file to its source, and internal/docscheck holds the generated queries
// to their generators. scripts/publish-grammar.sh leaves
// tree-sitter-nomi/embed.go out of the published grammar. The runners'
// compiler identity (internal/runners) hashes the directory of every
// embedding package, but for the target/ and node_modules/ a Rust or Node
// build leaves there.
//
// Two files are built with tools a user may lack, so only a release
// `nomi` carries them: the VS Code extension, built with npm (nomi_vsix,
// vsix_embed.go), and the Zed extension's WebAssembly, built with cargo
// (nomi_zedwasm, zedwasm_embed.go). scripts/release.sh builds both and
// stages them with internal/runners/hostrunnersgen's -vsix and -zed into
// the overlay that supplies the files those tags embed; `make
// install-vscode` and `make install-zed` stage the one they build the same
// way. A build without a tag carries none (vsix_none.go, zedwasm_none.go),
// and installing that editor says so and names the make target.
//
// # Steps
//
// Install and Uninstall make a list of steps (write a file, remove a path,
// run a command) from what they find on disk and on PATH, then run it. Each
// step prints one line, the path or the command, so a run reports every
// path it wrote. With DryRun the same list is printed and nothing runs, so
// a dry run shows exactly what the real one does. What could not be done
// (no `hx`, no C compiler) is said after the steps, which still run, and
// makes the command exit 1.
//
// Each editor's directory is the one it documents and its source uses:
// Helix's configuration directory ($XDG_CONFIG_HOME/helix,
// ~/.config/helix, or %AppData%\helix on Windows), Neovim's data
// directory's site/pack/nomi/start/nomi ($XDG_DATA_HOME/nvim,
// ~/.local/share/nvim, or %LocalAppData%\nvim-data on Windows), and Zed's
// data directory's extensions/installed/nomi (~/Library/Application
// Support/Zed on macOS, $FLATPAK_XDG_DATA_HOME/zed, $XDG_DATA_HOME/zed or
// ~/.local/share/zed on Linux, %LocalAppData%\Zed on Windows, as Zed's
// paths::data_dir says). Nothing is written outside them but the temporary
// files a command reads.
//
// Helix gets the queries in runtime/queries/, the grammar's C in
// runtime/grammars/sources/nomi/, and languages.toml's Nomi entries between
// `# BEGIN NOMI HELIX SUPPORT` and `# END NOMI HELIX SUPPORT`, with the
// grammar's source pointing at that copy, so no network is needed and the
// grammar is this `nomi`'s. A reinstall replaces only that block and an
// uninstall removes it with the blank line before it; the rest of the file
// is left byte for byte. `hx --grammar build` then builds only Nomi's
// grammar, under a temporary configuration that is the user's with
// `use-grammars` replaced.
//
// Neovim gets the plugin, and the parser compiled into its parser/ with
// $CC or cc. The directory is removed first, so a running Neovim keeps the
// old parser it mapped and never sees it rewritten in place.
//
// Zed gets the layout it unpacks a registry extension into: the manifest,
// languages/, debug_adapter_schemas/, grammars/nomi.wasm and
// extension.wasm. That layout is Zed's internal format, not a documented
// interface. An installed/nomi this command did not write, a dev
// extension's link or a registry install, is left alone unless the
// install is forced, and never uninstalled.
//
// Each install writes a marker, nomi-version, with this `nomi`'s version
// into the directory it owns (Helix's grammar copy, Neovim's package
// directory, Zed's extension), which `nomi editor` reads to say whether
// the installed files are this version's, and which tells Zed's
// uninstall that the extension is this command's.
//
// scripts/sync-helix-runtime.sh --dev, behind `make dev-helix`, runs this
// install from the checkout and then links the query directories to it.
package editors
