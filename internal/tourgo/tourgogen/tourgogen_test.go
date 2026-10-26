package tourgogen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsCurrent fails while internal/tourgo/linked_gen.go is not
// what the tour's blocks generate: a block's bindings or its Go changed
// without `go generate ./internal/tourgo`.
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
	blocks, err := Blocks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) < 2 {
		t.Fatalf("found %d tour blocks with Go bindings; the FFI chapter has two", len(blocks))
	}
}
