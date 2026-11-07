package analysis_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// TestOpenFromRecordReadsCatalogs: a program whose `phrase` file reads
// catalogs gets a record, which names the catalogs it read. Its documents
// opened from the record answer what the program checked whole does; a
// body edit of its importer is taken in the program opened from the
// record; a
// catalog edited while it is open forgets the program opened from the
// record; and a catalog changed on disk keeps the next server from opening
// the record at all.
func TestOpenFromRecordReadsCatalogs(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	p := phrasesProject(t)
	uri := func(key string) string {
		return fileuri.FromPath(filepath.Join(p.dir, filepath.FromSlash(key)+".nomi"))
	}
	texts := map[string]string{"main": p.texts["main"], "cart": p.texts["cart"]}
	writer := newManager()
	writer.SetWorkspaceRoot(p.dir)
	writer.Open(uri("main"), texts["main"])
	writer.NoteLoweredClean(uri("main"), texts["main"], nil)
	if _, stored := writer.StoredPrograms(); stored == 0 {
		t.Fatal("no record was written for a program that reads catalogs")
	}
	open := func(dm *analysis.DocumentManager) {
		dm.SetWorkspaceRoot(p.dir)
		for _, key := range []string{"main", "cart"} {
			dm.Open(uri(key), texts[key])
			dm.AnalyzeLatest(context.Background(), uri(key))
			dm.UpdateImportEdges(uri(key))
		}
	}
	full := func() *analysis.DocumentManager {
		was := os.Getenv(artifactcache.RootEnv)
		os.Setenv(artifactcache.RootEnv, "off")
		defer os.Setenv(artifactcache.RootEnv, was)
		dm := newManager()
		open(dm)
		return dm
	}
	compare := func(what string, stored, whole *analysis.DocumentManager) {
		t.Helper()
		for _, key := range []string{"main", "cart"} {
			if want, got := renderOpen(whole, uri(key), p.dir), renderOpen(stored, uri(key), p.dir); want != got {
				t.Fatalf("%s: %s opened from its record:\n%s", what, key, dumpDiff(want, got))
			}
		}
	}
	stored := newManager()
	open(stored)
	if opened, _ := stored.StoredPrograms(); opened == 0 {
		t.Fatal("the record was not opened")
	}
	whole := full()
	compare("opened", stored, whole)

	// A body edit of the file that formats the message is taken in the
	// program opened from the record. (An edit of the `phrase` file is
	// checked whole: its expansion reads more than the file.)
	edited := strings.Replace(texts["main"], "cart_items(3)", "cart_items(4)", 1)
	before := stored.StoredEdits()
	for _, dm := range []*analysis.DocumentManager{stored, whole} {
		dm.Update(uri("main"), edited)
		dm.UpdateImportEdges(uri("main"))
	}
	compare("a body edit", stored, whole)
	if stored.StoredEdits() == before {
		t.Errorf("the body edit was not taken in the program opened from its record: %v", stored.FullCheckReasons())
	}
	for _, dm := range []*analysis.DocumentManager{stored, whole} {
		dm.Update(uri("main"), texts["main"])
		dm.UpdateImportEdges(uri("main"))
	}
	compare("the body back", stored, whole)

	// A catalog changed under the open program.
	catalog := filepath.Join(p.dir, "locales", "pl", "cart.toml")
	text := strings.Replace(p.texts["locales/pl/cart.toml"], "produkty", "produktów", 1)
	if err := os.WriteFile(catalog, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	stored.InputsChanged(catalog)
	whole.InputsChanged(catalog)
	compare("a catalog changed", stored, whole)
	if reasons := stored.FullCheckReasons(); reasons["from the record: a file does not read, or an input of an expansion changed"] == 0 {
		t.Errorf("the catalog's change did not forget the program opened from its record: %v", reasons)
	}

	// A next server does not open a record of the old catalog.
	next := newManager()
	open(next)
	if opened, _ := next.StoredPrograms(); opened != 0 {
		t.Error("a record of the catalog's old text was opened")
	}
	compare("a next server", next, full())
}
