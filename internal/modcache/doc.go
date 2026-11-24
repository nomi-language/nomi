// Package modcache answers which modules a Nomi project needs, at which
// versions, under which names, and where each one is: a local directory a
// [dependencies] entry names with `dir`, or its directory in the module
// cache, where internal/modfetch extracts each repository a project
// fetches. It reads only the disk and never fetches, so the language
// server and the checker can use it. A process that may fetch (the `nomi`
// CLI, cmd/nomi/modules_cmd.go) installs internal/modfetch with
// SetFetcher, and Load calls it before reading, so `run`, `test`, `check`
// and `build` fetch a module before the front end reads the cache. The
// language server installs none and never fetches.
//
// # The manifest and the lock
//
// A module's nomi.toml is the one file it states its dependencies in
// (manifest.go). ParseModuleFile reads its [module].name, its
// [module].nomi and its [dependencies] table, whose entries
// (DependencyEntry, the type analysis's manifest declares the table with)
// hold `repo` and `version` or `commit` with an optional `subdir`, or
// `dir`, or all of them. The key of an entry is the name the module's own
// files import the dependency by. A `commit` is all 40 hex digits of one
// (IsCommit, CommitProblem), and a Requirement's and a Module's Version
// is then the commit, which no tag can be.
// ManifestProblem places an error in the table at its key, so analysis
// can report it at its line.
//
// [indirect] maps a repository the module reaches only through its
// dependencies to the lowest version it takes it at (Floor), a tag;
// a repository its [dependencies] names with no dir has none, since the
// entry gives its version. `nomi update` writes the project's.
//
// A module does not declare where it is published: [module] has no path.
// A fetched module's identity in the graph is its ID, the repository and
// the directory in it (`subdir`, "" for the root). A repository is
// `host/path`, with no scheme and no `.git` (CheckRepo), fetched over
// HTTPS; a subdir is a clean slash path below the root, never empty, `.`
// or with `..` (CheckSubdir). A version is a tag of the repository,
// vX.Y.Z, or a prerelease vX.Y.Z-... (IsVersion): one tag versions the
// whole repository, so there are no tags per directory, no
// pseudo-versions, no /vN paths and no +incompatible. A module is a
// directory of its repository at a tag that holds a nomi.toml; nothing
// checks that the nomi.toml names itself.
//
// [module].nomi is the lowest nomi release the module's source needs
// (nomi.go). The running nomi's release is what cmd/nomi records as it
// starts (SetRelease, Release): a release's version, or "" for a `nomi`
// built from source and for every other process. A release older than a
// module's [module].nomi refuses the module (NomiNeeded,
// NomiVersionError), and ParseModuleFile checks that before anything else
// in the file, since a module written for a later nomi may write the rest
// in a form this one does not read. ReadModuleFile answers that error
// bare, and whoever knows which module it read names it: the walk names
// the project, a fetched module by its repository, directory and version,
// and a module loaded from a directory by its directory. The walk stops at
// the first one, and the front end checks nothing of a build that reaches
// one (the analysis package's NomiVersionCode). A nomi built from source
// has no release, so it refuses no module and says nothing. There is no
// upper bound.
//
// nomi.lock beside the project's nomi.toml (lock.go) locks the build list:
// one line per fetched module, its repository, its subdir (`.` for the
// root), its version, the commit the tag named, the hash of its files and
// `direct` for a module the project names, sorted by repository and
// subdir. A module taken at a commit has the word `commit` as its version
// and the commit beside it: a tag is a version, so no tag is that word.
// The hash is `sha256:` and the hex SHA-256 of a line `<sum>
// <path>` per regular file of the module's directory at the tag, sorted by
// path, each sum the file's own SHA-256 and each path from the module's
// directory, so it carries no repository or version (FilesHash). A file's
// mode is not hashed. A local module has no line. modfetch writes the
// lock and checks every module it reads, fetched or in the cache, against
// it.
//
// # The cache
//
// The module cache is NOMI_MODULE_CACHE (CacheEnv), else
// os.UserCacheDir()/nomi/modules (Root). Under `go test` Root is an error
// unless the variable is set, so no test reads or writes the real cache.
// A repository at a tag is extracted once, whole, at
// `<host>/<path>@<tag>` (RepoDir), and every module of it is a directory
// there. Each upper-case letter of the name is written `!` and its
// lower-case letter, so two repositories whose names differ only in case
// stay apart on a file system that folds case. Beside the directory,
// `<path>@<tag>+files` lists the commit the tag named, each regular file
// with its SHA-256 and each link the archive held, which is not installed
// (Install, ReadRepoFiles), so a module's hash, and whether its directory
// holds a link (LinkIn), are read without reading its files. modfetch extracts into a
// temporary directory, writes the list, makes the tree read-only and
// renames it into place, so a directory there is complete (Cached). A
// resolve that finds a repository marks it used, and Prune removes the
// repositories nothing has resolved for two weeks, as every Nomi cache
// prunes (internal/artifactcache package doc, "Pruning"); RemoveTree makes
// a read-only tree writable to remove it.
//
// # The walk
//
// Walk reads the main module's nomi.toml, then each reached module's own
// nomi.toml from its directory, and follows the [dependencies] of each. A
// version is selected for each repository reached, by minimal version
// selection: the highest any reached module asks for of any module in it,
// and, in Walk, at least the version nomi.lock locks, and every module of
// the repository is taken at that one tag. Only selected versions'
// requirements are read, and the walk repeats until no reached module asks
// for a higher one, so with the lock current it reads the lock's build
// list and no version the lock lacks. Two requirements on one repository
// in different lines (Line: the major version from v1 on, the minor
// version below v1, since each 0.x line may break the one before) are an
// error that names both requirers: one tag is taken, and it would be in a
// line one requirer did not ask for. `nomi update` moves within a line by
// the same Line, so selection and update agree on what may break. A
// requirement at a commit takes its repository at that commit, whatever
// the lock or another tag holds: a build takes a repository at one point,
// so a commit and a tag on one repository anywhere in the graph, or two
// different commits, are an error naming both requirers, and two
// requirements at one commit are one (clash, which holds all three
// rules). Each module's [indirect] floors are requirements by that module
// on their repositories, which raise and are held to the others by the
// same rules, once the pass finds a requirement that reaches the
// repository: a floor puts no module in the build. Each Dep records the
// module whose [dependencies] first reached it (Via), which `nomi
// outdated` names.
// Resolve is the same walk without the lock: the versions the requirements
// alone select, which `nomi fetch`, `add`, `remove` and `update` lock.
// Missing lists the selected modules the cache lacks.
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
// # Dir entries
//
// A dir entry is read in the module being built and in each module loaded
// from a directory (localModules), relative to the directory of the
// nomi.toml that writes it: a module on disk has its neighbors on disk, so
// a project that names sqlkit by its directory takes sqlkit's
// `../migrate` too. An entry with a dir and a repository makes the
// directory stand for that module wherever another module requires it.
// The main module's entry for a module wins over a dependency's dir for
// it, and otherwise the first directory the walk reaches for a module
// does; a directory reached again is the module read before, so a cycle
// ends. A module loaded from a directory has no nomi.lock line
// (Dep.Local).
//
// In a fetched module's nomi.toml, a dir entry that stays inside its
// repository names the neighbor module in that directory at the same tag
// (neighbor): lib/sql/sqlkit's `sqlite = { dir = "../sqlite" }` is
// lib/sql/sqlite of the same repository at the same version, so modules
// of one repository name each other by directory with no version, in a
// checkout and fetched alike. A dir entry of a fetched module that leaves
// its repository is an error.
//
// A dependency's tool (`nomi tool sqlkit generate`) is a program rooted in
// the dependency's directory, so `nomi tool` sets NOMI_MAIN_MODULE
// (MainModuleEnv) to the project root, and while it is set a graph rooted
// at a module the project needs is the project's (MainModule, asked by
// Walk, Resolve and Load's fetch). The tool then reads its neighbor
// modules at the project's versions and through the project's dir
// entries. It is an environment variable so the process the tool runs in
// reads the same graph. TestLibRelease_AProjectOfLibModulesNeedsNoGo
// (cmd/nomi) runs sqlkit's generator from the module cache on a release
// `nomi` with no Go.
//
// # Errors name a nomi command
//
// A Nomi user edits nomi.toml through `nomi`, so an error or hint about a
// missing dependency names the `nomi` command that fixes it: ImportProblem
// for an import whose first name no file and no [dependencies] entry
// gives (`nomi add`, with the directory when one nearby holds a module of
// that name), or a dependency that is not a Nomi module; MissingHint for a
// needed module the cache lacks (`nomi fetch`).
package modcache
