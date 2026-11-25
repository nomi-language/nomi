package lsp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// itemWithLabel returns the items labeled label.
func itemsWithLabel(items []protocol.CompletionItem, label string) []protocol.CompletionItem {
	var out []protocol.CompletionItem
	for _, it := range items {
		if it.Label == label {
			out = append(out, it)
		}
	}
	return out
}

func onlyEdit(t *testing.T, it protocol.CompletionItem) protocol.TextEdit {
	t.Helper()
	if len(it.AdditionalTextEdits) != 1 {
		t.Fatalf("item %q has %d additional edits, want 1: %+v", it.Label, len(it.AdditionalTextEdits), it.AdditionalTextEdits)
	}
	return it.AdditionalTextEdits[0]
}

func wantEdit(t *testing.T, got protocol.TextEdit, sl, sc, el, ec uint32, text string) {
	t.Helper()
	r := got.Range
	if r.Start.Line != sl || r.Start.Character != sc || r.End.Line != el || r.End.Character != ec || got.NewText != text {
		t.Fatalf("edit = %d:%d-%d:%d %q, want %d:%d-%d:%d %q",
			r.Start.Line, r.Start.Character, r.End.Line, r.End.Character, got.NewText, sl, sc, el, ec, text)
	}
}

func TestCompletion_AutoImportStdlib(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		label string
		edit  func(t *testing.T, e protocol.TextEdit)
	}{
		{
			"type into a file with no imports",
			"fn f() -> Int\n    w: Wee" + cursorMark + " = Weeks(1)\n    1\nend\n",
			"Weeks",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 0, 0, 0, "import std::calendar.Weeks\n\n") },
		},
		{
			"type merged into the same file's import",
			"import std::calendar.Months\n\nfn f(m: Months) -> Int\n    w: Wee" + cursorMark + "\n    1\nend\n",
			"Weeks",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 7, 0, 27, "std::calendar.{Months, Weeks}") },
		},
		{
			"file object after the last import",
			"import std::io\n\nfn f() -> Int\n    jso" + cursorMark + "\nend\n",
			"json",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 1, 0, 1, 0, "import std::json\n") },
		},
		{
			"file object into an import block",
			"import\n    std::io\n    std::strings\nend\n\nfn f() -> Int\n    jso" + cursorMark + "\nend\n",
			"json",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 3, 0, 3, 0, "    std::json\n") },
		},
		{
			"function through its file object",
			"fn f()\n    read_li" + cursorMark + "\nend\n",
			"io.read_line",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 0, 0, 0, "import std::io\n\n") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer()
			s.snippetSupport = true
			list := completeIn(t, s, "file:///autoimport.nomi", tt.src)
			if !list.IsIncomplete {
				t.Error("a list with import candidates must be incomplete, so the client asks again as the word grows")
			}
			it := mustItem(t, list.Items, tt.label)
			tt.edit(t, onlyEdit(t, it))
			if it.Detail == nil {
				t.Fatal("no detail")
			}
		})
	}
}

// std's plumbing modules (`fs/host`), which declare nothing public, are
// never offered: not after `import std::`, and not as a file object to
// import. The public modules beside them are.
func TestCompletion_OffersOnlyPublicStdModules(t *testing.T) {
	paths := map[string]bool{}
	for _, label := range itemLabels(complete(t, "import std::f"+cursorMark+"\n\nfn main()\nend\n")) {
		paths[label] = true
	}
	if !paths["fs"] || !paths["fs/testing"] || paths["fs/host"] || paths["prelude"] {
		t.Errorf("after `import std::`: want fs and fs/testing, and neither fs/host nor prelude; got %v", paths)
	}
	for _, it := range complete(t, "fn f()\n    hos"+cursorMark+"\nend\n") {
		if it.Detail != nil && *it.Detail == "import std::fs::host" {
			t.Errorf("std::fs::host offered as an import: %q", it.Label)
		}
	}
}

func TestCompletion_AutoImportFunctionInsertsQualifiedCall(t *testing.T) {
	items := completeWith(t, true, "fn f()\n    read_li"+cursorMark+"\nend\n")
	it := mustItem(t, items, "io.read_line")
	if te := textEditOf(t, it); te.NewText != "io.read_line()$0" {
		t.Fatalf("insert = %q, want io.read_line()$0", te.NewText)
	}
	if it.FilterText == nil || *it.FilterText != "read_line" {
		t.Fatalf("filterText = %v, want read_line", it.FilterText)
	}

	// Already imported: the same item, no edit.
	items = completeWith(t, true, "import std::io\n\nfn f()\n    read_li"+cursorMark+"\nend\n")
	it = mustItem(t, items, "io.read_line")
	if len(it.AdditionalTextEdits) != 0 {
		t.Fatalf("an imported file's function carries an import edit: %+v", it.AdditionalTextEdits)
	}
}

