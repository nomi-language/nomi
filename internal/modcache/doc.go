// Package modcache answers which modules a Nomi project needs, at which
// versions, under which names, and where each one is: a local directory a
// [dependencies] entry names with `dir`, or its extracted copy in the Go
// module cache (GOMODCACHE). It reads only the disk and never downloads, so
// the language server and the checker can use it.
// internal/modfetch puts missing modules in the cache; a process that may
// download (the `nomi` CLI, cmd/nomi/modules_cmd.go) installs it with
// SetFetcher, and Load calls it before reading, so `run`, `test`, `check`
// and `build` download a module before the front end reads the cache. The
// language server installs none and never downloads.
//
// # The manifest and the lock
//
// A module's nomi.toml is the one file it states its dependencies in
// (manifest.go). ParseModuleFile reads its [module] name and path and its
// [dependencies] table, whose entries (DependencyEntry, the type analysis's
// manifest declares the table with) hold `module` and `version`, `dir`, or
// all three. The key of an entry is the name the module's own files import
// the dependency by. ManifestProblem places an error in the table at its
// key, so analysis can report it at its line.
//
// nomi.lock beside the project's nomi.toml (lock.go) locks the build list:
// one line per module from the proxy, its path, its selected version, the
// h1: hash of its zip and `direct` for a module the project names, sorted
// by path. A local module has no line. modfetch writes it and checks every
// download against it.
//
// # The walk
//
// Walk reads the main module's nomi.toml, then each reached module's own
// nomi.toml from its directory, and follows the [dependencies] of each. A
// version is selected for each module path reached: the highest any
// reached module asks for, and, in Walk, at least the version nomi.lock
// locks. Only selected versions' requirements are read, and the walk
// repeats until no reached module asks for a higher one, so with the lock
// current it reads the lock's build list and no version the lock lacks.
// Resolve is the same walk without the lock: the versions the requirements
// alone select, which `nomi fetch`, `add`, `remove` and `update` lock.
// Missing lists the selected versions the cache lacks.
//
// # Names
//
// Name resolution is per module. Graph.Names is each name the project's
// files import a dependency by, and each Dep's Names is the same for that
// dependency's own files, from its own [dependencies] and its own
// [module].name (NamesAt answers either by directory). A consumer's keys
// never reach a dependency's files. Each module also has one name in the
// program (Dep.Name), the first segment of its files' keys and the key of
// its entry under [effects.deps.modules]: the project's key for a module
// the project names, else the key of the first module that reaches it,
// with `~2`, `~3` added when that name is taken by another module or by a
// file or directory at the project's root (rootNames). A tool of a
// dependency runs under the project's key for it, and reads its table in
// the project's nomi.toml by that key (cmd/nomi's `nomi tool`,
// vmhost.ToolModuleEnv); a dependency's own [module].name (Dep.Self) is
// only what an effects list that writes it is told to write instead.
//
// # The main module
//
// A dir entry is read in the module being built and in each module loaded
// from a directory (localModules), relative to the directory of the
// nomi.toml that writes it: a module on disk has its siblings on disk, so
// a project that names sqlkit by its directory takes sqlkit's `../migrate`
// too. The module path a directory holds stands for that path wherever
// another module requires it, as a replace does in Go. The main module's
// entry for a path wins over a dependency's dir for it, and otherwise the
// first directory the walk reaches for a path does; a directory reached
// again is the module read before, so a cycle ends. In a downloaded
// module's nomi.toml, dir entries are not read: an entry with `module` and
// `version` is taken from the proxy, and one with only a dir is skipped,
// since it names a directory of its author's checkout that the module
// cache copy lacks. A module loaded from a directory has no nomi.lock
// line (Dep.Local).
//
// A dependency's tool (`nomi tool sqlkit generate`) is a program rooted in
// the dependency's directory, so `nomi tool` sets NOMI_MAIN_MODULE
// (MainModuleEnv) to the project root, and while it is set a graph rooted
// at a module the project needs is the project's (MainModule, asked by
// Walk, Resolve and Load's fetch). The tool then reads
// sibling modules at the project's versions and through its dir entries,
// which `nomi add` and `nomi update` keep on one commit for lib/ modules. It
// is an environment variable so the process the tool runs in reads the
// same graph. TestLibRelease_AProjectOfLibModulesNeedsNoGo (cmd/nomi) runs
// sqlkit's generator from the module cache on a release `nomi` with no Go.
//
// # Errors name a nomi command
//
// A Nomi user edits nomi.toml through `nomi` and never runs `go` for a Nomi
// dependency, so an error or hint about a missing dependency names the
// `nomi` command that fixes it: ImportProblem for an import whose first
// name no file and no [dependencies] entry gives (`nomi add`, with the
// directory when one nearby holds a module of that name), or a dependency
// that is not a Nomi module; MissingHint for a needed module the cache
// lacks (`nomi fetch`).
package modcache
