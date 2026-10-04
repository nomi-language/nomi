package ir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScope_TheEmitterIsTheFirstConsumer checks that `internal/irbuild`
// imports this package, so that one named consumer is a property of the tree.
//
// A builder that built this package's IR beside some other lowering and
// never read it would produce the same output and a green corpus. This test
// makes the missing import loud, which no comparison of output can do.
func TestScope_TheEmitterIsTheFirstConsumer(t *testing.T) {
	const importPath = `"github.com/nomi-language/nomi/internal/ir"`
	root := moduleDir(t)

	// The expected consumers. A list rather than a count, so adding a
	// consumer extends it deliberately instead of moving a number.
	wantConsumers := []string{filepath.Join("internal", "irbuild")}

	// Not production: this package's own external tests, and the planted
	// non-compiling consumer TestPosition_OmittingAPositionDoesNotCompile
	// builds under a build tag.
	skip := map[string]bool{
		filepath.Join("internal", "ir", "posplant", "plant.go"): true,
	}

	consumers := map[string]bool{}
	scanned, matched := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata holds fixtures that are not part of the module's own
			// build.
			if d.Name() == "testdata" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		if !strings.Contains(string(src), importPath) {
			return nil
		}
		matched++
		if skip[rel] || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		consumers[filepath.Dir(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// PLANT A POSITIVE: the scan must have read the module and found the
	// import somewhere, or every conclusion below is the answer for a walk
	// that read nothing.
	if scanned < 100 {
		t.Fatalf("the walk read %d .go files under %s, which cannot be the whole module",
			scanned, root)
	}
	if matched == 0 {
		t.Fatalf("the walk read %d .go files and found %s in none of them, though this "+
			"package's own tests import it. The consumer list below carries no information",
			scanned, importPath)
	}

	for _, want := range wantConsumers {
		if !consumers[want] {
			t.Errorf("%s does not import %s.\n\tThe builder lowers every body into this "+
				"package's graphs, so a tree where that import is gone is a tree where "+
				"the builder no longer produces the IR the VM runs.", want, importPath)
		}
	}
	got := make([]string, 0, len(consumers))
	for pkg := range consumers {
		got = append(got, pkg)
	}
	t.Logf("%d .go files scanned, %d reference %s; production consumers: %v",
		scanned, matched, importPath, got)
}
