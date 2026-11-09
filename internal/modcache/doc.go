// Package modcache answers which Go modules a Nomi project needs the source
// of, and where each one is: a local `replace` directory, or its extracted
// copy in the Go module cache (GOMODCACHE). It reads only the disk and never
// downloads, so the language server, the checker and the FFI wrapper can use
// it. internal/modfetch puts missing modules in the cache; a process that may
// download (the `nomi` CLI, cmd/nomi/modules_cmd.go) installs it with
// SetFetcher, and Load calls it before reading, so `run`, `test`, `check` and
// `build` download a module before the front end reads the cache. The
// language server installs none and never downloads.
//
// A project's dependencies are the modules its go.mod requires. The modules
// a project needs are the direct requires of its go.mod (those without
// `// indirect`), and, for each of them that is a Nomi module (its root
// holds a nomi.toml), every module its own go.mod requires that the
// project's go.mod also lists, transitively. Each is taken at the version
// the project's go.mod requires: since Go 1.17 a tidy go.mod lists every
// module its build needs at the version minimal version selection picks,
// and `nomi add` and `nomi fetch` keep it that way. A Nomi module's
// requirement the project's go.mod does not list is Unlisted, which `nomi
// fetch` adds.
//
// # The main module
//
// As in Go, only the project's go.mod is the main module's: a dependency's
// contributes requires, never replaces (which name directories of its own
// checkout, like sqlkit's `../migrate`). A dependency's tool (`nomi tool
// sqlkit generate`) is a program rooted in the dependency's directory, so
// `nomi tool` sets NOMI_MAIN_MODULE (MainModuleEnv) to the project root, and
// while it is set a graph rooted at a module the project needs is the
// project's (MainModule, asked by Walk, by Load's fetch and by
// ffirun.Prepare). The tool then reads sibling modules at the project's
// versions, which `nomi add` and `nomi update` keep on one commit for lib/
// modules. It is an environment variable so the FFI wrapper the tool runs
// in reads the same graph. TestLibRelease_ToolOfAModuleFromTheCache
// (cmd/nomi) runs sqlkit's generator from the module cache on the prebuilt
// lib wrapper and in one built with Go.
//
// # Errors name a nomi command
//
// A Nomi user never edits go.mod or runs `go`, so an error or hint about a
// missing requirement names the `nomi` command that fixes it (MissingHint,
// ImportProblem). For a `gopkg` no go.mod provides, the checker and a run's
// FFI validation give the same answer (ffirun.GoImportProblemFix): Go code
// beside a file outside any project names `nomi init`, and a missing
// dependency names `nomi add`.
package modcache
