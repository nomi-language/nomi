package ffirun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
)

// Every test here runs against a t.TempDir() cache root, and every
// one plants a positive: an assertion that something WAS evicted, in
// the same case as the assertion that something was not. A sweep that
// finds nothing eligible satisfies "the live entry survived" for the
// wrong reason, so the survival assertions are worthless without a
// removal beside them.

// plantEntry creates a cache directory named by hashing key, with an
// origin.json naming root, stamped age ago. Returns the 16-hex name.
func plantEntry(t *testing.T, cacheRoot, key, projectRoot string, age time.Duration) string {
	t.Helper()
	name := cacheKeyHash(key)
	dir := filepath.Join(cacheRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("planting %s: %v", name, err)
	}
	// A byte of payload, so an eviction removes a subtree rather than
	// an empty directory.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("planting payload in %s: %v", name, err)
	}
	if err := recordOrigin(dir, projectRoot, cacheKindRun); err != nil {
		t.Fatalf("recording origin in %s: %v", name, err)
	}
	backdate(t, filepath.Join(dir, originFile), age)
	return name
}

// plantLegacyEntry creates a directory in the shape the 2656
// pre-existing entries have: contents, no origin.json. Last use is
// only readable from the directory's own mtime.
func plantLegacyEntry(t *testing.T, cacheRoot, key string, age time.Duration) string {
	t.Helper()
	name := cacheKeyHash(key)
	dir := filepath.Join(cacheRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("planting %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("planting payload in %s: %v", name, err)
	}
	backdate(t, dir, age)
	return name
}

func backdate(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("backdating %s: %v", path, err)
	}
}

func present(t *testing.T, cacheRoot, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(cacheRoot, name))
	return err == nil
}

