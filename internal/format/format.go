package format

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
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
	return emitFile(nodes, fileEndTrivia), nil
}
