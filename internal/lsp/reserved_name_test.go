package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A type declared under a reserved name is refused, and the server reports
// the refusal alone, as `nomi check` does: the declaring document gets the
// one refusal and a document that imports the name gets none of the errors
// that follow from it (an orphan impl, a private import, a variant pattern).
func TestReservedTypeNameIsPublishedAlone(t *testing.T) {
	tmp := t.TempDir()
	files := map[string]string{
		"units.nomi": `pub enum Unit {
    Ohms
    Kiloohms
}

impl Display for Unit {
    fn to_string(unit: Unit) -> String {
        case unit {
            .Ohms -> "ohms"
            .Kiloohms -> "kiloohms"
        }
    }
}
`,
		"units_test.nomi": `import units.Unit

test "unit" {
    assert Display.to_string(Unit.Ohms) == "ohms"
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer()
	s.docs.SetWorkspaceRoot(tmp)
	s.docs.IndexWorkspace(context.Background())

	pubs := map[string][]protocol.Diagnostic{}
	notify := func(method string, params any) {
		if method == protocol.ServerTextDocumentPublishDiagnostics {
			p := params.(*protocol.PublishDiagnosticsParams)
			pubs[string(p.URI)] = p.Diagnostics
		}
	}
	const want = "type name 'Unit' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyUnit { ... }`)"
	for _, name := range []string{"units.nomi", "units_test.nomi"} {
		uri := fileuri.FromPath(filepath.Join(tmp, name))
		s.docs.Open(uri, files[name])
		s.docs.UpdateImportEdges(uri)
		s.publish(notify, uri)
		diags, ok := pubs[uri]
		if !ok {
			t.Fatalf("%s: nothing published", name)
		}
		if name == "units.nomi" {
			if len(diags) != 1 || diags[0].Message != want || diags[0].Range.Start.Line != 0 || diags[0].Range.Start.Character != 9 {
				t.Errorf("%s: published %v, want the refusal alone at 1:10", name, diagMessages(diags))
			}
			continue
		}
		if len(diags) != 0 {
			t.Errorf("%s: published %v, want nothing: every error there follows from the refusal", name, diagMessages(diags))
		}
	}
}
