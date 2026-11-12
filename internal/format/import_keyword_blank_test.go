package format

import "testing"

// An import path may end in a keyword (`opaque`), and the lexer reads a
// line break after one as a continuation, so a blank line after that
// entry does not read back. Sorting can put an entry that carries a blank
// line right after such an entry; formatting drops the blank line there
// the first time, as it would the second. (FuzzFormatKeepsMeaning found
// it in a layout variant of a corpus file.)
func TestFormat_BlankAfterKeywordImportEntryIsIdempotent(t *testing.T) {
	src := "import\n" +
		"    zz\n" +
		"\n" +
		"    // c\n" +
		"    struct\n" +
		"    opaque\n" +
		"end\n"
	want := "import\n" +
		"    opaque\n" +
		"    // c\n" +
		"    struct\n" +
		"    zz\n" +
		"end\n"
	once, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if once != want {
		t.Errorf("Format:\n%s\nwant:\n%s", once, want)
	}
	twice, err := Format(once)
	if err != nil {
		t.Fatalf("Format of the formatted text: %v", err)
	}
	if twice != once {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", once, twice)
	}
}
