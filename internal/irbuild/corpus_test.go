package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/frontend"
)

// runnerWouldDiscover is main.go's discoverTestFiles rule for one file.
//
// Restated rather than called because that logic lives in package main and
// cannot be imported. The two clauses are its two clauses; a change to either
// belongs in both places, and the population half of the denominator assertion
// is what would catch the drift.
func runnerWouldDiscover(path string) bool {
	if strings.HasSuffix(filepath.Base(path), "_test.nomi") {
		return true
	}
	declares, err := frontend.FileDeclaresTests(path)
	return err == nil && declares
}

// denominatorDeficit is the cases `nomi test` would run in files this sweep
// could not analyse. See assertCaseDenominator.
func denominatorDeficit(programs []corpusFile) (int, []string) {
	deficit := 0
	var names []string
	for _, prog := range programs {
		if prog.AnalyzeErr == nil || !runnerWouldDiscover(prog.Path) {
			continue
		}
		proj, err := frontend.New(frontend.Config{}).CheckFile(prog.Path, frontend.Mode{Tests: true})
		if err != nil {
			// The runner cannot load it either, so its cases are in neither
			// total and there is no gap to report.
			continue
		}
		n := len(frontend.CollectTestCases(proj.Nodes))
		if n == 0 {
			continue
		}
		deficit += n
		names = append(names, fmt.Sprintf("%s: %d case(s) the runner collects and this sweep does not", prog.Rel, n))
	}
	return deficit, names
}

// nomiProgramsUnder lists every .nomi file under root, sorted, so a report is
// deterministic run to run.
//
// Deliberately NOT named for the corpus. It is a plain directory walker that
// any root may use, and naming it `corpusPrograms` is what made the sharing
// guard next door count a call to it as evidence of a corpus walk. The corpus
// chokepoint is corpusRoot(), which is what that guard now censuses.
func nomiProgramsUnder(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".nomi" {
			return nil
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out, err
}
