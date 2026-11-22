package format

import (
	"strings"
	"testing"
)

// Comments inside an import block must survive formatting (they were silently
// dropped: the parser discarded block-entry trivia and emitImportBlock never
// rendered it). Leading comments above an entry, trailing same-line comments,
// and a comment before the closing brace are all preserved, and the result is
// idempotent.
func TestFormat_ImportBlockComments(t *testing.T) {
	src := "import\n" +
		"  // network things\n" +
		"  std::io\n" +
		"  std::iter // for loops\n" +
		"  // trailing note\n" +
		"end\n" +
		"\n" +
		"fn main()\n" +
		"  io.inspect(Iter.count([1]))\n" +
		"end\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	for _, want := range []string{"// network things", "// for loops", "// trailing note"} {
		if !strings.Contains(got, want) {
			t.Errorf("comment %q lost; got:\n%s", want, got)
		}
	}
	again, err := Format(got)
	if err != nil {
		t.Fatalf("Format (idempotence): %v", err)
	}
	if again != got {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

// A blank line before a comment inside a `pub import` block survives. This
// is the prelude shape (every import is re-exported).
func TestFormat_BlankBeforeCommentInPubImportBlock(t *testing.T) {
	src := "pub import\n" +
		"    std::maps.Map\n" +
		"\n" +
		"    // a comment\n" +
		"    std::unit.Unit\n" +
		"end\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != src {
		t.Errorf("blank line before comment in a `pub import` block was not preserved:\ngot:\n%s\nwant:\n%s", got, src)
	}
}
