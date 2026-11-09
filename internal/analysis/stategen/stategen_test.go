package stategen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedStateCodecIsCurrent regenerates state_codec_gen.go from
// internal/analysis's types and requires the checked-in file to match. A
// field added to, removed from or reordered in any type the stdlib's state
// reaches fails here until `go generate ./internal/analysis` is rerun, and
// a field the generator cannot write fails the generation.
func TestGeneratedStateCodecIsCurrent(t *testing.T) {
	want, err := Generate("..")
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	got, err := os.ReadFile(filepath.Join("..", OutputFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale (%d bytes checked in, %d generated); run "+
			"`go generate ./internal/analysis` from the repository root", OutputFile, len(got), len(want))
	}
}
