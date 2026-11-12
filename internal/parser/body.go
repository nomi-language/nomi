package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A body is written below its header line and closed by `end` (spec §2
// *Bodies*): `fn f() -> Int`, `if c then`, `case x`, `struct P`, `begin`,
// a lambda's `fn x ->` and an arm's `pattern ->` each end their line, and
// the statements below them run to the closing word. Braces never delimit
// code; a `{` always opens data.
//
// A header ends at the end of its line. Brackets carry it across lines, a
// line that starts with `where` continues it, and the lexer joins a line
// that starts with `|>` or a binary operator to the one above (spec §2
// *Statement ends*).
//
// What closes a body depends on its header:
//
//   - `end` alone, for a function, a declaration's body, a lambda, `begin`,
//     a `case`'s arms and a binding's `else`;
//   - `else` or `end` for an `if`'s branch: an `else if c then` chain
//     shares the one `end` that closes the last branch;
//   - for a `case` arm whose `->` ends its line, the next line that starts
//     an arm (startsCaseArm), or the `case`'s `end`.

// bodyStop says which words, besides `end`, close a body.
type bodyStop int

const (
	stopAtEnd  bodyStop = iota // only `end`
	stopAtElse                 // `else` or `end`: an `if`'s branch
	stopAtArm                  // `end` or the start of the next arm
)

// lastReal is the last token read that is not a line break.
func (p *Parser) lastReal() token.Token {
	for i := p.pos - 1; i >= 0; i-- {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE, token.SEMICOLON:
			continue
		}
		return p.tokens[i]
	}
	return token.Token{Line: 1, Col: 1}
}

// atLineEnd reports whether nothing but a comment follows the last token
// read on its line: a header, a `then`, an `else` or an `->` ended there.
func (p *Parser) atLineEnd() bool {
	if p.atEnd() {
		return true
	}
	last := p.lastReal()
	line, _ := tokenEndPos(last)
	next := p.peek()
	switch next.Type {
	case token.NEWLINE, token.BLANK_LINE, token.SEMICOLON, token.COMMENT, token.EOF:
		return true
	}
	return next.Line > line
}

// atEndWord reports whether the cursor is at `end`.
func (p *Parser) atEndWord() bool {
	return !p.atEnd() && p.peek().Type == token.END
}

// atEmptyBody reports whether `end` follows on the header's line: the
// one-line empty body, `fn noop() end`, `struct Empty end`.
func (p *Parser) atEmptyBody() bool {
	if !p.atEndWord() {
		return false
	}
	line, _ := tokenEndPos(p.lastReal())
	return p.peek().Line == line
}

// headerEnds reports an error unless the header just read ends its line.
// what names the construct, as in "a function's body".
func (p *Parser) headerEnds(what string) error {
	if p.atLineEnd() {
		return nil
	}
	next := p.peek()
	return errorAt(next.Line, next.Col, "unexpected %s: %s starts on the line after its header and ends at `end`", tokenShown(next), what)
}

// parseBody parses the statements of a body whose header was just read, up
// to the word that closes it, which it leaves unread. open is the header's
// first token, for the error at a body that never closes; hasValue is the
// arms' kind for stopAtArm.
func (p *Parser) parseBody(open token.Token, what string, stop bodyStop, hasValue bool) (*ast.Block, error) {
	if err := p.headerEnds(what); err != nil {
		return nil, err
	}
	startLine, startCol := tokenEndPos(p.lastReal())
	var stmts []ast.Node
	var endTrivia []ast.Trivia
	for {
		before := p.pos
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.END ||
			(stop == stopAtElse && p.peek().Type == token.ELSE) ||
			(p.resilient && p.blockClosedByColumnOneDecl()) {
			endTrivia = leading
			break
		}
		if stop == stopAtArm && p.startsCaseArm(p.pos, hasValue) {
			// The comments above the next arm are that arm's.
			p.pos = before
			break
		}
		if p.strayBodyMarker() {
			continue
		}
		stmtStart := p.pos
		node, attachedDoc, attachedLeading, err := p.parseBlockStmt()
		if err != nil {
			if !p.resilient {
				return nil, err
			}
			stmts = append(stmts, p.recoverStmtInBlock(stmtStart, err))
			continue
		}
		trailing := p.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), append(leading, attachedLeading...), trailing)
		attachDoc(node, attachedDoc)
		stmts = append(stmts, node)
		if err := p.statementEnds(); err != nil {
			if !p.resilient {
				return nil, err
			}
			stmts = append(stmts, p.recoverStmtInBlock(p.pos, err))
		}
	}
	block := &ast.Block{Stmts: stmts, Line: startLine, Col: startCol}
	switch {
	case stop == stopAtArm:
		block.EndLine, block.EndCol = tokenEndPos(p.lastReal())
	case !p.atEnd() && (p.peek().Type == token.END || p.peek().Type == token.ELSE):
		block.EndLine, block.EndCol = p.peek().Line, p.peek().Col
	default:
		if !p.resilient {
			return nil, p.missingEnd(open, what)
		}
		block.EndLine, block.EndCol = p.resilientBlockEnd(open)
	}
	for _, t := range endTrivia {
		block.AddTrailing(t)
	}
	return block, nil
}

