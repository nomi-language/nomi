package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// listComments places the comments of one bracketed, comma-separated list
// (ast.ItemComments) as its parser walks it. A list parser calls open after
// its opening bracket, item with each item it parses, listAfter after the
// item and again after its comma, and close (or end) before the closing
// bracket.
//
// A comment on the line an item or its comma ends on is that item's
// Trailing. A comment on a line of its own goes above the next item
// (Before), or below the last one (After) when the list closes after it. A
// comment on the opening bracket's line goes above the first item.
type listComments struct {
	pending []ast.Trivia
	// last is the slot of the item parsed most recently, nil before the
	// first.
	last func() *ast.ItemComments
}

// nodeComments is the comment slot of a list item that is a node, made on
// first use.
func nodeComments(n ast.Node) func() *ast.ItemComments {
	return func() *ast.ItemComments {
		h, ok := n.(ast.HasItemComments)
		if !ok {
			return nil
		}
		c := h.GetItemComments()
		if c == nil {
			c = &ast.ItemComments{}
			h.SetItemComments(c)
		}
		return c
	}
}

// fieldComments is the comment slot of a list item that is not a node.
func fieldComments(c *ast.ItemComments) func() *ast.ItemComments {
	return func() *ast.ItemComments { return c }
}

// open takes the comments after the list's opening bracket.
func (lc *listComments) open(p *Parser) {
	same, own := p.listBoundaryComments()
	lc.pending = append(lc.pending, same...)
	lc.pending = append(lc.pending, own...)
}

// item gives the comments waiting above the item just parsed to slot, its
// comment slot, and makes it the last item.
//
// A nil slot is an item that keeps no comment (a tuple destructure's `_`):
// the comments waiting above it go below the item before it, or wait for
// the item after it when it is the first.
func (lc *listComments) item(slot func() *ast.ItemComments) {
	if slot == nil {
		if lc.last != nil && len(lc.pending) > 0 {
			if prev := lc.last(); prev != nil {
				prev.After = append(prev.After, lc.pending...)
				lc.pending = nil
			}
		}
		return
	}
	lc.last = slot
	if len(lc.pending) == 0 {
		return
	}
	if c := slot(); c != nil {
		// The waiting comments come before any the item took inside it.
		c.Before = append(lc.pending, c.Before...)
	}
	lc.pending = nil
}

// listAfter takes the comments after the last item or after its comma.
func (p *Parser) listAfter(lc *listComments) {
	same, own := p.listBoundaryComments()
	if len(same) > 0 && len(lc.pending) == 0 && lc.last != nil {
		// One comment ends an item's line; a second, ending its comma's
		// line below it, goes on a line of its own.
		if c := lc.last(); c != nil && len(c.Trailing) == 0 {
			c.Trailing = append(c.Trailing, same...)
			same = nil
		}
	}
	lc.pending = append(lc.pending, same...)
	lc.pending = append(lc.pending, own...)
}

// close gives the comments still waiting to the last item, below it. In a
// list with no item they stay unclaimed, and attachDangling places them.
func (lc *listComments) close() {
	if len(lc.pending) == 0 || lc.last == nil {
		lc.pending = nil
		return
	}
	if c := lc.last(); c != nil {
		c.After = append(c.After, lc.pending...)
	}
	lc.pending = nil
}

// listBoundaryComments consumes the comments at a boundary of a list, and
// the line breaks around them. same is a comment on the line of the token
// before the boundary; own are the comments on lines of their own. Blank
// lines are dropped: the formatter writes such a list one item per line,
// each ended by a comma, and the lexer emits no blank line after a comma.
// A line break with no comment next to it is left for the list's parser.
func (p *Parser) listBoundaryComments() (same, own []ast.Trivia) {
	prevLine := -1
prev:
	for i := p.pos - 1; i >= 0; i-- {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT:
			continue
		}
		prevLine, _ = tokenEndPos(p.tokens[i])
		break prev
	}
	for !p.atEnd() {
		tk := p.peek()
		switch tk.Type {
		case token.NEWLINE, token.BLANK_LINE:
			j := p.pos
			for j < len(p.tokens) && (p.tokens[j].Type == token.NEWLINE || p.tokens[j].Type == token.BLANK_LINE) {
				j++
			}
			if len(same) == 0 && len(own) == 0 && (j >= len(p.tokens) || p.tokens[j].Type != token.COMMENT) {
				return same, own
			}
			p.pos = j
			continue
		case token.COMMENT:
			if isAttachedCommentTestStart(tk) || isAttachedCommentTestEnd(tk) {
				return same, own
			}
			t := ast.Trivia{Kind: ast.TriviaComment, Text: tk.Lexeme, Line: tk.Line, Col: tk.Col}
			if tk.Line == prevLine && len(same) == 0 && len(own) == 0 {
				same = append(same, t)
			} else {
				own = append(own, t)
			}
			p.advance()
			continue
		}
		return same, own
	}
	return same, own
}

