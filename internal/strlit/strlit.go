// Package strlit is the single source of truth for Nomi string-literal escape
// handling. The lexer (decode: source escape -> rune) and the formatter
// (encode: rune -> source escape) both derive from the simpleEscapes table
// here, so the two directions cannot drift out of being inverses. Letting them
// drift — the formatter using Go's strconv.Quote conventions while the lexer
// expected the braced \u{...} form — is exactly what corrupted strings
// containing format characters before this package existed.
package strlit

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// simpleEscapes maps the character after a backslash to the rune it decodes
// to, for the single-character escapes Nomi supports. The \u{HEX} form is not
// single-character and is handled separately by both the lexer and
// EncodeStringBody.
var simpleEscapes = map[byte]rune{
	'n':  '\n',
	't':  '\t',
	'\\': '\\',
	'"':  '"',
}

// runeToSimple is the reverse of simpleEscapes, used by EncodeStringBody so the
// encode side is derived from the same table the decode side uses.
var runeToSimple = func() map[rune]byte {
	m := make(map[rune]byte, len(simpleEscapes))
	for b, r := range simpleEscapes {
		m[r] = b
	}
	return m
}()

// DecodeSimple returns the rune a single-character escape decodes to (the
// argument is the character following the backslash) and whether it is a
// recognized escape. The lexer uses this and rejects the input when ok is
// false and the escape is not the \u{...} form.
func DecodeSimple(after byte) (rune, bool) {
	r, ok := simpleEscapes[after]
	return r, ok
}

// EncodeStringBody re-encodes a decoded string into the body of a single-line
// Nomi string literal (no surrounding quotes), emitting only escapes the lexer
// accepts: the simpleEscapes table, plus \u{HEX} for any other non-printable
// rune. Printable runes — including non-ASCII letters, emoji, and combining
// marks — pass through verbatim.
func EncodeStringBody(s string) string {
	var b strings.Builder
	for _, r := range s {
		if esc, ok := runeToSimple[r]; ok {
			b.WriteByte('\\')
			b.WriteByte(esc)
		} else if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, `\u{%x}`, r)
		}
	}
	return b.String()
}

// DecodeUnicodeEscape decodes the hex digits between the braces of a
// `\u{...}` escape. The digits must be 1–6 hex digits naming a Unicode scalar
// value: at most 10FFFF and not a surrogate (D800–DFFF). Otherwise it returns
// a complete diagnostic instead. String literals and codepoint literals both
// decode the escape here, so they accept the same spellings.
func DecodeUnicodeEscape(hex string) (rune, string) {
	invalid := `invalid \u{...} code point: ` + hex + `; `
	if hex == "" || len(hex) > 6 {
		return 0, invalid + `a \u{...} escape takes 1 to 6 hex digits`
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, invalid + `a \u{...} escape takes 1 to 6 hex digits`
	}
	if n > 0x10FFFF {
		return 0, invalid + `the largest code point is 10FFFF`
	}
	if n >= 0xD800 && n <= 0xDFFF {
		return 0, invalid + `D800 to DFFF are surrogates, not Unicode scalar values`
	}
	return rune(n), ""
}

// codepointEscapes are the single-character escapes a codepoint literal
// accepts: the string escapes with the literal's own delimiter in place of
// the string's. Each literal escapes only its own quote, so `\"` is invalid
// inside `'…'` and `\'` is invalid inside `"…"`; neither quote needs escaping
// inside the other.
var codepointEscapes = map[byte]rune{
	'n':  '\n',
	't':  '\t',
	'\\': '\\',
	'\'': '\'',
}

// DecodeCodepoint decodes the body of a codepoint literal (the text between
// the single quotes, escapes undecoded) to its one ASCII codepoint. When the
// body is not exactly one ASCII codepoint it returns a complete diagnostic
// instead. The lexer calls it to validate and the parser to read the value, so
// the two cannot disagree about what a literal means.
func DecodeCodepoint(body string) (rune, string) {
	if body == "" {
		return 0, "empty codepoint literal ''; a codepoint literal holds exactly one ASCII character"
	}
	var decoded []rune
	for i := 0; i < len(body); {
		if body[i] != '\\' {
			r, size := utf8.DecodeRuneInString(body[i:])
			if r < 0x20 || r == 0x7F {
				return 0, fmt.Sprintf("codepoint literal holds control character U+%04X written directly; write it as the escape '%s'", r, encodeCodepointRune(r))
			}
			decoded = append(decoded, r)
			i += size
			continue
		}
		if i+1 >= len(body) {
			return 0, "unterminated codepoint literal; close it with '"
		}
		esc := body[i+1]
		i += 2
		if esc == 'u' {
			if i >= len(body) || body[i] != '{' {
				return 0, `\u escape requires braces, e.g. \u{41}`
			}
			end := strings.IndexByte(body[i:], '}')
			if end < 0 {
				return 0, `unterminated \u{...} escape`
			}
			r, problem := DecodeUnicodeEscape(body[i+1 : i+end])
			if problem != "" {
				return 0, problem
			}
			i += end + 1
			decoded = append(decoded, r)
			continue
		}
		if r, ok := codepointEscapes[esc]; ok {
			decoded = append(decoded, r)
			continue
		}
		if esc == '"' {
			return 0, `invalid escape \" in codepoint literal; write '"' without a backslash`
		}
		r, _ := utf8.DecodeRuneInString(body[i-1:])
		return 0, fmt.Sprintf(`invalid escape \%c in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`, r)
	}
	for _, r := range decoded {
		if r > 0x7F {
			return 0, fmt.Sprintf(`codepoint literal '%s' is U+%04X, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "%s" as a string, or use Codepoint.from_int for another scalar value`, body, r, EncodeStringBody(string(decoded)))
		}
	}
	if len(decoded) > 1 {
		return 0, fmt.Sprintf(`codepoint literal '%s' holds more than one character; write "%s" as a string`, body, EncodeStringBody(string(decoded)))
	}
	return decoded[0], ""
}

// encodeCodepointRune is the escape a codepoint literal spells r with when r
// cannot be written directly.
func encodeCodepointRune(r rune) string {
	for b, e := range codepointEscapes {
		if e == r {
			return `\` + string(b)
		}
	}
	return fmt.Sprintf(`\u{%X}`, r)
}
