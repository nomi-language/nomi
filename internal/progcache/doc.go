// Package progcache keeps what a `nomi` command built from a program's
// files in the artifact cache, so the next command over the same files
// reads it instead of checking and lowering the program again. What a
// record holds, and when a program is kept, is vmhost's (its package doc,
// "Kept programs"); this package keeps the record and decides when it still
// describes the files.
//
// # Key
//
// An entry is keyed by the compiler's identity (artifactcache.CompilerID),
// what was built (its kind: `load`, or a build with its target and flags),
// the entry file's absolute path and the environment variables a build
// reads: every `NOMI_` variable but the cache's own and the diagnostics'
// (unkeyedEnv), and HOME, XDG_CACHE_HOME, LocalAppData, TZ, LANG and
// LC_ALL (keyEnv).
// A record nothing reads for five days is pruned, at most once an hour.
//
// # Snapshot
//
// A record names the files the build read. A [Tree] is walked for the files
// a build can read in it (`.nomi`, `.toml` and `nomi.lock`), or for every
// file when it is an `embed` base; hidden names and node_modules are
// skipped in both.
// Above each source tree the markers that decide its program are stat'ed
// too: `nomi.toml` and `main.nomi`, which decide the program's root
// (analysis.ProjectRoot), and a workspace's `nomi-workspace.toml` and
// `nomi.lock`, which select a member's dependencies. Single files outside the trees are named on their own.
// Each file is held by its size, times, inode and SHA-256. A tree with
// NamedSources holds its `.nomi` files by presence alone, and the record
// names the sources the build read on their own, with their text: an edit
// to a source the build did not read changes nothing, and a source that
// comes or goes is a difference, since it can change what an import names.
//
// A lookup walks the same trees and stats the same files. A file whose stat
// matches and was last changed more than two seconds (racyWindow) before
// the snapshot is taken as it was; any other is hashed and compared, so a
// file rewritten with its own text is still a hit and an edit in the same
// timestamp tick is not. Any difference, a file added or removed in a tree
// included, is a miss. Files a run writes in its project (a log, a
// database) change nothing unless an `embed` base holds them.
//
// The record is made from a snapshot taken before the build starts, and is
// written only when nothing in the snapshot changed while the build ran; a
// tree the snapshot had not predicted (a dependency's) is kept only when
// its files are older than two seconds. A source a walk holds by presence
// and the record names is held at its text then: the walk hashed it when
// it had changed within two seconds of the snapshot, and is hashed when it
// is stored otherwise, its stat still the walk's. So a record never pairs a
// file's later text with a build of earlier text.
//
// [Entry.Sibling] opens another kind's record for the same program from the
// same snapshot (vmhost's file records and stored passes). A record keeps
// its payload's SHA-256, made when it is stored, and a hit answers it
// ([Entry.Sum]): a record of another kind names by it the payload it was
// made from, with no hash made on the lookup. [Entry.ChangedSources] answers the
// files whose text changed and the `.nomi` files added and removed in a
// tree walked for sources, when nothing else differs: what a relink reads.
//
// A record that does not decode, is truncated or fails its checksum is a
// miss, never an error. Writes go through a temporary file renamed into
// place, so two processes racing on one key are harmless.
// `NOMI_DEBUG_ARTIFACT_CACHE=1` ([DebugEnv]) reports each lookup and write
// and why it missed or was not written.
//
// # Tests
//
// This package's tests hold the record's rules one by one: each kind of
// input change, a file rewritten with its own text, a same-sized edit with
// its time put back, a file changed while the build ran, the environment in
// the key, pruning, and racing writers (run them with -race).
// TestChangedSourcesNamesSourcesAddedAndRemoved holds which files may come
// and go, and TestANamedSourcesTreeHoldsTheTextOfTheNamedSources what a
// tree with NamedSources holds. Under `go test` a process keeps no program unless the test points
// NOMI_ARTIFACT_CACHE at a directory of its own (artifactcache.ProgramRoot).
// A `nomi` a test starts is not a test process: it keeps programs under the
// directory the test gave it, or the one artifactcache chose for the test
// binary.
package progcache
