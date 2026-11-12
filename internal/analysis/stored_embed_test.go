package analysis_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// TestOpenFromRecordReadsEmbeddedFiles: a program with an `embed` keeps a
// record, opened from it answers what the program checked whole answers,
// and a record is not opened once a file under an embedded directory came
// or changed its size, since the declaration's files and sizes are what
// the analysis reads of them.
func TestOpenFromRecordReadsEmbeddedFiles(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	dir := t.TempDir()
	files := map[string]string{
		"nomi.toml":    "[module]\nname = \"site\"\nentry_points = [\"main\"]\n",
		"main.nomi":    "import\n    std::embed.Files\n    std::io\n    assets\nend\n\nfn main()\n    io.print(Files.paths(assets.static))\nend\n",
		"assets.nomi":  "import std::embed.Files\n\n/// The static files.\npub embed static `static`\n",
		"static/a.txt": "a\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		// A record keeps a tree it did not predict, an embedded directory's,
		// only when its files are older than a build's run.
		old := time.Now().Add(-time.Minute)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	uri := func(key string) string { return fileuri.FromPath(filepath.Join(dir, key+".nomi")) }
	keys := []string{"main", "assets"}
	writeRecord := func() {
		t.Helper()
		writer := newManager()
		writer.SetWorkspaceRoot(dir)
		writer.Open(uri("main"), files["main.nomi"])
		writer.AnalyzeLatest(context.Background(), uri("main"))
		writer.NoteLoweredClean(uri("main"), files["main.nomi"], nil)
		if _, stored := writer.StoredPrograms(); stored == 0 {
			t.Fatal("no record was written for a program that embeds files")
		}
	}
	open := func() *analysis.DocumentManager {
		dm := newManager()
		dm.SetWorkspaceRoot(dir)
		for _, key := range keys {
			dm.Open(uri(key), files[key+".nomi"])
			dm.AnalyzeLatest(context.Background(), uri(key))
		}
		return dm
	}
	writeRecord()
	stored := open()
	if opened, _ := stored.StoredPrograms(); opened == 0 {
		t.Fatal("the record of a program that embeds files was not opened")
	}
	was := os.Getenv(artifactcache.RootEnv)
	os.Setenv(artifactcache.RootEnv, "off")
	whole := open()
	os.Setenv(artifactcache.RootEnv, was)
	for _, key := range keys {
		if want, got := renderOpen(whole, uri(key), dir), renderOpen(stored, uri(key), dir); want != got {
			t.Fatalf("%s opened from its record:\n%s", key, dumpDiff(want, got))
		}
	}

	for i, change := range []struct {
		what, path, text string
	}{
		{"a file added under the embedded directory", "static/b.txt", "b\n"},
		{"an embedded file's size changed", "static/a.txt", "a longer text\n"},
	} {
		if i > 0 {
			// A record of the files as they are now; the first change starts
			// from the one written above.
			writeRecord()
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(change.path)), []byte(change.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if opened, _ := open().StoredPrograms(); opened != 0 {
			t.Errorf("after %s, the record of the files before it was opened", change.what)
		}
	}
}
