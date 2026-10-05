package format

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
	"strings"
)

// Layout parameters: 4-space indentation at 100 columns, rustfmt's defaults.
const (
	defaultWidth  = 100
	defaultIndent = 4
	// Legacy conformance-list rendering keeps an indented content budget.
	conformanceWrapWidth = defaultWidth - defaultIndent
	// singleLineIfElseMaxWidth caps the flat width of a plain if/else that may
	// render on one line. It is rustfmt's single_line_if_else_max_width, whose
	// default is 50 at rustfmt's own 100-column width. It limits how much a
	// reader scans on one line, so it does not grow with the page width.
	singleLineIfElseMaxWidth = 50
	// singleLineBindingElseMaxWidth is the widest `Pattern = value else { x }`
	// that renders on one line, the whole statement counted. It is rustfmt's
	// single_line_let_else_max_width, whose default is also 50.
	singleLineBindingElseMaxWidth = 50
)

// IndentWidth is the formatter's indentation, in spaces, for an editor edit
// that writes one indented line itself.
const IndentWidth = defaultIndent

// Format formats Nomi source code using the canonical formatter rules.
// On parse error, returns the original source unchanged along with the error.
func Format(src string) (string, error) {
	tokens := lexer.Lex(src)
	nodes, fileEndTrivia, err := parser.ParseFile(tokens)
	if err != nil {
		return src, err
	}
	nodes = normalizeImportLayout(nodes)
	normalizeBodies(nodes)
	return withShebang(src, tokens, emitFile(nodes, fileEndTrivia)), nil
}

// withShebang puts src's `#!` line, which the lexer skips, back in front of
// the formatted body, byte for byte apart from trailing whitespace. A blank
// line after it in src stays as one blank line; no blank line stays none.
func withShebang(src string, tokens []token.Token, body string) string {
	shebang := strings.TrimRight(lexer.Shebang(src), " \t")
	if shebang == "" {
		return body
	}
	if body == "" {
		return shebang + "\n"
	}
	// The first token after the shebang tells whether a blank line came
	// between: line 2 means none.
	for _, tok := range tokens {
		if tok.Type == token.NEWLINE || tok.Type == token.BLANK_LINE {
			continue
		}
		if tok.Type != token.EOF && tok.Line > 2 {
			return shebang + "\n\n" + body
		}
		break
	}
	return shebang + "\n" + body
}
