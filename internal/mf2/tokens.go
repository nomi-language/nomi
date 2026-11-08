package mf2

import "sort"

// TokenKind says what a Token of a message's text is.
type TokenKind int

const (
	// TokenKeyword is `.input`, `.local` or `.match`.
	TokenKeyword TokenKind = iota
	// TokenVariable is `$name`, its `$` included.
	TokenVariable
	// TokenFunction is a function's name with the colon before it and its
	// namespace, if any: `:integer`, `:nomi:noun`.
	TokenFunction
	// TokenOption is an option's name: the `count` of `count=$n`.
	TokenOption
	// TokenAttribute is `@name`.
	TokenAttribute
	// TokenMarkup is a markup's `#name` or `/name`.
	TokenMarkup
	// TokenName is an unquoted literal that is a name: an option's value
	// such as `genitive`, or an operand.
	TokenName
	// TokenNumber is a number literal.
	TokenNumber
	// TokenQuoted is a `|quoted|` literal, its bars included.
	TokenQuoted
	// TokenKey is a variant key other than `*`: `one`, `few`, `0`.
	TokenKey
	// TokenCatchAll is the variant key `*`.
	TokenCatchAll
	// TokenQuote is a quoted pattern's `{{` or `}}`.
	TokenQuote
	// TokenBrace is a placeholder's or markup's `{` or `}`.
	TokenBrace
	// TokenOperator is the `=` of an option, an attribute or a `.local`,
	// and the `/` that ends a standalone markup.
	TokenOperator
)

// Token is one piece of a message's syntax at Span of its text. The text a
// pattern writes, escapes included, is no token.
type Token struct {
	Kind TokenKind
	Span Span
	// Name is a TokenVariable's name, without its `$`.
	Name string
}

// Tokens are the syntax tokens of text, in order, for an editor to color.
// A text with a syntax error has the tokens the parse read before it.
func Tokens(text string) []Token {
	p := &parser{src: text, record: true}
	p.message()
	sort.SliceStable(p.toks, func(i, j int) bool { return p.toks[i].Span.Start < p.toks[j].Span.Start })
	return p.toks
}

// emit records a token when the parse is reading tokens.
func (p *parser) emit(kind TokenKind, start, end int) {
	if p.record && end > start {
		p.toks = append(p.toks, Token{Kind: kind, Span: Span{start, end}})
	}
}

// emitLiteral records lit, an operand or option value written as a
// literal.
func (p *parser) emitLiteral(lit Operand) {
	if !p.record || lit.Span.End <= lit.Span.Start {
		return
	}
	switch {
	case p.src[lit.Span.Start] == '|':
		p.emit(TokenQuoted, lit.Span.Start, lit.Span.End)
	case IsNumber(p.src[lit.Span.Start:lit.Span.End]):
		p.emit(TokenNumber, lit.Span.Start, lit.Span.End)
	default:
		p.emit(TokenName, lit.Span.Start, lit.Span.End)
	}
}
