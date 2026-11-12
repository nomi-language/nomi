package format

import (
	"strings"
	"testing"
)

// A comment line with no text, `///` or `//`, is layout. The formatter
// drops a doc comment that is only blank lines (Doc keeps no text for it)
// and writes a doc comment's trailing blank lines as `//`; SameMeaning
// accepts both.
func TestFormat_BlankDocCommentIsLayout(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"///\nbegin end\n", "begin end\n"},
		{"///\nfn f() end\n", "fn f() end\n"},
		{"///\n///\nstruct P\n    ///\n    x: Int\nend\n", "//\n//\nstruct P\n    x: Int\nend\n"},
		{"/// Adds.\n///\nfn add() end\n", "/// Adds.\n//\nfn add() end\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// A doc comment's text is what follows `///` and one space: the formatter
// writes `///0` as `/// 0`, the same text. Found by FuzzFormatKeepsMeaning.
func TestFormat_DocCommentSpaceAfterMarker(t *testing.T) {
	formatsKeepingMeaning(t, "///0\nfn a() end\n", "/// 0\nfn a() end\n")
	if err := SameMeaning("/// a\nfn a() end\n", "/// b\nfn a() end\n"); err == nil {
		t.Error("SameMeaning accepts a doc comment with other text")
	}
}

// A comment with text is still compared.
func TestSameMeaning_ComparesCommentsWithText(t *testing.T) {
	for _, c := range []struct{ src, formatted string }{
		{"/// Adds.\nfn add() end\n", "fn add() end\n"},
		{"// why\nx = 1\n", "x = 1\n"},
		{"x = 1\n", "// added\nx = 1\n"},
	} {
		err := SameMeaning(c.src, c.formatted)
		if err == nil || !strings.Contains(err.Error(), "comments changed") {
			t.Errorf("SameMeaning(%q, %q) = %v, want a comment change", c.src, c.formatted, err)
		}
	}
}
