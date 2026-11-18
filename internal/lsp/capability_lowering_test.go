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

// A call nomi.toml's [capabilities] lists do not allow is a lowering
// diagnostic at the call, coded capability-not-allowed; saving nomi.toml
// with the name allowed clears it.
func TestLoweringDiagnostics_CapabilityNotAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nallow = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": capabilityMain, "nomi.toml": manifest})
	path := filepath.Join(dir, "main.nomi")

	diags := loweringDiagnostics(path, capabilityMain, nil)
	if len(diags) != 1 {
		t.Fatalf("lowering diagnostics %s; want one", renderDiagnostics(diags))
	}
	d := diags[0]
	want := "this program reaches std::fs, and nomi.toml allows only std::io\nhelp: reached from `main`\n" +
		"help: add \"std::fs\" to allow under [capabilities] in nomi.toml, or remove the call"
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
