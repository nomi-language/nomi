package parser

import (
	"errors"

	"github.com/nomi-language/nomi/internal/token"
)

// A file written in the brace-bodied syntax Nomi no longer reads (`fn f()
// -> Int {`, `if c {`, `case x {`, `struct P {`, `import {`) fails at
// whatever its first brace happens to confuse, an error that does not say
// why. When a file does not parse, a `{` that ends a header line, where a
// body would start, is its first error: a body is written on the lines
// after its header and closed by `end` (spec §2 *Bodies*). It is a
// diagnostic only; such a file is still refused.

// braceBodyMessage is the error at a `{` that ends a header line.
const braceBodyMessage = "a body is written on the lines after its header and closed by `end`, not in braces"

// headerStarts are the first tokens of a line that opens a body in the
// brace syntax: a declaration, a branch, an import block. A `}` starts
// `} else {`.
var headerStarts = map[token.TokenType]bool{
	token.FN: true, token.PUB: true, token.TEST: true, token.IF: true, token.ELSE: true,
	token.CASE: true, token.STRUCT: true, token.ENUM: true, token.INTERFACE: true,
	token.IMPL: true, token.IMPORT: true, token.BEGIN: true,
	token.DEFER: true, token.WHERE: true, token.WITH: true, token.RBRACE: true,
	token.EXPORT: true, token.OPAQUE: true,
}

// headerWords are the contextual words that start a body's header.
var headerWords = map[string]bool{"loop": true, "concurrent": true, "group": true}

// beforeBodyBrace are the tokens a header ends with before its old `{`:
// a name, a type's closing bracket, a test's name, or a word that takes
// no operand (`import {`, `else {`, `begin {`). A `{` after an operator,
// a `(` or a comma opens data.
var beforeBodyBrace = map[token.TokenType]bool{
	token.IDENT: true, token.TYPE_IDENT: true, token.RPAREN: true, token.RBRACKET: true,
	token.GT: true, token.STRING_LITERAL: true, token.STRING_END: true, token.QUESTION: true,
	token.IMPORT: true, token.ELSE: true, token.BEGIN: true, token.SELF: true,
}

// braceBodyError is the error at the first `{` in tokens that ends a header
// line, or nil when there is none.
func braceBodyError(tokens []token.Token) *ParseError {
	e, _ := braceBody(tokens)
	return e
}

// braceBody is braceBodyError's error and the column its header line starts
// at.
func braceBody(tokens []token.Token) (*ParseError, int) {
	lineStart := token.Token{}
	for i, t := range tokens {
		switch t.Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT, token.TEST_PROMPT, token.EOF:
			continue
		}
		if lineStart.Line != t.Line {
			lineStart = t
		}
		if t.Type != token.LBRACE || i == 0 || !headerStarts[lineStart.Type] && !(lineStart.Type == token.IDENT && headerWords[lineStart.Lexeme]) {
			continue
		}
		if typeHeader(tokens, i) {
			continue
		}
		prev := tokens[i-1]
		if !beforeBodyBrace[prev.Type] || adjacent(prev, t) || prev.Line != t.Line {
			continue
		}
		if !endsLine(tokens, i) {
			continue
		}
		return &ParseError{Line: t.Line, Col: t.Col, Message: braceBodyMessage,
			Hints: []string{"write the body on the lines below the header and close it with `end`"},
			Fix:   Replace("Remove the `{`", t.Line, fixStart(prev, t), t.Line, t.Col+1, "")}, lineStart.Col
	}
	return nil, 0
}

// typeHeader reports whether the line of the `{` at tokens[i] declares a
// `type`: a distinct or host type takes no body in either syntax, and the
// parser says so at the brace in words of its own.
func typeHeader(tokens []token.Token, i int) bool {
	for j := i - 1; j >= 0 && tokens[j].Line == tokens[i].Line; j-- {
		if tokens[j].Type == token.TYPE {
			return true
		}
	}
	return false
}

// endsLine reports whether nothing but a comment follows tokens[i] on its
// line.
func endsLine(tokens []token.Token, i int) bool {
	for _, n := range tokens[i+1:] {
		switch n.Type {
		case token.COMMENT:
			continue
		case token.NEWLINE, token.BLANK_LINE, token.EOF:
			return true
		}
		return n.Line > tokens[i].Line
	}
	return true
}

// fixStart is where the fix's removal starts: just after prev, so the
// space before the `{` goes with it, or the `{` itself after a string
// whose end the lexer did not record, since its lexeme need not be its
// source's width.
func fixStart(prev, brace token.Token) int {
	switch prev.Type {
	case token.STRING_LITERAL, token.STRING_END:
		if prev.EndCol == 0 || prev.EndLine > prev.Line {
			return brace.Col
		}
	}
	if _, col := tokenEndPos(prev); col < brace.Col {
		return col
	}
	return brace.Col
}

// withBraceBody puts the brace-body error in place of err when the parse
// failed. An error on an earlier line is the file's own, and one inside
// the header, past its first word, is the header's more specific refusal
// (`type Conn go {`, `strct P {`); both stand.
func withBraceBody(tokens []token.Token, err error) error {
	if err == nil {
		return nil
	}
	var pe ParseError
	if e, start := braceBody(tokens); e != nil && !(errors.As(err, &pe) && keeps(pe, *e, start)) {
		return *e
	}
	return err
}

// withBraceBodies puts the brace-body error first among errs, unless the
// first of them comes before the brace, dropping the others on its line.
func withBraceBodies(tokens []token.Token, errs []ParseError) []ParseError {
	if len(errs) == 0 {
		return errs
	}
	e, start := braceBody(tokens)
	if e == nil || keeps(errs[0], *e, start) {
		return errs
	}
	out := []ParseError{*e}
	for _, x := range errs {
		if x.Line != e.Line {
			out = append(out, x)
		}
	}
	return out
}

// keeps reports whether err stands before the brace-body error e, whose
// header line starts at column start: it is on an earlier line, or inside
// the header past its first word.
func keeps(err, e ParseError, start int) bool {
	return err.Line < e.Line || err.Line == e.Line && err.Col > start && err.Col < e.Col
}
