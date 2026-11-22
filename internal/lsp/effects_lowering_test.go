package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const effectMain = `import
    std::fs
    std::io
end

fn main()
    io.print(fs.read_text("notes.txt"))
end
`

// A dependency that reaches more than its own nomi.toml declares is a
// lowering diagnostic, coded effect-not-declared, in the entry at the
// project's call that leads there, naming the call in the dependency's
// file and pointing at it.
func TestLoweringDiagnostics_EffectNotDeclared(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	dir := writeProject(t, map[string]string{
		"app/nomi.toml":      "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.deps]\nstd = [\"std::io\", \"std::fs\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/main.nomi":      main,
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n\n[effects.self]\nstd = [\"std::io\"]\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
	diags := loweringDiagnostics(filepath.Join(dir, "app", "main.nomi"), main, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	if !strings.HasPrefix(d.Message, "your code has the effect std::fs in pricing here: `main` calls `load`\n"+
		"pricing has the effect std::fs, and its own [effects.self] allows it only std::io\n") {
		t.Errorf("message = %q, want the project's call, then the dependency's own list named", d.Message)
	}
	if d.Range.Start != (protocol.Position{Line: 6, Character: 13}) {
		t.Errorf("range %+v, want the project's call at 7:14", d.Range)
	}
	if d.Code == nil || d.Code.Value != "effect-not-declared" || len(d.RelatedInformation) != 1 ||
		d.RelatedInformation[0].Location.URI != protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "pricing", "rules.nomi"))) {
		t.Errorf("code %+v related %+v, want effect-not-declared pointing at rules.nomi", d.Code, d.RelatedInformation)
	}
}

// A call nomi.toml's [effects] tables do not allow is a lowering
// diagnostic at the call, coded effect-not-allowed; saving nomi.toml
// with the name allowed clears it.
func TestLoweringDiagnostics_EffectNotAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": effectMain, "nomi.toml": manifest})
	path := filepath.Join(dir, "main.nomi")

	diags := loweringDiagnostics(path, effectMain, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	want := "this module's own code has the effect std::fs, and [effects.self] allows it only std::io\nhelp: reached from `main`\n" +
		"help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	if d.Code == nil || d.Code.Value != "effect-not-allowed" || d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityError {
		t.Errorf("code %+v severity %v, want an effect-not-allowed error", d.Code, d.Severity)
	}
	if d.Range.Start != (protocol.Position{Line: 6, Character: 13}) {
		t.Errorf("range %+v, want the call at 7:14", d.Range)
	}

	// The server publishes it, and a save of nomi.toml that allows std::fs
	// clears it.
	uri := fileuri.FromPath(path)
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, effectMain)
	c.waitMessage(uri, "this module's own code has the effect std::fs, and [effects.self] allows it only std::io")
	before := s.lowering.lowered.Load()
	if got := s.lowering.diagnostics(uri); len(got) != 1 || got[0].Code == nil || got[0].Code.Value != "effect-not-allowed" {
		t.Fatalf("the server holds %s; want the effect diagnostic", renderDiagnostics(got))
	}
	manifestPath := filepath.Join(dir, "nomi.toml")
	if err := os.WriteFile(manifestPath, []byte(strings.Replace(manifest, `["std::io"]`, `["std::io", "std::fs"]`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	c.notify("workspace/didChangeWatchedFiles", protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{
		{URI: protocol.DocumentUri(fileuri.FromPath(manifestPath)), Type: protocol.FileChangeTypeChanged},
	}})
	deadline := time.Now().Add(60 * time.Second)
	for s.lowering.lowered.Load() == before || len(s.lowering.diagnostics(uri)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("after nomi.toml allowed std::fs, %d lowerings (%d before) and %s", s.lowering.lowered.Load(), before, renderDiagnostics(s.lowering.diagnostics(uri)))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
