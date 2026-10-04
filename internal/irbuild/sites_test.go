package irbuild

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSites_DenominatorDeficitIsReported plants the positive the sweep's own
// denominator assertion cannot currently see.
//
// assertCaseDenominator's third arm fires only for files this package bucketed
// as FRONT-END ERRORS, and the corpus has none, so on every real run that arm
// is silent for a reason that says nothing about whether it works. A zero from
// a check whose population is empty is not evidence. So: hand it a file
// that IS a front-end error as far as this sweep is concerned and that
// `nomi test` loads and collects two cases from, and require the deficit to be
// reported with the count and the name.
//
// The negative is asserted in the same test, because a check that always
// reports a deficit would pass the positive alone.
func TestSites_DenominatorDeficitIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "deficit_test.nomi")
	src := "test \"one\" {\n  assert True\n}\n\ntest \"two\" {\n  assert 1 == 1\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("staging the plant: %v", err)
	}

	// The file itself must be sound, or the plant proves the runner cannot
	// load it rather than that the check can see it.
	clean := []corpusFile{{Path: path, Rel: "deficit_test.nomi"}}
	if got, names := denominatorDeficit(clean); got != 0 {
		t.Fatalf("a file with no AnalyzeErr must contribute no deficit, got %d: %v", got, names)
	}

	planted := []corpusFile{{
		Path:       path,
		Rel:        "deficit_test.nomi",
		AnalyzeErr: errors.New("staged: this sweep bucketed the file as a front-end error"),
	}}
	got, names := denominatorDeficit(planted)
	if got != 2 {
		t.Fatalf("the plant declares 2 cases `nomi test` would run and this sweep would not "+
			"count; the deficit check reported %d, so it cannot see a real gap: %v", got, names)
	}
	if len(names) != 1 || !strings.Contains(names[0], "deficit_test.nomi") {
		t.Fatalf("the deficit must NAME the file, got %v", names)
	}
}

// TestSites_TheDenominatorPopulationIsTheRunnersOwn pins runnerWouldDiscover
// against main.go's two clauses, in both directions. A predicate that answered
// `true` for everything would satisfy the sweep's population check while
// asserting nothing.
func TestSites_TheDenominatorPopulationIsTheRunnersOwn(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
		return p
	}
	// Clause 1: the suffix, with no test in the file at all.
	bySuffix := write("empty_test.nomi", "fn helper(): Int {\n  1\n}\n")
	// Clause 2: no suffix, but the file declares a test.
	byContent := write("declares.nomi", "test \"t\" {\n  assert True\n}\n")
	// Neither: an ordinary module.
	neither := write("helper.nomi", "fn helper(): Int {\n  1\n}\n")

	if !runnerWouldDiscover(bySuffix) {
		t.Errorf("`nomi test` discovers every *_test.nomi regardless of content; this says otherwise")
	}
	if !runnerWouldDiscover(byContent) {
		t.Errorf("`nomi test` discovers a file FileDeclaresTests accepts; this says otherwise")
	}
	if runnerWouldDiscover(neither) {
		t.Errorf("a plain module is not discovered by `nomi test`, so a denominator counting its " +
			"cases would be a fraction of cases nothing runs — and this predicate cannot tell")
	}
}
