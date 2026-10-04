package analysis

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeManifestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifest_FullManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `
[module]
name = "todo"
entry_points = ["main", "tools/seed"]
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "todo" {
		t.Errorf("Name: got %q, want %q", m.Name, "todo")
	}
	if want := []string{"main", "tools/seed"}; !reflect.DeepEqual(m.EntryPoints, want) {
		t.Errorf("EntryPoints: got %v, want %v", m.EntryPoints, want)
	}
}

func TestLoadManifest_LibraryOnly(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "stringkit"
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "stringkit" {
		t.Errorf("Name: got %q, want %q", m.Name, "stringkit")
	}
	if len(m.EntryPoints) != 0 {
		t.Errorf("EntryPoints should be empty, got %v", m.EntryPoints)
	}
}

func TestLoadManifest_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(dir)
	if err == nil || !errors.Is(err, ErrManifestMissing) {
		t.Errorf("expected ErrManifestMissing, got %v", err)
	}
}

func TestLoadManifest_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module
name = "todo"
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
	if !strings.Contains(err.Error(), "nomi.toml") {
		t.Errorf("error message should mention nomi.toml, got: %v", err)
	}
}

func TestLoadManifest_MissingName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
entry_points = ["main"]
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadManifest_DuplicateEntries(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "todo"
entry_points = ["main", "main"]
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for duplicate entry_points")
	}
}

func TestLoadManifest_WhitespaceOnlyName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "   "
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for whitespace-only name")
	}
}

func TestLoadManifest_RejectsReservedStdName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "std"
entry_points = ["main"]
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatalf("expected reserved-name error, got nil")
	}
	if !strings.Contains(err.Error(), "std") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected error mentioning 'std' and 'reserved'; got: %q", err.Error())
	}
}

func TestLoadManifest_AllowsStdNameForConfiguredStdlib(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "prelude.nomi", "")
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "std"
entry_points = []
`)
	t.Setenv("NOMI_STD_PATH", dir)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error for configured stdlib manifest: %v", err)
	}
	if m.Name != "std" {
		t.Fatalf("Name: got %q, want std", m.Name)
	}
}

func TestParseManifestData_RejectsReservedStdName(t *testing.T) {
	_, err := ParseManifestData([]byte(`[module]
name = "std"
entry_points = []
`), "<virtual nomi.toml>")
	if err == nil {
		t.Fatal("expected reserved-name error")
	}
}

func TestLoadManifest_EmptyEntryPointString(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "todo"
entry_points = ["main", ""]
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for empty-string entry_points element")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %v", err)
	}
}
