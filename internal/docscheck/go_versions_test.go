package docscheck

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestGoVersions_EveryTrackedGoModMatchesTheRoot holds every tracked go.mod
// (FFI fixtures under tests/, testdata modules) to the root go.mod's
// `go` line. A fixture left on an older line is built by a different language
// version than the compiler it exercises, and nobody notices until a toolchain
// bump breaks it.
func TestGoVersions_EveryTrackedGoModMatchesTheRoot(t *testing.T) {
	root, files := trackedFilesMatching(t, "go.mod", "*/go.mod")
	want := goDirective(t, root, "go.mod")
	if want == "" {
		t.Fatalf("the root go.mod has no go directive")
	}
	checked := 0
	for _, rel := range files {
		if rel == "go.mod" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, rel)); os.IsNotExist(err) {
			continue // deleted in the working tree, not yet committed
		}
		checked++
		if got := goDirective(t, root, rel); got != want {
			t.Errorf("%s: go %s, want go %s (the root go.mod's)", rel, got, want)
		}
	}
	if checked == 0 {
		t.Fatalf("found no tracked go.mod besides the root's under %s; the scan is not reading the repository", root)
	}
}

// goDirective answers the version on rel's `go` line, or "" when it has none.
func goDirective(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	if f.Go == nil {
		return ""
	}
	return f.Go.Version
}
