package lsp

import (
	"context"
	"path/filepath"
	"testing"

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
			"fn f(): Int {\n    w: Wee" + cursorMark + " = Weeks(1)\n    1\n}\n",
			"Weeks",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 0, 0, 0, "import std/calendar.Weeks\n\n") },
		},
		{
			"type merged into the same file's import",
			"import std/calendar.Months\n\nfn f(m: Months): Int {\n    w: Wee" + cursorMark + "\n    1\n}\n",
			"Weeks",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 7, 0, 26, "std/calendar.{Months, Weeks}") },
		},
		{
			"file object after the last import",
			"import std/io\n\nfn f(): Int {\n    jso" + cursorMark + "\n}\n",
			"json",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 1, 0, 1, 0, "import std/json\n") },
		},
		{
			"file object into an import block",
			"import {\n    std/io\n    std/strings\n}\n\nfn f(): Int {\n    jso" + cursorMark + "\n}\n",
			"json",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 3, 0, 3, 0, "    std/json\n") },
		},
		{
			"function through its file object",
			"fn f() {\n    read_li" + cursorMark + "\n}\n",
			"io.read_line",
			func(t *testing.T, e protocol.TextEdit) { wantEdit(t, e, 0, 0, 0, 0, "import std/io\n\n") },
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

func TestCompletion_AutoImportFunctionInsertsQualifiedCall(t *testing.T) {
	items := completeWith(t, true, "fn f() {\n    read_li"+cursorMark+"\n}\n")
	it := mustItem(t, items, "io.read_line")
	if te := textEditOf(t, it); te.NewText != "io.read_line()$0" {
		t.Fatalf("insert = %q, want io.read_line()$0", te.NewText)
	}
	if it.FilterText == nil || *it.FilterText != "read_line" {
		t.Fatalf("filterText = %v, want read_line", it.FilterText)
	}

	// Already imported: the same item, no edit.
	items = completeWith(t, true, "import std/io\n\nfn f() {\n    read_li"+cursorMark+"\n}\n")
	it = mustItem(t, items, "io.read_line")
	if len(it.AdditionalTextEdits) != 0 {
		t.Fatalf("an imported file's function carries an import edit: %+v", it.AdditionalTextEdits)
	}
}

func TestCompletion_AutoImportNeverReimportsThePrelude(t *testing.T) {
	items := complete(t, "fn f(): Int {\n    x: Mayb"+cursorMark+"\n    1\n}\n")
	maybe := itemsWithLabel(items, "Maybe")
	if len(maybe) != 1 {
		t.Fatalf("Maybe offered %d times, want once", len(maybe))
	}
	if len(maybe[0].AdditionalTextEdits) != 0 {
		t.Fatalf("the prelude's Maybe is offered with an import: %+v", maybe[0].AdditionalTextEdits)
	}
	for _, it := range items {
		if len(it.AdditionalTextEdits) > 0 && (it.Label == "Display" || it.Label == "List" || it.Label == "Set") {
			t.Fatalf("prelude name %q offered with an import", it.Label)
		}
	}
}

func TestCompletion_AutoImportNothingWithoutAPrefix(t *testing.T) {
	list := completeIn(t, NewServer(), "file:///noprefix.nomi", "fn f(): Int {\n    x = "+cursorMark+"\n    1\n}\n")
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
		"server.nomi": "pub struct Config {\n    port: Int\n}\n\npub fn start_server(c: Config): Int {\n    c.port\n}\n\nfn hidden_helper(): Int {\n    1\n}\n",
		"main.nomi":   "fn main() {\n}\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")

	items := completeIn(t, s, uri, "fn main() {\n    start_s"+cursorMark+"\n}\n").Items
	it := mustItem(t, items, "server.start_server")
	wantEdit(t, onlyEdit(t, it), 0, 0, 0, 0, "import server\n\n")
	labelsExclude(t, items, "server.hidden_helper")

	items = completeIn(t, s, uri, "fn main() {\n    c: Conf"+cursorMark+"\n}\n").Items
	it = mustItem(t, items, "Config")
	wantEdit(t, onlyEdit(t, it), 0, 0, 0, 0, "import server.Config\n\n")
}
