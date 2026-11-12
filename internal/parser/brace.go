package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// braceOpensStruct reports whether a `{` stands at offset from the cursor.
// A `{` always opens data: an anonymous struct literal, type or pattern.
// Bodies are written as a header line and `end`, never in braces.
func (p *Parser) braceOpensStruct(offset int) bool {
	i := p.pos + offset
	return i < len(p.tokens) && p.tokens[i].Type == token.LBRACE
}

// punColonEnds reports whether the `:` just consumed after a field or
// argument name ends the item, `name:`, which is `name: name`: the next
// token past line breaks and comments is a `,` or close.
func (p *Parser) punColonEnds(close token.TokenType) bool {
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.COMMENT, token.BLANK_LINE:
			continue
		case token.COMMA:
			return true
		default:
			return p.tokens[i].Type == close
		}
	}
	return false
}

// punColonEndsAt is punColonEnds for the `:` at offset from the cursor,
// not yet consumed.
func (p *Parser) punColonEndsAt(offset int, close token.TokenType) bool {
	saved := p.pos
	p.pos += offset + 1
	defer func() { p.pos = saved }()
	return p.punColonEnds(close)
}

// canStartPattern reports whether the token at the cursor can begin a
// pattern: one canStartPatternAssertion names, or a `{` that opens a struct
// pattern, `{x:, y:}`.
func (p *Parser) canStartPattern() bool {
	if p.atEnd() {
		return false
	}
	t := p.peek().Type
	return canStartPatternAssertion(t) || t == token.LBRACE && p.braceOpensStruct(0)
}

// dotBraceError is the error at a `.{`, the `.` at dot: an anonymous
// struct, its type and its pattern open with the brace alone. The fix
// drops the dot.
func dotBraceError(dot token.Token) error {
	return ParseError{Line: dot.Line, Col: dot.Col,
		Message: "an anonymous struct is written with its brace alone, `{x: 1}`, with no `.`",
		Fix:     Replace("Drop the `.`", dot.Line, dot.Col, dot.Line, dot.Col+1, "")}
}

// barePunError is the error at a field written as its name alone, `P{a}`:
// a punned field is `a:`, which is `a: a`. head is the literal's or
// pattern's name, empty for an anonymous one. The fix writes the `:`.
func barePunError(field token.Token, head string) ParseError {
	name := field.Lexeme
	return ParseError{Line: field.Line, Col: field.Col,
		Message: "a field is written `" + name + ": value`, or `" + name + ":` for `" + name + ": " + name + "`",
		Hints:   []string{"write `" + head + "{" + name + ":}`"},
		Fix:     Replace("Write `"+name+":`", field.Line, field.Col+len(name), field.Line, field.Col+len(name), ":")}
}

// fieldColon reads what follows a field name the cursor is just past, in a
// struct literal or pattern. A `:` is left for the caller to consume, and
// pun is false. A field written as its name alone, when a separator or the
// close follows, is barePunError, recorded as a soft error: the parse goes
// on as though the `:` were written, pun is true, and every bare field of a
// file is reported in one pass. Anything else is a missing separator, as
// in `Point{x = 1}`, returned as err.
func (p *Parser) fieldColon(field token.Token, head string) (pun bool, err error) {
	if !p.atEnd() && p.peek().Type == token.COLON {
		return false, nil
	}
	if p.bareFieldEnds() {
		p.softError(barePunError(field, head))
		return true, nil
	}
	return false, missingFieldComma(p.peek(), field.Lexeme)
}

// bareFieldEnds reports whether the token at the cursor ends a field
// written as its name alone: a separator or the close.
func (p *Parser) bareFieldEnds() bool {
	if p.atEnd() {
		return true
	}
	switch p.peek().Type {
	case token.COMMA, token.RBRACE, token.NEWLINE, token.SEMICOLON, token.COMMENT, token.BLANK_LINE, token.EOF:
		return true
	}
	return false
}

// typeHead is how a struct literal's or pattern's name is written, empty
// for an anonymous one.
func typeHead(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	return t.TypeString()
}

// emptyAnonStructMessage is the error at an anonymous struct literal, type
// or pattern with no field, `{}`.
const emptyAnonStructMessage = "an anonymous struct has at least one field"
