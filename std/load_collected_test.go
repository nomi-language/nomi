package std

import (
	"runtime"
	"testing"
	"weak"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestLoad_AFinishedLoadIsCollected holds that nothing process-wide keeps a
// stdlib Load built once its caller drops it. A cache keyed by a std file's
// analysis kept every Load whole, about 140 MB each, and the irbuild tests,
// which Load the stdlib about sixty times, held 8 GB of them.
func TestLoad_AFinishedLoadIsCollected(t *testing.T) {
	var file weak.Pointer[analysis.FileAnalysis]
	func() {
		lib := Load()
		fa := lib.Files["iter"]
		if fa == nil {
			t.Fatal("Load built no std/iter")
		}
		// Reach the tables keyed by a std file's analysis, as a lowering
		// does.
		for _, f := range lib.Files {
			f.StdFuncs()
		}
		file = weak.Make(fa)
	}()
	// A cleanup runs on its own goroutine after the collection that finds
	// its key unreachable, so the collector runs a bounded number of times.
	for range 100 {
		runtime.GC()
		if file.Value() == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("a dropped Load's std/iter analysis is still reachable: a process-wide table keeps it")
}
