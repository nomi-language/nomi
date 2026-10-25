package casinggen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTablesAreCurrent(t *testing.T) {
	want, err := Generate(filepath.Join("..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "casing_tables.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("internal/stdstrings/casing_tables.go is stale; run `go generate ./internal/stdstrings`")
	}
}
