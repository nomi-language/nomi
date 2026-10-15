package ast

import (
	"bytes"
	"os"
	"testing"

	"github.com/nomi-language/nomi/internal/ast/walkgen"
)

// TestGeneratedChildrenAreCurrent regenerates children_gen.go from this
// package's node types and requires the checked-in file to match. A node
// type or a field added, removed or reordered fails here until
// `go generate ./internal/ast` is rerun, so no node type is ever left to
// the reflective fallback.
func TestGeneratedChildrenAreCurrent(t *testing.T) {
	want, err := walkgen.Generate(".")
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	got, err := os.ReadFile(walkgen.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale (%d bytes checked in, %d generated); run "+
			"`go generate ./internal/ast` from the repository root", walkgen.OutputFile, len(got), len(want))
	}
}
