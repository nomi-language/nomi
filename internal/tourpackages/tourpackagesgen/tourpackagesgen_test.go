package tourpackagesgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsCurrent fails while internal/tourpackages's
// packages_sources_gen.go is not what the tour packages' files under lib/
// generate: a package's Nomi files or embedded files changed without `go
// generate ./internal/tourpackages`.
func TestGeneratedFileIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	want, err := SourcesFile(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, GoDir, SourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("internal/tourpackages/%s is stale: a tour package's Nomi files under lib/ changed; run `go generate ./internal/tourpackages`", SourcesFileName)
	}
}
