package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The import-actions fixture is a clean Nomi file whose header comments
// document the edit scenarios below. These tests keep those claims honest: the
// committed file is clean valid Nomi, and each experiment produces exactly what
// the comment says.

const importActionsFixturePath = "testdata/import_actions.nomi"
const selectiveInspectFixturePath = "testdata/selective_inspect.nomi"

func mustAbsPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return abs
}

func applyWorkspaceEdit(t *testing.T, content string, action protocol.CodeAction, uri string) string {
	t.Helper()
	if action.Edit == nil {
		t.Fatal("code action had no edit")
	}
	edits := action.Edit.Changes[protocol.DocumentUri(uri)]
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 edit, got %d", len(edits))
	}
	return applyTextEdit(content, edits[0])
}

func readImportActionsFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(importActionsFixturePath)
	if err != nil {
		t.Fatalf("read demo: %v", err)
	}
	return string(b)
}

func readSelectiveInspectFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selectiveInspectFixturePath)
	if err != nil {
		t.Fatalf("read selective inspect fixture: %v", err)
	}
	return string(b)
}

// The committed demo compiles with no diagnostics and is already canonical, so
// it offers no fixAll action.
func TestImportActionsFixture_CleanAsCommitted(t *testing.T) {
	content := readImportActionsFixture(t)

	tokens := lexer.Lex(content)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	errs := append([]analysis.TypeError(nil), fa.TypeErrors...)
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	if len(errs) != 0 {
		t.Fatalf("demo should compile clean, got %d diagnostics: %+v", len(errs), errs)
	}
	if _, ok := runFixAll(t, content); ok {
		t.Error("clean demo should offer no fixAll action")
	}
}

func TestSelectiveInspectFixture_BareImportedInspectIsClean(t *testing.T) {
	content := readSelectiveInspectFixture(t)
	if !strings.Contains(content, "std/io.inspect") {
		t.Fatal("selective inspect fixture should import io.inspect selectively")
	}
	if _, ok := runOrganize(t, content); ok {
		t.Error("clean selective inspect fixture should offer no organize-imports action")
	}
	if _, ok := runFixAll(t, content); ok {
		t.Error("clean selective inspect fixture should offer no fixAll action")
	}
}

func TestSelectiveInspectFixture_ServerFixAllKeepsBareImportedInspect(t *testing.T) {
	content := readSelectiveInspectFixture(t)
	uri := "file://" + mustAbsPath(t, selectiveInspectFixturePath)

	server := NewServer()
	server.docs.Open(uri, content)
	only := []protocol.CodeActionKind{codeActionKindSourceFixAll}
	result, err := server.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Context:      protocol.CodeActionContext{Only: only},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	if result == nil {
		return
	}
	actions, ok := result.([]protocol.CodeAction)
	if !ok {
		t.Fatalf("expected []CodeAction, got %T", result)
	}
	for _, action := range actions {
		if action.Kind == nil || *action.Kind != codeActionKindSourceFixAll {
			continue
		}
		edited := applyWorkspaceEdit(t, content, action, uri)
		if !strings.Contains(edited, "std/io.inspect") {
			t.Fatalf("server fixAll removed std/io.inspect:\n%s", edited)
		}
	}
}

func TestStdMap_ServerFixAllKeepsSomeImport(t *testing.T) {
	path := "../../std/maps.nomi"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read std map: %v", err)
	}
	content := string(b)
	// The stdlib is one Nomi module, so its files name each other bare.
	content = strings.Replace(content, "maybe.Maybe.{self, None}\n", "maybe.Maybe.{self, None, Some}\n", 1)
	if !strings.Contains(content, "maybe.Maybe.{self, None, Some}") {
		t.Fatal("std map fixture should include Maybe.Some import")
	}
	uri := "file://" + mustAbsPath(t, path)

	server := NewServer()
	server.docs.Open(uri, content)
	only := []protocol.CodeActionKind{codeActionKindSourceFixAll}
	result, err := server.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Context:      protocol.CodeActionContext{Only: only},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	if result == nil {
		return
	}
	actions, ok := result.([]protocol.CodeAction)
	if !ok {
		t.Fatalf("expected []CodeAction, got %T", result)
	}
	for _, action := range actions {
		if action.Kind == nil || *action.Kind != codeActionKindSourceFixAll {
			continue
		}
		edited := applyWorkspaceEdit(t, content, action, uri)
		if !strings.Contains(edited, "maybe.Maybe.{self, None, Some}") {
			t.Fatalf("server fixAll removed Maybe.Some:\n%s", edited)
		}
	}
}

// ADD IO experiment: deleting the `std/io` block entry makes `io.print` and
// `io.inspect` undefined; fixAll re-adds `std/io` as a block entry (sorted),
// preserving the block — never a stray flat statement.
func TestImportActionsFixture_AddExperiment(t *testing.T) {
	content := readImportActionsFixture(t)
	edited := strings.Replace(content, "  std/io\n", "", 1)
	if edited == content {
		t.Fatal("fixture no longer contains the `  std/io` block entry the ADD IO experiment targets")
	}
	got, ok := runFixAll(t, edited)
	if !ok {
		t.Fatal("expected fixAll to act after removing std/io")
	}
	// entry-level checks (robust to the demo's other imports): std/io rejoins as
	// a block entry, never as a stray flat statement.
	if !strings.Contains(got, "  std/io\n") {
		t.Errorf("expected std/io re-added as a block entry, got:\n%s", got)
	}
	if strings.Contains(got, "\nimport std/io\n") {
		t.Errorf("std/io should rejoin the block, not appear as a flat statement:\n%s", got)
	}
}

// ADD DURATION experiment: deleting the `std/duration.Duration` entry makes
// `Duration.seconds` undefined; fixAll re-adds it as a block entry.
func TestImportActionsFixture_AddTypeExperiment(t *testing.T) {
	content := readImportActionsFixture(t)
	edited := strings.Replace(content, "  std/duration.Duration\n", "", 1)
	if edited == content {
		t.Fatal("fixture no longer contains the `  std/duration.Duration` entry the ADD DURATION experiment targets")
	}
	got, ok := runFixAll(t, edited)
	if !ok {
		t.Fatal("expected fixAll to act after removing std/duration")
	}
	// entry-level (the header comment also mentions std/duration.Duration).
	if !strings.Contains(got, "  std/duration.Duration\n") {
		t.Errorf("expected std/duration.Duration re-added as a block entry, got:\n%s", got)
	}
}

// REMOVE experiment: adding an unused `std/lists.List` block entry; fixAll drops it.
func TestImportActionsFixture_RemoveExperiment(t *testing.T) {
	content := readImportActionsFixture(t)
	edited := strings.Replace(content, "  std/duration.Duration\n", "  std/duration.Duration\n  std/lists.List\n", 1)
	if edited == content {
		t.Fatal("fixture no longer contains the `  std/duration.Duration` entry the REMOVE experiment anchors on")
	}
	got, ok := runFixAll(t, edited)
	if !ok {
		t.Fatal("expected fixAll to act after adding an unused std/lists.List")
	}
	// Match the import ENTRY line specifically — "std/lists" also appears in the
	// header comment that documents this experiment.
	if strings.Contains(got, "  std/lists.List\n") {
		t.Errorf("unused std/lists.List import should be removed, got:\n%s", got)
	}
	if !strings.Contains(got, "  std/duration.Duration\n") {
		t.Errorf("expected std/duration.Duration to remain, got:\n%s", got)
	}
}
