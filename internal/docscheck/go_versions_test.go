package docscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	"github.com/nomi-language/nomi/internal/modfetch"
)

// TestGoVersions_EveryTrackedGoModMatchesTheRoot holds every tracked go.mod
// of Go code (the benchmarks' Go programs) to the root go.mod's `go` line.
// A module left on an older line is built by a different language version
// than the compiler it exercises, and nobody notices until a toolchain bump
// breaks it. The go.mod nomi writes beside a Nomi module's nomi.toml holds
// only its module line (modfetch.GoModText) and builds no Go, so it has no
// `go` line to hold.
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
		data, err := os.ReadFile(filepath.Join(root, rel))
		if os.IsNotExist(err) {
			continue // deleted in the working tree, not yet committed
		}
		if err != nil {
			t.Fatal(err)
		}
		if f, err := modfile.ParseLax(rel, data, nil); err == nil && f.Go == nil {
			continue
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

// TestGoVersions_EveryNomiModuleHasTheGoModNomiWrites holds the go.mod
// beside each tracked nomi.toml with a [module].path, outside testdata, to
// the one `nomi fetch` writes there (modfetch.GoModProblem), so each lib/
// module is servable by Go's module proxy and out of the root module's zip.
func TestGoVersions_EveryNomiModuleHasTheGoModNomiWrites(t *testing.T) {
	root, files := trackedFilesMatching(t, "nomi.toml", "*/nomi.toml")
	checked := 0
	for _, rel := range files {
		if strings.Contains("/"+rel, "/testdata/") {
			continue
		}
		dir := filepath.Join(root, filepath.Dir(rel))
		if _, err := os.Stat(filepath.Join(dir, "nomi.toml")); os.IsNotExist(err) {
			continue
		}
		checked++
		problem, err := modfetch.GoModProblem(dir)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
		} else if problem != "" {
			t.Errorf("%s/go.mod: %s", filepath.Dir(rel), problem)
		}
	}
	if checked == 0 {
		t.Fatalf("found no tracked nomi.toml under %s; the scan is not reading the repository", root)
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
