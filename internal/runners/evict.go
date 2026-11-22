package runners

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

	"github.com/nomi-language/nomi/internal/artifactcache"
)

// Eviction for the build cache.
//
// WHY A MARKER FILE. The directory name is the first 16 hex digits of
// the SHA-256 of its key (the compiler root, the variant and the
// target), so "is this entry stale?" is a question about a path the
// name cannot be inverted to. origin.json is written by the one
// function every path funnels through (CacheDirForKey), so every
// directory the cache creates has one.
//
// The marker does two jobs, and they are the same job: it says WHICH
// compiler root this directory belongs to (in its contents) and WHEN
// that root last used it (in its mtime). The mtime half matters
// because a warm cache hit writes nothing (Cached short-circuits), so
// without it a runner used daily for a fortnight would look as cold as
// one abandoned on day one. CacheDirForKey rewrites the marker on every call, warm or cold, so
// last-use is a real reading rather than last-REGENERATION.
//
// THREE RULES, SEPARABLE:
//
//  1. Provably dead. The recorded compiler root no longer exists.
//     The key is the absolute root path, so a directory whose root is
//     gone can never be hit again by anything. Unbounded: there is no
//     working set to protect.
//
//  2. Unused. An entry nothing has used for artifactcache.Unused, the
//     age every cache Nomi keeps drops an entry at (internal/artifactcache
//     package doc, "Pruning"). This is what empties a cache under its
//     bound of the runners of a compiler build nobody runs any more.
//
//  3. Bounded LRU. Everything else, including the unmarked entries that
//     carry no marker, ranked by last use and cut at maxCacheEntries.
//     This is the rule that gives the cache a ceiling; rules 1 and 2
//     are what make satisfying the ceiling cheap, by spending the
//     removals on entries that cannot be wanted or were not.
//
// See planEviction for the concurrency argument.
const (
	// originFile records the compiler root a cache directory belongs
	// to, and stamps last use in its mtime.
	originFile = "origin.json"

	// maxEntriesEnv overrides maxCacheEntries. Zero or negative
	// disables eviction entirely; tests use it to plant a small
	// bound.
	maxEntriesEnv = "NOMI_RUNNER_CACHE_MAX_ENTRIES"

	// maxCacheEntries is the ceiling on cache directories.
	//
	// THE NUMBER. An entry is one compiler root's runner for one
	// variant and target, so a checkout that builds for its own
	// platform holds a few (the plain and lean runners, with and
	// without std/compiler's hosts, and the reactors), and 256 covers
	// tens of checkouts and their cross-target builds.
	//
	// THE BOUND IS ON COUNT, NOT BYTES. A runner binary is about 18 MB,
	// so 256 live entries hold about 4.6 GB.
	//
	// The cost of being wrong on the low side is one relink, measured
	// at 2.1 s (Go's own build cache absorbs compilation, so only the
	// link is paid), so the bound is generous rather than tight.
	maxCacheEntries = 256

	// evictGraceWindow protects any entry used within it, under every
	// rule. This is the concurrent-build defence — see planEviction.
	//
	// The number: the longest thing that runs inside a cache
	// directory is a cold `go build` of a runner, ~2 s, and the
	// longest whole-corpus sweep that keeps hitting one directory is
	// 213 s. One hour is 17x the sweep and three orders of magnitude
	// over the build. What it costs: an entry that dies now survives
	// until an hour has passed, so the cache can carry up to an
	// hour's worth of fresh corpses. That is bounded by the build
	// rate, which is bounded by 2.1 s per build.
	evictGraceWindow = time.Hour

	// sweepEvery is how often a cache inside its bound is scanned for
	// dead and unused entries (artifactcache.Due); over the bound every
	// call scans.
	sweepEvery = 24 * time.Hour

	// trashPrefix names a directory that has been renamed out of the
	// live key space and is being deleted. A leading dot keeps it out
	// of the 16-hex key space by construction, so the entry scanner
	// ignores it and a crashed sweep leaves litter that the next
	// sweep collects rather than an entry nothing will ever look at.
	trashPrefix = ".trash-"
)

// originRecord is <cacheDir>/origin.json.
//
// CompilerRoot is the compiler source root the entry was built from,
// without the variant and target its key adds: staleness is a question
// about the root's existence, not about which of the root's keys this
// is.
type originRecord struct {
	CompilerRoot string `json:"compiler_root"`
	Kind         string `json:"kind"`
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
func recordOrigin(cacheDir, compilerRoot, kind string) error {
	data, err := json.Marshal(originRecord{CompilerRoot: compilerRoot, Kind: kind})
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
	if err := json.Unmarshal(data, &r); err != nil || r.CompilerRoot == "" {
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
// RemoveAll, NOMI_RUNNER_CACHE_ROOT is a user-settable path, and a
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
	evictUnused
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
	// root is the recorded compiler root; hasRoot is false for an
	// unmarked entry, whose origin.json is missing or unreadable.
	root    string
	hasRoot bool
}

// scanCacheEntries lists the cache root's entries with a last-use
// reading for each.
//
// Last use is the marker's mtime when there is a marker, and the
// DIRECTORY's own mtime otherwise. The fallback is the best available
// reading for an unmarked entry: a directory's mtime moves when a file
// is created or removed in it, which covers every write the cache
// makes (the runner binary and its vm.json). It does NOT move on a warm hit, so an unmarked entry can
// read older than it truly is — which is exactly why the marker
// exists, and why unmarked entries are reachable only by the bound and
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
				e.root = rec.CompilerRoot
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
// unmounted volume is NOT evidence that a root is gone, and
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
// directory share it too. The failure to avoid is unlinking a runner
// out from under a running `go build`, which surfaces as a spurious
// ENOENT compile error. Three things stand between the sweep and that:
//
//  1. The caller's own entry is excluded by name. Whatever else is
//     true of the clock, the directory this process is about to build
//     in cannot be chosen.
//
//  2. Every entry used within evictGraceWindow is excluded, under
//     every rule. CacheDirForKey stamps the marker BEFORE any other
//     work in the directory, so a process that is currently building
//     has a stamp seconds old, and an hour of grace is three orders
//     of magnitude more than the ~2 s a cold runner build takes.
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
		switch {
		case protected(e):
		case e.hasRoot && rootIsGone(e.root):
			victims = append(victims, victim{name: e.name, reason: evictDeadRoot, lastUse: e.lastUse})
			continue
		case now.Sub(e.lastUse) >= artifactcache.Unused:
			victims = append(victims, victim{name: e.name, reason: evictUnused, lastUse: e.lastUse})
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
// TRIGGERING. The sweep costs one readdir and one stat when the cache
// is inside its budget and a stat plus a small read per entry when it
// is not, or when a day has passed since the last full sweep
// (sweepEvery, artifactcache.Due), which is what finds dead and
// unused entries under the bound. Because the sweep leaves the count
// AT the bound, the next build sees count == max and does no
// per-entry work, so a full sweep runs once per new compiler root and
// once a day rather than once per build. Measured on this machine at
// 2656 entries: see the eviction overhead figure in
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
	if keys <= max && !artifactcache.Due(root, sweepEvery) {
		return 0, nil
	}
	entries, err := scanCacheEntries(root)
	if err != nil {
		return 0, fmt.Errorf("scanning cache root %s: %w", root, err)
	}
	return applyEviction(root, planEviction(entries, keep, max, time.Now())), nil
}
