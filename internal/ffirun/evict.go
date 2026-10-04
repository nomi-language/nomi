package ffirun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Eviction for the build cache.
//
// WHY A MARKER FILE AND NOT hash.json. The directory name is
// SHA256(absolute project root)[:16] and nothing else, so "is this
// entry stale?" is a question about a path the name cannot be
// inverted to. hash.json is the obvious home for that path and it is
// the wrong one: on this machine 512 of 2656 cache directories were
// scratch directories with no hash.json at all. A carrier that 19% of
// entries never get is not a carrier. origin.json is written by the
// one function every path funnels through (cacheDirForKey), so every
// directory the cache creates has one.
//
// The marker does two jobs, and they are the same job: it says WHICH
// project root this directory belongs to (in its contents) and WHEN
// that root last used it (in its mtime). The mtime half matters
// because a warm cache hit writes nothing — ensureWrapper
// short-circuits — so before this file existed a project built daily
// for a fortnight looked exactly as cold as one abandoned on day one.
// cacheDirForKey rewrites the marker on every call, warm or cold, so
// last-use is a real reading rather than last-REGENERATION.
//
// TWO RULES, SEPARABLE:
//
//  1. Provably dead. The recorded project root no longer exists.
//     The key is the absolute root path, so a directory whose root is
//     gone can never be hit again by anything. Unbounded: there is no
//     working set to protect.
//
//  2. Bounded LRU. Everything else, including the legacy entries that
//     carry no marker, ranked by last use and cut at maxCacheEntries.
//     This is the rule that gives the cache a ceiling; rule 1 is what
//     makes satisfying the ceiling cheap, by spending the removals on
//     entries that provably cannot be wanted.
//
// See planEviction for the concurrency argument.
const (
	// originFile records the project root a cache directory belongs
	// to, and stamps last use in its mtime.
	originFile = "origin.json"

	// cacheKindRun is the kind origin.json records for a wrapper
	// directory. `nomi build`'s runner directories record
	// cacheKindRunner (runner.go); eviction treats both alike.
	cacheKindRun = "run"

	// maxEntriesEnv overrides maxCacheEntries. Zero or negative
	// disables eviction entirely; tests use it to plant a small
	// bound.
	maxEntriesEnv = "NOMI_FFIRUN_CACHE_MAX_ENTRIES"

	// maxCacheEntries is the ceiling on cache directories.
	//
	// THE NUMBER. One reading of an unbounded
	// ~/Library/Caches/nomi/builds found 2656 directories, 19 GB, of
	// which 50 had been written in the previous seven days. So 256 is five times the
	// measured week-long working set. It is also about twice the
	// ~120 project roots simultaneously reachable from this
	// workflow's ~20 live git worktrees (about six FFI-using roots
	// per worktree).
	//
	// THE CEILING IT BUYS IS 4.6 GB, and a reader who sees "bounded"
	// will assume less, so: the bound is on COUNT, not bytes. 256
	// entries of the kind that SURVIVES a sweep — live-rooted, and so
	// holding a linked wrapper binary — is 256 x 18.1 MB = 4.6 GB,
	// measured across the 1119 wrapper-holding directories of the
	// 2656.
	//
	// The 7.6 MB average across ALL 2656 is the wrong denominator and
	// would read 1.9 GB. It is dragged down by 1242 t.TempDir()-rooted
	// entries averaging 3.3 MB, which are the population cacheRoot()'s
	// refusal under `go test` prevents. And rule 1 pushes the
	// average UP rather than down: it removes the small dead entries
	// and leaves the large live ones.
	//
	// The cost of being wrong on the low side is one relink, measured
	// at 2.1 s (Go's own build cache absorbs compilation, so only the
	// link is paid). Halving 256 would cost a handful of
	// 2.1 s rebuilds; leaving the cache unbounded costs 19 GB, so the
	// bound is deliberately generous rather than tight.
	maxCacheEntries = 256

	// evictGraceWindow protects any entry used within it, under BOTH
	// rules. This is the concurrent-build defence — see planEviction.
	//
	// The number: the longest thing that runs inside a cache
	// directory is a cold `go build` of the wrapper, ~2 s, and the
	// longest whole-corpus sweep that keeps hitting one directory is
	// 213 s. One hour is 17x the sweep and three orders of magnitude
	// over the build. What it costs: an entry that dies now survives
	// until an hour has passed, so the cache can carry up to an
	// hour's worth of fresh corpses. That is bounded by the build
	// rate, which is bounded by 2.1 s per build.
	evictGraceWindow = time.Hour

	// trashPrefix names a directory that has been renamed out of the
	// live key space and is being deleted. A leading dot keeps it out
	// of the 16-hex key space by construction, so the entry scanner
	// ignores it and a crashed sweep leaves litter that the next
	// sweep collects rather than an entry nothing will ever look at.
	trashPrefix = ".trash-"
)

