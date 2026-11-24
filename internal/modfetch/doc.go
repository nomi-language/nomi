// Package modfetch puts the modules a Nomi project needs into the module
// cache, fetched from their repositories' hosts over HTTPS, with no Go
// toolchain and no Go variable read (docs/tooling.md, *Modules and
// dependencies*). internal/modcache decides which modules a project needs
// and reads them; this package fetches, verifies and extracts the
// repositories the cache lacks (fetch.go, host.go), writes nomi.lock, and
// edits nomi.toml's [dependencies] for `nomi add`, `nomi remove` and
// `nomi update` (edit.go). `nomi new` and `nomi init` write the first
// nomi.toml (cmd/nomi/init_cmd.go).
//
// # Hosts
//
// A repository is read in two requests, with no git. Its tags come from
// git's smart HTTP advertisement, `GET
// https://<repo>.git/info/refs?service=git-upload-pack` (sourcehut's
// without `.git`), sent with no Git-Protocol header so every server
// answers version 0: pkt-lines naming each ref and its object, from which
// each `refs/tags/vX.Y.Z` tag is taken with the commit it peels to
// (parseAdvertisement, tagCommits). A tag's files come from the host's
// archive of it, a tar.gz with one top directory, whatever its name, that
// extraction strips. A module taken at a commit needs no advertisement:
// its files come from the host's archive of the commit, by its id.
// knownHosts holds each host's archive URL, {ref} a tag or a commit:
//
//   - github.com: https://github.com/{owner}/{repo}/archive/refs/tags/{tag}.tar.gz,
//     and https://github.com/{owner}/{repo}/archive/{commit}.tar.gz for a commit
//   - gitlab.com: https://gitlab.com/{path}/-/archive/{ref}/{base}-{ref}.tar.gz
//   - codeberg.org: https://codeberg.org/{owner}/{repo}/archive/{ref}.tar.gz
//   - bitbucket.org: https://bitbucket.org/{owner}/{repo}/get/{ref}.tar.gz
//   - git.sr.ht: https://git.sr.ht/~{user}/{repo}/archive/{ref}.tar.gz
//
// Each form was answered by its host for a public repository's tag and
// for one of its commits (TestKnownHosts_ArchiveURLs holds the cases).
//
// Any other host, and a known host that answers 401, 403 or 404 when git
// is on PATH, is read through git: `git ls-remote --tags`, then a depth-1
// fetch of the tag into a temporary repository and `git archive` of it.
// A commit is fetched by its id at depth 1, with no tag listing. A server
// serves a commit by its id when a ref reaches it and it speaks protocol
// version 2, which git asks for, or sets uploadpack.allowReachableSHA1InWant;
// one that refuses (`not our ref`), or a commit the repository lacks, is a
// commitRefusedError, which says so and holds git's message (gitArchive,
// refusesCommit).
// git reads the user's configuration, so their credential helpers answer
// for a private repository and their `insteadOf` rewrites apply, and it
// never prompts on the terminal. There is no token variable and no lookup
// of a vanity path. With no git on PATH, such a repository is an error
// that says it needs git (noGitError).
//
// git reads a repository over HTTPS first, and over SSH, as
// `ssh://git@<repo>`, when HTTPS fails for want of credentials (gitRead):
// a developer's access to a private repository is often an SSH key with
// no credential helper for HTTPS. git says it wanted credentials only in
// its message, so wantsCredentials reads git's English message
// (LC_ALL=C) for the three ways git words it, and nothing else sends a
// repository to SSH. ssh runs as git would run it with `-o
// BatchMode=yes`, so it never prompts and an unknown host key fails with
// ssh's own message (sshCommand). One fetch reads a repository's tags and
// its tag's files over the one transport, and reads every repository of a
// host over SSH once one of them needed it. When both fail, the error
// holds both of git's messages and says what gives git access
// (gitAccessError). Tests run git through gitCommand, which a test
// replaces to answer as git would.
//
// `nomi add <repo>/<subdir>` splits its argument at the host's repository
// depth on a host whose repositories have a fixed number of path elements
// (two on GitHub, Codeberg, Bitbucket and sourcehut). Elsewhere it asks
// for the tags of ever-shorter prefixes of the argument and takes the
// longest a host answers (locate).
//
// # Extracting
//
// An archive is extracted into a temporary directory beside the cache
// entry (extract): only regular files and directories are installed. A
// link, symbolic or hard, is not installed and is recorded as a link in
// the list of the repository's files; a module whose directory holds one
// (its subdir, or the whole repository for the root module) is an error
// that names the link's path when it is resolved, a neighbor it names by
// its directory too, and a link elsewhere in the repository changes
// nothing. A device or a path that escapes is refused. Each file's SHA-256
// is taken as it is written, and a module's hash covers its regular files
// alone. Every module of the repository at that tag
// that nomi.lock holds is checked against its hash before anything is
// installed; then the tree is made read-only, the list of its files is
// written beside it, and it is renamed into place (download). A process
// that loses the race to rename keeps the other's copy. A fetch prunes the
// cache at most once a day (modcache.Prune).
//
// # Verifying and locking
//
// Every module, fetched or already in the cache, is checked against the
// commit and hash nomi.lock holds for its version (verify): a different
// hash, or a tag that names another commit, is a SECURITY ERROR, and a
// fetch that finds one installs nothing. A module nomi.lock does not hold
// is an error that says to run `nomi fetch`, unless the fetch may update
// the lock (Options.Update: `nomi fetch`, `add`, `remove` and `update`),
// which then locks its commit and hash. A cache hit makes no network
// request, so a run whose lock is current and whose modules are cached
// makes none. Offline (`--offline`, Options.Offline) a module the cache
// lacks is an error that names it.
//
// A plain fetch walks with the lock (modcache.Walk), so a run reads the
// locked versions. An updating one selects versions from the requirements
// alone (modcache.Resolve) and writes nomi.lock as that build list: each
// fetched module at its selected version with its commit and hash, marked
// `direct` when the project names it. A file with the same text is not
// written again.
//
// # Editing [dependencies] and [indirect]
//
// Add, AddLocal, Remove and Update change the project's entries and write
// the tables back through analysis.WriteRequirements, which keeps the
// file's comments, blank lines and other tables, keeps an entry's comment
// when the entry changes, and puts a new entry at the end of the table.
// Each then fetches with Update, so nomi.lock follows, and puts nomi.toml
// back as it was when that fetch fails (restore). One tag versions a
// repository, so the project's entries of one repository with no dir
// take it at one version: the highest they name (versions), to which Add
// and Update move them together (moveTo). Add names a module that holds a
// nomi.toml under the name given, else the name an entry already gives
// it, else the module's own [module].name. With no version written after
// `@`, it takes the version the project's entries already take the
// repository at (Added.Held), else the repository's latest release; a
// version written, `latest` among them, or a commit, which the entry
// writes as its `commit`, moves the repository's other entries to it
// (Added.Moved), into another line too (modcache.Line). AddLocal
// writes a dir entry, which keeps the repository and version of an entry
// of that name. Remove answers the modules nomi.lock held that the build
// no longer needs. Update moves entries with a version and no dir to their
// repository's highest release in the same line (modcache.Line, the line
// selection holds every requirement to: the major version from v1 on, the
// minor version below v1) and answers each change of the build list from
// the lock it read; `nomi add <repo>@<version>` is how a project crosses
// a line. An entry at a commit has no later release: Update leaves it as
// it is and answers it (Updated.AtCommit). With no entry named, Update
// then covers the whole build: it resolves it, raises each repository
// only a dependency names to its latest release in its line (newest, the
// rule the entries move by), writes each into the project's [indirect]
// table, and resolves again until none rises, then drops the floors of
// repositories the build no longer reaches. editDependencies writes both
// tables, and drops the floor of a repository an entry then names, so Add
// of a module of a floored repository takes its floor out. Outdated is
// the read-only view of the same rule: each repository of the locked
// build with a later release, the latest in its line and of all, and the
// modules that reach it (modcache.Dep.Via). It reads tags from the hosts,
// and offline it is an error: no list of tags is kept.
//
// The first-party libraries are directories of one repository,
// github.com/nomi-language/nomi (nomiRepo), versioned by its release tags,
// and they name each other by dir entries, which need no version. A
// release `nomi` has its own version (modcache.Release, which cmd/nomi's
// release records from internal/runners.ReleaseVersion when it is a tag's
// version): Add of a library with no version, to a project that takes no
// library yet, takes it at that version, and Update moves the libraries to
// it, down from a later tag too. A
// repository without that tag is a releaseTagError naming the tag. A
// version on the command line is taken as written. A `nomi` built from
// source takes the latest release, and says so when there is none.
// LibOffRelease answers the libraries nomi.lock locks at another version,
// which cmd/nomi notes before `run`, `test`, `check` and `build` fetch.
//
// # Tests
//
// Under `go test` the cache is an error unless NOMI_MODULE_CACHE is set
// (modcache.Root), so no test writes the real cache. Tests serve
// repositories from an in-process HTTP server that answers the tag
// advertisement and GitHub's archive form (testhost_test.go): a Go test
// replaces hostTable, and a `nomi` a test starts reaches the server
// through NOMI_TEST_MODULE_HOSTS (testHostsEnv), host=URL pairs, which
// nothing else sets. Only the tests of the git fallback need git on PATH,
// and they skip without it. No test reads the network.
package modfetch
