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
	src := "import {\n" +
		"  // network things\n" +
		"  std/io\n" +
		"  std/iter // for loops\n" +
		"  // trailing note\n" +
		"}\n\n" +
		"fn main() {\n" +
		"  io.inspect(Iter.count([1]))\n" +
		"}\n"
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

// A blank line before a comment inside a block survives even when the preceding
// entry ends in line-level `export` — the lexer used to drop that blank because
// EXPORT wasn't treated as a statement-ender (so no BLANK_LINE token followed).
// This is the prelude shape (every import is re-exported).
func TestFormat_BlankBeforeCommentAfterExport(t *testing.T) {
	src := "import {\n" +
		"    std/maps.Map export\n" +
		"\n" +
		"    // a comment\n" +
		"    std/unit.Unit export\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != src {
		t.Errorf("blank line before comment after an export entry was not preserved:\ngot:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_GoPackageHandleInImportRegion(t *testing.T) {
	src := "gopkg \"modernc.org/sqlite\"\n"
	want := "gopkg \"modernc.org/sqlite\"\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("unexpected formatted Go import block:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_GoPackageHandleStaysSeparateFromNomiImports(t *testing.T) {
	src := "gopkg \"database/sql\"\n" +
		"import std/results.Result\n"
	want := "gopkg \"database/sql\"\n" +
		"\n" +
		"import std/results.Result\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("Go imports should stay in a separate block:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