func namesIn(t *testing.T, cacheRoot string) []string {
	t.Helper()
	des, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatalf("reading cache root: %v", err)
	}
	var out []string
	for _, de := range des {
		if de.IsDir() && isCacheKeyName(de.Name()) {
			out = append(out, de.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestEvict_DeadRootIsEvictedAndLiveRootIsNot exercises rule 1 alone.
// The bound is set far above the entry count so only the dead-root
// rule can fire, and the live entry's survival is asserted in the
// same sweep that removes the dead one.
func TestEvict_DeadRootIsEvictedAndLiveRootIsNot(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "1000")

	liveRoot := t.TempDir()
	deadRoot := filepath.Join(t.TempDir(), "deleted-worktree", "project")
	if err := os.MkdirAll(deadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	live := plantEntry(t, cacheRoot, liveRoot, liveRoot, 40*24*time.Hour)
	dead := plantEntry(t, cacheRoot, deadRoot, deadRoot, 40*24*time.Hour)
	if err := os.RemoveAll(deadRoot); err != nil {
		t.Fatal(err)
	}

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	victims := planEviction(entries, "", 1000, time.Now())
	if len(victims) != 1 {
		t.Fatalf("want exactly one victim, got %d: %+v", len(victims), victims)
	}
	if victims[0].name != dead || victims[0].reason != evictDeadRoot {
		t.Fatalf("wrong victim: %+v (dead=%s)", victims[0], dead)
	}
	if got := applyEviction(cacheRoot, victims); got != 1 {
		t.Fatalf("applyEviction removed %d, want 1", got)
	}
	if present(t, cacheRoot, dead) {
		t.Fatal("the entry whose project root was deleted is still there")
	}
	if !present(t, cacheRoot, live) {
		t.Fatal("the entry whose project root still exists was evicted")
	}
}

// TestEvict_BoundKeepsNewestDropsOldest exercises rule 2 alone. Every
// planted root exists, so rule 1 cannot fire and the only thing under
// test is the ranking.
func TestEvict_BoundKeepsNewestDropsOldest(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "3")

	projectRoot := t.TempDir()
	// ages in days, oldest last
	ages := []int{2, 5, 9, 14, 21, 40}
	names := make([]string, len(ages))
	for i, d := range ages {
		names[i] = plantEntry(t, cacheRoot, projectRoot+"/p"+strconv.Itoa(i), projectRoot,
			time.Duration(d)*24*time.Hour)
	}

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	victims := planEviction(entries, "", 3, time.Now())
	if len(victims) != 3 {
		t.Fatalf("want 3 victims from 6 entries at a bound of 3, got %d", len(victims))
	}
	for _, v := range victims {
		if v.reason != evictOverBound {
			t.Fatalf("victim %s chose the wrong rule: %v", v.name, v.reason)
		}
	}
	if got := applyEviction(cacheRoot, victims); got != 3 {
		t.Fatalf("applyEviction removed %d, want 3", got)
	}
	for i, name := range names {
		want := i < 3 // the three newest
		if got := present(t, cacheRoot, name); got != want {
			t.Fatalf("entry aged %dd: present=%v, want %v (survivors %v)",
				ages[i], got, want, namesIn(t, cacheRoot))
		}
	}
}

// TestEvict_RecentEntryIsNotEvicted is the concurrent-build defence
// as a unit: a bound of 1 against three live-rooted entries, two of
// them stamped seconds ago. The grace window has to hold both recent
// entries even though the bound says one, and the 40-day entry's
// removal is the positive that proves the sweep ran.
func TestEvict_RecentEntryIsNotEvicted(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "1")

	projectRoot := t.TempDir()
	building := plantEntry(t, cacheRoot, projectRoot+"/a", projectRoot, 3*time.Second)
	alsoRecent := plantEntry(t, cacheRoot, projectRoot+"/b", projectRoot, 5*time.Minute)
	stale := plantEntry(t, cacheRoot, projectRoot+"/c", projectRoot, 40*24*time.Hour)

	if _, err := evictCache(cacheRoot, ""); err != nil {
		t.Fatalf("evictCache: %v", err)
	}
	if present(t, cacheRoot, stale) {
		t.Fatal("the 40-day entry survived, so this test proves nothing")
	}
	if !present(t, cacheRoot, building) {
		t.Fatal("an entry stamped 3 seconds ago was evicted out from under its build")
	}
	if !present(t, cacheRoot, alsoRecent) {
		t.Fatal("an entry stamped 5 minutes ago was evicted despite the grace window")
	}
}

// TestEvict_CurrentEntryIsNotEvicted pins the by-name exclusion
// independently of the clock. A bound of 1 against three live-rooted
// entries, all far older than the grace window, puts the caller's own
// entry squarely in the evictable tail — so `keep` is the only thing
// that can save it. The third entry going away is the positive.
func TestEvict_CurrentEntryIsNotEvicted(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "1")

	projectRoot := t.TempDir()
	newest := plantEntry(t, cacheRoot, projectRoot+"/newest", projectRoot, 10*24*time.Hour)
	mine := plantEntry(t, cacheRoot, projectRoot+"/mine", projectRoot, 40*24*time.Hour)
	other := plantEntry(t, cacheRoot, projectRoot+"/other", projectRoot, 41*24*time.Hour)

	if _, err := evictCache(cacheRoot, mine); err != nil {
		t.Fatalf("evictCache: %v", err)
	}
	if present(t, cacheRoot, other) {
		t.Fatal("nothing was evicted, so the keep assertion below is vacuous")
	}
	if !present(t, cacheRoot, mine) {
		t.Fatalf("the entry passed as keep was evicted (survivors %v)", namesIn(t, cacheRoot))
	}
	if !present(t, cacheRoot, newest) {
		t.Fatal("the most recently used entry was evicted at a bound of 1")
	}
}

