package rt

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// How a String is written as Nomi source. One table and two encoders serve
// every place a string is shown as code: `String.to_literal`, a String's
// Debug (`dbg`, `io.inspect`, `Debug.inspect`, at any depth), an assertion's
// `values:` row, the debugger's variable view, and the formatter. The lexer
// decodes escapes through DecodeStringEscape, so the two directions read one
// table.

// stringEscapes maps the character after a backslash to the rune it decodes
// to, for the single-character escapes a Nomi string literal has. `\u{HEX}`
// is not single-character, and `\$` is only an escape before `{`; both are
// handled beside the table.
var stringEscapes = map[byte]rune{
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'\\': '\\',
	'"':  '"',
}

// runeEscapes is stringEscapes reversed.
var runeEscapes = func() map[rune]byte {
	m := make(map[rune]byte, len(stringEscapes))
	for b, r := range stringEscapes {
		m[r] = b
	}
	return m
}()

// DecodeStringEscape answers the rune a single-character escape decodes to
// (after is the character following the backslash) and whether it is one.
func DecodeStringEscape(after byte) (rune, bool) {
	r, ok := stringEscapes[after]
	return r, ok
}

// StringLiteral is s as a Nomi string literal that reads back as s: quoted,
// with `\n`, `\r`, `\t`, `\\` and `\"` escaped, `\u{...}` for any other rune
// that is not printable (NUL `\u{0}`, DEL `\u{7f}`, a no-break space
// `\u{a0}`), and `${` written `\${`, so nothing in
// it interpolates. A `$` or a `{` alone, a non-ASCII letter, a combining mark
// and an emoji pass through as they are, the joiners and tags inside an emoji
// included (partOfEmoji), and so does a zero-width joiner or non-joiner
// between two non-ASCII letters or marks (joinsScript).
func StringLiteral(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	writeStringLiteralBody(&b, s, true)
	b.WriteByte('"')
	return b.String()
}

// StringLiteralBody is StringLiteral's escaping without the quotes and
// without the `${` rule, for a writer that places the text inside a literal
// it is building and decides interpolation openers itself (the formatter).
func StringLiteralBody(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	writeStringLiteralBody(&b, s, false)
	return b.String()
}

// writeStringLiteralBody walks s by grapheme cluster, so a rune that is
// invisible on its own but is part of an emoji (partOfEmoji) is written as it
// is: `"👩‍💻"` stays one character rather than `"👩\u{200d}💻"`.
func writeStringLiteralBody(b *strings.Builder, s string, openers bool) {
	at := 0
	var runes []rune
	EachGraphemeCluster(s, func(cluster string) bool {
		runes = runes[:0]
		for _, r := range cluster {
			runes = append(runes, r)
		}
		i := at
		for k, r := range runes {
			if esc, ok := runeEscapes[r]; ok {
				b.WriteByte('\\')
				b.WriteByte(esc)
			} else if r == '$' && openers && i+1 < len(s) && s[i+1] == '{' {
				b.WriteString(`\$`)
			} else if (unicode.IsPrint(r) && !variationSelector(r)) || (k > 0 && partOfEmoji(runes, k)) || joinsScript(s, i, r) {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(b, `\u{%x}`, r)
			}
			i += utf8.RuneLen(r)
		}
		at += len(cluster)
		return true
	})
}

// partOfEmoji reports whether cluster[k], a rune after the first of its
// grapheme cluster that is invisible on its own, belongs to an emoji: a
// variation selector after a symbol or before a keycap's U+20E3 (`1️⃣`), a
// zero-width joiner between a symbol and a later rune of the cluster
// (`👩‍💻`), or a tag character after a symbol (England's flag). Anything
// else stays escaped, so a joiner or selector after a letter, at the start of
// a string or at the end of its cluster is still visible.
func partOfEmoji(cluster []rune, k int) bool {
	symbol := unicode.Is(unicode.So, cluster[0])
	r := cluster[k]
	switch {
	case variationSelector(r):
		return symbol || (k+1 < len(cluster) && cluster[k+1] == 0x20E3)
	case r >= 0xE0020 && r <= 0xE007F:
		return symbol
	}
	return symbol && k+1 < len(cluster)
}

// joinsScript reports whether r, at byte i of s, is a zero-width joiner
// (U+200D) or non-joiner (U+200C) inside a word of a script that uses them:
// the rune before it and the rune after it are both non-ASCII letters or
// combining marks. That keeps a Devanagari conjunct (`क्‍ष`, ZWJ after a
// virama) and a Persian word (`می‌خواهم`, ZWNJ between letters) as typed.
// It reads the neighbours from s, not the cluster, since uniseg ends the
// cluster before the joiner with it (`क्‍` then `ष`, `ی‌` then `خ`), so the
// letter it joins is in the next cluster. A joiner at either end of s, beside
// an ASCII letter, a space, punctuation or another joiner stays escaped.
func joinsScript(s string, i int, r rune) bool {
	if r != 0x200C && r != 0x200D {
		return false
	}
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[i+utf8.RuneLen(r):])
	return scriptLetter(before) && scriptLetter(after)
}

// scriptLetter reports whether r is a non-ASCII letter or combining mark.
func scriptLetter(r rune) bool {
	return r >= utf8.RuneSelf && r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsMark(r))
}

// variationSelector reports whether r is a variation selector, a mark that
// shows nothing of its own and that unicode.IsPrint counts as printable.
func variationSelector(r rune) bool {
	return (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

// UnquoteStringLiteral is StringLiteral's inverse: the String a text that
// StringLiteral wrote reads back as, or false when text is not one (an
// assertion row that is not a String's). It reads the escapes StringLiteral
// writes and no others; the lexer reads the whole literal syntax.
func UnquoteStringLiteral(text string) (string, bool) {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return "", false
	}
	body := text[1 : len(text)-1]
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '"' {
			return "", false
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		after := body[i]
		if r, ok := stringEscapes[after]; ok {
			b.WriteRune(r)
			continue
		}
		switch {
		case after == '$' && i+1 < len(body) && body[i+1] == '{':
			b.WriteByte('$')
		case after == 'u' && i+1 < len(body) && body[i+1] == '{':
			end := strings.IndexByte(body[i+2:], '}')
			if end < 1 || end > 6 {
				return "", false
			}
			n, err := strconv.ParseUint(body[i+2:i+2+end], 16, 32)
			if err != nil || n > unicode.MaxRune || (n >= 0xD800 && n <= 0xDFFF) {
				return "", false
			}
			b.WriteRune(rune(n))
			i += 2 + end
		default:
			return "", false
		}
	}
	return b.String(), true
}
