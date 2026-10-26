package parser

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/token"
)

// tokenShown names a token in an error as the author wrote it: its text in
// backticks, or a phrase for a token with no text of its own (a line break,
// the end of the file, a comment, a string). An error never shows the
// lexer's name for a token.
func tokenShown(tok token.Token) string {
	switch tok.Type {
	case token.EOF:
		return "the end of the file"
	case token.NEWLINE:
		return "a line break"
	case token.BLANK_LINE:
		return "a blank line"
	case token.COMMENT, token.DOC_COMMENT, token.TEST_PROMPT:
		return "a comment"
	case token.STRING, token.STRING_LITERAL, token.STRING_START, token.STRING_PART, token.STRING_END,
		token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START, token.TRIPLE_STRING_PART, token.TRIPLE_STRING_END,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL,
		token.TAGGED_STRING_LITERAL, token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.TAGGED_TRIPLE_STRING_START, token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
		return "a string"
	case token.CODEPOINT_LITERAL:
		return "`'" + tok.Lexeme + "'`"
	}
	if tok.Lexeme == "" {
		return strings.ToLower(tok.Type.String())
	}
	return "`" + tok.Lexeme + "`"
}

// binaryOnlyOperator reports whether tt is an operator that only joins two
// operands, so it cannot begin an expression. `-` is also a prefix, `..`
// and `..=` have their own error, and `|>` may start a line.
func binaryOnlyOperator(tt token.TokenType) bool {
	switch tt {
	case token.EQEQ, token.BANGEQ, token.LT, token.GT, token.LTEQ, token.GTEQ,
		token.AND, token.OR, token.PLUS, token.STAR, token.SLASH, token.PERCENT:
		return true
	}
	return false
}

// endsOperand reports whether tt can be the last token of an operand, so a
// binary operator written after it would continue the expression.
func endsOperand(tt token.TokenType) bool {
	switch tt {
	case token.INT, token.FLOAT, token.DECIMAL, token.CODEPOINT_LITERAL,
		token.STRING, token.STRING_LITERAL, token.STRING_END,
		token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_END,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL,
		token.IDENT, token.TYPE_IDENT, token.PLACEHOLDER, token.IT, token.SELF,
		token.RPAREN, token.RBRACKET, token.RBRACE:
		return true
	}
	return false
}

// unexpectedToken is the error for tok, the token at the parser's position,
// where an expression should start. A binary operator that starts a line
// below an operand is one the line break cut off from its left operand:
// the error says so and how to continue the expression. Any other token is
// unexpected, shown as written.
func (p *Parser) unexpectedToken(tok token.Token) error {
	if binaryOnlyOperator(tok.Type) {
		if prev, ok := p.previousSignificant(p.pos); ok && prev.Line < tok.Line && endsOperand(prev.Type) {
			return ParseError{
				Line:    tok.Line,
				Col:     tok.Col,
				Message: fmt.Sprintf("`%s` cannot start a line: the line break ended the expression before it", tok.Lexeme),
				Hints:   []string{fmt.Sprintf("put `%s` at the end of the previous line, so the expression continues onto this one", tok.Lexeme)},
			}
		}
		if tok.Type == token.PERCENT {
			return errorAt(tok.Line, tok.Col, "unexpected `%%`: a map or set literal writes `%%` right before its `{` or `[`, as in `%%{k => v}` or `%%[a, b]`")
		}
		return errorAt(tok.Line, tok.Col, "`%s` needs an operand before it", tok.Lexeme)
	}
	return errorAt(tok.Line, tok.Col, "unexpected %s", tokenShown(tok))
}

// previousSignificant is the last token before index i that is not a line
// break, a blank line or a comment.
func (p *Parser) previousSignificant(i int) (token.Token, bool) {
	for j := min(i, len(p.tokens)) - 1; j >= 0; j-- {
		switch p.tokens[j].Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT, token.TEST_PROMPT:
			continue
		}
		return p.tokens[j], true
	}
	return token.Token{}, false
}
