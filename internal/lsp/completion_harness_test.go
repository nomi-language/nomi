package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// cursorMark marks the cursor in a completion test's source. It is not `|`,
// which Nomi uses for lambdas and pipes.
const cursorMark = "‸"

// completionItemsOf unwraps a completion response into its items.
func completionItemsOf(t *testing.T, res any) []protocol.CompletionItem {
	t.Helper()
	switch v := res.(type) {
	case nil:
		return nil
	case *protocol.CompletionList:
		return v.Items
	case []protocol.CompletionItem:
		return v
	}
	t.Fatalf("completion result = %T, want a CompletionList", res)
	return nil
}

// splitCursor removes the cursor mark from src and returns the 0-based LSP
// position it marked, in UTF-16 units.
func splitCursor(t testing.TB, src string) (string, protocol.Position) {
	t.Helper()
	off := strings.Index(src, cursorMark)
	if off < 0 {
		t.Fatalf("source has no cursor mark %q:\n%s", cursorMark, src)
	}
	content := src[:off] + src[off+len(cursorMark):]
	before := content[:off]
	line := strings.Count(before, "\n")
	lineStart := strings.LastIndex(before, "\n") + 1
	var u16 uint32
	for _, r := range before[lineStart:] {
		if r > 0xFFFF {
			u16 += 2
		} else {
			u16++
		}
	}
	return content, protocol.Position{Line: uint32(line), Character: u16}
}

// completeIn opens src (with its cursor mark) as uri on s and requests
// completion at the mark.
func completeIn(t *testing.T, s *Server, uri, src string) *protocol.CompletionList {
	t.Helper()
	content, pos := splitCursor(t, src)
	s.docs.Open(uri, content)
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if res == nil {
		return &protocol.CompletionList{}
	}
	list, ok := res.(*protocol.CompletionList)
	if !ok {
		t.Fatalf("completion result = %T, want *CompletionList", res)
	}
	return list
}

// complete runs one completion over a single-file document.
func complete(t *testing.T, src string) []protocol.CompletionItem {
	t.Helper()
	return completeIn(t, NewServer(), "file:///completion_case.nomi", src).Items
}

func itemLabels(items []protocol.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func findItem(items []protocol.CompletionItem, label string) *protocol.CompletionItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

// labelsInclude fails unless every want label is offered.
func labelsInclude(t *testing.T, items []protocol.CompletionItem, want ...string) {
	t.Helper()
	got := itemLabels(items)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("completion does not offer %q; got %v", w, got)
		}
	}
}

// labelsExclude fails if any of the labels is offered.
func labelsExclude(t *testing.T, items []protocol.CompletionItem, unwanted ...string) {
	t.Helper()
	got := itemLabels(items)
	for _, u := range unwanted {
		if slices.Contains(got, u) {
			t.Errorf("completion offers %q, which does not fit the position; got %v", u, got)
		}
	}
}

// labelsBefore fails unless a is ranked above b.
func labelsBefore(t *testing.T, items []protocol.CompletionItem, a, b string) {
	t.Helper()
	got := itemLabels(items)
	ia, ib := slices.Index(got, a), slices.Index(got, b)
	if ia < 0 || ib < 0 || ia > ib {
		t.Errorf("want %q ranked before %q; got %v", a, b, got)
	}
}

// writeProject writes files (relative path → content) under a fresh
// directory with a nomi.toml and returns the directory.
func writeProject(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["nomi.toml"]; !ok {
		files["nomi.toml"] = "[module]\nname = \"app\"\n"
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
