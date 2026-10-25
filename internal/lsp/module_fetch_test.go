package lsp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The language server never downloads a module. An import of a module go.mod
// requires and the module cache lacks is a diagnostic whose hint says to run
// `nomi fetch`; once the module is in the cache, the import resolves, and
// go-to-definition on `import greet` opens the module's root file greet.nomi.
func TestModules_TheServerReportsAMissingModuleAndNeverDownloads(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the language server requested %s from the module proxy", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer proxy.Close()
	cache := filepath.Join(t.TempDir(), "modcache")
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOPROXY", proxy.URL)
	t.Setenv("GOSUMDB", proxy.URL)

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
	write(filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.22\n\nrequire example.com/greet v0.1.0\n")
	write(filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	src := "import {\n    greet\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(\"you\"))\n}\n"
	path := filepath.Join(dir, "main.nomi")
	write(path, src)
	uri := "file://" + path

	s := NewServer()
	snap := s.docs.Open(uri, src)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
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
	mod := filepath.Join(cache, "example.com", "greet@v0.1.0")
	write(filepath.Join(mod, "go.mod"), "module example.com/greet\n\ngo 1.22\n")
	write(filepath.Join(mod, "nomi.toml"), "[module]\nname = \"greet\"\n")
	write(filepath.Join(mod, "greet.nomi"), "pub fn hello(name: String) -> String {\n    \"hello, ${name}\"\n}\n")
	write(filepath.Join(cache, "cache", "download", "example.com", "greet", "@v", "v0.1.0.ziphash"), "h1:x\n")

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
}