func TestCompletion_AutoImportNeverReimportsThePrelude(t *testing.T) {
	items := complete(t, "fn f() -> Int\n    x: Opti"+cursorMark+"\n    1\nend\n")
	option := itemsWithLabel(items, "Option")
	if len(option) != 1 {
		t.Fatalf("Option offered %d times, want once", len(option))
	}
	if len(option[0].AdditionalTextEdits) != 0 {
		t.Fatalf("the prelude's Option is offered with an import: %+v", option[0].AdditionalTextEdits)
	}
	for _, it := range items {
		if len(it.AdditionalTextEdits) > 0 && (it.Label == "Display" || it.Label == "List" || it.Label == "Set") {
			t.Fatalf("prelude name %q offered with an import", it.Label)
		}
	}
}

func TestCompletion_AutoImportNothingWithoutAPrefix(t *testing.T) {
	list := completeIn(t, NewServer(), "file:///noprefix.nomi", "fn f() -> Int\n    x = "+cursorMark+"\n    1\nend\n")
	for _, it := range list.Items {
		if len(it.AdditionalTextEdits) > 0 {
			t.Fatalf("an empty prefix offers import candidate %q", it.Label)
		}
	}
	if !list.IsIncomplete {
		t.Fatal("a list that held import candidates back must be incomplete")
	}
}

func TestCompletion_AutoImportProjectFiles(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"server.nomi": "pub struct Config\n    port: Int\nend\n\npub fn start_server(c: Config) -> Int\n    c.port\nend\n\nprivate fn hidden_helper() -> Int\n    1\nend\n",
		"main.nomi":   "fn main() end\n",
	})
	s := NewServer()
	s.docs.SetEditorFolder(dir)
	s.docs.IndexEditorFolder(context.Background())
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))

	items := completeIn(t, s, uri, "fn main()\n    start_s"+cursorMark+"\nend\n").Items
	it := mustItem(t, items, "server.start_server")
	wantEdit(t, onlyEdit(t, it), 0, 0, 0, 0, "import server\n\n")
	labelsExclude(t, items, "server.hidden_helper")

	items = completeIn(t, s, uri, "fn main()\n    c: Conf"+cursorMark+"\nend\n").Items
	it = mustItem(t, items, "Config")
	wantEdit(t, onlyEdit(t, it), 0, 0, 0, 0, "import server.Config\n\n")
}

// Within a module, completion offers another file's internal declarations,
// a function with no modifier, as auto-import, after the file's name and
// in an import's braces, and never a private one (spec §14 *Visibility*).
func TestCompletion_OffersThePackagesInternalDeclarations(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"engine.nomi": "pub fn start_engine() -> Int\n    1\nend\n\nfn spin_engine() -> Int\n    2\nend\n\n" +
			"private fn stall_engine() -> Int\n    3\nend\n\nstruct Gear\n    teeth: Int\nend\n\nprivate struct Bolt\n    size: Int\nend\n",
		"main.nomi": "fn main() end\n",
	})
	s := NewServer()
	s.docs.SetEditorFolder(dir)
	s.docs.IndexEditorFolder(context.Background())
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))

	items := completeIn(t, s, uri, "fn main()\n    spin_"+cursorMark+"\nend\n").Items
	wantEdit(t, onlyEdit(t, mustItem(t, items, "engine.spin_engine")), 0, 0, 0, 0, "import engine\n\n")
	items = completeIn(t, s, uri, "fn main()\n    stall_"+cursorMark+"\nend\n").Items
	labelsExclude(t, items, "engine.stall_engine")
	items = completeIn(t, s, uri, "fn main()\n    g: Ge"+cursorMark+"\nend\n").Items
	wantEdit(t, onlyEdit(t, mustItem(t, items, "Gear")), 0, 0, 0, 0, "import engine.Gear\n\n")
	items = completeIn(t, s, uri, "fn main()\n    b: Bol"+cursorMark+"\nend\n").Items
	labelsExclude(t, items, "Bolt")

	items = completeIn(t, s, uri, "import engine\n\nfn main()\n    engine."+cursorMark+"\nend\n").Items
	labelsInclude(t, items, "start_engine", "spin_engine")
	labelsExclude(t, items, "stall_engine")

	items = completeIn(t, s, uri, "import engine.{"+cursorMark+"}\n\nfn main() end\n").Items
	labelsInclude(t, items, "start_engine", "spin_engine", "Gear")
	labelsExclude(t, items, "stall_engine", "Bolt")
}
