package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `nomi check std` skips std's `_fixtures` tree, whose modules exist only
// in a test binary run with NOMI_STD_FIXTURES=1; checked as files, their
// bare std imports failed with "expected std/iter.Iter<T>, got
// iter.Iter<T>". The rest of std is still checked.
func TestCheckCommand_SkipsTheStdFixtures(t *testing.T) {
	stdRoot, err := analysis.StdlibPath()
	if err != nil {
		t.Skipf("no stdlib source tree: %v", err)
	}
	files, _, _, err := discoverCheckFiles(stdRoot)
	if err != nil {
		t.Fatal(err)
	}
	sawIter := false
	for _, f := range files {
		if strings.Contains(filepath.ToSlash(f), "/_fixtures/") {
			t.Errorf("checked a fixture: %s", f)
		}
		sawIter = sawIter || filepath.Base(f) == "iter.nomi"
	}
	if !sawIter {
		t.Fatalf("std/iter.nomi is not among the %d files checked", len(files))
	}
}
