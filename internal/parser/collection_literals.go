package parser

import (
	"github.com/nomi-language/nomi/internal/token"
)

// Collection literals and patterns carry no symbol (spec §3 *Literal
// forms*). A `[...]` is a List, a Vector or a Set, which the checker decides
// from the name in front of it or the type its position expects; a `{...}`
// whose first entry is `key =>` is a map, one whose first entry is `name:`
// a struct, and `{}` the empty map. The tokens decide only between the
// struct and the map, here.

// braceOpensMap reports whether the `{` at offset from the cursor opens a
// map literal or pattern rather than a struct: its first entry that is no
// spread is `key => value`, it is empty, or it holds two spreads and
// nothing else, which no struct update takes. A lone spread, `{..base}`, is
// a struct update.
func (p *Parser) braceOpensMap(offset int) bool {
	i := p.pos + offset
	if i >= len(p.tokens) || p.tokens[i].Type != token.LBRACE {
		return false
	}
	spreads, entries, depth := 0, 0, 0
	start := true
	for i++; i < len(p.tokens); i++ {
		t := p.tokens[i].Type
		switch t {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT:
			continue
		case token.EOF:
			return false
		}
		if depth == 0 {
			switch t {
			case token.RBRACE:
				return entries == 0 && spreads != 1
			case token.FAT_ARROW:
				return true
			case token.COLON:
				return false
			case token.COMMA:
				start = true
				continue
			}
			if start {
				start = false
				if t == token.DOTDOT {
					spreads++
				} else {
					entries++
				}
			}
		}
		switch t {
		case token.LBRACE, token.LBRACKET, token.LPAREN:
			depth++
		case token.RBRACE, token.RBRACKET, token.RPAREN:
			depth--
		}
	}
	return false
}

// sigilError is the error at a `#` or `%` (the cursor) written where a
// literal or, with pattern set, a pattern starts. Collection literals and
// patterns have no symbol; the fix drops it, or for an unnamed vector or
// set literal writes the collection's name in its place.
func (p *Parser) sigilError(pattern bool) error {
	tok := p.peek()
	next := p.peekAt(1)
	if tok.Type == token.HASH && next.Type == token.BANG && adjacent(tok, next) {
		return errorAt(tok.Line, tok.Col, "a `#!` line is allowed only as the first line of a file, starting at its first byte")
	}
	if tok.Type == token.HASH && next.Type == token.LPAREN && adjacent(tok, next) {
		return hashTupleError(tok)
	}
	if tok.Type == token.PERCENT && !p.percentOpensLiteral(0) || tok.Type == token.HASH && !adjacent(tok, next) {
		if pattern {
			return errorAt(tok.Line, tok.Col, "unexpected %s in pattern", tokenShown(tok))
		}
		return p.unexpectedToken(tok)
	}
	sigil := tok.Lexeme
	drop := Replace("Drop the `"+sigil+"`", tok.Line, tok.Col, tok.Line, tok.Col+1, "")
	var msg string
	switch {
	case next.Type == token.LBRACE && sigil == "%":
		msg = "a map is written without `%`: `{key => value}`"
		if pattern {
			msg = "a map pattern is written without `%`: `{key => pattern}`"
		}
	case next.Type == token.LBRACE:
		msg = "a struct is written without `#`: `{name: value}`"
	case next.Type == token.LBRACKET && pattern:
		msg = "a pattern is written without `" + sigil + "`: `[...]` takes apart the list, vector or set it matches"
	case next.Type == token.LBRACKET:
		name, shape := "Vector", "vector"
		if sigil == "%" {
			name, shape = "Set", "set"
		}
		return ParseError{Line: tok.Line, Col: tok.Col,
			Message: "a " + shape + " is written without `" + sigil + "`: `[1, 2]` where a " + name + " is expected, else `" + name + "[1, 2]`",
			Fix:     Replace("Write `"+name+"`", tok.Line, tok.Col, tok.Line, tok.Col+1, name)}
	default:
		msg = "a literal or pattern naming its type is written without `" + sigil + "`: `Name[...]`, `Name{...}`"
	}
	return ParseError{Line: tok.Line, Col: tok.Col, Message: msg, Fix: drop}
}
