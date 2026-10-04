package std

import (
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestIsStdlibLoad_ReadsTheRootResolvedOncePerProcess holds isStdlibLoad to
// the cached stdlibRoot. It runs on every module load, 144 times for hello
// world, and an uncached analysis.StdlibPath resolves the executable, walks its
// symlinks and stats candidate directories each time.
//
// The test repoints NOMI_STD_PATH after the root is cached. An uncached lookup
// would follow it (the positive control below shows StdlibPath does), so
// isStdlibLoad still answering against the first root is what shows it asked
// nothing of the environment or the disk.
func TestIsStdlibLoad_ReadsTheRootResolvedOncePerProcess(t *testing.T) {
	root, err := stdlibRoot()
	if err != nil {
		t.Fatalf("no stdlib root in this process: %v", err)
	}
	other := t.TempDir()
	t.Setenv("NOMI_STD_PATH", other)

	if got, _ := analysis.StdlibPath(); filepath.Clean(got) != filepath.Clean(other) {
		t.Fatalf("StdlibPath ignores NOMI_STD_PATH (got %q), so this test cannot tell a cached root from an uncached one", got)
	}
	if !isStdlibLoad(root, []string{"strings"}) {
		t.Fatalf("isStdlibLoad(%q) is false after NOMI_STD_PATH moved: it resolved the root again instead of reading stdlibRoot", root)
	}
	if isStdlibLoad(other, []string{"strings"}) {
		t.Fatalf("isStdlibLoad(%q) is true: it followed NOMI_STD_PATH instead of reading stdlibRoot", other)
	}
}
