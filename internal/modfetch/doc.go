// Package modfetch puts the modules a Nomi project needs into the Go module
// cache, through the module proxy protocol and the checksum database, with
// golang.org/x/mod and without the go command (docs/tooling.md, *Modules
// and dependencies*). A pure-Nomi dependency therefore needs no Go
// toolchain. internal/modcache decides which modules a project needs and
// reads them; this package downloads, verifies and extracts the ones the
// cache lacks (fetch.go), writes nomi.lock, and edits nomi.toml's
// [dependencies] for `nomi add`, `nomi remove` and `nomi update` (edit.go),
// and keeps the module's go.mod (gomod.go). `nomi new` and `nomi init`
// write the first nomi.toml (cmd/nomi/init_cmd.go).
//
// # Verifying and locking
//
// Every module zip, downloaded or already in the cache, is checked against
// the h1: hash nomi.lock holds for its version, and a different hash is a
// SECURITY ERROR that installs nothing. A zip nomi.lock does not hold is
// checked against the checksum database (GOSUMDB, unless GONOSUMDB,
// GOPRIVATE or GOSUMDB=off exempts it) when the fetch may update the lock
// (Options.Update: `nomi fetch`, `add`, `remove` and `update`, and
// GOFLAGS=-mod=mod), and kept for it; otherwise it is an error that says to
// run `nomi fetch`, as the go command's -mod=readonly default refuses a
// missing go.sum line. A downloaded go.mod is checked against the checksum
// database when the fetch may update; nomi.lock holds no go.mod hash.
//
// A plain fetch walks with the lock (modcache.Walk), so a run reads the
// locked versions. An updating one selects versions from the requirements
// alone (modcache.Resolve) and writes nomi.lock as that build list: each
// module from the proxy at its selected version with its verified hash,
// marked `direct` when the project names it. A file with the same text is
// not written again.
//
// # Proxies, credentials and direct
//
// A proxy is an http(s) server or a file:// directory (readURL). The user
// info of an http(s) proxy's URL in GOPROXY is its credentials, sent as
// basic authentication; nomi reads no .netrc and no GOAUTH, and an error
// names a URL with its password left out (url.URL.Redacted).
//
// nomi reads no version control repository. A module GOPROXY sends
// `direct` (GONOPROXY or GOPRIVATE names it, or the proxies before
// `direct` do not have it; directError) is downloaded by the go command
// when one is on PATH, and its hash is then checked against nomi.lock as
// any download's. With no `go` on PATH it is an error that names the
// module, says why it went `direct` and what to set to serve it from a
// proxy (directError.noGo).
//
// # go.mod
//
// Go's module proxy reads a module's path from its go.mod: it serves a
// module in a subdirectory of its repository, or at a /vN path, only with a
// go.mod there that names it, and leaves a directory with a go.mod out of
// the zip of the module around it. So a module whose nomi.toml has a
// [module].path keeps a go.mod beside it that holds only a header comment
// and its module line (gomod.go, GoModText), and nomi reads nothing from
// it. WriteGoMod writes it when the file there is missing or differs; a
// fetch that writes nomi.lock calls it, and so do `nomi new` and `nomi
// init` (cmd/nomi). GoModProblem says what is wrong with the file, which
// `nomi check` fails with, naming `nomi fetch`. A Nomi module holds no Go
// code, so a go.mod there that says more than its module line is nomi's
// too, and is rewritten. A directory that holds Go code is a Go module's
// (an embedding program's, or this repository's root), so its go.mod is
// kept as written and nothing is said about it (goModPlan, holdsGo); a
// module with no path gets none. In a directory with no Go code, a go.sum,
// and a go.mod beside a nomi.toml with no path, are read by nothing
// (UnreadGoFiles): cmd/nomi's check and module commands say so in a note,
// and nomi deletes neither.
//
// # Editing [dependencies]
//
// Add, AddLocal, Remove and Update change the project's entries and write
// the table back through analysis.WriteDependencies, which keeps the
// file's comments, blank lines and other tables, keeps an entry's comment
// when the entry changes, and puts a new entry at the end of the table.
// Each then fetches with Update, so nomi.lock follows. Add names a module
// that holds a nomi.toml, under the name given, else the name an entry
// already gives its path, else the module's own [module].name; AddLocal
// writes a dir entry. Remove answers the modules nomi.lock held that the
// build no longer needs. Update moves entries with a version and no dir to
// their latest release and answers each change of the build list from the
// lock it read.
//
// Every edit keeps the first-party lib/ entries on one version
// (alignLib): they are versioned by nomi's commits and tested together.
//
// # Tests
//
// Under `go test` a download refuses unless GOMODCACHE is set, so no test
// writes the real module cache. Tests serve modules from a file:// GOPROXY
// and a test checksum database (fetch_test.go) or GOSUMDB=off, never the
// network.
package modfetch
