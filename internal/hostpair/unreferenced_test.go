package hostpair

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const importPath = "github.com/nomi-language/nomi/internal/hostpair"

// productionReferences returns every non-test .go file under root that imports
// pkg, plus every test file that does so from OUTSIDE the package's own
// directory.
//
// Both halves matter. A production import means a consumer has already
// switched to this package, which must land as its own change. A test import
// from elsewhere means some other package's acceptance now depends on this
// derivation, which would make the switch unprovable for the same reason.
func productionReferences(root, pkg, ownDir string) ([]string, error) {
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == ".git" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		inOwnDir := filepath.Dir(path) == ownDir
		if inOwnDir && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			// An unparseable .go file cannot be importing anything, and
			// testdata trees legitimately hold broken Go. Reporting it as an
			// offender would be a false positive; reporting nothing at all
			// would hide a real import behind a typo somewhere else, so say
			// so rather than swallow it.
			return nil
		}
		for _, spec := range file.Imports {
			if spec.Path == nil {
				continue
			}
			value, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil || value != pkg {
				continue
			}
			offenders = append(offenders, path)
			break
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(offenders)
	return offenders, nil
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	// .../internal/hostpair/unreferenced_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestPackageIsUnreferencedByProduction asserts that no production code
// imports this package.
//
// The derivation is built and proven to agree; NO CONSUMER IS REPOINTED.
// Switching a consumer must change nothing observable, and that is only
// provable if the switch lands on its own. So the
// absence of a production import is a deliverable, not an omission — and it is
// asserted here rather than remembered.
func TestPackageIsUnreferencedByProduction(t *testing.T) {
	root := moduleRoot(t)
	ownDir := filepath.Join(root, "internal", "hostpair")
	if _, err := os.Stat(ownDir); err != nil {
		t.Fatalf("own directory %s not found; the walk would be vacuous: %v", ownDir, err)
	}
	offenders, err := productionReferences(root, importPath, ownDir)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(offenders) > 0 {
		rel := make([]string, 0, len(offenders))
		for _, path := range offenders {
			r, relErr := filepath.Rel(root, path)
			if relErr != nil {
				r = path
			}
			rel = append(rel, r)
		}
		t.Fatalf("%s is referenced outside its own tests, so a consumer switch has already happened and cannot be proven to change nothing:\n  %s",
			importPath, strings.Join(rel, "\n  "))
	}
}

// TestUnreferencedCheckCanFail is the planted positive for the check above.
//
// A walk that reports nothing is indistinguishable from a walk that found
// nothing, and this one reports nothing on the real tree by design — so it
// must be shown to report something on a tree where the offence exists. Three
// plants: a production import, a test import from another package, and the
// permitted case that must NOT be reported.
func TestUnreferencedCheckCanFail(t *testing.T) {
	plants := []struct {
		name       string
		files      map[string]string
		wantOffend []string
	}{
		{
			name: "production import elsewhere is reported",
			files: map[string]string{
				"internal/irbuild/stdlib.go":             importingFile("irbuild"),
				"internal/hostpair/hostpair.go":          plainFile("hostpair"),
				"internal/hostpair/agree_ffirun_test.go": importingFile("hostpair"),
			},
			wantOffend: []string{"internal/irbuild/stdlib.go"},
		},
		{
			name: "test import from another package is reported",
			files: map[string]string{
				"internal/ffirun/discovery_test.go": importingFile("ffirun"),
				"internal/hostpair/hostpair.go":     plainFile("hostpair"),
			},
			wantOffend: []string{"internal/ffirun/discovery_test.go"},
		},
		{
			name: "the package's own tests are not reported",
			files: map[string]string{
				"internal/hostpair/hostpair.go":               plainFile("hostpair"),
				"internal/hostpair/agree_ffirun_test.go":      importingFile("hostpair"),
				"internal/hostpair/agree_generated_test.go":   importingFile("hostpair"),
				"internal/hostpair/testdata/broken/broken.go": "this is not go at all",
			},
			wantOffend: nil,
		},
		{
			name: "a non-test file inside the package is reported",
			files: map[string]string{
				"internal/hostpair/hostpair.go": plainFile("hostpair"),
				"internal/hostpair/bridge.go":   importingFile("hostpair"),
			},
			wantOffend: []string{"internal/hostpair/bridge.go"},
		},
	}

	for _, plant := range plants {
		t.Run(plant.name, func(t *testing.T) {
			root := writeProject(t, plant.files)
			offenders, err := productionReferences(root, importPath, filepath.Join(root, "internal", "hostpair"))
			if err != nil {
				t.Fatalf("walking planted tree: %v", err)
			}
			var rel []string
			for _, path := range offenders {
				r, relErr := filepath.Rel(root, path)
				if relErr != nil {
					t.Fatalf("relativizing %s: %v", path, relErr)
				}
				rel = append(rel, filepath.ToSlash(r))
			}
			if strings.Join(rel, "|") != strings.Join(plant.wantOffend, "|") {
				t.Fatalf("offenders = %v, want %v", rel, plant.wantOffend)
			}
		})
	}
}

func plainFile(pkg string) string {
	return fmt.Sprintf("package %s\n", pkg)
}

func importingFile(pkg string) string {
	return fmt.Sprintf("package %s\n\nimport _ %q\n", pkg, importPath)
}
