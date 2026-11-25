package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The language server never fetches a package. An import of a package
// nomi.toml names and the package cache lacks is a diagnostic whose hint says to run
// `nomi fetch`; once the package is in the cache, the import resolves, and
// go-to-definition on `import greet` opens the package's root file greet.nomi.
func TestPackages_TheServerReportsAMissingPackageAndNeverDownloads(t *testing.T) {
	host := pkgfetchtest.New(t)
	host.Tag("example.com/greet", "v0.1.0", map[string]string{"nomi.toml": "[package]\nname = \"greet\"\n"})
	t.Setenv("NOMI_TEST_PACKAGE_HOSTS", host.Env("example.com"))
	cache := pkgfetchtest.CacheDir(t)
	t.Setenv(pkgcache.CacheEnv, cache)

	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+
		"greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n")
	src := "import\n    greet\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(\"you\"))\nend\n"
	path := filepath.Join(dir, "main.nomi")
	write(path, src)
	uri := fileuri.FromPath(path)

	s := NewServer()
	snap := s.docs.Open(uri, src)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Analysis, snap.Errors, snap.Analysis.TypeErrors)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "no module `greet`") &&
			strings.Contains(d.Message, "example.com/greet v0.1.0, which is not in the package cache; run `nomi fetch`") {
			found = true
		}
	}
	if !found {
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.Message)
		}
		t.Fatalf("no diagnostic says the module is missing and to run `nomi fetch`:\n  %s", strings.Join(msgs, "\n  "))
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatalf("the language server created the module cache %s", cache)
	}

	// `nomi fetch` (here, by hand) puts the package in the cache.
	pkgfetchtest.CacheRepo(t, cache, "example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello(name: String) -> String\n    \"hello, ${name}\"\nend\n",
	})
	mod := pkgcache.RepoDir(cache, "example.com/greet", "v0.1.0")

	s = NewServer()
	snap = s.docs.Open(uri, src)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	for _, e := range snap.Analysis.TypeErrors {
		t.Errorf("with the module in the cache: %d:%d: %s", e.Line, e.Col, e.Message)
	}
	loc := mustDefinitionLocation(t, s, uri, protocol.Position{Line: 1, Character: 6})
	if want := protocol.DocumentUri(pathToURI(filepath.Join(mod, "greet.nomi"))); loc.URI != want {
		t.Fatalf("go-to-definition on `import greet` went to %s, want %s", loc.URI, want)
	}
	if n := host.Requests(); n != 0 {
		t.Errorf("the language server made %d requests of the module's host", n)
	}
}
