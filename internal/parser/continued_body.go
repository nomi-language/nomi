package parser

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A case arm's body and a lambda's body are one expression unless braced.
// Expanding one onto several lines without the braces,
//
//	Ok(n) ->
//	    io.print("got ${n}")
//	    n
//
// leaves lines below the body that the parser would otherwise read as the
// next arm, or as junk in an argument list. Indentation never ends a body,
// so those lines are an error, reported at the first of them with the
// braced form as its hint. The parse then goes on as if the braces were
// there, so what follows reports its own errors and nothing spurious.

// continuedArm checks the lines after a case arm's unbraced body, which
// ends at the cursor. A line indented deeper than the arm's own line that
// does not start an arm continues the body: continuedArm records the error
// and returns the body as a block holding it and every such line. Otherwise
// it returns body. headStart is the index of the arm's first token and
// arrow that of its `->`.
func (p *Parser) continuedArm(hasValue bool, keyword string, headStart, arrow int, body ast.Node) (ast.Node, error) {
	indent := p.lineIndentCol(headStart)
	next, ok := p.continuationAt(indent)
	if !ok || p.startsCaseArm(next, hasValue) {
		return body, nil
	}
	first := p.tokens[next]
	stmts, err := p.parseContinuation(indent, func() bool { return p.startsCaseArm(p.pos, hasValue) })
	if err != nil {
		return nil, err
	}
	endLine, endCol := tokenEnd(p.tokens[p.pos-1])
	afterLine, afterCol := tokenEnd(p.tokens[arrow])
	p.softError(ParseError{
		Line:    first.Line,
		Col:     first.Col,
		Message: fmt.Sprintf("this `%s` arm continues on the next line; an arm with more than one statement needs braces", keyword),
		Hints:   []string{bracedHint("put the arm's body in braces", p.tokensText(headStart, arrow+1, "pattern ->"))},
		Fix: &Fix{Wrap: &BodyWrap{
			After: Pos{afterLine, afterCol},
			End:   Pos{endLine, endCol},
		}},
	})
	bodyStart := arrow + 1
	for p.tokens[bodyStart].Type == token.NEWLINE || p.tokens[bodyStart].Type == token.COMMENT || p.tokens[bodyStart].Type == token.BLANK_LINE {
		bodyStart++
	}
	return &ast.Block{
		Stmts:   append([]ast.Node{asStatement(body)}, stmts...),
		Line:    p.tokens[bodyStart].Line,
		Col:     p.tokens[bodyStart].Col,
		EndLine: endLine,
		EndCol:  endCol,
	}, nil
}

// continuedLambdaArg checks an argument list that has no `,` or `)` where
// the cursor is. When the argument before it is a lambda whose unbraced
// body ended its line, and the cursor is on a line indented deeper than the
// lambda's own line, those lines continue the body: continuedLambdaArg
// records the error, adds them to the lambda's body, and reports true.
func (p *Parser) continuedLambdaArg(arg ast.Node) (bool, error) {
	if named, ok := arg.(*ast.NamedArg); ok {
		arg = named.Value
	}
	lam, ok := arg.(*ast.Lambda)
	if !ok {
		return false, nil
	}
	bars, ok := p.lambdaBars[lam]
	if !ok || p.atEnd() {
		return false, nil
	}
	indent := p.lineIndentCol(bars[0])
	next, ok := p.continuationAt(indent)
	if !ok {
		return false, nil
	}
	first := p.tokens[next]
	stmts, err := p.parseContinuation(indent, nil)
	if err != nil {
		return false, err
	}
	endLine, endCol := tokenEnd(p.tokens[p.pos-1])
	afterLine, afterCol := tokenEnd(p.tokens[bars[1]])
	lam.Body.Stmts = append(lam.Body.Stmts, stmts...)
	lam.EndLine, lam.EndCol = endLine, endCol
	p.softError(ParseError{
		Line:    first.Line,
		Col:     first.Col,
		Message: "this lambda's body continues on the next line; a lambda body with more than one statement needs braces",
		Hints:   []string{bracedHint("put the lambda's body in braces", p.tokensText(bars[0], bars[1]+1, "|x|"))},
		Fix: &Fix{Wrap: &BodyWrap{
			After: Pos{afterLine, afterCol},
			End:   Pos{endLine, endCol},
		}},
	})
	return true, nil
}

// bracedHint is a hint showing head followed by a braced body.
func bracedHint(what, head string) string {
	return what + ":\n" + head + " {\n    ...\n}"
}

// continuationAt reports whether the cursor is at the end of a line and
// the next line holding code is indented deeper than indent, and returns
// the index of that line's first token. Comments are passed over.
func (p *Parser) continuationAt(indent int) (int, bool) {
	i := p.pos
	crossed := false
	for i < len(p.tokens) {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE:
			crossed = true
			i++
			continue
		case token.COMMENT:
			i++
			continue
		}
		break
	}
	if !crossed || i >= len(p.tokens) {
		return 0, false
	}
	tok := p.tokens[i]
	if tok.Col <= indent || closesBody(tok.Type) {
		return 0, false
	}
	return i, true
}

// closesBody reports whether a token of type tt, starting a line, cannot
// continue a body: it closes what holds the body, or is not code.
func closesBody(tt token.TokenType) bool {
	switch tt {
	case token.EOF, token.RBRACE, token.RPAREN, token.RBRACKET, token.COMMA,
		token.TEST_PROMPT, token.DOC_COMMENT:
		return true
	}
	return false
}