// TestEvict_StampMovedAfterThePlanSparesTheEntry is the plan/apply
// race guard. A concurrent build claims a planned victim by stamping
// its marker between planEviction and applyEviction; the entry must
// survive, and its sibling must still go.
//
// Both entries are selected by the dead-root rule, so the plan is
// deterministic and the bound plays no part — what is under test is
// only the re-read applyEviction does immediately before removing.
func TestEvict_StampMovedAfterThePlanSparesTheEntry(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)

	scratch := t.TempDir()
	rootA := filepath.Join(scratch, "a")
	rootB := filepath.Join(scratch, "b")
	for _, r := range []string{rootA, rootB} {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claimed := plantEntry(t, cacheRoot, rootA, rootA, 40*24*time.Hour)
	unclaimed := plantEntry(t, cacheRoot, rootB, rootB, 41*24*time.Hour)
	for _, r := range []string{rootA, rootB} {
		if err := os.RemoveAll(r); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	victims := planEviction(entries, "", 1000, time.Now())
	if len(victims) != 2 {
		t.Fatalf("want both dead entries planned, got %+v", victims)
	}

	// The concurrent build's first act on the entry, which is what
	// cacheDirForKey does before it touches anything else.
	if err := recordOrigin(filepath.Join(cacheRoot, claimed), rootA, cacheKindRun); err != nil {
		t.Fatalf("simulating the concurrent claim: %v", err)
	}

	if got := applyEviction(cacheRoot, victims); got != 1 {
		t.Fatalf("applyEviction removed %d, want 1", got)
	}
	if present(t, cacheRoot, unclaimed) {
		t.Fatal("the unclaimed entry survived, so the guard below is vacuous")
	}
	if !present(t, cacheRoot, claimed) {
		t.Fatal("an entry claimed between plan and apply was removed anyway")
	}
}

// TestEvict_LegacyEntryWithNoOriginIsReachableByTheBound covers the
// 2656 pre-existing directories: no marker, so no recorded root, so
// rule 1 can never touch them. They have to be reachable by rule 2,
// ranked on the directory's own mtime.
func TestEvict_LegacyEntryWithNoOriginIsReachableByTheBound(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "2")

	newer := plantLegacyEntry(t, cacheRoot, "legacy-a", 10*24*time.Hour)
	middle := plantLegacyEntry(t, cacheRoot, "legacy-b", 20*24*time.Hour)
	older := plantLegacyEntry(t, cacheRoot, "legacy-c", 30*24*time.Hour)
	oldest := plantLegacyEntry(t, cacheRoot, "legacy-d", 40*24*time.Hour)

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	for _, e := range entries {
		if e.hasRoot {
			t.Fatalf("legacy entry %s reported a recorded root", e.name)
		}
	}
	victims := planEviction(entries, "", 2, time.Now())
	if len(victims) != 2 {
		t.Fatalf("want 2 victims from 4 legacy entries at a bound of 2, got %d", len(victims))
	}
	for _, v := range victims {
		if v.reason != evictOverBound {
			t.Fatalf("a legacy entry was selected by the dead-root rule: %+v", v)
		}
	}
	if got := applyEviction(cacheRoot, victims); got != 2 {
		t.Fatalf("applyEviction removed %d, want 2", got)
	}
	if !present(t, cacheRoot, newer) || !present(t, cacheRoot, middle) {
		t.Fatalf("the two most recent legacy entries were evicted: %v", namesIn(t, cacheRoot))
	}
	if present(t, cacheRoot, older) || present(t, cacheRoot, oldest) {
		t.Fatalf("the two oldest legacy entries survived the bound: %v", namesIn(t, cacheRoot))
	}
}

