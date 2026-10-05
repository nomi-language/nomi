package termcolor

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"io"
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// Whether colour is on, and how a span is wrapped in it, are rt's — see
// rt/color.go. The test reporter lives in rt and its output is
// a byte-for-byte contract with `nomi test`, so a second enablement rule here
// would make that contract depend on which binary you ran.
//
// What stays here is Nomi syntax highlighting, below, which runs the lexer and
// therefore cannot live in rt. It is installed into rt's hook by init so the
// test reports keep it.

// The token colours highlighting uses. Not in rt: nothing there knows what a
// keyword is.
const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

func EnabledFor(w io.Writer) bool { return rt.ColorEnabledFor(w) }

func ColorFor(w io.Writer, code, s string) string { return rt.ColorFor(w, code, s) }

func Green(s string) string { return rt.Green(s) }

// The shared test reporter lives in rt and renders Nomi source lines inside an
// assertion failure. It cannot tokenize, so it asks whoever can. Linking this
// package is what makes a report highlighted; a binary that links only rt
// prints the same lines plain.
func init() { rt.Highlight = NomiFor }

func NomiFor(w io.Writer, src string) string {
	if !EnabledFor(w) || src == "" {
		return src
	}
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		lines[i] = NomiLineFor(w, line)
	}
	return strings.Join(lines, "\n")
}

func NomiLineFor(w io.Writer, line string) string {
	tokens := lexer.Lex(line)
	var b strings.Builder
	pos := 0
	for _, tok := range tokens {
		if tok.Type == token.EOF || tok.Type == token.NEWLINE {
			continue
		}
		start := tok.Col - 1
		if start < pos || start >= len(line) {
			continue
		}
		if start > pos {
			b.WriteString(line[pos:start])
		}
		end := tokenSourceEnd(line, start, tok)
		if end <= start {
			end = start + len(tok.Lexeme)
		}
		if end > len(line) {
			end = len(line)
		}
		b.WriteString(ColorFor(w, nomiTokenColor(tok), line[start:end]))
		pos = end
	}
	if pos < len(line) {
		b.WriteString(line[pos:])
	}
	return b.String()
}

func tokenSourceEnd(line string, start int, tok token.Token) int {
	if start < 0 || start >= len(line) {
		return start
	}
	if tok.Type == token.CODEPOINT_LITERAL {
		// The Lexeme is the body; the source adds the two quotes.
		return start + len(tok.Lexeme) + 2
	}
	if tok.Lexeme != "" && strings.HasPrefix(line[start:], tok.Lexeme) {
		return start + len(tok.Lexeme)
	}
	if isStringToken(tok.Type) {
		return stringTokenSourceEnd(line, start)
	}
	return start + len(tok.Lexeme)
}

func isStringToken(tt token.TokenType) bool {
	switch tt {
	case token.STRING, token.STRING_LITERAL, token.STRING_START, token.STRING_PART,
		token.STRING_END, token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START,
		token.TRIPLE_STRING_PART, token.TRIPLE_STRING_END, token.RAW_STRING_LITERAL,
		token.RAW_TRIPLE_STRING_LITERAL, token.TAGGED_STRING_LITERAL,
		token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.TAGGED_TRIPLE_STRING_START, token.RAW_TAGGED_STRING_LITERAL,
		token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
		return true
	default:
		return false
	}
}

func stringTokenSourceEnd(line string, start int) int {
	i := start
	if i < len(line) && line[i] == '~' {
		i++
	}
	for i < len(line) && (line[i] == '_' || line[i] >= '0' && line[i] <= '9' ||
		line[i] >= 'A' && line[i] <= 'Z' || line[i] >= 'a' && line[i] <= 'z') {
		i++
	}
	if i >= len(line) || line[i] != '"' {
		return start
	}
	if strings.HasPrefix(line[i:], `"""`) {
		if end := strings.Index(line[i+3:], `"""`); end >= 0 {
			return i + 3 + end + 3
		}
		return len(line)
	}
	i++
	for i < len(line) {
		if line[i] == '\\' && i+1 < len(line) {
			i += 2
			continue
		}
		if line[i] == '"' {
			return i + 1
		}
		i++
	}
	return len(line)
}

func nomiTokenColor(tok token.Token) string {
	switch tok.Type {
	case token.AND, token.OR, token.IF, token.ELSE, token.FN, token.RETURN,
		token.CASE, token.WHEN, token.TYPE, token.STRUCT, token.ENUM,
		token.TYPEALIAS, token.BREAK, token.CONTINUE, token.IT,
		token.INTERFACE, token.IMPL, token.AS, token.FOR, token.IMPORT,
		token.EXTERN, token.EXPORT, token.PUB, token.ONCE,
		token.OPAQUE, token.WHERE, token.WITH, token.DEFER,
		token.TEST, token.TESTS, token.ASSERT, token.REFUTE, token.DBG, token.TODO,
		token.SELF, token.TRY, token.THEN:
		return ansiMagenta
	case token.TYPE_IDENT:
		return ansiCyan
	case token.STRING, token.STRING_LITERAL, token.STRING_START, token.STRING_PART,
		token.STRING_END, token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START,
		token.TRIPLE_STRING_PART, token.TRIPLE_STRING_END, token.RAW_STRING_LITERAL,
		token.RAW_TRIPLE_STRING_LITERAL, token.TAGGED_STRING_LITERAL,
		token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.TAGGED_TRIPLE_STRING_START, token.RAW_TAGGED_STRING_LITERAL,
		token.RAW_TAGGED_TRIPLE_STRING_LITERAL, token.CODEPOINT_LITERAL:
		return ansiGreen
	case token.INT, token.FLOAT, token.DECIMAL:
		return ansiYellow
	case token.COMMENT, token.DOC_COMMENT:
		return ansiDim
	case token.IDENT:
		return ansiBlue
	default:
		return ""
	}
}

func StripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