// originRecord is <cacheDir>/origin.json.
//
// ProjectRoot is the absolute path cacheDirForKey hashed (without any
// key discriminator — staleness is a question about the root's
// existence, not about which of the root's two keys this is).
type originRecord struct {
	ProjectRoot string `json:"project_root"`
	Kind        string `json:"kind"`
}

// recordOrigin writes the marker, replacing any existing one so its
// mtime advances to now. This runs on every invocation including a
// warm hit — that is the point of it.
//
// Not written through a temp+rename: a torn write leaves a prefix of
// a JSON object, which does not parse, which readOrigin reports as
// "no recorded root", which demotes the entry to the LRU rule. Every
// failure mode of this write is a safe one, so it does not need to be
// atomic and pays one open instead of three syscalls plus a rename on
// every build.
func recordOrigin(cacheDir, projectRoot, kind string) error {
	data, err := json.Marshal(originRecord{ProjectRoot: projectRoot, Kind: kind})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, originFile), data, 0o644)
}

// readOrigin reads the marker. The bool is false for a missing,
// unreadable, malformed, or rootless record — every one of which
// means the same thing to the policy: this entry's staleness is not
// decidable, judge it by the bound.
func readOrigin(cacheDir string) (originRecord, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, originFile))
	if err != nil {
		return originRecord{}, false
	}
	var r originRecord
	if err := json.Unmarshal(data, &r); err != nil || r.ProjectRoot == "" {
		return originRecord{}, false
	}
	return r, true
}

