package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// phrasesProject is a project with a `phrase` file and its catalogs, in a
// directory a check reads the catalogs from. The catalogs are in texts too,
// by their path, so that an edit step can name one.
func phrasesProject(tb testing.TB) incrementalProject {
	dir := tb.TempDir()
	files := map[string]string{
		"nomi.toml":            "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\npartial_locales = [\"de\"]\n",
		"locales/pl/cart.toml": "[cart_items]\nsource = '''{$count :integer} items'''\ntext = '''{$count :integer} produkty'''\n",
		"locales/de/cart.toml": "[cart_items]\nsource = '''{$count :integer} items'''\ntext = '''{$count :integer} Artikel'''\n",
	}
	p := incrementalProject{name: "phrases", entry: "main", dir: dir, texts: map[string]string{
		"main": "import\n    std::io\n    cart\nend\n\nfn main()\n    io.print(Phrase.format(cart.cart_items(3), Locale\"pl\"))\nend\n",
		"cart": "pub phrase cart_items(count: Int) `{$count :integer} items`\n",
	}}
	for key, text := range p.texts {
		files[key+".nomi"] = text
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
		if strings.HasSuffix(name, ".toml") && name != "nomi.toml" {
			p.texts[name] = text
		}
	}
	return p
}

// phrasesEdits edit a phrase's text, its catalogs and its signature. An
// edit to a catalog keeps every phrase's signature, so it rebuilds the
// declaring file's bodies alone, as an edit of the phrase's text does.
var phrasesEdits = []editStep{
	replace("catalog body only: a translation", "locales/pl/cart.toml", "produkty", "produktów"),
	replace("catalog body only: a stale source", "locales/pl/cart.toml", "items'''", "things'''"),
	restore("catalog body only: back", "locales/pl/cart.toml"),
	replace("catalog body only: a partial locale's stale entry", "locales/de/cart.toml", "items'''", "things'''"),
	replace("phrase body only: its text", "cart", "items`", "things`"),
	replace("phrase body only: its text back", "cart", "things`", "items`"),
	replace("phrase signature", "cart", "(count: Int)", "(count: Int, name: String)"),
	restore("phrase signature back", "cart"),
}

// TestOpenDocumentCatalogEditsMatchFreshBuilds: with a phrase file and its
// importer open, an edit to a catalog on disk (the server's watched-file
// event, InputsChanged) rechecks the program through the bodies of the file
// that reads the catalog, and each open document gets what a fresh analysis
// of it gives. An edit to the phrase's own text does the same through the
// document.
func TestOpenDocumentCatalogEditsMatchFreshBuilds(t *testing.T) {
	p := phrasesProject(t)
	uri := func(key string) string {
		return fileuri.FromPath(filepath.Join(p.dir, filepath.FromSlash(key)+".nomi"))
	}
	dm := newManager()
	cur := map[string]string{}
	for key, text := range p.texts {
		cur[key] = text
		if !strings.HasSuffix(key, ".toml") {
			dm.Open(uri(key), text)
		}
	}
	for _, key := range []string{"main", "cart"} {
		dm.UpdateImportEdges(uri(key))
	}
	for i, s := range phrasesEdits {
		text, ok := s.change(cur[s.key], p.texts[s.key])
		if !ok {
			t.Fatalf("step %d (%s) does not apply to %s", i+1, s.kind, s.key)
		}
		cur[s.key] = text
		bodies, _, _, _ := dm.ProgramChecks()
		if strings.HasSuffix(s.key, ".toml") {
			path := filepath.Join(p.dir, filepath.FromSlash(s.key))
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			dm.InputsChanged(path)
		} else {
			if err := os.WriteFile(fileuri.ToPath(uri(s.key)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			dm.Update(uri(s.key), text)
			dm.UpdateImportEdges(uri(s.key))
			dm.PropagateChange(uri(s.key))
		}
		after, _, _, _ := dm.ProgramChecks()
		if fast := after > bodies; fast != bodyOnly(s.kind) {
			t.Errorf("step %d (%s, editing %s): rebuilt bodies alone: %v (whole programs because %v)", i+1, s.kind, s.key, fast, dm.FullCheckReasons())
		}
		for _, key := range []string{"main", "cart"} {
			got := renderBuild(analysis.InstalledForTest(dm, uri(key)))
			want := renderBuild(analysis.BuildForTest(newManager(), uri(key), cur[key], false))
			if got != want {
				t.Fatalf("step %d (%s, editing %s): %s gets\n%s\nand a fresh analysis of it gives\n%s", i+1, s.kind, s.key, key, got, want)
			}
		}
	}
}
