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

const capabilityMain = `import
    std::fs
    std::io
end

fn main()
    io.print(fs.read_text("notes.txt"))
end
`

// A dependency that reaches more than its own nomi.toml declares is a
// lowering diagnostic, coded capability-not-declared, in the entry naming
// the call in the dependency's file and pointing at it.
func TestLoweringDiagnostics_CapabilityNotDeclared(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	dir := writeProject(t, map[string]string{
		"app/go.mod":         "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml":      "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nstd = [\"std::io\", \"std::fs\"]\n",
		"app/main.nomi":      main,
		"pricing/go.mod":     "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n\n[capabilities]\nstd = [\"std::io\"]\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
	diags := loweringDiagnostics(filepath.Join(dir, "app", "main.nomi"), main, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	if !strings.HasPrefix(d.Message, "in rules.nomi:4:5: example.com/pricing reaches std::fs, and its own nomi.toml allows it only std::io\n") {
		t.Errorf("message = %q, want the dependency's own list named at its call", d.Message)
	}
	if d.Code == nil || d.Code.Value != "capability-not-declared" || len(d.RelatedInformation) != 1 ||
		d.RelatedInformation[0].Location.URI != protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "pricing", "rules.nomi"))) {
		t.Errorf("code %+v related %+v, want capability-not-declared pointing at rules.nomi", d.Code, d.RelatedInformation)
	}
}

// A call nomi.toml's [capabilities] lists do not allow is a lowering
// diagnostic at the call, coded capability-not-allowed; saving nomi.toml
// with the name allowed clears it.
func TestLoweringDiagnostics_CapabilityNotAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nstd = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": capabilityMain, "nomi.toml": manifest})
	path := filepath.Join(dir, "main.nomi")

	diags := loweringDiagnostics(path, capabilityMain, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	want := "this program reaches std::fs, and nomi.toml allows only std::io\nhelp: reached from `main`\n" +
		"help: add \"std::fs\" to std under [capabilities] in nomi.toml, or remove the call"
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	if d.Code == nil || d.Code.Value != "capability-not-allowed" || d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityError {
		t.Errorf("code %+v severity %v, want a capability-not-allowed error", d.Code, d.Severity)
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
	c.open(uri, capabilityMain)
	c.waitMessage(uri, "this program reaches std::fs, and nomi.toml allows only std::io")
	before := s.lowering.lowered.Load()
	if got := s.lowering.diagnostics(uri); len(got) != 1 || got[0].Code == nil || got[0].Code.Value != "capability-not-allowed" {
		t.Fatalf("the server holds %s; want the capability diagnostic", renderDiagnostics(got))
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

// A Go binding in a module the project's go does not list is a lowering
// diagnostic at the binding, coded capability-not-allowed, naming the
// module to add to go.
func TestLoweringDiagnostics_CapabilityGoBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import std::io\n\ngopkg \"strings\" as gostrings\n\nfn upper(s: String) -> String go gostrings.ToUpper\n\nfn main()\n    io.print(upper(\"hi\"))\nend\n"
	dir := writeProject(t, map[string]string{
		"go.mod":    "module example.com/app\n\ngo 1.27.0\n",
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nstd = [\"std::io\"]\n",
		"main.nomi": main,
	})
	diags := loweringDiagnostics(filepath.Join(dir, "main.nomi"), main, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	want := "example.com/app binds Go through go:strings, and nomi.toml lets no module bind Go\nhelp: reached from `main`; `main` calls `upper`\n" +
		"help: add \"example.com/app\" to go under [capabilities] in nomi.toml, or remove the call"
	if d.Message != want || d.Code == nil || d.Code.Value != "capability-not-allowed" {
		t.Errorf("message %q code %+v, want %q coded capability-not-allowed", d.Message, d.Code, want)
	}
}
