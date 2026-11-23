package analysis

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"weak"

	"github.com/nomi-language/nomi/internal/fileuri"
)

func ownedCacheLen[K, V any](c *ownedCache[K, V]) int {
	n := 0
	c.m.Range(func(any, any) bool { n++; return true })
	return n
}

// collectedWithin runs the collector until done reports true, at most a
// bounded number of times, and answers whether it did. A cleanup runs on its
// own goroutine after the collection that finds its key unreachable, so one
// collection is not enough to see it.
func collectedWithin(done func() bool) bool {
	for range 100 {
		runtime.GC()
		if done() {
			return true
		}
		runtime.Gosched()
	}
	return false
}

// TestOwnedCache_AnEntryGoesWithItsKey holds what ownedCache is for: an
// entry is answered while its key lives, and deleted once the key is
// collected.
func TestOwnedCache_AnEntryGoesWithItsKey(t *testing.T) {
	var c ownedCache[FileAnalysis, []int]
	const n = 8
	keys := make([]*FileAnalysis, n)
	for i := range keys {
		keys[i] = &FileAnalysis{}
		c.store(keys[i], []int{i})
	}
	if got := c.store(keys[0], []int{99}); got[0] != 0 {
		t.Fatalf("a second store answered %v, want the first answer [0]", got)
	}
	c.set(keys[1], []int{11})
	for i, k := range keys {
		want := i
		if i == 1 {
			want = 11
		}
		if v, ok := c.load(k); !ok || v[0] != want {
			t.Fatalf("key %d answers %v, %v; want [%d], true", i, v, ok, want)
		}
	}
	if got := ownedCacheLen(&c); got != n {
		t.Fatalf("%d entries for %d live keys", got, n)
	}
	keep := keys[n-1]
	keys = nil
	if !collectedWithin(func() bool { return ownedCacheLen(&c) == 1 }) {
		t.Fatalf("%d entries after all but one key were collected, want 1", ownedCacheLen(&c))
	}
	if v, ok := c.load(keep); !ok || v[0] != n-1 {
		t.Fatalf("the live key answers %v, %v; want [%d], true", v, ok, n-1)
	}
	runtime.KeepAlive(keep)
}

// TestDocumentManager_AFinishedManagerIsCollected holds that nothing
// process-wide keeps a document manager's analyses once the manager is
// gone. A closed file's analysis builds the stdlib from source, and a cache
// that held one of its std files kept the file's program, and through its
// loader the manager and every analysis it held, for the life of the
// process: about 25 MB for each closed file a language server or a test
// ever analyzed.
func TestDocumentManager_AFinishedManagerIsCollected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.nomi")
	if err := os.WriteFile(path, []byte("fn f() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var manager weak.Pointer[DocumentManager]
	var program weak.Pointer[Program]
	func() {
		dm := NewDocumentManager()
		snap := dm.Analyzed(fileuri.FromPath(path))
		if snap == nil || snap.Analysis == nil {
			t.Fatal("no analysis of the closed file")
		}
		manager, program = weak.Make(dm), weak.Make(snap.Analysis.Program())
	}()
	if !collectedWithin(func() bool { return manager.Value() == nil && program.Value() == nil }) {
		t.Fatalf("a finished document manager is still reachable (manager %v, its program %v): "+
			"a process-wide table keeps one of its analyses", manager.Value() != nil, program.Value() != nil)
	}
}
