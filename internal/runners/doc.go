// Package runners finds, checks, downloads, builds and caches the runners
// `nomi build` appends a program's IR image to: cmd/nomi-vm, the wasip1
// reactor cmd/nomi-vm-reactor, and the lean variant of each (Variant).
// `nomi build`, `nomi build-vm`, `nomi build --web` and `nomi run --web`
// call it. Running, checking and testing a program use none of it.
//
// A runner is built from the compiler's own module and shared by every
// program: one per compiler identity, target and variant (with or without
// std/compiler's hosts). A wasip1 program with an `export fn` is appended
// to the reactor instead, the same VM linked with -buildmode=c-shared as a
// WASI reactor. The cache key holds the compiler identity
// (CompilerIdentity), because a runner is a binary linking the compiler's
// VM and rt: a warm cache must not serve one built from a different tree.
//
// # Finding a runner
//
// Runner answers a runner for a target and variant without building one
// when it can: beside `nomi`, then built from a source checkout, then in the
// download cache, then from the release this `nomi` was cut as, which
// ReleaseVersion names (scripts/release.sh stamps it). A release `nomi`
// skips the source checkout: its release ships every variant, std/compiler's
// in an archive of its own per target, so it never builds a runner.
// prebuilt.go's opening comment gives the order and the check that a
// prebuilt runner comes from the same commit as `nomi` and is the variant
// asked for, by the build tags it records.
//
// # The compiler's source
//
// A runner built from source needs the compiler module
// (ResolveCompilerSource, compilersource.go): the checkout this `nomi` was
// built from; for a versioned `nomi` that is not a release (main module
// version a tag, or a pseudo-version from `go install ...@version`, no
// +dirty) that version, which Go fetches; or the checkout
// NOMI_COMPILER_SOURCE names.
//
// # The cache
//
// Every runner cache directory is made through CacheDirForKey under os.UserCacheDir()/nomi/builds/<hash>/, or
// under NOMI_RUNNER_CACHE_ROOT, which a test must set. A runner's vm.json
// (Record) says which compiler, target and variant built it, and a runner
// is reused only when every field matches (Cached). Each directory records
// the root it belongs to; one whose root is gone, or that nothing has used
// for artifactcache.Unused, is removed, and the count is bounded by
// NOMI_RUNNER_CACHE_MAX_ENTRIES (evict.go).
//
// The `go` every build runs is findGo's: the one NOMI_GO names, else the one
// on PATH. With neither, the error names the PATH entries it searched.
package runners
