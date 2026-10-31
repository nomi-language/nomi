package format

import "testing"

// A capture's parenthesized body keeps its parentheses under postfix `!` and
// `?` marks: `&""!` does not parse, since a capture's `&` is written against
// `(`, a call or a `#` literal.
func TestFormat_CaptureBodyKeepsParensUnderPostfixMarks(t *testing.T) {
	src := "fn main() {\n" +
		"    a = &(\"\")!\n" +
		"    b = &(x)?\n" +
		"    c = &(\"\")!?\n" +
		"    d = &f(&1)!\n" +
		"    e = &(not x)\n" +
		"}\n"
	formatsKeepingMeaning(t, src, src)
}
