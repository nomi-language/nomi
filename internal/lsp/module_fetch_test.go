package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The language server never fetches a module. An import of a module
// nomi.toml names and the module cache lacks is a diagnostic whose hint says to run
// `nomi fetch`; once the module is in the cache, the import resolves, and
// go-to-definition on `import greet` opens the module's root file greet.nomi.
func TestModules_TheServerReportsAMissingModuleAndNeverDownloads(t *testing.T) {
	host := modfetchtest.New(t)
	host.Tag("example.com/greet", "v0.1.0", map[string]string{"nomi.toml": "[module]\nname = \"greet\"\n"})
	t.Setenv("NOMI_TEST_MODULE_HOSTS", host.Env("example.com"))
	cache := modfetchtest.CacheDir(t)
	t.Setenv(modcache.CacheEnv, cache)

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
	write(filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+
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
			strings.Contains(d.Message, "example.com/greet v0.1.0, which is not in the module cache; run `nomi fetch`") {
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

	// `nomi fetch` (here, by hand) puts the module in the cache.
	modfetchtest.CacheRepo(t, cache, "example.com/greet", "v0.1.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello(name: String) -> String\n    \"hello, ${name}\"\nend\n",
	})
	mod := modcache.RepoDir(cache, "example.com/greet", "v0.1.0")

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
