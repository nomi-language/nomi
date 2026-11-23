// Package artifactcache is where a compiler keeps what it builds once and
// reads back in later processes: the standard library's interfaces and
// lowering (std's artifact, internal/irbuild's std bundle), and each
// program's checked and lowered image (internal/progcache).
//
// The cache is os.UserCacheDir()/nomi/artifacts, or $NOMI_ARTIFACT_CACHE.
// Under `go test` without the variable it is a directory under os.TempDir,
// and the variable is set to it for the rest of the process, so a test never
// writes the user's cache and neither does a `nomi` a test runs, which
// inherits it. NOMI_ARTIFACT_CACHE=off turns the cache off.
//
// An entry is keyed by the compiler's identity (CompilerID), so a binary
// never reads another binary's entry. The identity is the executable's Go
// build ID, which ReadBuildID reads from the build ID note of an ELF file
// and from the start of the text of a Mach-O or PE one;
// TestReadBuildIDOnEveryReleaseTarget holds it to `go tool buildid` for
// every target a release ships `nomi` for. Writes go to a temporary sibling and
// are renamed into place, so a reader sees no file or a whole one; a reader
// still checks what it reads, since a file can be truncated or corrupted
// after it is written.
//
// # Pruning
//
// Every cache Nomi keeps drops what nothing uses, by one rule: an entry's
// modification time is its last use, a reader moves it (Touch), and a
// prune removes the entries of a directory, files or directories, that
// nothing has used for a limit (Prune), never the one the running process
// is about to use. The limit is Unused, two weeks, unless a kind keeps
// less: std's interfaces, state and bundle, keyed by the compiler build,
// go a day after the last process of that build used them, and a kept
// program (internal/progcache) after five days. A prune runs where an
// entry is written, or at most once a day where a directory is only read
// (Due). The directories and what prunes each:
//
//   - artifacts/std: std/artifact.go, std/state.go and internal/irbuild's
//     std bundle, when a build writes its own entry.
//   - artifacts/programs: internal/progcache, at most once an hour, when
//     it writes a record.
//   - builds/ (os.UserCacheDir()/nomi/builds, or NOMI_RUNNER_CACHE_ROOT):
//     internal/runners' runner builds, whose eviction (internal/runners) adds this rule to its own,
//     once a day or when the count is over its bound.
//   - ~/.cache/nomi/std (or NOMI_STD_CACHE_ROOT/std): internal/stdcache's
//     std sources, once per language server.
//
// A test process runs the same code over os.TempDir()/nomi-test-cache, and
// internal/parser's tree-sitter tests keep a compiled grammar per checkout
// under os.TempDir()/nomi-tree-sitter-lib by the same rule.
// Go's module cache is not pruned by age: it is Go's.
package artifactcache
