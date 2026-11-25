// Package runners finds, checks, downloads, builds and caches the runners
// `nomi build` appends a program's IR image to: cmd/nomi-vm, the wasip1
// reactor cmd/nomi-vm-reactor, and the lean variant of each (Variant).
// `nomi build`, `nomi build-vm`, `nomi build --web` and `nomi run --web`
// call it. Running, checking and testing a program use none of it.
//
// A runner is built from the compiler's own module and shared by every
// program: one per compiler identity, target and variant. A wasip1 program
// with an `export fn` is appended to the reactor instead, the same VM linked
// with -buildmode=c-shared as a WASI reactor. The cache key holds the
// compiler identity (CompilerIdentity), because a runner is a binary
// linking the compiler's VM and rt: a warm cache must not serve one built
// from a different tree.
//
// # Two tiers
//
// The runners come in two tiers and never a combination. The lean one
// (nomi-vm-nonet, Variant.NoNetwork) is built with every optional family's
// tag (families.LeanTags) and has no family; the full one (nomi-vm) has
// every family. A program that reaches no family's crossing is appended to
// the lean one, and any other to the full one. A new optional family joins
// the full tier and adds no runner. std/compiler's runner (Variant.Compiler)
// is the full one with the front end linked, and has no lean variant:
// Runner refuses one.
//
// # Finding a runner
//
// Runner answers a runner for a target and variant without building one
// when it can: the one this `nomi` carries, then beside `nomi`, then built
// from a source checkout, then in the cache of runners unpacked or
// downloaded, then from the release this `nomi` is, which
// buildversion.Release names. A `nomi` scripts/release.sh built
// (buildversion.BuiltByReleaseScript) carries its own platform's two
// tiers, gzipped (built with the
// nomi_hostrunners tag from what internal/runners/hostrunnersgen writes,
// as the std bundle is built in with nomi_stdbundle), and unpacks one into
// the cache the first time a build needs it. It skips the source checkout:
// its release ships every other runner, each platform's two in a runners
// archive and std/compiler's in a compiler archive, so it never builds one.
// A `nomi` built with plain `go build` or `go install` carries none.
// prebuilt.go's opening comment gives the order and the check that a
// prebuilt runner comes from the same commit as `nomi` and is the variant
// asked for, by the build tags it records.
//
// # The compiler's source
//
// A runner built from source needs the compiler module
// (ResolveCompilerSource, compilersource.go): the checkout this `nomi` was
// built from; for a versioned `nomi` scripts/release.sh did not build
// (main module version a tag, or a pseudo-version from `go install
// ...@version`, no +dirty) that version, which Go fetches; or the checkout
// NOMI_COMPILER_SOURCE names.
//
// # The cache
//
// Every runner cache directory built from source is made through
// CacheDirForKey under os.UserCacheDir()/nomi/builds/<hash>/, or under
// NOMI_RUNNER_CACHE_ROOT, which a test must set. A runner a release `nomi`
// carries or downloads goes under the same root, in
// nomi-vm/<version>/<goos>_<goarch>/. A runner's vm.json
// (Record) says which compiler, target and variant built it, and a runner
// is reused only when every field matches (Cached). Each directory records
// the root it belongs to; one whose root is gone, or that nothing has used
// for artifactcache.Unused, is removed, and the count is bounded by
// NOMI_RUNNER_CACHE_MAX_ENTRIES (evict.go).
//
// A release's directory, nomi-vm/<version>/, follows the artifact cache's
// rule (internal/artifactcache package doc, "Pruning"): answering one of its
// runners marks it used (artifactcache.Touch), and at most once a day that
// answer removes every other release's directory nothing has used for
// artifactcache.Unused (useRelease). The running release's own directory is
// never a candidate, so a removed release is one that is not building, and
// it unpacks the runners it carries, or downloads the others, again when it
// next builds. A prune leaves what it cannot remove, as Windows refuses to
// remove a file another process holds open.
//
// The `go` every build runs is findGo's: the one NOMI_GO names, else the one
// on PATH. With neither, the error names the PATH entries it searched.
package runners
