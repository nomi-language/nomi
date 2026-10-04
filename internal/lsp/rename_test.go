package lsp

import (
	"context"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"path/filepath"
	"testing"
)

func TestRename_SameFile(t *testing.T) {
	src := "fn double(n: Int): Int { n + n }\nfn main() { double(1) }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	// Find "double" at the call site (line 2, col 17)
	sym := fa.SymbolAt(analysis.Pos{Line: 2, Col: 17})
	if sym == nil {
		t.Fatal("expected symbol at call site")
	}
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	id := symbolIdentity(sym)

	edits := buildRenameEdits(fa, target, id, "triple")

	if len(edits) != 2 {
		t.Fatalf("expected 2 edits (1 def + 1 ref), got %d: %+v", len(edits), edits)
	}
	for _, e := range edits {
		if e.NewText != "triple" {
			t.Errorf("expected newText 'triple', got %q", e.NewText)
		}
		width := e.Range.End.Character - e.Range.Start.Character
		if width != 6 {
			t.Errorf("expected range width 6 (len of 'double'), got %d", width)
		}
	}
}

func TestRename_CrossFile(t *testing.T) {
	dir := t.TempDir()
	mathFile := filepath.Join(dir, "math.nomi")
	mainFile := filepath.Join(dir, "main.nomi")

	mathSrc := "pub fn double(n: Int): Int { n + n }"
	mainSrc := "import math\nfn main() { math.double(21) }"
	os.WriteFile(mathFile, []byte(mathSrc), 0644)
	os.WriteFile(mainFile, []byte(mainSrc), 0644)

	dm := analysis.NewDocumentManager()
	mainURI := "file://" + mainFile
	dm.Open(mainURI, mainSrc)

	// Rename at the call site "double" in main.nomi.
	workspaceEdit := collectWorkspaceRename(context.Background(), dm, mainURI, analysis.Pos{Line: 2, Col: 18}, "triple")
	if workspaceEdit == nil {
		t.Fatal("expected workspace edit, got nil")
	}

	// Expect edits in both files
	mathURI := "file://" + mathFile
	mathEdits := workspaceEdit.Changes[mathURI]
	mainEdits := workspaceEdit.Changes[mainURI]

	if len(mathEdits) == 0 {
		t.Errorf("expected at least 1 edit in math.nomi (the definition), got %d", len(mathEdits))
	}
	if len(mainEdits) == 0 {
		t.Errorf("expected at least 1 edit in main.nomi (the call site), got %d", len(mainEdits))
	}

	// All edits should have the new name
	for uri, edits := range workspaceEdit.Changes {
		for _, e := range edits {
			if e.NewText != "triple" {
				t.Errorf("%s: expected newText 'triple', got %q", uri, e.NewText)
			}
		}
	}
}

func TestRename_SelectiveImport(t *testing.T) {
	dir := t.TempDir()
	modelsFile := filepath.Join(dir, "models.nomi")
	mainFile := filepath.Join(dir, "main.nomi")

	modelsSrc := "pub struct User { name: String }"
	mainSrc := "import models.{User}\nfn main() { User{name: \"Alice\"} }"
	os.WriteFile(modelsFile, []byte(modelsSrc), 0644)
	os.WriteFile(mainFile, []byte(mainSrc), 0644)

	dm := analysis.NewDocumentManager()
	mainURI := "file://" + mainFile
	dm.Open(mainURI, mainSrc)

	// Trigger rename at the User use site in main.nomi, line 2 col 13
	workspaceEdit := collectWorkspaceRename(context.Background(), dm, mainURI, analysis.Pos{Line: 2, Col: 13}, "Person")
	if workspaceEdit == nil {
		t.Fatal("expected workspace edit, got nil")
	}

	modelsURI := "file://" + modelsFile
	modelsEdits := workspaceEdit.Changes[modelsURI]
	mainEdits := workspaceEdit.Changes[mainURI]

	// models.nomi: one edit — the struct definition (no separate export
	// entry now that visibility is inline).
	if len(modelsEdits) != 1 {
		t.Errorf("expected 1 edit in models.nomi (definition), got %d", len(modelsEdits))
	}
	// main.nomi: two edits expected — import site + use site
	if len(mainEdits) < 2 {
		t.Errorf("expected >=2 edits in main.nomi (import + use), got %d: %+v", len(mainEdits), mainEdits)
	}
}

func TestRename_NoSymbolAtPosition(t *testing.T) {
	src := "fn double(n: Int): Int { n + n }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	// Whitespace position — no symbol there
	sym := fa.SymbolAt(analysis.Pos{Line: 1, Col: 1})
	_ = sym // at col 1 we land on 'f' in 'fn' which may or may not be a symbol

	// A column that's definitely whitespace
	sym = fa.SymbolAt(analysis.Pos{Line: 1, Col: 3}) // space after "fn"
	if sym != nil {
		t.Fatalf("expected nil symbol at whitespace, got %+v", sym)
	}
}
