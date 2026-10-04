package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small helper that mkdir-p's the parent and writes
// the given content. Tests fail-fast on filesystem errors.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestBuildModuleIndex_HappyPath mirrors the plan's example layout:
// a `todo` project requires a sibling `stringkit` module via a local
// `replace` directive. BuildModuleIndex resolves the require, loads
// the sibling's nomi.toml, and indexes by the manifest's Name.
func TestBuildModuleIndex_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	stringkit := filepath.Join(tmp, "stringkit")

	writeFile(t, filepath.Join(todo, "go.mod"), `module todo

go 1.22

require stringkit v0.0.0

replace stringkit => ../stringkit
`)
	writeFile(t, filepath.Join(todo, "nomi.toml"), `[module]
name = "todo"
`)
	writeFile(t, filepath.Join(todo, "main.nomi"), `fn main(): Unit { () }`)

	writeFile(t, filepath.Join(stringkit, "go.mod"), `module stringkit

go 1.22
`)
	writeFile(t, filepath.Join(stringkit, "nomi.toml"), `[module]
name = "stringkit"
`)
	writeFile(t, filepath.Join(stringkit, "pad.nomi"), `pub fn pad(): Unit { () }`)

	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("expected 1 entry, got %d: %#v", len(idx), idx)
	}
	got, ok := idx["stringkit"]
	if !ok {
		t.Fatalf("expected key %q, index = %#v", "stringkit", idx)
	}
	// Compare via EvalSymlinks: t.TempDir on macOS hands back a path
	// under /var/folders/... that is itself a symlink to /private/var/...,
	// and filepath.Join("../stringkit") through filepath.Join doesn't
	// resolve symlinks. The directories on disk are the same; check
	// that with EvalSymlinks rather than string equality.
	wantResolved, err := filepath.EvalSymlinks(stringkit)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", stringkit, err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", got, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("index[%q] = %s, want %s", "stringkit", gotResolved, wantResolved)
	}
}

// TestBuildModuleIndex_NoGoMod confirms the documented contract: a
// project without a go.mod returns an empty index, no error. This
// is what keeps single-file `nomi run` and no-deps projects valid.
func TestBuildModuleIndex_NoGoMod(t *testing.T) {
	tmp := t.TempDir()
	idx, err := BuildModuleIndex(tmp)
	if err != nil {
		t.Fatalf("BuildModuleIndex on dir without go.mod: %v", err)
	}
	if len(idx) != 0 {
		t.Fatalf("expected empty index, got %#v", idx)
	}
}

// TestBuildModuleIndex_RequireWithoutReplace tests the "no replace"
// case. Stage 1 only supports local-replace deps (no module proxy
// resolution), so a bare require with no matching replace is silently
// skipped — not an error.
func TestBuildModuleIndex_RequireWithoutReplace(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "go.mod"), `module todo

go 1.22

require example.com/remote v1.2.3
`)
	idx, err := BuildModuleIndex(tmp)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx) != 0 {
		t.Fatalf("expected empty index (require skipped), got %#v", idx)
	}
}

// TestBuildModuleIndex_MissingTargetDir tests the case where a replace
// points at a directory that doesn't exist on disk. LoadManifest will
// fail with a non-ErrManifestMissing error (the parent directory is
// missing entirely), so BuildModuleIndex must surface that.
func TestBuildModuleIndex_MissingTargetDir(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "go.mod"), `module todo

go 1.22

require stringkit v0.0.0

replace stringkit => ../does-not-exist
`)
	_, err := BuildModuleIndex(tmp)
	if err == nil {
		t.Fatalf("expected error for missing replace target, got nil")
	}
	// The error should mention the offending nomi.toml path so the
	// user knows which dep failed. We don't pin the exact wording.
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q should mention missing target directory", err)
	}
}

// TestBuildModuleIndex_PureGoDep tests a sibling module that has a
// go.mod but no nomi.toml — a pure-Go dep used for FFI rather than
// Nomi source. The require is skipped silently; not every Go module
// in a project's graph is a Nomi module.
func TestBuildModuleIndex_PureGoDep(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	gokit := filepath.Join(tmp, "gokit")

	writeFile(t, filepath.Join(todo, "go.mod"), `module todo

go 1.22

require gokit v0.0.0

replace gokit => ../gokit
`)
	writeFile(t, filepath.Join(todo, "nomi.toml"), `[module]
name = "todo"
`)

	// gokit has a go.mod but no nomi.toml — pure-Go dep.
	writeFile(t, filepath.Join(gokit, "go.mod"), `module gokit

go 1.22
`)

	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx) != 0 {
		t.Fatalf("expected empty index (pure-Go dep skipped), got %#v", idx)
	}
}

// TestBuildModuleIndex_DuplicateNames tests two siblings whose
// nomi.toml claim the same short-name. The index is keyed by short-
// name, so this is a hard error — the project can't disambiguate.
func TestBuildModuleIndex_DuplicateNames(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	libA := filepath.Join(tmp, "lib-a")
	libB := filepath.Join(tmp, "lib-b")

	writeFile(t, filepath.Join(todo, "go.mod"), `module todo

go 1.22

require lib-a v0.0.0
require lib-b v0.0.0

replace lib-a => ../lib-a
replace lib-b => ../lib-b
`)
	writeFile(t, filepath.Join(todo, "nomi.toml"), `[module]
name = "todo"
`)
	writeFile(t, filepath.Join(libA, "nomi.toml"), `[module]
name = "shared"
`)
	writeFile(t, filepath.Join(libB, "nomi.toml"), `[module]
name = "shared"
`)

	_, err := BuildModuleIndex(todo)
	if err == nil {
		t.Fatalf("expected error for duplicate names, got nil")
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Fatalf("error %q should mention the colliding name", err)
	}
}

// TestBuildModuleIndex_MalformedGoMod ensures parse errors aren't
// swallowed silently — the user should see them.
func TestBuildModuleIndex_MalformedGoMod(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "go.mod"), "this is not a valid go.mod {\n")
	_, err := BuildModuleIndex(tmp)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
}

// TestBuildModuleIndex_AbsoluteReplace tests a replace whose target
// is already absolute (not relative to projectRoot). The replace
// directive's target should be used as-is.
func TestBuildModuleIndex_AbsoluteReplace(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	stringkit := filepath.Join(tmp, "stringkit")

	writeFile(t, filepath.Join(stringkit, "nomi.toml"), `[module]
name = "stringkit"
`)
	writeFile(t, filepath.Join(todo, "go.mod"), `module todo

go 1.22

require stringkit v0.0.0

replace stringkit => `+stringkit+`
`)
	writeFile(t, filepath.Join(todo, "nomi.toml"), `[module]
name = "todo"
`)

	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	got, ok := idx["stringkit"]
	if !ok {
		t.Fatalf("expected key %q, index = %#v", "stringkit", idx)
	}
	wantResolved, _ := filepath.EvalSymlinks(stringkit)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Fatalf("index[stringkit] = %s, want %s", gotResolved, wantResolved)
	}
}
