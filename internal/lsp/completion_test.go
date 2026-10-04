package lsp

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestSymbolKindToCompletionKind(t *testing.T) {
	tests := []struct {
		kind     analysis.SymbolKind
		expected protocol.CompletionItemKind
	}{
		{analysis.SymbolFunction, protocol.CompletionItemKindFunction},
		{analysis.SymbolStruct, protocol.CompletionItemKindStruct},
		{analysis.SymbolEnum, protocol.CompletionItemKindEnum},
		{analysis.SymbolEnumVariant, protocol.CompletionItemKindEnumMember},
		{analysis.SymbolInterface, protocol.CompletionItemKindInterface},
		{analysis.SymbolBinding, protocol.CompletionItemKindVariable},
		{analysis.SymbolParam, protocol.CompletionItemKindVariable},
		{analysis.SymbolModule, protocol.CompletionItemKindModule},
	}
	for _, tt := range tests {
		got := symbolKindToCompletionKind(tt.kind)
		if got != tt.expected {
			t.Errorf("kind %v: expected %v, got %v", tt.kind, tt.expected, got)
		}
	}
}

func TestMatchQuality(t *testing.T) {
	tests := []struct {
		prefix, name string
		want         int
		ok           bool
	}{
		{"", "anything", 1, true},
		{"trim", "trim", 0, true},
		{"tr", "trim", 1, true},
		{"TR", "trim", 2, true},
		{"tl", "to_lower", 3, true},
		{"to_l", "to_lower", 1, true},
		{"tlow", "to_lower", 3, true},
		{"sw", "starts_with?", 3, true},
		{"oA", "orApply", 3, true},
		{"tlr", "to_lower", 4, true},
		{"x", "to_lower", 0, false},
		{"lt", "to_lower", 0, false},
	}
	for _, tt := range tests {
		got, ok := matchQuality(tt.prefix, tt.name)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("matchQuality(%q, %q) = %d, %v; want %d, %v", tt.prefix, tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGetPrefix(t *testing.T) {
	content := "fn add(x: Int): Int { x + y }"
	// cursor after "add" at position (0, 6)
	prefix := getPrefix(content, 0, 6)
	if prefix != "add" {
		t.Errorf("expected 'add', got '%s'", prefix)
	}

	// cursor at start of line
	prefix = getPrefix(content, 0, 0)
	if prefix != "" {
		t.Errorf("expected empty, got '%s'", prefix)
	}

	// cursor after "x" in body
	prefix = getPrefix(content, 0, 23)
	if prefix != "x" {
		t.Errorf("expected 'x', got '%s'", prefix)
	}
}

func TestGetPrefix_PredicateSuffix(t *testing.T) {
	// A `?`-suffixed partial identifier: the trailing predicate marker must
	// be part of the extracted prefix so completion filtering still works.
	content := "fn main(): Unit {\n    empty?"
	prefix := getPrefix(content, 1, 10)
	if prefix != "empty?" {
		t.Errorf("expected 'empty?', got '%s'", prefix)
	}
}

func TestWordBounds(t *testing.T) {
	tests := []struct {
		src         string // ‸ marks the cursor
		word, typed string
	}{
		{"x = fo‸o + 1", "foo", "fo"},
		{"x = empty?‸", "empty?", "empty?"},
		{"x = emp‸ty?", "empty?", "emp"},
		{"x = ‸", "", ""},
		{"io.pr‸", "pr", "pr"},
	}
	for _, tt := range tests {
		off := strings.Index(tt.src, cursorMark)
		content := tt.src[:off] + tt.src[off+len(cursorMark):]
		start, end := wordBounds(content, off)
		if content[start:end] != tt.word || content[start:off] != tt.typed {
			t.Errorf("wordBounds(%q) = %q typed %q; want %q typed %q", tt.src, content[start:end], content[start:off], tt.word, tt.typed)
		}
	}
}

func TestInlineGoHelperCompletions(t *testing.T) {
	items := inlineGoHelperCompletions("toNomiE")
	var labels []string
	for _, item := range items {
		labels = append(labels, item.Label)
		if item.Detail == nil || !strings.HasPrefix(*item.Detail, "func "+item.Label) {
			t.Fatalf("completion %q missing helper signature detail: %+v", item.Label, item)
		}
		if item.Documentation == nil {
			t.Fatalf("completion %q missing documentation", item.Label)
		}
	}
	want := []string{"toNomiErr", "toNomiErrString"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("labels: got %v, want %v", labels, want)
	}
}

func TestCompletion_InlineGoHelperInGoBody(t *testing.T) {
	src := "fn parse(raw: String): Result<Int, String> go {\n  return toNomiE" + cursorMark + "\n}\n"
	labels := itemLabels(complete(t, src))
	if strings.Join(labels, ",") != "toNomiErr,toNomiErrString" {
		t.Fatalf("labels: got %v", labels)
	}
}

// Completion on a file API object offers the file's root-level declarations
// and none of the functions declared in its `impl` blocks, which the checker
// rejects when spelled file-qualified (`duration.seconds`).
func TestCompletion_FileObjectOmitsTypeOwnedFunctions(t *testing.T) {
	items := complete(t, "import std/duration\n\nfn main() {\n  duration."+cursorMark+"\n}\n")
	labelsExclude(t, items, "seconds", "as_seconds")
	if !slices.Contains(itemLabels(items), "Duration") {
		t.Fatalf("completion on `duration.` did not reach the file's members (want Duration): %v", itemLabels(items))
	}
}
