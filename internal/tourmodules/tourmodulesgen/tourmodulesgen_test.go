package tourmodulesgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsCurrent fails while internal/tourmodules's
// modules_sources_gen.go is not what the tour modules' files under lib/
// generate: a module's Nomi files or embedded files changed without `go
// generate ./internal/tourmodules`.
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
		t.Fatalf("internal/tourmodules/%s is stale: a tour module's Nomi files under lib/ changed; run `go generate ./internal/tourmodules`", SourcesFileName)
	}
}
