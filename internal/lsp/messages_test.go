package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// messageProject is a project whose Polish catalog entry was translated
// from an older English text: the catalog has one diagnostic, on line 2.
func messageProject() map[string]string {
	return map[string]string{
		"nomi.toml":            "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
		"main.nomi":            "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    cart\n}\n\nfn main() {\n    io.print(Message.format(cart.greeting(\"Anna\"), Locale`pl`))\n}\n",
		"cart.nomi":            "/// Greets a customer.\npub message greeting(name: String) `Hello, {$name}!`\n",
		"locales/pl/cart.toml": "[greeting]\nsource = '''Hi, {$name}!'''\ntext = '''Cześć, {$name}!'''\n",
	}
}

const currentCatalog = "[greeting]\nsource = '''Hello, {$name}!'''\ntext = '''Cześć, {$name}!'''\n"

func catalogDiagnostic(t *testing.T, diags []protocol.Diagnostic) protocol.Diagnostic {
	t.Helper()
	if len(diags) != 1 || !strings.HasPrefix(diags[0].Message, "pl: `greeting` was translated from an older English text") || diags[0].Range.Start.Line != 1 {
		t.Fatalf("the catalog's diagnostics are %+v, want the stale entry on line 2", diags)
	}
	return diags[0]
}

// A catalog's diagnostics are published on the catalog's URI, from the
// analysis of the file that declares the message, whether it is open or
// not, and are cleared once the catalog is fixed on disk.
func TestMessages_ACatalogsDiagnosticsArePublishedOnIt(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		root := writeProject(t, messageProject())
		s, published := recordingServer()
		s.docs.SetWorkspaceRoot(root)
		cartURI := pathToURI(filepath.Join(root, "cart.nomi"))
		catalogPath := filepath.Join(root, "locales", "pl", "cart.toml")
		catalogURI := pathToURI(catalogPath)
		s.docs.Open(cartURI, messageProject()["cart.nomi"])
		s.publish(s.notify, cartURI)
		catalogDiagnostic(t, published()[catalogURI])
		for _, d := range published()[cartURI] {
			t.Errorf("cart.nomi shows the catalog's diagnostic: %+v", d)
		}

		if err := os.WriteFile(catalogPath, []byte(currentCatalog), 0o644); err != nil {
			t.Fatal(err)
		}
		s.docs.InputsChanged(catalogPath)
		s.publish(s.notify, cartURI)
		if ds, ok := published()[catalogURI]; !ok || len(ds) != 0 {
			t.Errorf("the fixed catalog's diagnostics are %+v, want them cleared", ds)
		}
	})

	t.Run("unopened", func(t *testing.T) {
		root := writeProject(t, messageProject())
		s, published := recordingServer()
		s.docs.SetWorkspaceRoot(root)
		s.scanWorkspace()
		waitClosedDiagnostics(t, s, time.Minute)
		catalogDiagnostic(t, published()[pathToURI(filepath.Join(root, "locales", "pl", "cart.toml"))])
	})
}

// A call of a message goes to the declaration, and the declaration's
// implementations are its catalog entries.
func TestMessages_NavigationGoesToTheDeclarationAndItsEntries(t *testing.T) {
	files := messageProject()
	files["locales/pl/cart.toml"] = currentCatalog
	s, mainURI := openProject(t, files, "main.nomi")
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	main := files["main.nomi"]
	call := strings.Index(main, "greeting(")
	def, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(mainURI)},
		Position:     offsetPosition(main, call+1),
	}})
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := def.(*protocol.Location)
	if !ok || uriToPath(string(loc.URI)) != filepath.Join(root, "cart.nomi") || loc.Range.Start.Line != 1 || loc.Range.Start.Character != 12 {
		t.Fatalf("definition %+v, want cart.nomi 2:13, the message's name", def)
	}

	cartURI := pathToURI(filepath.Join(root, "cart.nomi"))
	s.docs.Open(cartURI, files["cart.nomi"])
	impl, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(cartURI)},
		Position:     protocol.Position{Line: 1, Character: 14},
	}})
	if err != nil {
		t.Fatal(err)
	}
	locs, ok := impl.([]protocol.Location)
	if !ok || len(locs) != 1 || uriToPath(string(locs[0].URI)) != filepath.Join(root, "locales", "pl", "cart.toml") || locs[0].Range.Start.Line != 0 {
		t.Fatalf("implementations %+v, want the pl catalog's entry", impl)
	}
	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(cartURI)},
		Position:     protocol.Position{Line: 1, Character: 14},
	}})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the message's name: %v", err)
	}
	if text := hover.Contents.(protocol.MarkupContent).Value; !strings.Contains(text, "greeting(name: String) -> Message") || !strings.Contains(text, "Greets a customer.") {
		t.Errorf("hover %q, want the function's signature and the doc comment", text)
	}
}