// maxCacheEntriesSetting resolves the bound, honoring the override. A
// value that does not parse is ignored in favour of the default
// rather than treated as zero: a typo in an env var must not silently
// switch eviction off.
func maxCacheEntriesSetting() int {
	if v := os.Getenv(maxEntriesEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return maxCacheEntries
}

// isCacheKeyName reports whether name is a directory name this cache
// could have produced: exactly 16 lowercase hex digits.
//
// This is a safety property and not tidiness. Eviction is a
// RemoveAll, NOMI_FFIRUN_CACHE_ROOT is a user-settable path, and a
// root pointed at a home directory must not turn cache hygiene into
// data loss. Only names the cache itself could have written are ever
// candidates.
func isCacheKeyName(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, c := range []byte(name) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// evictReason says which rule selected a victim. Carried so the two
// rules can be asserted apart.
type evictReason int

const (
	evictDeadRoot evictReason = iota
	evictOverBound
)

// victim is one selected entry: its key name, the rule that chose it,
// and the last-use reading the choice was made on. The reading is
// kept so applyEviction can re-check it — see there.
type victim struct {
	name    string
	reason  evictReason
	lastUse time.Time
}

// cacheEntry is one scanned directory.
type cacheEntry struct {
	name    string
	lastUse time.Time
	// root is the recorded project root; hasRoot is false for a
	// legacy entry that predates origin.json.
	root    string
	hasRoot bool
}

// scanCacheEntries lists the cache root's entries with a last-use
// reading for each.
//
// Last use is the marker's mtime when there is a marker, and the
// DIRECTORY's own mtime otherwise. The fallback is the best available
// reading for a legacy entry: a directory's mtime moves when a file
// is created or removed in it, which covers every write the cache
// makes (the wrapper binary, the hash.json rename, a regenerated
// main.go). It does NOT move on a warm hit, so a legacy entry can
// read older than it truly is — which is exactly why the marker
// exists, and why legacy entries are reachable only by the bound and
// never by the dead-root rule.
func scanCacheEntries(root string) ([]cacheEntry, error) {
	dirents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	entries := make([]cacheEntry, 0, len(dirents))
	for _, de := range dirents {
		if !de.IsDir() || !isCacheKeyName(de.Name()) {
			continue
		}
		e := cacheEntry{name: de.Name()}
		dir := filepath.Join(root, de.Name())
		if st, err := os.Stat(filepath.Join(dir, originFile)); err == nil {
			e.lastUse = st.ModTime()
			if rec, ok := readOrigin(dir); ok {
				e.root = rec.ProjectRoot
				e.hasRoot = true
			}
		} else if info, err := de.Info(); err == nil {
			e.lastUse = info.ModTime()
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// rootIsGone reports whether path is provably absent.
//
// Only fs.ErrNotExist counts. A permission error, an I/O error or an
// unmounted volume is NOT evidence that a project is gone, and
// treating it as such would delete a live working set the first time
// a disk hiccuped.
func rootIsGone(path string) bool {
	_, err := os.Stat(path)
	return err != nil && errors.Is(err, fs.ErrNotExist)
}

// planEviction selects victims. keep is the entry the caller is about
// to use and is never selected. now is injected so tests can plant
// ages without sleeping.
//
// SAFETY AGAINST A CONCURRENT BUILD. Several `nomi run` / `nomi
// build` processes share this root, and two omp instances in one
// directory share it too. The failure to avoid is unlinking a wrapper
// out from under a running `go build`, which surfaces as a spurious
// ENOENT compile error. Three things stand between the sweep and that:
//
//  1. The caller's own entry is excluded by name. Whatever else is
//     true of the clock, the directory this process is about to build
//     in cannot be chosen.
//
//  2. Every entry used within evictGraceWindow is excluded, under
//     both rules. cacheDirForKey stamps the marker BEFORE any other
//     work in the directory, so a process that is currently building
//     has a stamp seconds old, and an hour of grace is three orders
//     of magnitude more than the ~2 s a cold wrapper build takes.
//     This is why there is no lock file: an O_EXCL lock is only
//     correct with a staleness timeout for the process that crashed
//     holding it, and that timeout IS this window, reached with more
//     moving parts and a new failure mode of its own.
//
//  3. applyEviction re-reads the stamp immediately before removing
//     and renames before deleting. See there.
//
// A race between two sweeps is harmless: they may pick the same
// victim, and the loser's rename fails while the winner's RemoveAll
// proceeds.
func planEviction(entries []cacheEntry, keep string, max int, now time.Time) []victim {
	if max <= 0 {
		return nil
	}
	protected := func(e cacheEntry) bool {
		return e.name == keep || now.Sub(e.lastUse) < evictGraceWindow
	}
	var victims []victim
	survivors := make([]cacheEntry, 0, len(entries))
	for _, e := range entries {
		if !protected(e) && e.hasRoot && rootIsGone(e.root) {
			victims = append(victims, victim{name: e.name, reason: evictDeadRoot, lastUse: e.lastUse})
			continue
		}
		survivors = append(survivors, e)
	}
	if len(survivors) <= max {
		return victims
	}
	// Newest first, so the tail is the least recently used. Ties
	// broken by name so the plan is deterministic.
	sort.Slice(survivors, func(i, j int) bool {
		if !survivors[i].lastUse.Equal(survivors[j].lastUse) {
			return survivors[i].lastUse.After(survivors[j].lastUse)
		}
		return survivors[i].name < survivors[j].name
	})
	for _, e := range survivors[max:] {
		if protected(e) {
			continue
		}
		victims = append(victims, victim{name: e.name, reason: evictOverBound, lastUse: e.lastUse})
	}
	return victims
}

// applyEviction removes the planned victims and returns how many it
// removed.
//
// Two guards, both about the gap between planning and deleting:
//
//   - The marker's mtime is re-read and compared against the reading
//     the plan was made on. If a process started using this entry in
//     the meantime it has already stamped the marker, so the mtime
//     moved and the entry is spared. This narrows the exposure from
//     "the duration of a sweep" to "the gap between one stat and one
//     rename".
//
//   - The victim is renamed out of the key space before it is
//     deleted. Rename is atomic, so a concurrent process either sees
//     the whole directory or sees nothing and rebuilds from cold at
//     the measured 2.1 s. It never sees a half-deleted one, which is
//     the state that would produce a confusing compile error instead
//     of a rebuild.
//
// Errors are not returned per victim: eviction is hygiene and must
// never fail a build. An entry that cannot be removed is left for the
// next sweep.
func applyEviction(root string, victims []victim) int {
	removed := 0
	for _, v := range victims {
		dir := filepath.Join(root, v.name)
		if st, err := os.Stat(filepath.Join(dir, originFile)); err == nil && st.ModTime().After(v.lastUse) {
			// Somebody claimed it after the plan was made.
			continue
		}
		trash := filepath.Join(root, trashPrefix+v.name)
		if err := os.Rename(dir, trash); err != nil {
			continue
		}
		if err := os.RemoveAll(trash); err != nil {
			continue
		}
		removed++
	}
	return removed
}

// collectTrash deletes litter a crashed or interrupted sweep left
// behind. Cheap: the names come from the readdir the scan already
// needs.
func collectTrash(root string, dirents []os.DirEntry) {
	for _, de := range dirents {
		if strings.HasPrefix(de.Name(), trashPrefix) {
			_ = os.RemoveAll(filepath.Join(root, de.Name()))
		}
	}
}

// evictCache enforces the budget on root, never touching keep.
//
// TRIGGERING. The sweep costs one readdir when the cache is inside
// its budget and a stat plus a small read per entry when it is not.
// The readdir is unconditional and cheap; the per-entry work happens
// only when the directory count exceeds the bound. Because the sweep
// leaves the count AT the bound, the next build sees count == max and
// does no per-entry work — so a full sweep runs once per new project
// root rather than once per build. Measured on this machine at 2656
// entries: see the eviction overhead figure in
// TestEvict_SweepCostOnALargeRoot.
func evictCache(root, keep string) (int, error) {
	max := maxCacheEntriesSetting()
	if max <= 0 {
		return 0, nil
	}
	dirents, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	collectTrash(root, dirents)
	keys := 0
	for _, de := range dirents {
		if de.IsDir() && isCacheKeyName(de.Name()) {
			keys++
		}
	}
	if keys <= max {
		return 0, nil
	}
	entries, err := scanCacheEntries(root)
	if err != nil {
		return 0, fmt.Errorf("ffirun: scanning cache root %s: %w", root, err)
	}
	return applyEviction(root, planEviction(entries, keep, max, time.Now())), nil
}
