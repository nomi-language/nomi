// Package pkgcache answers which packages a Nomi project needs, at which
// versions, under which names, and where each one is: a local directory a
// [dependencies] entry names with `dir`, or its directory in the package
// cache, where internal/pkgfetch extracts each repository a project
// fetches. It reads only the disk and never fetches, so the language
// server and the checker can use it. A process that may fetch (the `nomi`
// CLI, cmd/nomi/packages_cmd.go) installs internal/pkgfetch with
// SetFetcher, and Load calls it before reading, so `run`, `test`, `check`
// and `build` fetch a package before the front end reads the cache. The
// language server installs none and never fetches.
//
// # The manifest and the lock
//
// A package's nomi.toml is the one file it states its dependencies in
// (manifest.go). ParseManifest reads its [package].name, its
// [package].nomi and its [dependencies] table, whose entries
// (DependencyEntry, the type analysis's manifest declares the table with)
// hold `repo` and `version` or `commit` with an optional `subdir`, or
// `dir`, or all of them. The key of an entry is the name the package's own
// files import the dependency by. A `commit` is all 40 hex digits of one
// (IsCommit, CommitProblem), and a Requirement's and a Package's Version
// is then the commit, which no tag can be.
// ManifestProblem places an error in the table at its key, so analysis
// can report it at its line.
//
// The [dependencies] table is the only place a package asks for a version.
// A package that needs a repository it reaches through its dependencies at
// a higher version than they ask for lists it there too: an entry no file
// imports is allowed, and it is a requirement like any other. A higher
// version that `nomi update` takes for a repository only a dependency
// names is held by nomi.lock alone (Lock.Versions).
//
// A package does not declare where it is published: [package] has no path.
// A fetched package's identity in the graph is its ID, the repository and
// the directory in it (`subdir`, "" for the root). A repository is
// `host/path`, with no scheme and no `.git` (CheckRepo), fetched over
// HTTPS; a subdir is a clean slash path below the root, never empty, `.`
// or with `..` (CheckSubdir). A version is a tag of the repository,
// vX.Y.Z, or a prerelease vX.Y.Z-... (IsVersion): one tag versions the
// whole repository, so there are no tags per directory, no
// pseudo-versions, no /vN paths and no +incompatible. A package is a
// directory of its repository at a tag that holds a nomi.toml; nothing
// checks that the nomi.toml names itself.
//
// [package].nomi is the lowest nomi release the package's source needs
// (nomi.go). The running nomi's release is what cmd/nomi records as it
// starts (SetRelease, Release): a release's version, or "" for a `nomi`
// built from source and for every other process. A release older than a
// package's [package].nomi refuses the package (NomiNeeded,
// NomiVersionError), and ParseManifest checks that before anything else
// in the file, since a package written for a later nomi may write the rest
// in a form this one does not read. ReadManifest answers that error
// bare, and whoever knows which package it read names it: the walk names
// the project, a fetched package by its repository, directory and version,
// and a package loaded from a directory by its directory. The walk stops at
// the first one, and the front end checks nothing of a build that reaches
// one (the analysis package's NomiVersionCode). A nomi built from source
// has no release, so it refuses no package and says nothing. There is no
// upper bound.
//
// nomi.lock (lock.go), beside the project's nomi.toml or its workspace's
// nomi-workspace.toml ("Workspaces" below), locks the build list:
// one line per fetched package, its repository, its subdir (`.` for the
// root), its version, the commit the tag named, the hash of its files and
// `direct` for a package the project names, sorted by repository and
// subdir. A package taken at a commit has the word `commit` as its version
// and the commit beside it: a tag is a version, so no tag is that word.
// The hash is `sha256:` and the hex SHA-256 of a line `<sum>
// <path>` per regular file of the package's directory at the tag, sorted by
// path, each sum the file's own SHA-256 and each path from the package's
// directory, so it carries no repository or version (FilesHash). A file's
// mode is not hashed. A local package has no line. pkgfetch writes the
// lock and checks every package it reads, fetched or in the cache, against
// it.
//
// # Workspaces
//
// A workspace is several packages developed together with one nomi.lock
// (workspace.go). Its root holds a nomi-workspace.toml (WorkspaceName,
// WorkspaceFile): `members`, the member directories, `exclude`, the
// directories whose packages are on their own, each in which `*` matches
// within one path segment; it has no other key. ParseWorkspace checks the
// entries' form; the text an editor holds, the keys the
// file may hold, and the line of each problem are the analysis package's,
// which installs them as a WorkspaceReader since this package cannot
// import it. readWorkspace is the one place that reads the file, declOf
// the one that checks its text, EntryDirs the one that answers the
// directories an entry names, and expand the one that turns the entries
// into member directories: an entry with no `*` must name a directory
// with a nomi.toml, an entry with `*` skips one without, and every
// member's nomi.toml names a package. The root holds no nomi.toml, no
// member holds a nomi-workspace.toml, and no exclude entry names a member
// or a directory above one. CheckWorkspace is the same checks of a text
// an editor holds, which the language server reports in the file.
//
// WorkspaceOf answers which workspace governs a package root, and every
// place that finds a lock asks it: walk reads the lock at the
// workspace's root, and internal/pkgfetch's fetcher reads and writes that
// lock. It is NearestWorkspace when the package is a member. That walks
// from the package's root upward to the file system's root with no bound,
// and the nearest nomi-workspace.toml decides, with nothing further up
// read: the package is a member when a members entry names it, on its own
// when an exclude entry names it or a directory above it
// (Workspace.Excludes), and else an UnlistedError, which analysis reports
// at the package's nomi.toml with a fix that adds it to members. With no
// file above, or a directory the walk cannot read, the package is on its
// own. A member's own nomi.lock is an error. The answer is read each time,
// as the nearest nomi.toml is. WorkspaceAt answers the workspace whose
// root a directory is, which the commands that act on every member at the
// root ask (internal/frontend.WorkspaceMembers).
//
// A member is its own main package for names (Graph.Names), entry points
// and effects; only selection is the workspace's. Walk of a member is its
// own graph at the versions the workspace's lock selects.
// Resolve and WalkAll cover the whole workspace: every member's
// requirements are the roots of one selection (one explore per member
// over a shared selection, repeated until none rises), and the answer is
// one graph of every package any member needs (joined), Direct when a
// member names it. Two members whose asks one build cannot take (clash)
// are an error naming both members and their entries, and so is one
// package taken from two places, a directory in one member and its
// repository or another directory in another. A package fetched from a
// repository is never looked up, so a member a consumer fetches by repo
// and subdir is read alone.
//
// # The cache
//
// The package cache is NOMI_PACKAGE_CACHE (CacheEnv), else
// os.UserCacheDir()/nomi/packages (Root). Under `go test` Root is an error
// unless the variable is set, so no test reads or writes the real cache.
// A repository at a tag is extracted once, whole, at
// `<host>/<path>@<tag>` (RepoDir), and every package of it is a directory
// there. Each upper-case letter of the name is written `!` and its
// lower-case letter, so two repositories whose names differ only in case
// stay apart on a file system that folds case. Beside the directory,
// `<path>@<tag>+files` lists the commit the tag named, each regular file
// with its SHA-256 and each link the archive held, which is not installed
// (Install, ReadRepoFiles), so a package's hash, and whether its directory
// holds a link (LinkIn), are read without reading its files. pkgfetch extracts into a
// temporary directory, writes the list, makes the tree read-only and
// renames it into place, so a directory there is complete (Cached). A
// resolve that finds a repository marks it used, and Prune removes the
// repositories nothing has resolved for two weeks, as every Nomi cache
// prunes (internal/artifactcache package doc, "Pruning"); RemoveTree makes
// a read-only tree writable to remove it.
//
// # The walk
//
// Walk reads the main package's nomi.toml, then each reached package's own
// nomi.toml from its directory, and follows the [dependencies] of each. A
// version is selected for each repository reached, by minimal version
// selection: the highest any reached package asks for of any package in it,
// and, in Walk, at least the version nomi.lock locks, and every package of
// the repository is taken at that one tag. Only selected versions'
// requirements are read, and the walk repeats until no reached package asks
// for a higher one, so with the lock current it reads the lock's build
// list and no version the lock lacks. Two requirements on one repository
// in different lines are an error that names both requirers: one tag is
// taken, and it would be in a line one requirer did not ask for. Line has
// three tiers: the major version from v1 on, the minor version from v0.1
// on, since each 0.x line may break the one before, and below v0.1 the
// release itself, since each 0.0.x release may break the one before.
// LineRule states the tier's rule, as `nomi update` gives it for not
// leaving a line. A prerelease is in its release's line. `nomi update` moves
// within a line by the same Line, so selection and update agree on what
// may break. A requirement at a commit takes its repository at that
// commit: a build takes a repository at one point,
// so a commit and a tag on one repository anywhere in the graph, or two
// different commits, are an error naming both requirers, and two
// requirements at one commit are one (clash, which holds all three
// rules). Each Dep records the package whose [dependencies] first reached
// it (Via), which `nomi outdated` names.
//
// The versions a selection starts from are the lock's (Lock.Versions),
// each one more ask on its repository that clash holds to the
// requirements, every member's in a workspace: a lock in another line
// than the manifests, or at a tag where they name a commit, is a
// ClashError naming nomi.lock, which `nomi fetch --update` and `nomi add`
// of a version of that repository get out of. Within its line, a version
// the lock holds above what the requirements ask for stays: the
// raise `nomi update` makes to a repository only a dependency names lives
// in nomi.lock and nowhere else. A starting version only raises a
// repository a requirement reaches; it puts nothing in the build. Resolve
// is the same walk over the whole workspace in one from versions its
// caller gives: the commands that write nomi.lock (internal/pkgfetch) pass
// the lock's, changed where the command means to change them, and `nomi
// fetch --update` passes none, so the requirements alone select and such a
// repository returns to the highest version a dependency asks for.
// Missing lists the selected packages the cache lacks.
//
// The compiler's own repository (CompilerRepo) holds the first-party
// libraries under lib/, which share its release tags, and a library at
// one release is written against that release's std. So a release nomi
// takes the repository at its own version whatever tag a requirement or a
// lock line names, from the project or from any fetched package
// (Pinned, Takes): requirements on it never conflict by version, the lock
// records that version, and a package that needs a later compiler says so
// through its [package].nomi. A commit is an explicit request and is taken
// as named, so a commit beside a tag on it is still an error. A nomi
// built from source pins nothing.
//
// # Names
//
// Name resolution is per package. Graph.Names is each name the project's
// files import a dependency by, and each Dep's Names is the same for that
// dependency's own files, from its own [dependencies] and its own
// [package].name (NamesAt answers either by directory). A consumer's keys
// never reach a dependency's files. Each package also has one name in the
// program (Dep.Name), the first segment of its files' keys and the key of
// its entry under [effects.deps.packages]: the project's key for a package
// the project names, else the key of the first package that reaches it,
// with `~2`, `~3` added when that name is taken by another package or by a
// file or directory at the project's root (rootNames). A tool of a
// dependency runs under the project's key for it, and reads its table in
// the project's nomi.toml by that key (cmd/nomi's `nomi tool`,
// vmhost.ToolPackageEnv); a dependency's own [package].name (Dep.Self) is
// only what an effects list that writes it is told to write instead.
//
// # Dir entries
//
// A dir entry is read in the package being built and in each package loaded
// from a directory (localPackages), relative to the directory of the
// nomi.toml that writes it: a package on disk has its neighbors on disk, so
// a project that names sqlkit by its directory takes sqlkit's
// `../migrate` too. An entry with a dir and a repository makes the
// directory stand for that package wherever another package requires it.
// The main package's entry for a package wins over a dependency's dir for
// it, and otherwise the first directory the walk reaches for a package
// does; a directory reached again is the package read before, so a cycle
// ends. A package loaded from a directory has no nomi.lock line
// (Dep.Local).
//
// In a fetched package's nomi.toml, a dir entry that stays inside its
// repository names the neighbor package in that directory at the same tag
// (neighbor): lib/sql/sqlkit's `sqlite = { dir = "../sqlite" }` is
// lib/sql/sqlite of the same repository at the same version, so packages
// of one repository name each other by directory with no version, in a
// checkout and fetched alike. A dir entry of a fetched package that leaves
// its repository is an error.
//
// A dependency's tool (`nomi tool sqlkit generate`) is a program rooted in
// the dependency's directory, so `nomi tool` sets NOMI_MAIN_PACKAGE
// (MainPackageEnv) to the project root, and while it is set a graph rooted
// at a package the project needs is the project's (MainPackage, asked by
// Walk, Resolve and Load's fetch). The tool then reads its neighbor
// packages at the project's versions and through the project's dir
// entries. It is an environment variable so the process the tool runs in
// reads the same graph. TestLibRelease_AProjectOfLibPackagesNeedsNoGo
// (cmd/nomi) runs sqlkit's generator from the package cache on a release
// `nomi` with no Go.
//
// # Errors name a nomi command
//
// A Nomi user edits nomi.toml through `nomi`, so an error or hint about a
// missing dependency names the `nomi` command that fixes it: ImportProblem
// for an import whose first name no file and no [dependencies] entry
// gives (`nomi add`, with the directory when one nearby holds a package of
// that name), or a dependency that is not a package; MissingHint for a
// needed package the cache lacks (`nomi fetch`).
package pkgcache