// TestEvict_OnlyTouchesCacheKeyNames is the blast-radius guard. A
// cache root that is also holding something else — a mis-set
// NOMI_FFIRUN_CACHE_ROOT, or a human's notes — must not be swept. The
// dead entry's removal proves the sweep ran over the same directory.
func TestEvict_OnlyTouchesCacheKeyNames(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "1")

	deadRoot := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(deadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dead := plantEntry(t, cacheRoot, deadRoot, deadRoot, 40*24*time.Hour)
	if err := os.RemoveAll(deadRoot); err != nil {
		t.Fatal(err)
	}
	// A second key so the count exceeds the bound and the sweep
	// actually scans; the strangers are not counted, which is itself
	// part of the contract.
	liveRoot := t.TempDir()
	live := plantEntry(t, cacheRoot, liveRoot, liveRoot, time.Second)
	strangerDir := filepath.Join(cacheRoot, "Documents")
	if err := os.MkdirAll(strangerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A 16-char name that is not hex, and a hex name of the wrong
	// length: both are near misses that must still be left alone.
	for _, name := range []string{"zzzzzzzzzzzzzzzz", "abcdef", "0123456789abcdef0"} {
		if err := os.MkdirAll(filepath.Join(cacheRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
		backdate(t, filepath.Join(cacheRoot, name), 90*24*time.Hour)
	}
	strangerFile := filepath.Join(cacheRoot, "notes.txt")
	if err := os.WriteFile(strangerFile, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := evictCache(cacheRoot, ""); err != nil {
		t.Fatalf("evictCache: %v", err)
	}
	if present(t, cacheRoot, dead) {
		t.Fatal("the dead entry survived, so the survival assertions below are vacuous")
	}
	for _, p := range []string{strangerDir, strangerFile,
		filepath.Join(cacheRoot, "zzzzzzzzzzzzzzzz"),
		filepath.Join(cacheRoot, "abcdef"),
		filepath.Join(cacheRoot, "0123456789abcdef0")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("eviction removed a non-cache path %s: %v", p, err)
		}
	}
	if !present(t, cacheRoot, live) {
		t.Fatal("the live entry was evicted")
	}
}

// TestEvict_TrashFromACrashedSweepIsCollected pins the litter
// contract: a rename that was never followed by its RemoveAll is
// collected by the next sweep rather than becoming a permanent
// orphan.
func TestEvict_TrashFromACrashedSweepIsCollected(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "1")

	orphan := filepath.Join(cacheRoot, trashPrefix+"0123456789abcdef")
	if err := os.MkdirAll(filepath.Join(orphan, "nomi_src"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	// Two entries over a bound of 1 so the sweep gets past its
	// early return and reaches collectTrash's caller.
	plantEntry(t, cacheRoot, projectRoot+"/a", projectRoot, 40*24*time.Hour)
	plantEntry(t, cacheRoot, projectRoot+"/b", projectRoot, 41*24*time.Hour)

	if _, err := evictCache(cacheRoot, ""); err != nil {
		t.Fatalf("evictCache: %v", err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("trash from a crashed sweep was not collected")
	}
}

// TestCacheDir_RecordsProjectRoot is the staleness-decidability
// contract: every directory the cache creates records the project root
// it belongs to.
func TestCacheDir_RecordsProjectRoot(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	t.Setenv(maxEntriesEnv, "1000")
	projectRoot := t.TempDir()

	runDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	rec, ok := readOrigin(runDir)
	if !ok {
		t.Fatalf("%s: no origin record", runDir)
	}
	if rec.ProjectRoot != projectRoot {
		t.Fatalf("%s: recorded root %q, want %q", runDir, rec.ProjectRoot, projectRoot)
	}
	if rec.Kind != cacheKindRun {
		t.Fatalf("%s: recorded kind %q, want %q", runDir, rec.Kind, cacheKindRun)
	}
}

// TestCacheDir_StampAdvancesOnAWarmHit is the reason the marker
// carries last use rather than last regeneration. ensureWrapper
// short-circuits on a warm hit and writes nothing, so without this
// the LRU would rank a project built every day beside one abandoned
// on day one.
func TestCacheDir_StampAdvancesOnAWarmHit(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	t.Setenv(maxEntriesEnv, "1000")
	projectRoot := t.TempDir()

	dir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	marker := filepath.Join(dir, originFile)
	backdate(t, marker, 30*24*time.Hour)
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cacheDirForProject(projectRoot); err != nil {
		t.Fatalf("second cacheDirForProject: %v", err)
	}
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().After(before.ModTime()) {
		t.Fatalf("marker mtime did not advance on reuse: %v then %v",
			before.ModTime(), after.ModTime())
	}
	if time.Since(after.ModTime()) > time.Minute {
		t.Fatalf("marker mtime is not a reading of now: %v", after.ModTime())
	}
}

// TestEvict_MalformedOriginFallsBackToTheBound: a torn or hand-edited
// marker must not make an entry undeletable, and must never be read
// as a dead root.
func TestEvict_MalformedOriginFallsBackToTheBound(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)

	projectRoot := t.TempDir()
	torn := plantEntry(t, cacheRoot, projectRoot+"/torn", projectRoot, 40*24*time.Hour)
	if err := os.WriteFile(filepath.Join(cacheRoot, torn, originFile),
		[]byte(`{"project_root":"/gone`), 0o644); err != nil {
		t.Fatal(err)
	}
	backdate(t, filepath.Join(cacheRoot, torn, originFile), 40*24*time.Hour)
	fine := plantEntry(t, cacheRoot, projectRoot+"/fine", projectRoot, 1*time.Second)

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	for _, e := range entries {
		if e.name == torn && e.hasRoot {
			t.Fatal("a malformed origin record was read as a recorded root")
		}
	}
	victims := planEviction(entries, "", 1, time.Now())
	if len(victims) != 1 || victims[0].name != torn || victims[0].reason != evictOverBound {
		t.Fatalf("want the torn entry selected by the bound, got %+v", victims)
	}
	if got := applyEviction(cacheRoot, victims); got != 1 {
		t.Fatalf("applyEviction removed %d, want 1", got)
	}
	if !present(t, cacheRoot, fine) {
		t.Fatal("the intact entry was evicted")
	}
}

// TestEvict_UnreadableRootIsNotProvablyDead: only ErrNotExist counts
// as death. A root behind a permission error is a live project on a
// machine having a bad day, and deleting its cache would be the
// eviction bug this whole design exists to avoid.
func TestEvict_UnreadableRootIsNotProvablyDead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)

	parent := t.TempDir()
	hidden := filepath.Join(parent, "project")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if !func() bool { _, err := os.Stat(hidden); return err != nil }() {
		t.Skip("the filesystem did not enforce the permission bits")
	}
	unreadable := plantEntry(t, cacheRoot, hidden, hidden, 40*24*time.Hour)

	deadRoot := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(deadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dead := plantEntry(t, cacheRoot, deadRoot, deadRoot, 40*24*time.Hour)
	if err := os.RemoveAll(deadRoot); err != nil {
		t.Fatal(err)
	}

	entries, err := scanCacheEntries(cacheRoot)
	if err != nil {
		t.Fatalf("scanCacheEntries: %v", err)
	}
	victims := planEviction(entries, "", 1000, time.Now())
	if len(victims) != 1 {
		t.Fatalf("want only the provably-dead entry, got %+v", victims)
	}
	if victims[0].name != dead {
		t.Fatalf("selected %s, want the provably-dead %s", victims[0].name, dead)
	}
	_ = unreadable
}

// TestEvict_DisabledByZeroBound: an operator (or a test) can switch
// the whole mechanism off, and off means off for both rules — a
// provably dead entry stays. The second half is the positive: the
// same entry goes as soon as the bound is on and the count exceeds
// it, which also pins the trigger (a cache inside its budget is not
// scanned at all, so the per-entry cost is not paid per build).
func TestEvict_DisabledByZeroBound(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)
	t.Setenv(maxEntriesEnv, "0")

	deadRoot := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(deadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dead := plantEntry(t, cacheRoot, deadRoot, deadRoot, 90*24*time.Hour)
	if err := os.RemoveAll(deadRoot); err != nil {
		t.Fatal(err)
	}
	if n, err := evictCache(cacheRoot, ""); err != nil || n != 0 {
		t.Fatalf("evictCache with the bound disabled: removed %d, err %v", n, err)
	}
	if !present(t, cacheRoot, dead) {
		t.Fatal("eviction ran with the bound set to 0")
	}

	// Bound on, count still inside it: no scan, so nothing goes.
	t.Setenv(maxEntriesEnv, "1")
	if n, err := evictCache(cacheRoot, ""); err != nil || n != 0 {
		t.Fatalf("one entry at a bound of 1 was swept: removed %d, err %v", n, err)
	}
	if !present(t, cacheRoot, dead) {
		t.Fatal("a cache inside its budget was swept anyway")
	}

	// Over budget: the scan runs and the dead entry goes.
	liveRoot := t.TempDir()
	live := plantEntry(t, cacheRoot, liveRoot, liveRoot, time.Second)
	if n, err := evictCache(cacheRoot, ""); err != nil || n != 1 {
		t.Fatalf("over-budget sweep: removed %d, err %v", n, err)
	}
	if present(t, cacheRoot, dead) {
		t.Fatal("the dead entry survived an over-budget sweep")
	}
	if !present(t, cacheRoot, live) {
		t.Fatal("the live entry was evicted")
	}
}

// TestEvict_SweepCostOnALargeRoot is the overhead measurement wired
// as a test so it cannot rot: build a root the size of the one this
// change was written for and assert the sweep stays inside a budget.
// The threshold is loose on purpose — it is a regression tripwire for
// an accidental O(n) file read per entry, not a benchmark.
func TestEvict_SweepCostOnALargeRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("plants 2656 directories")
	}
	cacheRoot := t.TempDir()
	t.Setenv(cacheRootEnv, cacheRoot)

	const n = 2656
	projectRoot := t.TempDir()
	rec, err := json.Marshal(originRecord{ProjectRoot: projectRoot, Kind: cacheKindRun})
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		dir := filepath.Join(cacheRoot, cacheKeyHash(projectRoot+"/p"+strconv.Itoa(i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, originFile), rec, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// In-budget: readdir only, no per-entry work.
	t.Setenv(maxEntriesEnv, strconv.Itoa(n))
	start := time.Now()
	if removed, err := evictCache(cacheRoot, ""); err != nil || removed != 0 {
		t.Fatalf("in-budget sweep: removed %d, err %v", removed, err)
	}
	inBudget := time.Since(start)

	// Over budget: readdir plus a stat and a read per entry, plus the
	// planning sort. Every root is live and every stamp is fresh, so
	// nothing is removable and this measures the scan alone.
	t.Setenv(maxEntriesEnv, "256")
	start = time.Now()
	if _, err := evictCache(cacheRoot, ""); err != nil {
		t.Fatalf("over-budget sweep: %v", err)
	}
	fullScan := time.Since(start)

	t.Logf("eviction overhead at %d entries: in-budget %v, full scan %v", n, inBudget, fullScan)
	if inBudget > 250*time.Millisecond {
		t.Fatalf("the in-budget path costs %v at %d entries; it should be one readdir", inBudget, n)
	}
	if fullScan > 3*time.Second {
		t.Fatalf("the full scan costs %v at %d entries", fullScan, n)
	}
}
