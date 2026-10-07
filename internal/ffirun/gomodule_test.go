package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoImportProblem_WhatTheGoModProvides: a `gopkg` path the project's
// go.mod provides has no problem, and any other path, or one that is not a
// Go import path at all, is named.
func TestGoImportProblem_WhatTheGoModProvides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module testproject

go 1.26.3

require example.com/cached v1.2.3

replace echobinding => ./echobinding
`), 0o644); err != nil {
		t.Fatal(err)
	}
	nomiPath := filepath.Join(dir, "main.nomi")
	for _, importPath := range []string{
		"testproject", "testproject/a/b", "example.com/cached/sub", "echobinding", "strings",
	} {
		if why := GoImportProblem(nomiPath, importPath); why != "" {
			t.Errorf("%s refused: %s", importPath, why)
		}
	}
	for importPath, want := range map[string]string{
		"example.com/elsewhere": "outside the project's own Go module testproject, the Go standard library, and every module its go.mod requires or replaces",
		"testprojectx":          "outside the project's own Go module testproject",
		"callback:= f(s":        "not a valid Go import path: invalid char ':'",
		"":                      "not a valid Go import path: empty string",
	} {
		if why := GoImportProblem(nomiPath, importPath); !strings.Contains(why, want) {
			t.Errorf("%q: got %q, want it to contain %q", importPath, why, want)
		}
	}
}

// TestGoImportProblem_NoGoMod: with no go.mod above the declaring file, no
// module provides any import path.
func TestGoImportProblem_NoGoMod(t *testing.T) {
	if _, found := findGoModRoot(os.TempDir()); found {
		t.Skip("a go.mod above the temp directory provides a module")
	}
	nomiPath := filepath.Join(t.TempDir(), "main.nomi")
	want := "no go.mod above the declaring file, so no Go module provides it"
	if why := GoImportProblem(nomiPath, "example.com/x"); why != want {
		t.Fatalf("got %q, want %q", why, want)
	}
}
