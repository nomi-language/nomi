// Package strlit is Nomi string-literal escape handling for the lexer, the
// parser and the formatter. The escape table and the encoder are rt's
// (rt/stringliteral.go), which every place that shows a String as code calls,
// so the lexer's decoding and every encoder read one table.
package strlit

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nomi-language/nomi/rt"
)

// DecodeSimple returns the rune a single-character escape decodes to (the
// argument is the character following the backslash) and whether it is a
// recognized escape. The lexer uses this and rejects the input when ok is
// false and the escape is not the \u{...} form.
func DecodeSimple(after byte) (rune, bool) {
	return rt.DecodeStringEscape(after)
}

// EncodeStringBody re-encodes a decoded string into the body of a single-line
// Nomi string literal (no surrounding quotes and no `${` rule):
// rt.StringLiteralBody.
func EncodeStringBody(s string) string {
	return rt.StringLiteralBody(s)
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