// peekPastComments is the type of the n-th token ahead, counting from 0,
// that is not a comment or a line break.
func (p *Parser) peekPastComments(n int) token.TokenType {
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT:
			continue
		}
		if n == 0 {
			return p.tokens[i].Type
		}
		n--
	}
	return token.EOF
}

// end hands back the comments still waiting, for a list whose node keeps
// the comments before its closing bracket in an EndTrivia of its own.
func (lc *listComments) end() []ast.Trivia {
	out := lc.pending
	lc.pending = nil
	return out
}

// listSeparators steps over the comma and line breaks after an item of a
// brace-delimited list (a map literal or pattern, a struct pattern),
// taking the comments among them. A second comma is an error.
func (p *Parser) listSeparators(lc *listComments) error {
	for {
		p.listAfter(lc)
		if p.atEnd() {
			return nil
		}
		switch p.peek().Type {
		case token.COMMA:
			if err := p.doubledComma(); err != nil {
				return err
			}
			p.advance()
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			p.advance()
		default:
			return nil
		}
	}
}

// doubledComma is the error at a comma the cursor is on when the last token
// before it, past line breaks and comments, is a comma too: no list leaves
// an empty slot between two commas. It answers nil anywhere else.
func (p *Parser) doubledComma() error {
	if p.atEnd() || p.peek().Type != token.COMMA {
		return nil
	}
	for i := p.pos - 1; i >= 0; i-- {
		switch p.tokens[i].Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT:
			continue
		case token.COMMA:
			at := p.peek()
			return errorAt(at.Line, at.Col, "unexpected `,`: there is nothing between these commas")
		}
		return nil
	}
	return nil
}

// skipListBreaks steps over the line breaks and blank lines between items
// of a list. A blank line there means nothing: the formatter writes the
// list one item per line or on one line.
func (p *Parser) skipListBreaks() {
	for !p.atEnd() {
		switch p.peek().Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			p.advance()
		default:
			return
		}
	}
}

// commaSince reports whether a comma was consumed between token index from
// and the cursor: the separator a struct literal or pattern needs between
// two fields, where a line break alone does not separate them.
func (p *Parser) commaSince(from int) bool {
	for i := from; i < p.pos && i < len(p.tokens); i++ {
		if p.tokens[i].Type == token.COMMA {
			return true
		}
	}
	return false
}

// missingFieldComma is the error at a struct literal's or pattern's field
// that follows the field prev with no comma between them.
func missingFieldComma(at token.Token, prev string) error {
	return errorAt(at.Line, at.Col, "expected `,` or `}` after the field `%s`", prev)
}

// missingEntryComma is the error at a map literal's or pattern's entry
// that follows another with no comma between them.
func missingEntryComma(at token.Token) error {
	return errorAt(at.Line, at.Col, "expected `,` or `}` after the map entry")
}

// structLitFieldEnd steps over the separators after a struct literal's
// field, f: line breaks and one comma (a second is an error). A comment on the line the field or its comma ends on is f's
// Trailing; a comment on a line of its own is left for the next field's
// LeadingComments or the literal's EndTrivia. It reports whether it stepped
// over a comma.
func (p *Parser) structLitFieldEnd(f *ast.StructFieldVal) (bool, error) {
	start := p.pos
	skipBreaks := func() {
		for !p.atEnd() && (p.peek().Type == token.NEWLINE || p.peek().Type == token.SEMICOLON) {
			p.advance()
		}
	}
	skipBreaks()
	if !p.atEnd() && p.peek().Type == token.COMMA {
		p.advance()
		skipBreaks()
	}
	if err := p.doubledComma(); err != nil {
		return false, err
	}
	if p.atEnd() || p.peek().Type != token.COMMENT {
		return p.commaSince(start), nil
	}
	prevLine := -1
	for i := p.pos - 1; i >= 0; i-- {
		if t := p.tokens[i].Type; t != token.NEWLINE && t != token.SEMICOLON {
			prevLine, _ = tokenEndPos(p.tokens[i])
			break
		}
	}
	if tr := p.collectCommentOnLine(prevLine); len(tr) > 0 {
		f.Trailing = append(f.Trailing, tr...)
		skipBreaks()
	}
	return p.commaSince(start), nil
}

