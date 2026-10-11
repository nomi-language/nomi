// Package textpos converts columns within one line of text between bytes,
// which the lexer and analyzer count, and UTF-16 code units, which LSP
// clients count by default and JavaScript strings index by. The language
// server (internal/lsp) and the tour's highlighter (internal/semtokens,
// cmd/nomi-wasm) both convert through it.
package textpos

import "unicode/utf8"

// UTF16Col is the 0-based UTF-16 column of the 0-based byte column byteCol
// in line. A byte column inside a multi-byte character counts that whole
// character; one past the end of line is line's UTF-16 length. A byte that
// is not valid UTF-8 counts as one unit, as an editor shows it as one
// U+FFFD.
func UTF16Col(line string, byteCol int) int {
	u16 := 0
	for i := 0; i < len(line) && i < byteCol; {
		r, size := utf8.DecodeRuneInString(line[i:])
		u16 += unitsOf(r)
		i += size
	}
	return u16
}

// ByteCol is the inverse of UTF16Col: the 0-based byte column of the
// 0-based UTF-16 column u16 in line. A column inside a surrogate pair counts
// that whole character; one past the end of line is len(line).
func ByteCol(line string, u16 int) int {
	units, i := 0, 0
	for i < len(line) && units < u16 {
		r, size := utf8.DecodeRuneInString(line[i:])
		units += unitsOf(r)
		i += size
	}
	return i
}

// UTF16Len is the length of s in UTF-16 code units.
func UTF16Len(s string) int {
	return UTF16Col(s, len(s))
}

// unitsOf is the number of UTF-16 code units r encodes to: two for a
// character outside the Basic Multilingual Plane (a surrogate pair), one
// otherwise, including utf8.RuneError for an invalid byte.
func unitsOf(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}
