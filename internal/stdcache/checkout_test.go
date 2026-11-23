package stdcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A navigator names a checkout file only while it holds the embedded
// content, decides per file, and never names a checkout file the embedded
// std lacks.
func TestNavigator_NamesTheCheckoutOnlyForAMatchingFile(t *testing.T) {
	files := stdFS("pub enum Option<T>\n  Some(T)\n  None\nend\n")
	checkout := t.TempDir()
	for _, name := range []string{"option.nomi", "strings.nomi"} {
		if err := os.WriteFile(filepath.Join(checkout, name), files[name].Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(checkout, "extra.nomi"), []byte("fn f() end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := New(filepath.Join(t.TempDir(), "std"), files)
	nav := NewNavigator(checkout, dir)

	want := func(rel, path string) {
		t.Helper()
		if got := nav.Path(rel); got != path {
			t.Fatalf("Path(%s) = %s, want %s", rel, got, path)
		}
	}
	want("option.nomi", filepath.Join(checkout, "option.nomi"))
	want("strings.nomi", filepath.Join(checkout, "strings.nomi"))
	want("extra.nomi", dir.Location("extra.nomi"))
	want("_fixtures/nested/deeper/module.nomi", dir.Location("_fixtures/nested/deeper/module.nomi"))

	option := filepath.Join(checkout, "option.nomi")
	if err := os.WriteFile(option, []byte("pub enum Option<T>\n  None\n  Some(T)\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(option, later, later); err != nil {
		t.Fatal(err)
	}
	want("option.nomi", dir.Location("option.nomi"))
	want("strings.nomi", filepath.Join(checkout, "strings.nomi"))

	if err := os.Remove(option); err != nil {
		t.Fatal(err)
	}
	want("option.nomi", dir.Location("option.nomi"))

	if got := NewNavigator("", dir).Path("strings.nomi"); got != dir.Location("strings.nomi") {
		t.Fatalf("with no checkout, Path(strings.nomi) = %s", got)
	}
}