// unlistItem moves n's ItemComments to its dangling comments, for an item
// that turned out to stand alone: the one pattern in parentheses, which
// only group it.
func unlistItem(n ast.Node) {
	h, ok := n.(ast.HasItemComments)
	if !ok || h.GetItemComments() == nil {
		return
	}
	c := h.GetItemComments()
	h.SetItemComments(nil)
	d, ok := n.(ast.HasDangling)
	if !ok {
		return
	}
	for _, t := range c.Before {
		d.AddDanglingBefore(t)
	}
	for _, t := range append(append([]ast.Trivia{}, c.Trailing...), c.After...) {
		d.AddDanglingAfter(t)
	}
}

// parsePatternList parses the patterns in parentheses after the `(`,
// through the `)`: a tuple pattern's comma-separated patterns when tuple is
// set, and otherwise the one pattern a grouping holds. tok is the `(`, for
// errors.
func (p *Parser) parsePatternList(tok token.Token, tuple bool) ([]ast.Node, error) {
	var patterns []ast.Node
	var lc listComments
	lc.open(p)
	for {
		pat, err := p.parseSinglePattern()
		if err != nil {
			return nil, err
		}
		lc.item(nodeComments(pat))
		patterns = append(patterns, pat)
		p.listAfter(&lc)
		if p.atEnd() {
			if !tuple {
				return nil, errorAt(tok.Line, tok.Col, "expected ')' after pattern")
			}
			return nil, errorAt(tok.Line, tok.Col, "expected ')' in tuple pattern")
		}
		if p.peek().Type == token.RPAREN {
			lc.close()
			p.advance()
			break
		}
		if !tuple {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ')' after pattern")
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ')' in tuple pattern")
		}
		p.advance() // consume COMMA
		p.listAfter(&lc)
	}
	if len(patterns) == 1 {
		unlistItem(patterns[0])
	}
	return patterns, nil
}

// listGap takes the comments at a boundary of a list whose parser steps
// over every line break there (a tuple's), and the line breaks.
func (p *Parser) listGap(lc *listComments) {
	for {
		p.listAfter(lc)
		if p.atEnd() {
			return
		}
		switch p.peek().Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			p.advance()
		default:
			return
		}
	}
}

// parseTypeArgs parses a generic type's comma-separated arguments after its
// `<`, stopping before the `>`.
func (p *Parser) parseTypeArgs() ([]ast.TypeExpr, error) {
	var params []ast.TypeExpr
	var lc listComments
	lc.open(p)
	for {
		param, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		lc.item(nodeComments(param))
		params = append(params, param)
		p.listAfter(&lc)
		if p.atEnd() || p.peek().Type != token.COMMA {
			break
		}
		p.advance() // consume COMMA
		p.listAfter(&lc)
	}
	lc.close()
	p.skipBreaksBefore(token.GT)
	return params, nil
}

// skipBreaksBefore steps over line breaks when the token after them is
// close. The lexer drops a line break before `)` and `]` but not before
// `>` or `|`, so a list closed by one of those on a line of its own, as the
// formatter writes a list holding a comment, has a line break before it.
func (p *Parser) skipBreaksBefore(close token.TokenType) {
	j := p.pos
	for j < len(p.tokens) && (p.tokens[j].Type == token.NEWLINE || p.tokens[j].Type == token.BLANK_LINE) {
		j++
	}
	if j < len(p.tokens) && p.tokens[j].Type == close {
		p.pos = j
	}
}

// commentsOnly is trivia without its blank lines.
func commentsOnly(trivia []ast.Trivia) []ast.Trivia {
	var out []ast.Trivia
	for _, t := range trivia {
		if t.Kind == ast.TriviaComment {
			out = append(out, t)
		}
	}
	return out
}
