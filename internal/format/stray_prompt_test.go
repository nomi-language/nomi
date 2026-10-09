package format

import (
	"strings"
	"testing"
)

// A `//!` after code is a parse error, so the formatter refuses it instead
// of moving it to a line of its own, where it would read as an attached-test
// prompt and the output would not parse. Found by FuzzFormatKeepsMeaning:
// `A{a//!000000\n}` formatted to a literal whose third line was `//!000000`.
func TestFormat_RefusesAPromptAfterCode(t *testing.T) {
	for _, src := range []string{
		"A{a//!000000\n}",
		"A{a: 1 //! x\n}\n",
		"fn f(): A {\n    A{a: 1, //! x\n        b: 2}\n}\n",
		"fn f(): List<Int> {\n    [\n        1,\n        2, //! x\n    ]\n}\n",
		"fn f(): Int { //! x\n    1\n}\n",
	} {
		out, err := Format(src)
		if err == nil {
			t.Errorf("Format accepted a `//!` after code:\n%s\noutput:\n%s", src, out)
			continue
		}
		if !strings.Contains(err.Error(), "`//!` attached test must start its own line") {
			t.Errorf("Format(%q) error = %v, want the stray-prompt error", src, err)
		}
	}
}

// The same literal with `//` and `//#` comments formats and keeps its
// meaning.
func TestFormat_CommentAfterPunnedFieldKeepsMeaning(t *testing.T) {
	formatsKeepingMeaning(t, "A{a// x\n}", "A{\n    a,\n    // x\n}\n")
	formatsKeepingMeaning(t, "A{a//# x\n}", "A{\n    a,\n    //# x\n}\n")
}
