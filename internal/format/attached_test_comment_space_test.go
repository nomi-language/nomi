package format

import "testing"

// The renderer strips trailing spaces from every line it writes, a comment
// line's included, and SameMeaning compares comment text trimmed of
// trailing space. A comment line among a function's `//!` prompts is held
// to the same rule. Found by FuzzFormatKeepsMeaning: `// ` after `//!assert`
// became `//`, and SameMeaning compared the comment's raw text.
func TestFormat_AttachedTestCommentTrailingSpace(t *testing.T) {
	for _, src := range []string{
		"//!assert\n// \nfn A(){}",
		"//! assert\n// \t\nfn A() {}\n",
		"//! assert\n// note \nfn A() {}\n",
		"//! assert\n/// doc \nfn A() {}\n",
		"// \n//! assert\nfn A() {}\n",
		"// note \n//! assert\nfn A() {}\n",
		"//! assert\n// \n//! assert\nfn A() {}\n",
		"//! assert\n// note \n//! assert\nfn A() {}\n",
	} {
		got, err := Format(src)
		if err != nil {
			t.Fatalf("Format(%q): %v", src, err)
		}
		if err := SameMeaning(src, got); err != nil {
			t.Errorf("SameMeaning(%q, %q): %v", src, got, err)
		}
		if again, _ := Format(got); again != got {
			t.Errorf("not idempotent: %q then %q", got, again)
		}
	}
	// Its text is still compared.
	if err := SameMeaning("//! assert\n// a\nfn A() {}\n", "//! assert\n// b\nfn A() {}\n"); err == nil {
		t.Error("SameMeaning accepts a comment among the prompts with other text")
	}
}