// parseContinuation parses statements, as a block's, for as long as each
// starts deeper than indent and stop, when given, is false at it. It leaves
// the cursor after the last one.
func (p *Parser) parseContinuation(indent int, stop func() bool) ([]ast.Node, error) {
	var stmts []ast.Node
	for {
		save := p.pos
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Col <= indent || closesBody(p.peek().Type) || (stop != nil && stop()) {
			p.pos = save
			return stmts, nil
		}
		if p.strayBodyMarker() {
			continue
		}
		node, doc, attachedLeading, err := p.parseBlockStmt()
		if err != nil {
			return nil, err
		}
		trailing := p.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), append(leading, attachedLeading...), trailing)
		attachDoc(node, doc)
		stmts = append(stmts, node)
	}
}

// asStatement is body as a block's statement.
func asStatement(body ast.Node) ast.Node {
	switch body.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return body
	}
	return &ast.ExprStmt{Expr: body, Line: body.LineNum()}
}

// lineIndentCol is the column of the first token on the line of token i.
func (p *Parser) lineIndentCol(i int) int {
	line := p.tokens[i].Line
	col := p.tokens[i].Col
	for j := i - 1; j >= 0; j-- {
		t := p.tokens[j]
		if t.Line != line {
			break
		}
		if t.Type != token.NEWLINE && t.Type != token.SEMICOLON {
			col = t.Col
		}
	}
	return col
}

// tokensText is the source of tokens[from:to], which sit on one line, as
// written. When a token's text cannot be recovered from its lexeme, it is
// fallback.
func (p *Parser) tokensText(from, to int, fallback string) string {
	var b strings.Builder
	endCol := 0
	for i := from; i < to && i < len(p.tokens); i++ {
		t := p.tokens[i]
		if t.Line != p.tokens[from].Line || t.EndLine > t.Line {
			return fallback
		}
		text := t.Lexeme
		if t.EndCol > 0 && t.EndCol-t.Col != len(text) {
			switch {
			case t.Type == token.STRING_LITERAL && t.EndCol-t.Col == len(text)+2:
				text = `"` + text + `"`
			default:
				return fallback
			}
		}
		if endCol > 0 {
			b.WriteString(strings.Repeat(" ", max(t.Col-endCol, 0)))
		}
		b.WriteString(text)
		endCol = t.Col + len(text)
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

// strayBodyMarker checks the cursor, at the start of a statement in a body
// or of a case arm, for a `///` doc comment, or a `//!` attached test that
// no declaration follows. Neither can go there: a doc comment documents the
// declaration after it, and an attached test tests one. The likely intent
// is a comment, such as a line commented out, so strayBodyMarker records
// the error with that fix, steps over the run of such lines as if they
// were comments, and reports true.
func (p *Parser) strayBodyMarker() bool {
	if p.atEnd() {
		return false
	}
	tok := p.peek()
	if tok.Type != token.DOC_COMMENT && tok.Type != token.TEST_PROMPT {
		return false
	}
	marks, after := p.markerRun(tok.Type)
	var message string
	if tok.Type == token.DOC_COMMENT {
		message = "a `///` doc comment documents the declaration after it, so it can't go inside a body; use `//` for a comment here"
	} else {
		if p.startsDeclarationAt(after) {
			return false
		}
		message = "a `//!` attached test goes above the declaration it tests, so it can't go before a statement in a body; use `//` for a comment here"
	}
	p.softError(ParseError{Line: tok.Line, Col: tok.Col, Message: message, Fix: &Fix{Markers: marks}})
	p.pos = after
	return true
}

// markerRun reads the run of lines starting with a tt marker (`///` or
// `//!`) at the cursor, without moving it: each marker's position, and the
// index of the first token after the run. A blank line ends the run.
func (p *Parser) markerRun(tt token.TokenType) ([]Pos, int) {
	var marks []Pos
	last := 0 // the last line the run covers
	i := p.pos
	for ; i < len(p.tokens); i++ {
		t := p.tokens[i]
		switch {
		case t.Type == tt && (last == 0 || t.Line == last+1):
			marks = append(marks, Pos{t.Line, t.Col})
			last = tokenEndLine(t)
		case t.Type == token.NEWLINE || t.Type == token.SEMICOLON:
		case t.Line <= last && t.Type != token.EOF:
			// A `//!` line's code.
			last = max(last, tokenEndLine(t))
		default:
			return marks, i
		}
	}
	return marks, i
}

// startsDeclarationAt reports whether the first token at or after i that is
// not layout or a comment begins a declaration an attached test can sit
// above.
func (p *Parser) startsDeclarationAt(i int) bool {
	for i < len(p.tokens) {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT:
			i++
			continue
		}
		break
	}
	if i >= len(p.tokens) {
		return false
	}
	t := p.tokens[i]
	switch t.Type {
	case token.TEST_PROMPT, token.DOC_COMMENT, token.AT, token.PUB, token.EXTERN,
		token.FN, token.ONCE, token.TYPE, token.STRUCT, token.ENUM, token.TYPEALIAS,
		token.INTERFACE, token.IMPL, token.OPAQUE, token.TEST, token.TESTS:
		return true
	}
	if isHostToken(t) && i+1 < len(p.tokens) {
		next := p.tokens[i+1].Type
		return next == token.FN || next == token.TYPE
	}
	return false
}
