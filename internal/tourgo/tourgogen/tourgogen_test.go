package tourgogen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsCurrent fails while internal/tourgo's generated files
// are not what the tour's blocks and modules generate: a block's bindings
// or its Go, or a tour module's files under lib/, changed without `go
// generate ./internal/tourgo`.
func TestGeneratedFileIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	want, err := File(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, GoDir, "linked_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/tourgo/linked_gen.go is stale; run `go generate ./internal/tourgo`")
	}
	want, err = ModulesFile(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(root, GoDir, ModulesFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("internal/tourgo/%s is stale; run `go generate ./internal/tourgo`", ModulesFileName)
	}
	want, err = SourcesFile(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(root, GoDir, SourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("internal/tourgo/%s is stale: a tour module's Nomi files under lib/ changed; run `go generate ./internal/tourgo`", SourcesFileName)
	}
	blocks, err := Blocks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) < 2 {
		t.Fatalf("found %d tour blocks with Go bindings; the FFI chapter has two", len(blocks))
	}
}
