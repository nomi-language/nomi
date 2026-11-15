package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `nomi check std` checks std's `_fixtures` tree with the rest of std. Each
// fixture is checked as its std module, so its bare std imports are std's
// (TestCheck_EveryStdFixtureChecksClean in vmhost checks them clean).
func TestCheckCommand_ChecksTheStdFixtures(t *testing.T) {
	stdRoot, err := analysis.StdlibPath()
	if err != nil {
		t.Skipf("no stdlib source tree: %v", err)
	}
	files, _, _, err := discoverCheckFiles(stdRoot)
	if err != nil {
		t.Fatal(err)
	}
	sawIter, sawConn := false, false
	for _, f := range files {
		slashed := filepath.ToSlash(f)
		sawIter = sawIter || filepath.Base(f) == "iter.nomi"
		sawConn = sawConn || strings.HasSuffix(slashed, "/_fixtures/conn.nomi")
	}
	if !sawIter || !sawConn {
		t.Fatalf("std/iter.nomi checked: %v, std/_fixtures/conn.nomi checked: %v, among %d files", sawIter, sawConn, len(files))
	}
}