// missingEnd is the error at a body that reached the end of the file, or a
// closing word it does not take, without its `end`.
func (p *Parser) missingEnd(open token.Token, what string) error {
	return errorAt(open.Line, open.Col, "expected `end` to close %s", what)
}

// expectEnd consumes the `end` that closes a body. In resilient mode a
// missing one is a recovery (parseBody already counted it).
func (p *Parser) expectEnd(open token.Token, what string) (token.Token, error) {
	if p.atEndWord() {
		end := p.peek()
		p.advance()
		return end, nil
	}
	if p.resilient {
		return p.lastReal(), nil
	}
	return token.Token{}, p.missingEnd(open, what)
}

// parseEndBody parses a body closed by `end`, the `end` included: the
// statements below the header, or nothing when `end` follows on the
// header's line.
func (p *Parser) parseEndBody(open token.Token, what string) (*ast.Block, error) {
	if p.atEmptyBody() {
		line, col := tokenEndPos(p.lastReal())
		end := p.peek()
		p.advance()
		return &ast.Block{Line: line, Col: col, EndLine: end.Line, EndCol: end.Col}, nil
	}
	block, err := p.parseBody(open, what, stopAtEnd, false)
	if err != nil {
		return nil, err
	}
	if _, err := p.expectEnd(open, what); err != nil {
		return nil, err
	}
	return block, nil
}

// parseInlineBranch parses the one-line body of an inline `if` branch, an
// arm or a binding's `else`: `break`, `continue` or `return`, or an
// expression.
func (p *Parser) parseInlineBranch() (ast.Node, error) {
	switch p.peek().Type {
	case token.BREAK:
		return p.parseBreak()
	case token.CONTINUE:
		return p.parseContinue()
	case token.RETURN:
		return p.parseReturn()
	}
	return p.parseExpr(1)
}

// inlineBlock wraps an inline branch in the block that scopes it, at the
// word (`then`, `else`) that opens it.
func inlineBlock(at token.Token, node ast.Node) *ast.Block {
	switch node.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
	default:
		node = &ast.ExprStmt{Expr: node, Line: node.LineNum()}
	}
	return &ast.Block{Stmts: []ast.Node{node}, Line: at.Line, Col: at.Col}
}

// parseBegin parses a block expression, `begin` and its statements to
// `end`.
func (p *Parser) parseBegin() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume BEGIN
	block, err := p.parseEndBody(tok, "a `begin` block")
	if err != nil {
		return nil, err
	}
	block.Line, block.Col = tok.Line, tok.Col
	return block, nil
}

// singleBody is an arm's body below its `->`: the statement itself when it
// is one expression or a jump, so `p ->` above a long expression is the arm
// `p -> expr`, and the block otherwise.
func singleBody(b *ast.Block) ast.Node {
	if len(b.Stmts) != 1 || len(b.Trailing) > 0 {
		return b
	}
	switch n := b.Stmts[0].(type) {
	case *ast.ExprStmt:
		if _, ok := n.Expr.(*ast.Block); ok {
			// A `begin` block below the `->` is the body's one statement;
			// unwrapped, it would read as the body itself.
			return b
		}
		for _, t := range append(append([]ast.Trivia(nil), n.Leading...), n.Trailing...) {
			if t.Kind == ast.TriviaComment {
				// The comments are the statement's; the expression alone
				// would lose them.
				return b
			}
		}
		return n.Expr
	case *ast.Binding, *ast.TupleDestructure, *ast.StructDestructure, *ast.PatternDestructure,
		*ast.PatternBinding, *ast.DistinctDestructure, *ast.With, *ast.Defer, *ast.Assertion,
		*ast.FuncDef, *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias,
		*ast.InterfaceDef, *ast.ImplBlock, *ast.TestDecl, *ast.Subtests, *ast.ErrorNode:
		return b
	default:
		return n
	}
}

// interfaceBodyFollows reports whether an interface function's signature,
// just read, has a default body: `end` on its line (an empty one), or a
// line below that does not start the next member or close the interface.
// A required function has no body, so the next line is another member, a
// doc comment or `//!` prompt above one, or the interface's `end`.
func (p *Parser) interfaceBodyFollows() bool {
	if p.atEmptyBody() {
		return true
	}
	if !p.atLineEnd() {
		return false
	}
	for i := p.pos; i < len(p.tokens); i++ {
		t := p.tokens[i]
		switch t.Type {
		case token.NEWLINE, token.SEMICOLON, token.COMMENT, token.BLANK_LINE:
			continue
		case token.END, token.PUB, token.DOC_COMMENT, token.TEST_PROMPT, token.EOF, token.HOST:
			return false
		case token.FN:
			// `fn name(` or `fn name<` starts the next member; any other
			// `fn` is a lambda, the body's first statement.
			if i+2 < len(p.tokens) && (p.tokens[i+1].Type == token.IDENT || p.tokens[i+1].Type == token.TYPE_IDENT) &&
				(p.tokens[i+2].Type == token.LPAREN || p.tokens[i+2].Type == token.LT) {
				return false
			}
			return true
		case token.IDENT:
			switch t.Lexeme {
			case "open", "field", "host":
				return false
			}
		}
		return true
	}
	return false
}
