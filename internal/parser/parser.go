package parser

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/strlit"
	"github.com/nomi-language/nomi/internal/token"
	"strconv"
	"strings"
	"unicode"
)

// Parser holds the token slice and current position.
type Parser struct {
	tokens        []token.Token
	pos           int
	errors        []ParseError
	noStructLit   bool         // when true, TYPE_IDENT { is not parsed as a struct literal
	fileEndTrivia []ast.Trivia // trivia after the last top-level decl through EOF

	// resilient turns on statement-level recovery inside blocks and
	// error-body recovery for lambdas. Set only by ParseResilient, which
	// is the LSP's entry point; see its doc comment for why the
	// distinction is load-bearing.
	resilient bool
	// recoveries counts in-block recoveries performed so far. The
	// top-level loop reads it before and after each declaration to
	// decide whether that declaration is damaged.
	recoveries int
	// damaged holds one span per top-level declaration that recovery
	// had to repair.
	damaged []Span
}

// Span is a closed source region, in 1-based line / 1-based byte column,
// matching ast node positions and ParseError.
type Span struct {
	StartLine, StartCol int
	EndLine, EndCol     int
}

// Contains reports whether a (line, col) position falls inside the span.
func (s Span) Contains(line, col int) bool {
	if line < s.StartLine || line > s.EndLine {
		return false
	}
	if line == s.StartLine && col < s.StartCol {
		return false
	}
	if line == s.EndLine && col > s.EndCol {
		return false
	}
	return true
}

// Parse is the public API. It takes a token slice (from lexer.Lex) and returns
// a list of top-level AST nodes (statements), or an error.
//
// Trivia (comments / blank lines) that sits after the last top-level node but
// before EOF is silently dropped by this entry point. Callers that need to
// preserve end-of-file trivia (the formatter is the only one today) should
// use ParseFile instead.
func Parse(tokens []token.Token) ([]ast.Node, error) {
	p := &Parser{tokens: tokens}
	stmts, err := p.parse()
	if err != nil {
		return nil, err
	}
	return stmts, nil
}

// ParseFile is Parse with end-of-file trivia preserved. The returned trivia
// slice contains every comment / blank-line token that appeared after the
// last top-level declaration and before EOF, in source order. Used by the
// formatter so `nomi fmt -w` preserves trailing top-level comments.
func ParseFile(tokens []token.Token) ([]ast.Node, []ast.Trivia, error) {
	p := &Parser{tokens: tokens}
	stmts, err := p.parse()
	if err != nil {
		return nil, nil, err
	}
	return stmts, p.fileEndTrivia, nil
}

func (p *Parser) parse() ([]ast.Node, error) {
	var stmts []ast.Node

	for !p.atEnd() {
		leading := p.collectLeadingTrivia()
		if p.atEnd() {
			// Trivia after the last top-level decl is end-of-file trivia.
			// Stashed on the parser so ParseFile callers (the formatter)
			// can preserve trailing top-level comments — Parse drops it
			// for backwards compatibility with non-formatter callers.
			p.fileEndTrivia = leading
			break
		}
		docTok := p.peek()
		doc := p.collectDocComments()
		node, attachedDoc, attachedLeading, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		trailing := p.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), append(leading, attachedLeading...), trailing)
		if !attachDoc(node, mergeDocComments(doc, attachedDoc)) {
			return nil, strayDocComment(docTok, node)
		}
		stmts = append(stmts, node)
	}

	return stmts, nil
}

// ParseWithRecovery parses tokens with error recovery. Returns all successfully
// parsed nodes and all errors encountered. Unlike Parse, it does not stop on
// the first error — it synchronizes to the next top-level declaration and continues.
//
// Like Parse, this drops end-of-file trivia.
func ParseWithRecovery(tokens []token.Token) ([]ast.Node, []ParseError) {
	p := &Parser{tokens: tokens}
	stmts, errs := p.parseWithRecovery()
	return stmts, errs
}

// ParseResilient is the LSP's parse. It recovers at statement boundaries
// INSIDE blocks — not only at top-level ones — so a syntax error in a
// function body no longer discards the whole function. The valid
// statements around the damage survive, the enclosing declaration reaches
// the builder, and completion has the function's parameters and locals to
// offer at the cursor. Damaged runs become *ast.ErrorNode.
//
// It returns three things:
//
//   - nodes: the recovered tree. May contain *ast.ErrorNode, and a
//     declaration containing one is PARTIAL — some of its body was never
//     read. Suitable for scopes, symbols and navigation. Not suitable for
//     any judgement about the declaration as a whole.
//   - errs: the parse errors. These come from the STRICT pass, not the
//     resilient one, so the editor's syntax diagnostics are exactly the
//     ones ParseWithRecovery reports today. Recovery adds nodes; it never
//     adds, moves or removes a diagnostic.
//   - damaged: one span per top-level declaration that recovery repaired.
//     A caller that runs a type checker over the tree must discard type
//     diagnostics inside these spans: a partially-read declaration is
//     ill-typed by construction, and reporting that would bury the real
//     syntax error under noise on the valid half.
//
// A file that parses cleanly takes the strict path and gets exactly
// ParseWithRecovery's tree — resilient mode only ever runs on tokens that
// already failed, so it cannot change the AST of a valid program.
//
// This is deliberately NOT what Parse or ParseWithRecovery do. `nomi run`
// and `nomi build` go through Parse, which refuses a file with a syntax
// error before producing an AST at all. Recovery is for the editor.
func ParseResilient(tokens []token.Token) ([]ast.Node, []ParseError, []Span) {
	strict := &Parser{tokens: tokens}
	nodes, errs := strict.parseWithRecovery()
	if len(errs) == 0 {
		return nodes, nil, nil
	}
	p := &Parser{tokens: tokens, resilient: true}
	recovered, _ := p.parseWithRecovery()
	return recovered, errs, p.damaged
}

func (p *Parser) parseWithRecovery() ([]ast.Node, []ParseError) {
	var stmts []ast.Node
	for !p.atEnd() {
		leading := p.collectLeadingTrivia()
		if p.atEnd() {
			break
		}
		docTok := p.peek()
		doc := p.collectDocComments()
		declStart := p.pos
		recoveriesBefore := p.recoveries
		node, attachedDoc, attachedLeading, err := p.parseStmt()
		if err != nil {
			p.recordParseError(err)
			p.synchronize()
			continue
		}
		trailing := p.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), append(leading, attachedLeading...), trailing)
		if !attachDoc(node, mergeDocComments(doc, attachedDoc)) {
			p.recordParseError(strayDocComment(docTok, node))
		}
		if p.recoveries > recoveriesBefore {
			p.damaged = append(p.damaged, p.spanOfTokens(declStart, p.pos))
		}
		stmts = append(stmts, node)
	}
	return stmts, p.errors
}

// spanOfTokens returns the source span covered by tokens[from:to).
func (p *Parser) spanOfTokens(from, to int) Span {
	if from >= len(p.tokens) {
		from = len(p.tokens) - 1
	}
	if to <= from {
		to = from + 1
	}
	if to > len(p.tokens) {
		to = len(p.tokens)
	}
	start := p.tokens[from]
	endLine, endCol := tokenEnd(p.tokens[to-1])
	return Span{StartLine: start.Line, StartCol: start.Col, EndLine: endLine, EndCol: endCol}
}

func (p *Parser) recordParseError(err error) {
	var parseErr ParseError
	if errors.As(err, &parseErr) {
		p.errors = append(p.errors, parseErr)
		return
	}
	tok := p.peek()
	p.errors = append(p.errors, ParseError{
		Line:    tok.Line,
		Col:     tok.Col,
		Message: err.Error(),
	})
}

// collectLeadingTrivia pulls pending COMMENT/BLANK_LINE tokens into a slice.
// NEWLINEs between trivia tokens are consumed silently.
func (p *Parser) collectLeadingTrivia() []ast.Trivia {
	var out []ast.Trivia
	for !p.atEnd() {
		switch p.peek().Type {
		case token.COMMENT:
			if isAttachedCommentTestStart(p.peek()) || isAttachedCommentTestEnd(p.peek()) {
				return out
			}
			tk := p.peek()
			out = append(out, ast.Trivia{
				Kind: ast.TriviaComment,
				Text: tk.Lexeme,
				Line: tk.Line,
				Col:  tk.Col,
			})
			p.advance()
		case token.BLANK_LINE:
			tk := p.peek()
			out = append(out, ast.Trivia{
				Kind: ast.TriviaBlankLine,
				Line: tk.Line,
				Col:  tk.Col,
			})
			p.advance()
		case token.NEWLINE, token.SEMICOLON:
			p.advance()
		default:
			return out
		}
	}
	return out
}

// collectTrailingComment grabs a same-line COMMENT following the just-parsed node.
// A comment counts as "same line" if it sits on either the node's starting line
// (nodeLine) or the line of the most recently consumed token (the node's end
// line for well-formed expressions). This accommodates multi-line constructs
// whose closing delimiter is on a later line than the node's start, so the
// idiomatic `f(\n  x,\n) // trailing` still attaches correctly.
func (p *Parser) collectTrailingComment(nodeLine int) []ast.Trivia {
	if p.atEnd() {
		return nil
	}
	if p.peek().Type != token.COMMENT {
		return nil
	}
	cmtLine := p.peek().Line
	endLine := nodeLine
	if p.pos > 0 {
		// Line of the last-consumed token = node's end line.
		if l := p.tokens[p.pos-1].Line; l > endLine {
			endLine = l
		}
	}
	if cmtLine == nodeLine || cmtLine == endLine {
		tk := p.peek()
		p.advance()
		return []ast.Trivia{{
			Kind: ast.TriviaComment,
			Text: tk.Lexeme,
			Line: tk.Line,
			Col:  tk.Col,
		}}
	}
	return nil
}

// collectCommentOnLine takes a COMMENT that sits on line, the line the
// body member just parsed ends on. A type-body member (variant, field,
// interface requirement) ends at a newline or at a same-line comment
// alike, and the comment stays with the member it follows.
func (p *Parser) collectCommentOnLine(line int) []ast.Trivia {
	if p.atEnd() || p.peek().Type != token.COMMENT || p.peek().Line != line {
		return nil
	}
	tk := p.peek()
	if isAttachedCommentTestStart(tk) || isAttachedCommentTestEnd(tk) {
		return nil
	}
	p.advance()
	return []ast.Trivia{{Kind: ast.TriviaComment, Text: tk.Lexeme, Line: tk.Line, Col: tk.Col}}
}

// prevLine is the line of the most recently consumed token.
func (p *Parser) prevLine() int {
	if p.pos == 0 {
		return 0
	}
	return p.tokens[p.pos-1].Line
}

// collectEndOfBodyTrivia consumes COMMENT and BLANK_LINE tokens (plus
// NEWLINE / SEMICOLON separators) until a non-trivia token is reached.
// Used at the tail of brace-bodied parsers (struct, enum, interface,
// struct literal, anonymous struct type, struct-shaped enum variants) to
// capture trivia sitting between the last member and the closing `}`.
//
// Mirrors collectLeadingTrivia but skips no closing-delimiter check —
// the caller is responsible for verifying the next token is the expected
// `}` (or other terminator) after this helper returns. Comments and
// blank-lines become Trivia entries in source order; bare NEWLINE /
// SEMICOLON tokens are consumed silently. The result is suitable for
// stashing on an EndTrivia field.
func (p *Parser) collectEndOfBodyTrivia() []ast.Trivia {
	var out []ast.Trivia
	for !p.atEnd() {
		switch p.peek().Type {
		case token.COMMENT:
			if isAttachedCommentTestStart(p.peek()) || isAttachedCommentTestEnd(p.peek()) {
				return out
			}
			tk := p.peek()
			out = append(out, ast.Trivia{
				Kind: ast.TriviaComment,
				Text: tk.Lexeme,
				Line: tk.Line,
				Col:  tk.Col,
			})
			p.advance()
		case token.BLANK_LINE:
			tk := p.peek()
			out = append(out, ast.Trivia{
				Kind: ast.TriviaBlankLine,
				Line: tk.Line,
				Col:  tk.Col,
			})
			p.advance()
		case token.NEWLINE, token.SEMICOLON:
			p.advance()
		default:
			return out
		}
	}
	return out
}

// attachTrivia attaches leading and trailing trivia to anything that
// implements HasTrivia. Call sites pass either an ast.Node (which must satisfy
// HasTrivia via its embedded TriviaCarrier) or a non-Node carrier like
// *ast.CaseBranch.
func attachTrivia(h ast.HasTrivia, leading, trailing []ast.Trivia) {
	if h == nil {
		return
	}
	for _, t := range leading {
		h.AddLeading(t)
	}
	for _, t := range trailing {
		h.AddTrailing(t)
	}
}

func (p *Parser) synchronize() {
	for !p.atEnd() {
		switch p.peek().Type {
		case token.FN, token.TYPE, token.STRUCT, token.ENUM,
			token.TYPEALIAS, token.INTERFACE,
			token.IMPORT, token.PUB, token.EXTERN,
			token.EXPORT, token.ONCE, token.TEST, token.TESTS:
			return
		}
		p.advance()
	}
}

// recoverStmtInBlock is resilient mode's in-block recovery. It is called
// from a block's statement loop after parseStmt failed, with `from` = the
// token index the failed statement started at. It skips the damaged run
// and returns an *ast.ErrorNode covering it, leaving the cursor at the
// start of the next statement (or at the token that closes the block).
//
// The boundary is the STATEMENT, for two reasons. A statement inside a
// block has an unambiguous terminator — a newline or `;` at the block's
// own bracket depth, or the `}` that closes the block — so the run to
// discard is decidable without guessing. And it is the coarsest boundary
// that still keeps what the LSP needs: the function survives, and so does
// every valid binding before and after the damage. Recovering at the
// BLOCK boundary instead would keep the signature but throw away the
// locals, which is most of what completion wants. Recovering inside an
// EXPRESSION would mean inventing operands at dozens of Pratt-parser
// sites, each one a place where a resilient parse could accept a shape
// the strict parse rejects — the one property this must not have.
// The single exception is a lambda body; see parseLambda.
func (p *Parser) recoverStmtInBlock(from int, err error) *ast.ErrorNode {
	p.recoveries++
	// parseStmt may have failed without consuming anything (a stray
	// closing bracket where a statement was expected). Step over one
	// token so the enclosing loop always makes progress.
	if p.pos == from && !p.atEnd() {
		p.advance()
	}
	depth := 0
skip:
	for !p.atEnd() {
		switch p.peek().Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			if depth == 0 {
				// Closes an enclosing construct, not anything the
				// damaged statement opened. Leave it for the caller.
				break skip
			}
			depth--
		case token.NEWLINE, token.SEMICOLON:
			if depth == 0 {
				p.advance() // the terminator belongs to the damaged run
				break skip
			}
		case token.FN, token.TYPE, token.STRUCT, token.ENUM,
			token.TYPEALIAS, token.INTERFACE,
			token.IMPORT, token.PUB, token.EXTERN,
			token.EXPORT, token.ONCE, token.TEST, token.TESTS:
			if depth == 0 {
				// A top-level keyword at block depth means the block's
				// `}` is missing. Stop here so the block can close
				// itself and the declaration that follows still parses.
				break skip
			}
		}
		p.advance()
	}
	span := p.spanOfTokens(from, p.pos)
	// Stretch the end to where the next token begins. The whitespace the
	// lexer dropped is exactly where the caret sits mid-edit: `|x| n + `
	// ends at the `+`, but the cursor is two columns further right, and a
	// span stopping at the last token read would not contain it — which
	// for a lambda means ScopeAt misses the lambda and its parameter is
	// not offered, the whole point of recovering here.
	if p.pos < len(p.tokens) {
		next := p.tokens[p.pos]
		if next.Line > span.EndLine || (next.Line == span.EndLine && next.Col > span.EndCol) {
			span.EndLine, span.EndCol = next.Line, next.Col
		}
	}
	return &ast.ErrorNode{
		Message: err.Error(),
		Line:    span.StartLine,
		Col:     span.StartCol,
		EndLine: span.EndLine,
		EndCol:  span.EndCol,
	}
}

// blockClosedByColumnOneDecl reports whether the cursor sits on a
// declaration keyword in column 1. Resilient mode uses it to stop a
// block's statement loop: an unclosed `{` would otherwise swallow every
// declaration after it as a NESTED declaration — legal Nomi, and a worse
// reading than the one the user meant. A nested declaration is always
// indented (it is inside a block, and `nomi fmt` indents block bodies),
// so a declaration keyword in column 1 means the block's `}` is missing.
// Only consulted when p.resilient, so this cannot affect a valid parse.
func (p *Parser) blockClosedByColumnOneDecl() bool {
	if p.atEnd() {
		return false
	}
	tok := p.peek()
	if tok.Col != 1 {
		return false
	}
	switch tok.Type {
	case token.FN, token.TYPE, token.STRUCT, token.ENUM,
		token.TYPEALIAS, token.INTERFACE,
		token.IMPORT, token.PUB, token.EXTERN,
		token.EXPORT, token.ONCE, token.TEST, token.TESTS:
		return true
	}
	return false
}

// resilientBlockEnd supplies the end position for a block whose closing
// `}` never arrived, and counts the omission as a recovery so the
// enclosing declaration is marked damaged. An unclosed body is the
// commonest mid-edit state there is — you have just typed `fn f() {` and
// pressed enter — and closing the block at the last token read keeps
// every declaration after it parsing normally. Resilient mode only.
func (p *Parser) resilientBlockEnd(openTok token.Token) (line, col int) {
	p.recoveries++
	if p.pos > 0 {
		return tokenEnd(p.tokens[p.pos-1])
	}
	return openTok.Line, openTok.Col
}

// rejectDecorator refuses a `@name` line where a declaration or a body item
// starts. Nomi has no decorators: a derived conformance is a `derive Iface for
// Type` declaration and implementation functions live in `impl` blocks, so
// `@derive` and `@impl` name those forms. The derive lowering builds its own
// ast.Decorator values; source never produces one.
func (p *Parser) rejectDecorator() error {
	if p.peek().Type != token.AT {
		return nil
	}
	at := p.peek()
	name := p.peekAt(1)
	switch name.Lexeme {
	case "derive":
		return errorAt(name.Line, name.Col, "`@derive` is not supported; declare a derived conformance with `derive Iface for Type`")
	case "impl":
		return errorAt(name.Line, name.Col, "`@impl` is not supported; write implementation functions inside `impl Interface for Type { ... }` blocks")
	}
	if name.Line == at.Line && isWordLexeme(name.Lexeme) {
		return errorAt(at.Line, at.Col, "`@%s` is not supported: Nomi has no decorators", name.Lexeme)
	}
	return errorAt(at.Line, at.Col, "unexpected '@': Nomi has no decorators")
}

// isWordLexeme reports whether s is spelled like a name or keyword.
func isWordLexeme(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

func isAttachedCommentTestStart(tok token.Token) bool {
	return tok.Type == token.TEST_PROMPT
}

func isAttachedCommentTestEnd(tok token.Token) bool {
	return tok.Type == token.TEST_PROMPT
}

func (p *Parser) parseAttachedTestPrompts() ([]ast.AttachedTest, error) {
	var tests []ast.AttachedTest
	for isAttachedCommentTestStart(p.peek()) {
		test, err := p.parseAttachedCommentTest()
		if err != nil {
			return tests, err
		}
		tests = append(tests, test)
		for tt := p.peek().Type; tt == token.NEWLINE || tt == token.SEMICOLON; tt = p.peek().Type {
			p.advance()
		}
		if p.peek().Type == token.BLANK_LINE {
			if !p.hasAttachedTestPromptAfterBlankLine() {
				break
			}
			for tt := p.peek().Type; tt == token.NEWLINE || tt == token.SEMICOLON || tt == token.BLANK_LINE; tt = p.peek().Type {
				p.advance()
			}
		}
	}
	return tests, nil
}

func (p *Parser) hasAttachedTestPromptAfterBlankLine() bool {
	seenBlank := false
	for offset := 0; ; offset++ {
		switch p.peekAt(offset).Type {
		case token.NEWLINE, token.SEMICOLON:
			continue
		case token.BLANK_LINE:
			seenBlank = true
			continue
		case token.TEST_PROMPT:
			return seenBlank
		default:
			return false
		}
	}
}

func (p *Parser) parseAttachedCommentTest() (ast.AttachedTest, error) {
	openTok := p.peek()
	bodyTokens, closeTok, trailingPromptBlanks, err := p.collectAttachedTestBodyTokens(openTok)
	if err != nil {
		return ast.AttachedTest{}, err
	}
	body, recovered, err := parseAttachedTestBody(bodyTokens, openTok, closeTok, p.resilient)
	if err != nil {
		return ast.AttachedTest{}, err
	}
	p.recoveries += recovered
	// A damaged line may be the assertion being typed (`//! assert u.`),
	// so a recovered body keeps its bindings without one.
	if _, _, ok := firstNestedAssertion(body); !ok && recovered == 0 {
		return ast.AttachedTest{}, errorAt(openTok.Line, openTok.Col, "attached test must contain at least one assert or refute")
	}
	return ast.AttachedTest{
		Body:                 body,
		Kind:                 "test",
		Inline:               false,
		Line:                 openTok.Line,
		Col:                  openTok.Col,
		EndLine:              body.EndLine,
		EndCol:               body.EndCol,
		TrailingPromptBlanks: trailingPromptBlanks,
	}, nil
}

func (p *Parser) collectAttachedTestBodyTokens(openTok token.Token) ([]token.Token, token.Token, int, error) {
	var bodyTokens []token.Token
	closeTok := openTok
	pendingBlankPrompts := 0
	for {
		if p.atEnd() {
			return bodyTokens, closeTok, pendingBlankPrompts, nil
		}
		if p.peek().Type != token.TEST_PROMPT {
			return bodyTokens, closeTok, pendingBlankPrompts, nil
		}
		prompt := p.peek()
		p.advance()
		closeTok = prompt
		coveredThroughLine := prompt.Line
		promptHadBody := false
		for !p.atEnd() && p.peek().Type != token.TEST_PROMPT {
			tk := p.peek()
			if tk.Type == token.BLANK_LINE {
				if !promptHadBody {
					pendingBlankPrompts++
				}
				return bodyTokens, closeTok, pendingBlankPrompts, nil
			}
			if tk.Line != prompt.Line &&
				tk.Line > coveredThroughLine &&
				(isAttachedTestPostludeStart(tk.Type) || (isHostToken(tk) && (p.peekAt(1).Type == token.FN || p.peekAt(1).Type == token.TYPE))) {
				if !promptHadBody {
					pendingBlankPrompts++
				}
				return bodyTokens, closeTok, pendingBlankPrompts, nil
			}
			if tk.Line != prompt.Line &&
				tk.Line > coveredThroughLine &&
				tk.Type != token.NEWLINE &&
				tk.Type != token.SEMICOLON &&
				tk.Type != token.BLANK_LINE {
				return nil, token.Token{}, 0, errorAt(tk.Line, tk.Col, "expected `//!` before attached test body line")
			}
			if !promptHadBody && tk.Type != token.NEWLINE && tk.Type != token.SEMICOLON {
				for i := 0; i < pendingBlankPrompts; i++ {
					bodyTokens = append(bodyTokens, token.Token{Type: token.BLANK_LINE, Line: prompt.Line, Col: prompt.Col})
				}
				pendingBlankPrompts = 0
				promptHadBody = true
			}
			bodyTokens = append(bodyTokens, tk)
			closeTok = tk
			if endLine := tokenEndLine(tk); endLine > coveredThroughLine {
				coveredThroughLine = endLine
			}
			p.advance()
		}
		if !promptHadBody {
			pendingBlankPrompts++
		}
	}
}

func isAttachedTestPostludeStart(tt token.TokenType) bool {
	switch tt {
	case token.DOC_COMMENT,
		token.COMMENT,
		token.AT,
		token.PUB,
		token.EXTERN,
		token.FN,
		token.ONCE,
		token.TYPE,
		token.STRUCT,
		token.ENUM,
		token.TYPEALIAS,
		token.INTERFACE,
		token.IMPL,
		token.OPAQUE,
		token.TEST,
		token.TESTS:
		return true
	default:
		return false
	}
}

func tokenEndLine(tok token.Token) int {
	if tok.EndLine != 0 {
		return tok.EndLine
	}
	return tok.Line
}

// parseAttachedTestBody parses the statements of a `//!` group. In resilient
// mode a statement that does not parse becomes an *ast.ErrorNode, as in a
// block, so the group's other bindings stay in scope for the editor;
// recovered counts them.
func parseAttachedTestBody(tokens []token.Token, openTok, closeTok token.Token, resilient bool) (body *ast.Block, recovered int, err error) {
	bodyTokens := append([]token.Token(nil), tokens...)
	bodyTokens = append(bodyTokens, token.Token{Type: token.EOF, Line: closeTok.Line, Col: closeTok.Col})
	bodyParser := &Parser{tokens: bodyTokens, resilient: resilient}
	var stmts []ast.Node
	var endTrivia []ast.Trivia
	for !bodyParser.atEnd() {
		leading := bodyParser.collectLeadingTrivia()
		if bodyParser.atEnd() {
			endTrivia = leading
			break
		}
		stmtStart := bodyParser.pos
		node, attachedDoc, attachedLeading, err := bodyParser.parseStmt()
		if err != nil {
			if !resilient {
				return nil, 0, err
			}
			stmts = append(stmts, bodyParser.recoverStmtInBlock(stmtStart, err))
			continue
		}
		trailing := bodyParser.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), append(leading, attachedLeading...), trailing)
		attachDoc(node, attachedDoc)
		stmts = append(stmts, node)
	}
	body = &ast.Block{
		Stmts:   stmts,
		Line:    openTok.Line,
		Col:     openTok.Col,
		EndLine: closeTok.Line,
		EndCol:  closeTok.Col + len(closeTok.Lexeme),
	}
	for _, t := range endTrivia {
		body.AddTrailing(t)
	}
	return body, bodyParser.recoveries, nil
}

func (p *Parser) parseAttachedTestPromptsAndDoc() ([]ast.AttachedTest, string, []ast.Trivia, error) {
	tests, err := p.parseAttachedTestPrompts()
	if err != nil {
		return nil, "", nil, err
	}
	if len(tests) == 0 {
		return tests, "", nil, nil
	}
	for {
		after, doc := p.collectPostAttachedPrelude()
		if doc != "" {
			tests[len(tests)-1].DocAfter = doc
		}
		tests[len(tests)-1].After = after
		if p.peek().Type != token.TEST_PROMPT {
			return tests, doc, nil, nil
		}
		more, err := p.parseAttachedTestPrompts()
		if err != nil {
			return nil, "", nil, err
		}
		tests = append(tests, more...)
	}
}

func (p *Parser) collectPostAttachedPrelude() ([]ast.AttachedTestAfter, string) {
	var after []ast.AttachedTestAfter
	var docLines []string
	for !p.atEnd() {
		switch p.peek().Type {
		case token.DOC_COMMENT:
			line := strings.TrimRight(p.peek().Lexeme, " \t\r")
			line = strings.TrimPrefix(line, " ")
			after = append(after, ast.AttachedTestAfter{IsDoc: true, Doc: line})
			docLines = append(docLines, line)
			p.advance()
		case token.COMMENT:
			tk := p.peek()
			after = append(after, ast.AttachedTestAfter{
				Trivia: ast.Trivia{
					Kind: ast.TriviaComment,
					Text: tk.Lexeme,
					Line: tk.Line,
					Col:  tk.Col,
				},
			})
			p.advance()
		case token.BLANK_LINE:
			p.advance()
		case token.NEWLINE, token.SEMICOLON:
			p.advance()
		default:
			if len(docLines) == 0 {
				return after, ""
			}
			return after, strings.Join(docLines, "\n")
		}
	}
	if len(docLines) == 0 {
		return after, ""
	}
	return after, strings.Join(docLines, "\n")
}

func mergeDocComments(before, after string) string {
	switch {
	case before == "":
		return after
	case after == "":
		return before
	default:
		return before + "\n" + after
	}
}

func firstNestedAssertion(n ast.Node) (line, col int, ok bool) {
	switch v := n.(type) {
	case nil:
		return 0, 0, false
	case *ast.Assertion:
		return v.Line, v.Col, true
	case *ast.PatternDestructure:
		if v.AssertLine > 0 {
			return v.AssertLine, v.AssertCol, true
		}
		return v.Line, v.Col, true
	case *ast.Block:
		for _, stmt := range v.Stmts {
			if line, col, ok := firstNestedAssertion(stmt); ok {
				return line, col, true
			}
		}
	case *ast.ExprStmt:
		return firstNestedAssertion(v.Expr)
	case *ast.Binding:
		return firstNestedAssertion(v.Value)
	case *ast.With:
		return firstNestedAssertion(v.Value)
	case *ast.Defer:
		return firstNestedAssertion(v.Call)
	case *ast.TupleDestructure:
		return firstNestedAssertion(v.Value)
	case *ast.StructDestructure:
		return firstNestedAssertion(v.Value)
	case *ast.MapDestructure:
		return firstNestedAssertion(v.Value)
	case *ast.DistinctDestructure:
		return firstNestedAssertion(v.Value)
	case *ast.PatternBinding:
		if line, col, ok := firstNestedAssertion(v.Value); ok {
			return line, col, true
		}
		if v.Else == nil {
			return 0, 0, false
		}
		if v.Else.Block != nil {
			return firstNestedAssertion(v.Else.Block)
		}
		for _, arm := range v.Else.Arms {
			if line, col, ok := firstNestedAssertion(arm.Guard); ok {
				return line, col, true
			}
			if line, col, ok := firstNestedAssertion(arm.Body); ok {
				return line, col, true
			}
		}
	case *ast.Binary:
		if line, col, ok := firstNestedAssertion(v.Left); ok {
			return line, col, true
		}
		return firstNestedAssertion(v.Right)
	case *ast.Unary:
		return firstNestedAssertion(v.Right)
	case *ast.GroupedExpr:
		return firstNestedAssertion(v.Expr)
	case *ast.Call:
		if line, col, ok := firstNestedAssertion(v.Func); ok {
			return line, col, true
		}
		for _, arg := range v.Args {
			if line, col, ok := firstNestedAssertion(arg); ok {
				return line, col, true
			}
		}
	case *ast.NamedArg:
		return firstNestedAssertion(v.Value)
	case *ast.FieldAccess:
		return firstNestedAssertion(v.Object)
	case *ast.ListLit:
		for _, item := range v.Items {
			if line, col, ok := firstNestedAssertion(item); ok {
				return line, col, true
			}
		}
	case *ast.VectorLit:
		for _, item := range v.Items {
			if line, col, ok := firstNestedAssertion(item); ok {
				return line, col, true
			}
		}
	case *ast.SetLit:
		for _, item := range v.Items {
			if line, col, ok := firstNestedAssertion(item); ok {
				return line, col, true
			}
		}
	case *ast.ListSpreadLit:
		for _, item := range v.Heads {
			if line, col, ok := firstNestedAssertion(item); ok {
				return line, col, true
			}
		}
		return firstNestedAssertion(v.TailSpread)
	case *ast.TupleLit:
		for _, item := range v.Items {
			if line, col, ok := firstNestedAssertion(item); ok {
				return line, col, true
			}
		}
	case *ast.MapLit:
		for _, entry := range v.Entries {
			if line, col, ok := firstNestedAssertion(entry.Key); ok {
				return line, col, true
			}
			if line, col, ok := firstNestedAssertion(entry.Value); ok {
				return line, col, true
			}
		}
	case *ast.StructLit:
		for _, field := range v.Fields {
			if line, col, ok := firstNestedAssertion(field.Value); ok {
				return line, col, true
			}
		}
	case *ast.If:
		if line, col, ok := firstNestedAssertion(v.Cond); ok {
			return line, col, true
		}
		if line, col, ok := firstNestedAssertion(v.Then); ok {
			return line, col, true
		}
		return firstNestedAssertion(v.Else)
	case *ast.Case:
		if line, col, ok := firstNestedAssertion(v.Value); ok {
			return line, col, true
		}
		for _, branch := range v.Branches {
			if line, col, ok := firstNestedAssertion(branch.Guard); ok {
				return line, col, true
			}
			if line, col, ok := firstNestedAssertion(branch.Body); ok {
				return line, col, true
			}
		}
	case *ast.Lambda:
		return firstNestedAssertion(v.Body)
	case *ast.Then:
		return firstNestedAssertion(v.Lambda)
	case *ast.TryOp:
		return firstNestedAssertion(v.Expr)
	case *ast.Dbg:
		return firstNestedAssertion(v.Expr)
	case *ast.Return:
		return firstNestedAssertion(v.Value)
	case *ast.Break:
		return firstNestedAssertion(v.Value)
	case *ast.ConcurrentBlock:
		return firstNestedAssertion(v.Body)
	}
	return 0, 0, false
}

func (p *Parser) parseStmt() (ast.Node, string, []ast.Trivia, error) {
	if err := p.rejectDecorator(); err != nil {
		return nil, "", nil, err
	}
	attachedTests, attachedDoc, attachedLeading, err := p.parseAttachedTestPromptsAndDoc()
	if err != nil {
		return nil, "", nil, err
	}
	if err := p.rejectDecorator(); err != nil {
		return nil, "", nil, err
	}

	start := p.pos
	node, err := p.parseStmtInner()
	if err != nil {
		return nil, "", nil, err
	}
	p.recordSpan(node, start)

	if len(attachedTests) > 0 {
		if err := attachDeclarationAttachedTests(node, attachedTests, declarationContext(node)); err != nil {
			return nil, "", nil, err
		}
	}
	return node, attachedDoc, attachedLeading, nil
}

func declarationContext(node ast.Node) string {
	switch node.(type) {
	case *ast.FuncDef:
		return "a function declaration"
	case *ast.ExternFunc:
		return "a host fn declaration"
	case *ast.StructDef:
		return "a struct declaration"
	case *ast.EnumDef:
		return "an enum declaration"
	case *ast.TypeDef:
		return "a type declaration"
	case *ast.ExternType:
		return "a host type declaration"
	case *ast.TypeAlias:
		return "a typealias declaration"
	case *ast.InterfaceDef:
		return "an interface declaration"
	case *ast.ImplBlock:
		return "an impl block"
	case *ast.ImplConformance:
		return "a derive declaration"
	case *ast.OnceBinding:
		return "a once binding"
	default:
		return "this declaration kind"
	}
}

func attachDeclarationAttachedTests(node ast.Node, tests []ast.AttachedTest, context string) error {
	if len(tests) == 0 {
		return nil
	}
	if !attachAttachedTests(node, tests) {
		return errorAt(tests[0].Line, tests[0].Col, "attached test prompt is not supported on %s", context)
	}
	return nil
}

func attachAttachedTests(node ast.Node, tests []ast.AttachedTest) bool {
	switch n := node.(type) {
	case *ast.FuncDef:
		n.AttachedTests = tests
	case *ast.ExternFunc:
		n.AttachedTests = tests
	case *ast.StructDef:
		n.AttachedTests = tests
	case *ast.EnumDef:
		n.AttachedTests = tests
	case *ast.TypeDef:
		n.AttachedTests = tests
	case *ast.ExternType:
		n.AttachedTests = tests
	case *ast.TypeAlias:
		n.AttachedTests = tests
	case *ast.InterfaceDef:
		n.AttachedTests = tests
	case *ast.ImplBlock:
		n.AttachedTests = tests
	case *ast.ImplConformance:
		n.AttachedTests = tests
	case *ast.OnceBinding:
		n.AttachedTests = tests
	default:
		return false
	}
	return true
}

func rejectAttachedTestsOnNonDeclaration(tests []ast.AttachedTest, context string) error {
	if len(tests) == 0 {
		return nil
	}
	return errorAt(tests[0].Line, tests[0].Col, "attached test prompt is not supported on %s", context)
}

func (p *Parser) parseStmtInner() (ast.Node, error) {
	// Inline visibility modifier: `pub fn`, `pub type`, `pub opaque type`,
	// `pub interface`, `pub typealias`, `pub once`, and `pub host (fn|type)`.
	if p.peek().Type == token.PUB {
		pubTok := p.peek()
		p.advance() // consume PUB
		if p.atEnd() {
			return nil, errorAt(pubTok.Line, pubTok.Col, "expected 'fn', 'struct', 'enum', 'type', 'opaque', 'interface', 'typealias', 'once', or 'host' after 'pub'")
		}
		switch p.peek().Type {
		case token.FN:
			return p.parseFuncDef(true, false)
		case token.STRUCT:
			return p.parseStructDef(true, false)
		case token.ENUM:
			return p.parseEnumDef(true, false)
		case token.TYPE:
			return p.parseTypeDefOrForeignBinding(true, false)
		case token.OPAQUE:
			p.advance() // consume OPAQUE
			if p.atEnd() {
				return nil, errorAt(pubTok.Line, pubTok.Col, "'pub opaque' must be followed by 'struct', 'enum', or 'type'")
			}
			switch p.peek().Type {
			case token.STRUCT:
				return p.parseStructDef(true, true)
			case token.ENUM:
				return p.parseEnumDef(true, true)
			case token.TYPE:
				return p.parseTypeDefOrForeignBinding(true, true)
			default:
				return nil, errorAt(pubTok.Line, pubTok.Col, "'pub opaque' must be followed by 'struct', 'enum', or 'type'")
			}
		case token.INTERFACE:
			return p.parseInterfaceDef(true)
		case token.TYPEALIAS:
			return p.parseTypeAlias(true)
		case token.ONCE:
			return p.parseOnceBinding(true)
		case token.IDENT:
			if p.peek().Lexeme != "host" {
				return nil, errorAt(pubTok.Line, pubTok.Col, "'pub' is not valid before %s", p.peek().Type)
			}
			return p.parseHost(true, false)
		case token.EXTERN:
			return nil, errorAt(pubTok.Line, pubTok.Col, "runtime-provided declarations use `host`, not `extern`")
		default:
			return nil, errorAt(pubTok.Line, pubTok.Col, "'pub' is not valid before %s", p.peek().Type)
		}
	}

	if p.peek().Type == token.IDENT && p.peek().Lexeme == "gopkg" {
		return p.parseGoPackageDecl()
	}

	if p.peek().Type == token.IDENT && p.peek().Lexeme == "go" {
		if p.peekAt(1).Type == token.LBRACE {
			return p.parseGoBlock()
		}
		return nil, errorAt(p.peek().Line, p.peek().Col, "Go package handles use `gopkg \"import/path\"`")
	}

	// `extern` is reserved for the FFI surface, but runtime-provided
	// declarations use the clearer `host` spelling.
	if p.peek().Type == token.EXTERN {
		return p.parseExtern(false, false)
	}

	// Host declarations: host fn ... or host type ...
	if p.startsHostDecl() {
		return p.parseHost(false, false)
	}

	// Function definition: func name(...) { ... }
	if p.peek().Type == token.FN {
		return p.parseFuncDef(false, false)
	}

	// Interface definition: Interface Name { ... }
	if p.peek().Type == token.INTERFACE {
		return p.parseInterfaceDef(false)
	}

	if p.peek().Type == token.IDENT && p.peek().Lexeme == "implements" {
		return nil, errorAt(p.peek().Line, p.peek().Col, "interface implementation uses `impl`, not `implements`")
	}

	// Top-level derive: derive [<generics>] Iface for Receiver.
	if p.peek().Type == token.IDENT && p.peek().Lexeme == "derive" {
		return p.parseDeriveDecl()
	}

	// Top-level impl: impl [<generics>] Iface for Receiver { ... }.
	// The header names the one interface; the body holds plain `fn` /
	// `host fn` methods.
	if p.peek().Type == token.IMPL {
		return p.parseImplBlock()
	}

	// Import statement: import Path
	if p.peek().Type == token.IMPORT {
		return p.parseImportStmt()
	}

	// Visibility is declared inline with `pub` on each declaration; there
	// is no top-level `export` block. Consume the EXPORT token (and
	// synchronize past any malformed body) so the recovery loop doesn't
	// spin on the same token forever.
	if p.peek().Type == token.EXPORT {
		exportTok := p.peek()
		p.advance() // consume EXPORT
		p.synchronize()
		return nil, errorAt(exportTok.Line, exportTok.Col, "unexpected 'export' — declare visibility inline with `pub` on each declaration")
	}

	// Once binding: once name: T = expression
	if p.peek().Type == token.ONCE {
		return p.parseOnceBinding(false)
	}

	// Struct definition: struct Name { ... }
	if p.peek().Type == token.STRUCT {
		return p.parseStructDef(false, false)
	}

	// Enum definition: enum Name { ... }
	if p.peek().Type == token.ENUM {
		return p.parseEnumDef(false, false)
	}

	// Type definition or external type binding: type Name [InnerType] /
	// [opaque] type RawConn go ffi.Conn
	if p.peek().Type == token.TYPE {
		return p.parseTypeDefOrForeignBinding(false, false)
	}

	// Opaque distinct-type / struct / enum (private): opaque (struct|enum|type) Name ...
	if p.peek().Type == token.OPAQUE {
		opaqueTok := p.peek()
		p.advance() // consume OPAQUE
		if p.atEnd() {
			return nil, errorAt(opaqueTok.Line, opaqueTok.Col, "'opaque' must be followed by 'struct', 'enum', or 'type'")
		}
		switch p.peek().Type {
		case token.STRUCT:
			return p.parseStructDef(false, true)
		case token.ENUM:
			return p.parseEnumDef(false, true)
		case token.TYPE:
			return p.parseTypeDefOrForeignBinding(false, true)
		default:
			return nil, errorAt(opaqueTok.Line, opaqueTok.Col, "'opaque' must be followed by 'struct', 'enum', or 'type'")
		}
	}

	// Type alias: typealias Name Target
	if p.peek().Type == token.TYPEALIAS {
		return p.parseTypeAlias(false)
	}

	// Return statement: return [expr]
	if p.peek().Type == token.RETURN {
		return p.parseReturn()
	}

	// Break statement: break [expr]
	if p.peek().Type == token.BREAK {
		return p.parseBreak()
	}

	// Continue statement: continue
	if p.peek().Type == token.CONTINUE {
		return p.parseContinue()
	}

	if p.peek().Type == token.DEFER {
		return p.parseDefer()
	}

	// `with App.field = value`: an application-field override lasting to
	// the end of the enclosing block.
	if p.peek().Type == token.WITH {
		return p.parseWith()
	}

	// Test declarations: test "name" { ... } / tests "name" { ... }
	if p.peek().Type == token.TEST || p.peek().Type == token.TESTS {
		return p.parseTestDecl()
	}

	// Assertions: assert expr / refute expr / assert pattern = expr
	if p.peek().Type == token.ASSERT || p.peek().Type == token.REFUTE {
		return p.parseAssertion()
	}

	// Map destructuring: {keyExpr => ident, ...} = expr
	if p.peek().Type == token.LBRACE {
		savedPos := p.pos
		line := p.peek().Line
		col := p.peek().Col
		p.advance() // consume LBRACE
		p.skipNewlines()

		// Speculatively parse the first key as an expression; map destructure
		// iff it's followed by '=>'.
		isMapDestructure := !p.atEnd() && p.detectMapPatternEntry()

		if isMapDestructure {
			var entries []ast.MapPatternEntry
			valid := true
			for valid && !p.atEnd() && p.peek().Type != token.RBRACE {
				// The key is an arbitrary expression (same parser map literals
				// use for keys). A parse failure means this isn't a map
				// destructure — restore below and try struct destructure.
				keyNode, kerr := p.parseExpr(1)
				if kerr != nil {
					valid = false
					break
				}
				if p.atEnd() || p.peek().Type != token.FAT_ARROW {
					valid = false
					break
				}
				p.advance() // consume FAT_ARROW
				if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.UNDERSCORE) {
					valid = false
					break
				}
				bindTok := p.peek()
				var pat ast.Node
				if bindTok.Type == token.UNDERSCORE {
					pat = &ast.WildcardPattern{Line: bindTok.Line, Col: bindTok.Col}
				} else {
					pat = &ast.IdentPattern{Name: bindTok.Lexeme, Line: bindTok.Line, Col: bindTok.Col}
				}
				p.advance()
				entries = append(entries, ast.MapPatternEntry{Key: keyNode, Pattern: pat})

				if !p.atEnd() && (p.peek().Type == token.COMMA || p.peek().Type == token.NEWLINE) {
					p.advance()
					p.skipNewlines()
				}
			}

			if valid && len(entries) > 0 && !p.atEnd() && p.peek().Type == token.RBRACE {
				p.advance() // consume RBRACE
				if !p.atEnd() && p.peek().Type == token.EQ {
					p.advance() // consume EQ
					val, err := p.parseExpr(1)
					if err != nil {
						return nil, err
					}
					return p.withBindingElse(savedPos, &ast.MapDestructure{Entries: entries, Value: val, Line: line, Col: col}, val, line, col)
				}
			}

			// Not a map destructure — restore position
			p.pos = savedPos
		} else {
			// Restore position so struct destructure can try
			p.pos = savedPos
		}
	}

	// Struct destructuring: {ident, ident} = expr  or  {ident: ident, ...} = expr
	if p.peek().Type == token.LBRACE {
		savedPos := p.pos
		line := p.peek().Line
		col := p.peek().Col
		p.advance() // consume LBRACE
		p.skipNewlines()

		var fields []ast.StructPatternField
		valid := true
		for valid {
			if p.atEnd() || p.peek().Type != token.IDENT {
				valid = false
				break
			}
			fieldTok := p.peek()
			fieldName := fieldTok.Lexeme
			p.advance()
			spf := ast.StructPatternField{Name: fieldName, NameLine: fieldTok.Line, NameCol: fieldTok.Col, Binding: fieldName, BindingLine: fieldTok.Line, BindingCol: fieldTok.Col}

			if !p.atEnd() && p.peek().Type == token.COLON {
				p.advance() // consume COLON
				if p.atEnd() || p.peek().Type != token.IDENT {
					valid = false
					break
				}
				spf.Binding = p.peek().Lexeme
				spf.BindingLine = p.peek().Line
				spf.BindingCol = p.peek().Col
				p.advance()
			}
			fields = append(fields, spf)

			// Skip comma/newline separators
			if !p.atEnd() && (p.peek().Type == token.COMMA || p.peek().Type == token.NEWLINE) {
				p.advance()
				p.skipNewlines()
			}

			if !p.atEnd() && p.peek().Type == token.RBRACE {
				break
			}
		}

		if valid && len(fields) > 0 && !p.atEnd() && p.peek().Type == token.RBRACE {
			p.advance() // consume RBRACE
			if !p.atEnd() && p.peek().Type == token.EQ {
				p.advance() // consume EQ
				val, err := p.parseExpr(1)
				if err != nil {
					return nil, err
				}
				return p.withBindingElse(savedPos, &ast.StructDestructure{Fields: fields, Value: val, Line: line, Col: col}, val, line, col)
			}
		}

		// Not a struct destructure — restore position
		p.pos = savedPos
	}

	// Tuple destructuring: (ident, ident, ...) = expr
	if p.peek().Type == token.LPAREN {
		savedPos := p.pos
		line := p.peek().Line
		col := p.peek().Col
		p.advance() // consume LPAREN

		var bindings []*ast.Ident
		valid := true
		for valid {
			if p.atEnd() {
				valid = false
				break
			}
			t := p.peek()
			if t.Type == token.IDENT {
				bindings = append(bindings, &ast.Ident{Name: t.Lexeme, Line: t.Line, Col: t.Col})
				p.advance()
			} else if t.Type == token.UNDERSCORE {
				bindings = append(bindings, nil) // nil = wildcard
				p.advance()
			} else {
				valid = false
				break
			}

			if p.atEnd() {
				valid = false
				break
			}
			if p.peek().Type == token.RPAREN {
				p.advance() // consume RPAREN
				break
			}
			if p.peek().Type != token.COMMA {
				valid = false
				break
			}
			p.advance() // consume COMMA
		}

		if valid && len(bindings) >= 2 && !p.atEnd() && p.peek().Type == token.EQ {
			p.advance() // consume EQ
			val, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			return p.withBindingElse(savedPos, &ast.TupleDestructure{Bindings: bindings, Value: val, Line: line, Col: col}, val, line, col)
		}

		// Not a tuple destructure — restore position
		p.pos = savedPos
	}

	// Distinct type destructure: TypeName(binding) = expr or
	// module.TypeName(binding) = expr.
	if p.peek().Type == token.TYPE_IDENT || (p.peek().Type == token.IDENT && p.peekAt(1).Type == token.DOT) {
		savedPos := p.pos
		typeExpr, typeName, typeLine, typeCol, ok := p.parseDistinctDestructureTypePrefix()
		if !ok || p.atEnd() || p.peek().Type != token.LPAREN {
			p.pos = savedPos
		} else {
			p.advance() // consume LPAREN

			valid := false
			var binding *ast.Ident
			if !p.atEnd() {
				bt := p.peek()
				if bt.Type == token.IDENT {
					binding = &ast.Ident{Name: bt.Lexeme, Line: bt.Line, Col: bt.Col}
					p.advance()
					valid = true
				} else if bt.Type == token.UNDERSCORE {
					p.advance()
					valid = true
				}
			}
			if valid && !p.atEnd() && p.peek().Type == token.RPAREN {
				p.advance() // consume RPAREN
				if !p.atEnd() && p.peek().Type == token.EQ {
					p.advance() // consume EQ
					value, err := p.parseExpr(1)
					if err != nil {
						return nil, err
					}
					return p.withBindingElse(savedPos, &ast.DistinctDestructure{
						TypeName:     typeName,
						TypeNameExpr: typeExpr,
						Binding:      binding,
						Value:        value,
						Line:         typeLine,
						Col:          typeCol,
					}, value, typeLine, typeCol)
				}
			}
			p.pos = savedPos
		}
	}

	// Binding: IDENT EQ expr / _ EQ expr / IDENT COLON TypeExpr EQ expr /
	// _ COLON TypeExpr EQ expr (but not IDENT EQEQ — that's an equality
	// expression).
	if (p.peek().Type == token.IDENT || p.peek().Type == token.UNDERSCORE) &&
		(p.peekAt(1).Type == token.EQ || p.peekAt(1).Type == token.COLON) {
		start := p.pos
		tok := p.peek()
		p.advance() // consume binding name

		var typeAnn ast.TypeExpr
		if p.peek().Type == token.COLON {
			p.advance() // consume COLON
			ann, err := p.parseTypeAnnotation()
			if err != nil {
				return nil, err
			}
			typeAnn = ann
		}

		if p.peek().Type != token.EQ {
			got := p.peek()
			return nil, errorAt(got.Line, got.Col, "expected `=` after binding name%s, got %s", annotationContext(typeAnn), got.Type)
		}
		p.advance() // consume EQ

		value, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		return p.withBindingElse(start, &ast.Binding{
			Name:           tok.Lexeme,
			TypeAnnotation: typeAnn,
			Value:          value,
			Line:           tok.Line,
			Col:            tok.Col,
		}, value, tok.Line, tok.Col)
	}

	// Any other pattern followed by `=`: `Ok((w, h)) = parse(text) else { ... }`.
	if canStartPatternAssertion(p.peek().Type) {
		if node, ok, err := p.tryParsePatternBinding(); ok || err != nil {
			return node, err
		}
	}

	expr, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}

	return &ast.ExprStmt{Expr: expr, Line: expr.LineNum(), Col: 0}, nil
}

// parseExpr is the Pratt parsing core.
func (p *Parser) parseExpr(minPrec int) (ast.Node, error) {
	return p.parseExprWithPipeStop(minPrec, false)
}

func (p *Parser) parseExprWithPipeStop(minPrec int, stopAtPipe bool) (ast.Node, error) {
	start := p.pos
	left, err := p.parsePrefixWithPipeStop(stopAtPipe)
	if err != nil {
		return nil, err
	}

	for {
		// Each operand, call and access the loop builds spans from the
		// expression's first token to the last one consumed so far.
		p.recordSpan(left, start)
		if p.atEnd() {
			break
		}
		var leadingForRight []ast.Trivia
		if trivia, ok := p.consumeStandaloneInfixComment(minPrec); ok {
			leadingForRight = trivia
		}
		// Tolerate a trailing comment immediately followed by an infix
		// continuation (e.g., `foo() // note` on one line, `|> bar()` on
		// the next). When the token after the COMMENT would itself advance
		// the expression, we consume the COMMENT silently so the expression
		// keeps parsing. Otherwise we leave the COMMENT in place so the
		// statement-level trailing-comment logic can attach it.
		if p.peek().Type == token.COMMENT {
			next := p.peekAt(1).Type
			if infixPrecedence(next) >= minPrec {
				// Mid-expression COMMENT tokens between infix operators attach
				// as TRAILING trivia so formatters can emit them after the
				// corresponding step (e.g. mid-pipe trailing comments:
				// `foo() // note` before `|> bar()`).
				//
				// When `left` is itself a Binary (we're in the middle of a
				// chain), attach to its RHS — that's the "step" the comment
				// actually follows. Otherwise attach to `left` itself (first
				// step in a chain).
				tk := p.peek()
				target := left
				if b, ok := left.(*ast.Binary); ok && b.Right != nil {
					target = b.Right
				}
				if ht, ok := target.(ast.HasTrivia); ok {
					ht.AddTrailing(ast.Trivia{
						Kind: ast.TriviaComment,
						Text: tk.Lexeme,
						Line: tk.Line,
						Col:  tk.Col,
					})
				}
				p.advance()
				continue
			}
		}
		// Turbofish: `f<T>(...)` — explicit type args on a call. Speculatively
		// parse a `<Type, ...>` list immediately followed by `(`; otherwise `<`
		// is the comparison operator handled below. Restricted to callable
		// left-hand sides (a name or a module/field path). The trailing-`(`
		// requirement keeps this from misreading ordinary comparisons; the only
		// residual ambiguity, `expr < Type > (args)`, is virtually never a valid
		// comparison (it would compare a Bool).
		if p.peek().Type == token.LT && isTurbofishCallable(left) {
			if typeArgs, ok := p.tryParseTurbofish(); ok {
				lp := p.peek()
				p.advance() // consume LPAREN
				args, err := p.parseArgList()
				if err != nil {
					return nil, err
				}
				left = &ast.Call{Func: left, Args: args, TypeArgs: typeArgs, Line: lp.Line, Col: lp.Col}
				continue
			}
		}

		tok := p.peek()
		if stopAtPipe && tok.Type == token.PIPE {
			break
		}
		prec := infixPrecedence(tok.Type)
		if prec < minPrec {
			break
		}

		switch tok.Type {
		case token.DOT:
			p.advance() // consume DOT
			field := p.peek()
			if field.Type != token.IDENT && field.Type != token.TYPE_IDENT && field.Type != token.INT {
				return nil, errorAt(tok.Line, tok.Col, "expected field name after '.'")
			}
			p.advance()
			left = &ast.FieldAccess{Object: left, Field: &ast.Ident{Name: field.Lexeme, Line: field.Line, Col: field.Col}, Line: tok.Line, Col: tok.Col}

		case token.LPAREN:
			p.advance() // consume LPAREN
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			left = &ast.Call{Func: left, Args: args, Line: tok.Line, Col: tok.Col}

		case token.DOTDOT, token.DOTDOTEQ:
			// Bounded range operator: requires both operands. The
			// open-ended forms (`5..`, `5..=`) have been removed —
			// use `Range.from(N)` instead.
			p.advance() // consume DOTDOT/DOTDOTEQ
			if p.atEnd() || !canStartRangeOperand(p.peek().Type) {
				return nil, errorAt(tok.Line, tok.Col, "range requires a right-hand operand; "+
					"open-ended ranges have been removed — "+
					"use `Range.from(N)` for unbounded ascending from N")
			}
			end, err := p.parseExprWithPipeStop(prec+1, stopAtPipe)
			if err != nil {
				return nil, err
			}
			left = &ast.RangeLit{
				Start:     left,
				End:       end,
				Inclusive: tok.Type == token.DOTDOTEQ,
				Line:      tok.Line, Col: tok.Col,
			}

		case token.PIPE:
			p.advance()
			right, err := p.parsePipeRight(left, tok, leadingForRight)
			if err != nil {
				return nil, err
			}
			left = right

		default:
			p.advance()
			// Left-associative: use prec+1 for the right operand.
			right, err := p.parseExprWithPipeStop(prec+1, stopAtPipe)
			if err != nil {
				return nil, err
			}
			for _, tr := range leadingForRight {
				if hasTrivia, ok := right.(ast.HasTrivia); ok {
					hasTrivia.AddLeading(tr)
				}
			}
			left = &ast.Binary{
				Left:  left,
				Op:    tok.Lexeme,
				Right: right,
				Line:  tok.Line,
				Col:   tok.Col,
			}
		}
	}

	p.recordSpan(left, start)
	return left, nil
}

func (p *Parser) parsePipeRight(left ast.Node, pipeTok token.Token, leadingForRight []ast.Trivia) (ast.Node, error) {
	switch {
	case p.atPipeValueKeyword():
		return p.parsePipeValueKeywordStage(left, pipeTok, leadingForRight)
	case !p.atEnd() && p.peek().Type == token.IF:
		return p.parsePipeIfStage(left, pipeTok, leadingForRight)
	case !p.atEnd() && p.peek().Type == token.CASE:
		return p.parsePipeCaseStage(left, pipeTok, leadingForRight)
	case !p.atEnd() && p.peek().Type == token.THEN:
		return p.parsePipeThenStage(left, pipeTok, leadingForRight)
	default:
		right, err := p.parseExpr(infixPrecedence(token.PIPE) + 1)
		if err != nil {
			return nil, err
		}
		for _, tr := range leadingForRight {
			if hasTrivia, ok := right.(ast.HasTrivia); ok {
				hasTrivia.AddLeading(tr)
			}
		}
		return pipeNode(left, right, pipeTok.Line, pipeTok.Col), nil
	}
}

// parsePipeThenStage parses `|> then |v| body`, the stage that applies a
// lambda to the piped value. The lambda's body ends at the next `|>` of the
// pipeline, the one place a lambda body does not run to the end of its
// expression; braces (`then |v| { v |> f() }`) keep a pipe inside it.
func (p *Parser) parsePipeThenStage(left ast.Node, pipeTok token.Token, leadingForRight []ast.Trivia) (ast.Node, error) {
	start := p.pos
	tok := p.peek()
	p.advance() // consume THEN
	if p.atEnd() || p.peek().Type != token.BAR {
		return nil, errorAt(tok.Line, tok.Col, "`then` takes a lambda: write `then |v| ...`")
	}
	lamStart := p.pos
	lam, err := p.parseLambdaStop(true)
	if err != nil {
		return nil, err
	}
	p.recordSpan(lam, lamStart)
	stage := &ast.Then{Lambda: lam.(*ast.Lambda), Line: tok.Line, Col: tok.Col}
	p.recordSpan(stage, start)
	for _, tr := range leadingForRight {
		stage.AddLeading(tr)
	}
	return pipeNode(left, stage, pipeTok.Line, pipeTok.Col), nil
}

func (p *Parser) parsePipeValueKeywordStage(left ast.Node, pipeTok token.Token, leadingForRight []ast.Trivia) (ast.Node, error) {
	var keywords []ast.Node
	for p.atPipeValueKeyword() {
		kw := p.parseBarePipeValueKeyword()
		keywords = append(keywords, kw)
		if !p.atEnd() && p.peek().Type == token.THEN && p.peek().Line == kw.LineNum() {
			return nil, errorAt(p.peek().Line, p.peek().Col, "%s does not prefix a `then` stage: write `|> then |v| ...` and `|> %s` as two stages",
				pipeKeywordName(kw), strings.Trim(pipeKeywordName(kw), "`"))
		}
		if p.atEnd() || p.peek().Line != kw.LineNum() || !canStartRangeOperand(p.peek().Type) {
			if len(keywords) == 1 {
				for _, tr := range leadingForRight {
					if hasTrivia, ok := kw.(ast.HasTrivia); ok {
						hasTrivia.AddLeading(tr)
					}
				}
				return pipeNode(left, kw, pipeTok.Line, pipeTok.Col), nil
			}
			return nil, p.errorOnLine(kw.LineNum(), "keyword-prefixed pipe stage requires an expression after %s", pipeKeywordName(kw))
		}
	}

	stage, err := p.parseExpr(infixPrecedence(token.PIPE) + 1)
	if err != nil {
		return nil, err
	}
	for _, tr := range leadingForRight {
		if hasTrivia, ok := stage.(ast.HasTrivia); ok {
			hasTrivia.AddLeading(tr)
		}
	}

	result := pipeNode(left, stage, pipeTok.Line, pipeTok.Col)
	for i := len(keywords) - 1; i >= 0; i-- {
		result = pipeNode(result, keywords[i], keywords[i].LineNum(), 0)
	}
	return result, nil
}

func (p *Parser) parsePipeIfStage(left ast.Node, pipeTok token.Token, leadingForRight []ast.Trivia) (ast.Node, error) {
	stageStart := p.pos
	tok := p.peek()
	p.advance() // consume IF

	var condition ast.Node
	if p.atEnd() || p.peek().Type != token.LBRACE {
		oldNoStructLit := p.noStructLit
		p.noStructLit = true
		var err error
		condition, err = p.parseExpr(1)
		p.noStructLit = oldNoStructLit
		if err != nil {
			return nil, err
		}
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after if condition")
	}
	thenNode, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	var elseNode ast.Node
	savedPos := p.pos
	for !p.atEnd() && p.peek().Type == token.NEWLINE {
		p.advance()
	}
	if !p.atEnd() && p.peek().Type == token.ELSE {
		p.advance()
		if !p.atEnd() && p.peek().Type == token.IF {
			elseNode, err = p.parseIf()
			if err != nil {
				return nil, err
			}
		} else if !p.atEnd() && p.peek().Type == token.LBRACE {
			elseNode, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, errorAt(tok.Line, tok.Col, "expected '{' or 'if' after else")
		}
	} else {
		p.pos = savedPos
	}

	ifNode := &ast.If{Cond: nil, Then: thenNode.(*ast.Block), Else: elseNode, Line: tok.Line, Col: tok.Col}
	p.recordSpan(ifNode, stageStart)
	for _, tr := range leadingForRight {
		ifNode.AddLeading(tr)
	}

	if condition == nil {
		return pipeNode(left, ifNode, pipeTok.Line, pipeTok.Col), nil
	}
	return pipeNode(pipeNode(left, condition, pipeTok.Line, pipeTok.Col), ifNode, tok.Line, tok.Col), nil
}

func (p *Parser) parsePipeCaseStage(left ast.Node, pipeTok token.Token, leadingForRight []ast.Trivia) (ast.Node, error) {
	stageStart := p.pos
	tok := p.peek()
	p.advance() // consume CASE

	var value ast.Node
	if p.atEnd() || p.peek().Type != token.LBRACE {
		var err error
		value, err = p.parseExpr(1)
		if err != nil {
			return nil, err
		}
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after case")
	}
	p.advance()

	var branches []ast.CaseBranch
	var endTrivia []ast.Trivia
	for {
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.RBRACE {
			endTrivia = leading
			break
		}
		branch, err := p.parseCaseBranch(true)
		if err != nil {
			return nil, err
		}
		var trailing []ast.Trivia
		if branch.Body != nil {
			trailing = p.collectTrailingComment(branch.Body.LineNum())
		}
		attachTrivia(&branch, leading, trailing)
		branches = append(branches, branch)
		for !p.atEnd() && p.peek().Type == token.COMMA {
			p.advance()
		}
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '}' to close case expression")
	}
	p.advance()

	caseNode := &ast.Case{Value: nil, Branches: branches, Line: tok.Line, Col: tok.Col}
	p.recordSpan(caseNode, stageStart)
	for _, tr := range leadingForRight {
		caseNode.AddLeading(tr)
	}
	for _, tr := range endTrivia {
		caseNode.AddTrailing(tr)
	}

	if value == nil {
		return pipeNode(left, caseNode, pipeTok.Line, pipeTok.Col), nil
	}
	return pipeNode(pipeNode(left, value, pipeTok.Line, pipeTok.Col), caseNode, tok.Line, tok.Col), nil
}

func (p *Parser) atPipeValueKeyword() bool {
	if p.atEnd() {
		return false
	}
	tok := p.peek()
	switch tok.Type {
	case token.TRY, token.ASSERT, token.REFUTE, token.DBG:
		return true
	default:
		return false
	}
}

func (p *Parser) parseBarePipeValueKeyword() ast.Node {
	start := p.pos
	node := p.parseBarePipeValueKeywordInner()
	p.recordSpan(node, start)
	return node
}

func (p *Parser) parseBarePipeValueKeywordInner() ast.Node {
	tok := p.peek()
	p.advance()
	switch tok.Type {
	case token.TRY:
		return &ast.TryOp{Line: tok.Line, Col: tok.Col}
	case token.ASSERT:
		return &ast.Assertion{Line: tok.Line, Col: tok.Col}
	case token.REFUTE:
		return &ast.Assertion{Refute: true, Line: tok.Line, Col: tok.Col}
	case token.DBG:
		return &ast.Dbg{Line: tok.Line, Col: tok.Col}
	}
	return &ast.Ident{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}
}

// parseTodo parses `todo` and `todo "reason"`. The reason is optional and,
// when present, starts on the `todo`'s line. It is a string literal in any of
// the four uninterpolated forms (`"..."`, `"""..."""` and the two raw forms):
// the reason is fixed text that `nomi build` and the editor list without
// running anything, so an interpolated or tagged string is rejected here.
func (p *Parser) parseTodo() (ast.Node, error) {
	tok := p.peek()
	p.advance()
	n := &ast.Todo{Line: tok.Line, Col: tok.Col}
	if p.atEnd() || p.peek().Line != tok.Line {
		return n, nil
	}
	switch next := p.peek(); next.Type {
	case token.STRING_LITERAL, token.TRIPLE_STRING_LITERAL,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL:
		reasonStart := p.pos
		reason, err := p.parsePrefix()
		if err != nil {
			return nil, err
		}
		p.recordSpan(reason, reasonStart)
		n.Reason = reason.(*ast.StringLit)
	case token.STRING_START, token.TRIPLE_STRING_START,
		token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_START,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
		return nil, errorAt(next.Line, next.Col,
			"a `todo` reason is a plain string literal: it cannot interpolate or carry a tag")
	}
	return n, nil
}

func pipeNode(left, right ast.Node, line, col int) *ast.Binary {
	b := &ast.Binary{Left: left, Op: "|>", Right: right, Line: line, Col: col}
	if l, ok := left.(ast.HasSpan); ok {
		if r, ok := right.(ast.HasSpan); ok {
			ls, rs := l.GetSpan(), r.GetSpan()
			if !ls.IsZero() && !rs.IsZero() {
				b.Span = ast.Span{StartLine: ls.StartLine, StartCol: ls.StartCol, EndLine: rs.EndLine, EndCol: rs.EndCol}
			}
		}
	}
	return b
}

func pipeKeywordName(n ast.Node) string {
	switch v := n.(type) {
	case *ast.TryOp:
		return "`try`"
	case *ast.Dbg:
		return "`dbg`"
	case *ast.Assertion:
		if v.Refute {
			return "`refute`"
		}
		return "`assert`"
	default:
		return "keyword"
	}
}

// consumeStandaloneInfixComment recognizes a comment line between the current
// expression and an infix continuation:
//
//	x
//	// note about the next step
//	|> f()
//
// Same-line comments (`x // note`) stay trailing trivia on the left operand;
// standalone comments are leading trivia for the right operand so the formatter
// can keep them on their own line before the operator.
func (p *Parser) consumeStandaloneInfixComment(minPrec int) ([]ast.Trivia, bool) {
	if p.atEnd() {
		return nil, false
	}
	tt := p.peek().Type
	if tt != token.NEWLINE && tt != token.SEMICOLON {
		return nil, false
	}
	j := p.pos
	for j < len(p.tokens) {
		tt := p.tokens[j].Type
		if tt != token.NEWLINE && tt != token.SEMICOLON {
			break
		}
		j++
	}
	if j >= len(p.tokens) || p.tokens[j].Type != token.COMMENT {
		return nil, false
	}
	k := j + 1
	for k < len(p.tokens) {
		tt := p.tokens[k].Type
		if tt != token.NEWLINE && tt != token.SEMICOLON {
			break
		}
		k++
	}
	if k >= len(p.tokens) || infixPrecedence(p.tokens[k].Type) < minPrec {
		return nil, false
	}
	tk := p.tokens[j]
	p.pos = k
	return []ast.Trivia{{
		Kind: ast.TriviaComment,
		Text: tk.Lexeme,
		Line: tk.Line,
		Col:  tk.Col,
	}}, true
}

// parseArgList parses a comma-separated list of expressions until RPAREN.
// Accepts an optional trailing comma.
func (p *Parser) parseArgList() ([]ast.Node, error) {
	// An argument list is delimited, so a struct literal in it is
	// unambiguous wherever the call sits.
	oldNoStructLit := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = oldNoStructLit }()
	var args []ast.Node
	if !p.atEnd() && p.peek().Type == token.RPAREN {
		p.advance()
		return args, nil
	}
	seenNamed := false
	for {
		// Check for named argument: IDENT COLON expr
		if p.peek().Type == token.IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type == token.COLON {
			name := p.peek().Lexeme
			line := p.peek().Line
			col := p.peek().Col
			p.advance() // consume IDENT
			p.advance() // consume COLON
			val, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			args = append(args, &ast.NamedArg{Name: name, Value: val, Line: line, Col: col})
			seenNamed = true
		} else {
			argLine := p.peek().Line
			arg, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			if seenNamed {
				// A single *final* positional is allowed after named args:
				// slot routing fills the first unclaimed slot, so with one
				// argument left there is nothing to be ambiguous about, and
				// `each(items, opts: …, |x| …)`-style calls are too common
				// to require naming the callback. More than one, or one that
				// is not last, is the usual "positional after named" error.
				//
				// The test is the slot, not the syntax: `each(items, opts: …,
				// handler)` and `each(items, opts: …, |x| handler(x))` pass
				// the same kind of value to the same parameter, so accepting
				// one and rejecting the other would be a rule about how the
				// argument was written.
				nextIsClose := !p.atEnd() && p.peek().Type == token.RPAREN
				nextIsTrailingComma := !p.atEnd() && p.peek().Type == token.COMMA && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type == token.RPAREN
				if !nextIsClose && !nextIsTrailingComma {
					return nil, p.errorOnLine(argLine, "positional argument after named argument")
				}
			}
			args = append(args, arg)
		}
		if p.atEnd() {
			return nil, fmt.Errorf("expected ')' after arguments")
		}
		if p.peek().Type == token.RPAREN {
			p.advance()
			return args, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ')' in argument list")
		}
		p.advance() // consume COMMA
		// Allow trailing comma: COMMA followed by RPAREN closes the list.
		if !p.atEnd() && p.peek().Type == token.RPAREN {
			p.advance()
			return args, nil
		}
	}
}

// parsePrefixWithPipeStop is parsePrefix inside the body of a `then` stage
// when stopAtPipe is set: the prefixes whose operand runs to the end of the
// expression, a lambda and `dbg expr`, end it at the next `|>` instead.
func (p *Parser) parsePrefixWithPipeStop(stopAtPipe bool) (ast.Node, error) {
	if !stopAtPipe || p.atEnd() {
		return p.parsePrefix()
	}
	tok := p.peek()
	switch tok.Type {
	case token.BAR:
		return p.parseLambdaStop(true)
	case token.DBG:
		next := p.peekAt(1)
		if next.Line != tok.Line || !canStartRangeOperand(next.Type) {
			return p.parsePrefix()
		}
		p.advance()
		expr, err := p.parseExprWithPipeStop(1, true)
		if err != nil {
			return nil, err
		}
		return &ast.Dbg{Expr: expr, Line: tok.Line, Col: tok.Col}, nil
	}
	return p.parsePrefix()
}

// parsePrefix handles literals, unary operators, and grouping.
func (p *Parser) parsePrefix() (ast.Node, error) {
	tok := p.peek()

	switch tok.Type {
	case token.INT:
		p.advance()
		val, err := strconv.ParseInt(tok.Lexeme, 0, 64)
		if err != nil {
			return nil, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
		}
		return &ast.IntLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.FLOAT:
		p.advance()
		val, err := strconv.ParseFloat(tok.Lexeme, 64)
		if err != nil {
			return nil, errorAt(tok.Line, tok.Col, "invalid float %q", tok.Lexeme)
		}
		return &ast.FloatLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.DECIMAL:
		p.advance()
		// Keep the lexeme verbatim (incl. the trailing `d`);
		// rt.ParseDecimalLexeme strips the suffix and parses it so scale is
		// preserved (a Float would lose precision).
		return &ast.DecimalLit{Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.CODEPOINT_LITERAL:
		p.advance()
		return codepointLit(tok), nil

	case token.STRING_LITERAL:
		p.advance()
		return &ast.StringLit{Value: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.TRIPLE_STRING_LITERAL:
		p.advance()
		return &ast.StringLit{Value: tok.Lexeme, Triple: true, Line: tok.Line, Col: tok.Col}, nil

	case token.RAW_STRING_LITERAL:
		p.advance()
		return &ast.StringLit{Value: tok.Lexeme, Raw: true, Line: tok.Line, Col: tok.Col}, nil

	case token.RAW_TRIPLE_STRING_LITERAL:
		p.advance()
		return &ast.StringLit{Value: tok.Lexeme, Triple: true, Raw: true, Line: tok.Line, Col: tok.Col}, nil

	case token.TAGGED_STRING_LITERAL:
		p.advance()
		return &ast.TaggedString{
			Tag:   tok.Tag,
			Parts: []ast.StringPart{ast.StringText{Value: tok.Lexeme}},
			Line:  tok.Line, Col: tok.Col,
		}, nil

	case token.TAGGED_TRIPLE_STRING_LITERAL:
		p.advance()
		return &ast.TaggedString{
			Tag:    tok.Tag,
			Triple: true,
			Parts:  []ast.StringPart{ast.StringText{Value: tok.Lexeme}},
			Line:   tok.Line, Col: tok.Col,
		}, nil

	case token.RAW_TAGGED_STRING_LITERAL:
		p.advance()
		return &ast.TaggedString{
			Tag:   tok.Tag,
			Raw:   true,
			Parts: []ast.StringPart{ast.StringText{Value: tok.Lexeme}},
			Line:  tok.Line, Col: tok.Col,
		}, nil

	case token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
		p.advance()
		return &ast.TaggedString{
			Tag:    tok.Tag,
			Raw:    true,
			Triple: true,
			Parts:  []ast.StringPart{ast.StringText{Value: tok.Lexeme}},
			Line:   tok.Line, Col: tok.Col,
		}, nil

	case token.STRING_START, token.TRIPLE_STRING_START:
		return p.parseStringInterp()

	case token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_START:
		return p.parseTaggedStringInterp()

	case token.BANG:
		p.advance()
		right, err := p.parseExpr(prefixPrecedence)
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Op: "!", Right: right, Line: tok.Line, Col: tok.Col}, nil

	case token.TRY:
		// Prefix `try` error-propagation keyword: `try EXPR` produces an
		// ast.TryOp. The bare form is accepted only so `value |> try`
		// can use the same pipe-stage model as `dbg`/`assert`;
		// the checker rejects bare `try` outside a pipe. With an operand,
		// it binds exactly one postfix-level expression to its right —
		// calls, field access, and index (all prec >= tryOperandPrecedence,
		// looser than `*`) attach to the operand, while `+`/`|>`/etc.
		// (lower precedence) stay outside. So `try a() |> b()` is
		// `(try a()) |> b()`, `try a() + b()` is `(try a()) + b()`, and
		// `try a.b.c` is `try (a.b.c)`. Line/Col point at `try`.
		p.advance()
		if p.atEnd() || p.peek().Line != tok.Line || !canStartRangeOperand(p.peek().Type) {
			return &ast.TryOp{Line: tok.Line, Col: tok.Col}, nil
		}
		operand, err := p.parseExpr(tryOperandPrecedence)
		if err != nil {
			return nil, err
		}
		return &ast.TryOp{Expr: operand, Line: tok.Line, Col: tok.Col}, nil

	case token.ASSERT, token.REFUTE:
		return p.parseAssertion()

	case token.DBG:
		p.advance()
		if p.atEnd() || p.peek().Line != tok.Line || !canStartRangeOperand(p.peek().Type) {
			return &ast.Dbg{Line: tok.Line, Col: tok.Col}, nil
		}
		expr, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		return &ast.Dbg{Expr: expr, Line: tok.Line, Col: tok.Col}, nil

	case token.TODO:
		return p.parseTodo()

	case token.DOTDOT, token.DOTDOTEQ:
		// Open-ended range forms (`..5`, `..=5`, `5..`, `..`) have been
		// removed. Bare `..` in element position of a list literal is now
		// the spread token, handled by parseListLit before parsePrefix is
		// reached. Anywhere else, `..` without a left operand is an error.
		return nil, errorAt(tok.Line, tok.Col, "open-ended ranges have been removed; "+
			"use `0..N` / `0..=N` for closed lower bound, "+
			"`Range.from(N)` for unbounded ascending from N, "+
			"`Range.naturals()` for unbounded from 0")

	case token.MINUS:
		p.advance()
		right, err := p.parseExpr(prefixPrecedence)
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Op: "-", Right: right, Line: tok.Line, Col: tok.Col}, nil

	case token.UNDERSCORE:
		p.advance()
		return &ast.Placeholder{Line: tok.Line, Col: tok.Col}, nil

	case token.DOT:
		// Dot-leading variant shorthand: `.X`, `.X(args)`, `.X{...}`,
		// `.X[...]`, `.X{"k" => v}`. Must be followed by an uppercase
		// variant identifier (TYPE_IDENT). Resolution against an
		// expected enum type happens in the analyzer.
		if next := p.peekAt(1).Type; next == token.IDENT || next == token.INT {
			return p.parseFieldAccessor()
		}
		if p.peekAt(1).Type != token.TYPE_IDENT {
			return nil, errorAt(tok.Line, tok.Col, "expected a variant or field name after '.'")
		}
		p.advance() // consume DOT
		nameTok := p.peek()
		p.advance() // consume TYPE_IDENT
		// Literal-attach forms: `.X{...}` (struct or map by =>),
		// `.X[...]` (list). The TypeName field carries a DotVariantType,
		// signalling the analyzer to resolve against the expected enum.
		if !p.noStructLit && !p.atEnd() && p.peek().Type == token.LBRACE {
			dvt := &ast.DotVariantType{Name: nameTok.Lexeme, Line: tok.Line, Col: tok.Col}
			return p.parseStructLit(dvt, tok.Line, tok.Col)
		}
		if !p.atEnd() && p.peek().Type == token.LBRACKET {
			dvt := &ast.DotVariantType{Name: nameTok.Lexeme, Line: tok.Line, Col: tok.Col}
			return p.parseTypePrefixedListLit(dvt, tok.Line, tok.Col)
		}
		// Bare `.X` — wrapped in a DotVariant expression node. The
		// call-form `.X(args)` falls through to the postfix Call parser
		// since DotVariant lands here as a regular expression.
		return &ast.DotVariant{Name: nameTok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.IDENT:
		// Contextual keyword: `concurrent { ... }` is the structured-
		// concurrency scope from spec §20. We can't reserve
		// `concurrent` as a hard keyword in the lexer because the stdlib
		// `import std/tasks.{Task, spawn, await}` line — and any
		// user `import std/tasks.{...}` — relies on `concurrent`
		// lexing as IDENT in module-path positions. So we keep the
		// lexer producing IDENT and check the lexeme + lookahead here,
		// mirroring how `field` and `open` are contextual inside
		// interface bodies (and `embeds` after a struct/enum head).
		if tok.Lexeme == "concurrent" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type == token.LBRACE {
			return p.parseConcurrentBlock()
		}
		// A MODULE-QUALIFIED LITERAL-ATTACH HEAD. `shapes.Circle{r: 1}`,
		// `shapes.Shape.Ring{r: 1}` and `shapes.Bag.Items[1, 2]` name a type
		// or a variant through the module that exports it, and the brace or
		// bracket body belongs to that name.
		//
		// Without this, none of them parsed as a literal:
		// the body was taken as a separate anonymous struct or
		// list, so `shapes.Shape.Ring{r: 1}` reported
		// `non-final expression has type {r: Int}` and
		// `random.Error.OsEntropy{reason: "b"}` reported
		// `return type mismatch: expected Error, got {reason: String}`.
		//
		// THE BOUNDARY IS THE BRACE PATH, NOT THE DEPTH, and that is what
		// localises it here. `shapes.Shape.Square(4)` works at every depth
		// because a call is a POSTFIX form and the postfix parser already
		// walks a FieldAccess chain; `{` and `[` are literal-attach forms
		// that only the primary parser builds. The equivalent scan exists
		// directly below under `case token.TYPE_IDENT`, and it never fired
		// for these because the chain starts at a LOWERCASE module name.
		//
		// Scanned before anything is consumed, and only a body closes the
		// chain off — the same rule the TYPE_IDENT scan states: a chain
		// ending in anything else is ordinary member access, which the
		// postfix parser builds, and taking it here would change how
		// `shapes.Shape.Square` parses today.
		if p.peekAt(1).Type == token.DOT && p.peekAt(2).Type == token.TYPE_IDENT {
			segs := 0
			for p.peekAt(1+2*segs).Type == token.DOT && p.peekAt(2+2*segs).Type == token.TYPE_IDENT {
				segs++
			}
			term := p.peekAt(1 + 2*segs).Type
			if term == token.LBRACKET || (term == token.LBRACE && !p.noStructLit) {
				// Every segment but the last belongs to the owner: the
				// module alone for `shapes.Circle`, the module plus the enum
				// for `shapes.Shape.Ring`. ResolveTypeExpr keys on the joined
				// dotted name either way, and the enum spelling falls through
				// to checkStructLit's qualified-head lookup.
				owner := []string{tok.Lexeme}
				p.advance() // consume the module IDENT
				var lastTok token.Token
				for i := range segs {
					p.advance() // consume DOT
					lastTok = p.peek()
					p.advance() // consume the segment
					if i < segs-1 {
						owner = append(owner, lastTok.Lexeme)
					}
				}
				qualType := &ast.QualifiedType{
					Module:     strings.Join(owner, "."),
					ModuleLine: tok.Line,
					ModuleCol:  tok.Col,
					Member:     &ast.SimpleType{Name: lastTok.Lexeme, Line: lastTok.Line, Col: lastTok.Col},
				}
				if term == token.LBRACE {
					return p.parseStructLit(qualType, tok.Line, tok.Col)
				}
				return p.parseTypePrefixedListLit(qualType, tok.Line, tok.Col)
			}
		}
		p.advance()
		return &ast.Ident{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.SELF:
		// `self` in expression position is parsed as a plain Ident so the
		// analyzer can report the precise source error. In current Nomi,
		// `self` is an interface type placeholder only; it is not a value or call
		// qualifier.
		p.advance()
		return &ast.Ident{Name: "self", Line: tok.Line, Col: tok.Col}, nil

	case token.BAR:
		return p.parseLambda()

	case token.THEN:
		return nil, errorAt(tok.Line, tok.Col, "`then` is a pipe stage: write `value |> then |v| ...`")

	case token.TYPE_IDENT:
		p.advance()
		if !p.noStructLit && !p.atEnd() && p.peek().Type == token.LBRACE {
			return p.parseStructLit(&ast.SimpleType{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}, tok.Line, tok.Col)
		}
		// Type-prefixed list literal: `Items[1, 2, 3]` or `Arr[1, 2, 3]`.
		// Mirrors the type-prefixed map literal `Kvs{"a" => 1}` — the
		// literal-attach construction form for list-payload variants and
		// list-distinct types. The analyzer resolves the type name and
		// validates the inner list against it.
		if !p.atEnd() && p.peek().Type == token.LBRACKET {
			return p.parseTypePrefixedListLit(&ast.SimpleType{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}, tok.Line, tok.Col)
		}
		// Check for dotted name: Shape.Rectangle{ or Shape.Point
		if !p.atEnd() && p.peek().Type == token.DOT {
			// A dotted type name can put more than one segment in front of
			// the variant: `Probe.Reading.Blip{at: 9}` constructs a variant
			// of `Probe.Reading`. Scan the chain before consuming any of it,
			// because the extra segments belong to the name only when a
			// literal body closes it off — a chain that ends in anything else
			// is ordinary member access, which the postfix parser already
			// builds, and taking it here would change how `Probe.Reading.Steady`
			// parses today.
			segs := 0
			for p.peekAt(2*segs).Type == token.DOT && p.peekAt(2*segs+1).Type == token.TYPE_IDENT {
				segs++
			}
			if segs >= 2 {
				term := p.peekAt(2 * segs).Type
				if term == token.LBRACKET || (term == token.LBRACE && !p.noStructLit) {
					owner := []string{tok.Lexeme}
					var lastTok token.Token
					for i := 0; i < segs; i++ {
						p.advance() // consume DOT
						lastTok = p.peek()
						p.advance() // consume the segment
						if i < segs-1 {
							owner = append(owner, lastTok.Lexeme)
						}
					}
					qualType := &ast.QualifiedType{
						Module:     strings.Join(owner, "."),
						ModuleLine: tok.Line,
						ModuleCol:  tok.Col,
						Member:     &ast.SimpleType{Name: lastTok.Lexeme, Line: lastTok.Line, Col: lastTok.Col},
					}
					if term == token.LBRACE {
						return p.parseStructLit(qualType, tok.Line, tok.Col)
					}
					return p.parseTypePrefixedListLit(qualType, tok.Line, tok.Col)
				}
			}
			// Only consume DOT if next-next token is a TYPE_IDENT (uppercase variant name)
			// For Io.print, Iter.map etc., leave DOT for postfix parsing
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Type == token.TYPE_IDENT {
				p.advance() // consume DOT
				memberTok := p.peek()
				p.advance() // consume member TYPE_IDENT
				qualType := &ast.QualifiedType{
					Module:     tok.Lexeme,
					ModuleLine: tok.Line,
					ModuleCol:  tok.Col,
					Member:     &ast.SimpleType{Name: memberTok.Lexeme, Line: memberTok.Line, Col: memberTok.Col},
				}
				if !p.noStructLit && !p.atEnd() && p.peek().Type == token.LBRACE {
					// Shape.Rectangle{...} — struct literal with dotted type name
					return p.parseStructLit(qualType, tok.Line, tok.Col)
				}
				// Module.Variant[...] — type-prefixed list literal with
				// dotted type name (e.g., `Json.Arr[Int(1), Int(2)]`).
				if !p.atEnd() && p.peek().Type == token.LBRACKET {
					return p.parseTypePrefixedListLit(qualType, tok.Line, tok.Col)
				}
				// Shape.Point or Shape.Circle (field access)
				return &ast.FieldAccess{
					Object: &ast.TypeIdent{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col},
					Field:  &ast.Ident{Name: memberTok.Lexeme, Line: memberTok.Line, Col: memberTok.Col},
					Line:   tok.Line,
					Col:    tok.Col,
				}, nil
			}
		}
		return &ast.TypeIdent{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil

	case token.CASE:
		return p.parseCase()

	case token.IF:
		return p.parseIf()

	case token.LBRACKET:
		return p.parseListLit()

	case token.HASH:
		return p.parseHashCollectionLit()

	case token.LBRACE:
		return p.parseBlockOrLambda()

	case token.LPAREN:
		p.advance() // consume LPAREN
		// Parentheses delimit their contents, so a struct literal is
		// unambiguous inside them even where `Type {` would open a block
		// (an `if` condition, a `with` value).
		oldNoStructLit := p.noStructLit
		p.noStructLit = false
		defer func() { p.noStructLit = oldNoStructLit }()
		p.skipNewlines()
		expr, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		// Check for comma — if present, this is a tuple
		if !p.atEnd() && p.peek().Type == token.COMMA {
			items := []ast.Node{expr}
			for !p.atEnd() && p.peek().Type == token.COMMA {
				p.advance() // consume COMMA
				p.skipNewlines()
				// Allow trailing comma: COMMA followed by RPAREN ends the tuple.
				if !p.atEnd() && p.peek().Type == token.RPAREN {
					break
				}
				item, err := p.parseExpr(1)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
				p.skipNewlines()
			}
			if p.atEnd() || p.peek().Type != token.RPAREN {
				return nil, errorAt(tok.Line, tok.Col, "expected ')' after tuple elements")
			}
			// Spec §7 / §15: no single-element tuples. The trailing-comma
			// form `(x,)` collected exactly one item before the comma →
			// reject so it can't sneak past as a 1-tuple value when the
			// type form `(T,)` is also disallowed.
			if len(items) == 1 {
				return nil, errorAt(tok.Line, tok.Col, "single-element tuples are not allowed; `(x)` is grouping. Use a struct or distinct type if you need a wrapper")
			}
			p.advance() // consume RPAREN
			return &ast.TupleLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
		if p.atEnd() || p.peek().Type != token.RPAREN {
			return nil, errorAt(tok.Line, tok.Col, "expected ')' after expression")
		}
		p.advance()
		return &ast.GroupedExpr{Expr: expr, Line: tok.Line, Col: tok.Col}, nil

	default:
		if tok.Problem != "" {
			return nil, errorAt(tok.Line, tok.Col, "%s", tok.Problem)
		}
		return nil, errorAt(tok.Line, tok.Col, "unexpected token %s %q", tok.Type, tok.Lexeme)
	}
}

// codepointLit builds the node for a CODEPOINT_LITERAL token. The lexer
// emits the kind only for a body strlit.DecodeCodepoint accepts.
func codepointLit(tok token.Token) *ast.CodepointLit {
	r, _ := strlit.DecodeCodepoint(tok.Lexeme)
	return &ast.CodepointLit{Value: r, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}
}

// parseListLit parses a list literal: [ expr, expr, ... ] or [ expr | tail ]
func (p *Parser) parseListLit() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume LBRACKET
	// Use plain separator-skip (no COMMENT / BLANK_LINE consumption) so a
	// trivia-only leading run gets a chance to land in EndTrivia at the
	// top of the loop. The previous skipNewlines() here silently swallowed
	// comments before the first element.
	p.skipSeparatorsOnly()

	var items []ast.Node
	var endTrivia []ast.Trivia
	if !p.atEnd() && p.peek().Type == token.RBRACKET {
		p.advance() // consume RBRACKET
		return &ast.ListLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
	}

	for {
		p.skipSeparatorsOnly()
		// End-of-body trivia: trivia followed only by `]` becomes
		// EndTrivia (preserved through the formatter). Trivia followed by
		// another element token falls through to the standard parser
		// path; ListLit's items have no inter-element trivia carrier today.
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACKET {
				endTrivia = collected
				p.advance() // consume RBRACKET
				return &ast.ListLit{Items: items, EndTrivia: endTrivia, Line: tok.Line, Col: tok.Col}, nil
			}
			p.pos = save
		}
		// Spread tail: `..expr` with no LHS is the optional spread element.
		// Bare `..` here is unambiguous because the infix range operator
		// requires a left operand (which would have been parsed by the
		// preceding `parseExpr(1)` call). Per the design, the spread must
		// be the last element of the literal.
		if !p.atEnd() && p.peek().Type == token.DOTDOT {
			spreadTok := p.peek()
			p.advance() // consume DOTDOT
			p.skipNewlines()
			tail, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			p.skipNewlines()
			if p.atEnd() || p.peek().Type != token.RBRACKET {
				return nil, errorAt(spreadTok.Line, spreadTok.Col, "list spread `..` must be the last element")
			}
			p.advance() // consume RBRACKET
			return &ast.ListSpreadLit{Heads: items, TailSpread: tail, Line: tok.Line, Col: tok.Col}, nil
		}
		item, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		// Skip plain separators only — COMMENT / BLANK_LINE is captured
		// at the top of the next iteration as either EndTrivia (if `]`
		// follows) or inter-element trivia (not yet representable, falls
		// through).
		p.skipSeparatorsOnly()

		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "expected ']' to close list literal")
		}
		if p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.ListLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ']' in list literal")
		}
		p.advance() // consume COMMA
		// Allow trailing comma: COMMA followed by RBRACKET closes the list.
		p.skipSeparatorsOnly()
		if !p.atEnd() && p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.ListLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
	}
}

// parseHashCollectionLit parses a hash-prefixed collection literal:
// `#[...]` for Vector and `#{...}` for Set.
func (p *Parser) parseHashCollectionLit() (ast.Node, error) {
	tok := p.peek()
	switch p.peekAt(1).Type {
	case token.LBRACKET:
		return p.parseVectorLit()
	case token.LBRACE:
		return p.parseSetLit()
	case token.BANG:
		if next := p.peekAt(1); next.Line == tok.Line && next.Col == tok.Col+1 {
			return nil, errorAt(tok.Line, tok.Col, "a `#!` line is allowed only as the first line of a file, starting at its first byte")
		}
		fallthrough
	default:
		return nil, errorAt(tok.Line, tok.Col, "expected '[' or '{' after '#' for collection literal")
	}
}

// parseVectorLit parses a vector literal: #[ expr, expr, ... ].
func (p *Parser) parseVectorLit() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume HASH
	if p.atEnd() || p.peek().Type != token.LBRACKET {
		return nil, errorAt(tok.Line, tok.Col, "expected '[' after '#' for vector literal")
	}
	p.advance() // consume LBRACKET
	p.skipSeparatorsOnly()

	var items []ast.Node
	var endTrivia []ast.Trivia
	if !p.atEnd() && p.peek().Type == token.RBRACKET {
		p.advance() // consume RBRACKET
		return &ast.VectorLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
	}

	for {
		p.skipSeparatorsOnly()
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACKET {
				endTrivia = collected
				p.advance() // consume RBRACKET
				return &ast.VectorLit{Items: items, EndTrivia: endTrivia, Line: tok.Line, Col: tok.Col}, nil
			}
			p.pos = save
		}
		if !p.atEnd() && p.peek().Type == token.DOTDOT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "vector literals do not support list spread `..`; use Vector.concat or Iter.to_vector")
		}
		item, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		p.skipSeparatorsOnly()

		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "expected ']' to close vector literal")
		}
		if p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.VectorLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ']' in vector literal")
		}
		p.advance() // consume COMMA
		p.skipSeparatorsOnly()
		if !p.atEnd() && p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.VectorLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
	}
}

// parseSetLit parses a set literal: #{ expr, expr, ... }.
func (p *Parser) parseSetLit() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume HASH
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after '#' for set literal")
	}
	p.advance() // consume LBRACE
	p.skipSeparatorsOnly()

	var items []ast.Node
	var endTrivia []ast.Trivia
	if !p.atEnd() && p.peek().Type == token.RBRACE {
		p.advance() // consume RBRACE
		return &ast.SetLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
	}

	for {
		p.skipSeparatorsOnly()
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = collected
				p.advance() // consume RBRACE
				return &ast.SetLit{Items: items, EndTrivia: endTrivia, Line: tok.Line, Col: tok.Col}, nil
			}
			p.pos = save
		}
		item, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		p.skipSeparatorsOnly()

		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "expected '}' to close set literal")
		}
		if p.peek().Type == token.RBRACE {
			p.advance() // consume RBRACE
			return &ast.SetLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or '}' in set literal")
		}
		p.advance() // consume COMMA
		p.skipSeparatorsOnly()
		if !p.atEnd() && p.peek().Type == token.RBRACE {
			p.advance() // consume RBRACE
			return &ast.SetLit{Items: items, Line: tok.Line, Col: tok.Col}, nil
		}
	}
}

// skipSeparatorsOnly consumes NEWLINE / SEMICOLON tokens but preserves
// COMMENT / BLANK_LINE so the caller can capture them as EndTrivia /
// inter-element trivia. Compare to skipNewlines (which also eats
// COMMENT / BLANK_LINE — convenient for grammar positions where trivia
// is genuinely meaningless, dangerous everywhere else).
func (p *Parser) skipSeparatorsOnly() {
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			return
		}
	}
}

// Precedence table:
//
//	or           = 2
//	and          = 3
//	== !=        = 4
//	< > <= >=    = 5
//	|>           = 6
//	.. ..=       = 7
//	+ -          = 8
//	* / %        = 9
//	unary ! -    = 10
const prefixPrecedence = 10

// tryOperandPrecedence is the minimum infix precedence consumed by the
// operand of a prefix `try`. It sits just above `*`/`/`/`%` (STAR, 9) and
// at-or-below calls/field access, so `try` grabs exactly one postfix-level
// operand: field access (DOT, 12) and calls (LPAREN, 11) attach to the
// operand, while `*`/`/`/`%` (9) and every lower-precedence binary operator
// (`+`, `|>`, …) stay outside it. Thus `try a() |> b()` is `(try a()) |> b()`
// and `try a.b.c` is `try (a.b.c)`.
const tryOperandPrecedence = 10

// canStartRangeOperand reports whether the given token type can begin
// an expression in range-operand position. Used to distinguish forms
// like `1..` (no end) from `1..5` — when the token after `..`/`..=`
// can't start an expression, the range is open-ended on that side.
func canStartRangeOperand(tt token.TokenType) bool {
	switch tt {
	case token.RPAREN, token.RBRACKET, token.RBRACE,
		token.COMMA, token.SEMICOLON, token.NEWLINE, token.EOF,
		token.COLON, token.PIPE, token.AND, token.OR,
		token.EQEQ, token.BANGEQ, token.LT, token.GT, token.LTEQ, token.GTEQ,
		token.DOTDOT, token.DOTDOTEQ,
		token.COMMENT, token.BLANK_LINE:
		return false
	}
	return true
}

func infixPrecedence(tt token.TokenType) int {
	switch tt {
	case token.PIPE:
		return 6
	case token.OR:
		return 2
	case token.AND:
		return 3
	case token.EQEQ, token.BANGEQ:
		return 4
	case token.LT, token.GT, token.LTEQ, token.GTEQ:
		return 5
	// Range operators sit between comparison and arithmetic so
	// `1+2..5+6` parses as `(1+2)..(5+6)` and `1..5 == 1..5` as
	// `(1..5) == (1..5)`.
	case token.DOTDOT, token.DOTDOTEQ:
		return 7
	case token.PLUS, token.MINUS:
		return 8
	case token.STAR, token.SLASH, token.PERCENT:
		return 9
	case token.LPAREN:
		return 11
	case token.DOT:
		return 12
	default:
		return 0
	}
}

// parseStringInterp parses an interpolated string. The opening token is
// either STRING_START (single-line `"..."`) or TRIPLE_STRING_START
// (triple-quoted `"""..."""`); the rest of the sequence must use the
// matching family — single START/PART/END or TRIPLE_START/PART/END. A
// cross-family token is treated as a parse error (it would mean the
// lexer mis-routed a triple-quoted string).
func (p *Parser) parseStringInterp() (ast.Node, error) {
	tok := p.peek()
	triple := tok.Type == token.TRIPLE_STRING_START
	partKind := token.STRING_PART
	endKind := token.STRING_END
	if triple {
		partKind = token.TRIPLE_STRING_PART
		endKind = token.TRIPLE_STRING_END
	}
	p.advance() // consume START

	var parts []ast.StringPart
	if tok.Lexeme != "" {
		parts = append(parts, ast.StringText{Value: tok.Lexeme})
	}

	// Parse the first interpolated expression
	expr, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	parts = append(parts, ast.StringExpr{Expr: expr})

	// Loop over PART / END (matching the family chosen by the START kind).
	for {
		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "unterminated interpolated string")
		}
		cur := p.peek()
		if cur.Type == endKind {
			p.advance()
			if cur.Lexeme != "" {
				parts = append(parts, ast.StringText{Value: cur.Lexeme})
			}
			break
		}
		if cur.Type == partKind {
			p.advance()
			if cur.Lexeme != "" {
				parts = append(parts, ast.StringText{Value: cur.Lexeme})
			}
			expr, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			parts = append(parts, ast.StringExpr{Expr: expr})
			continue
		}
		return nil, errorAt(cur.Line, cur.Col, "unexpected token %s in interpolated string", cur.Type)
	}

	return &ast.StringInterp{Parts: parts, Triple: triple, Line: tok.Line, Col: tok.Col}, nil
}

// parseTaggedStringInterp parses an interpolated tagged-string literal.
// Mirrors parseStringInterp but the opener is TAGGED_STRING_START or
// TAGGED_TRIPLE_STRING_START (carrying the Tag) and the result is an
// *ast.TaggedString. Subsequent PART/END tokens reuse the untagged
// STRING_PART/STRING_END (or TRIPLE_STRING_PART/TRIPLE_STRING_END)
// kinds — only the opener is tagged at the token layer.
func (p *Parser) parseTaggedStringInterp() (ast.Node, error) {
	tok := p.peek()
	triple := tok.Type == token.TAGGED_TRIPLE_STRING_START
	partKind := token.STRING_PART
	endKind := token.STRING_END
	if triple {
		partKind = token.TRIPLE_STRING_PART
		endKind = token.TRIPLE_STRING_END
	}
	p.advance() // consume START

	var parts []ast.StringPart
	if tok.Lexeme != "" {
		parts = append(parts, ast.StringText{Value: tok.Lexeme})
	}

	// Parse the first interpolated expression
	expr, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	parts = append(parts, ast.StringExpr{Expr: expr})

	for {
		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "unterminated interpolated tagged string")
		}
		cur := p.peek()
		if cur.Type == endKind {
			p.advance()
			if cur.Lexeme != "" {
				parts = append(parts, ast.StringText{Value: cur.Lexeme})
			}
			break
		}
		if cur.Type == partKind {
			p.advance()
			if cur.Lexeme != "" {
				parts = append(parts, ast.StringText{Value: cur.Lexeme})
			}
			expr, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			parts = append(parts, ast.StringExpr{Expr: expr})
			continue
		}
		return nil, errorAt(cur.Line, cur.Col, "unexpected token %s in interpolated tagged string", cur.Type)
	}

	return &ast.TaggedString{
		Tag:    tok.Tag,
		Triple: triple,
		Parts:  parts,
		Line:   tok.Line, Col: tok.Col,
	}, nil
}

// parseIf parses an if expression:
//
//	if <cond> { <block> } [else { <block> } | else if ...]
//	if <pattern> = <expr> { <block> } [else { <block> } | else if ...]
func (p *Parser) parseIf() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume IF

	var cond ast.Node
	var condPattern ast.Node
	var err error

	// First try the refutable-match form. Commit only if a single pattern is
	// followed by `=`; otherwise restore and parse the ordinary Bool condition.
	savedPatternPos := p.pos
	if !p.atEnd() && canStartPatternAssertion(p.peek().Type) {
		if pat, patErr := p.parseSinglePattern(); patErr == nil && !p.atEnd() && p.peek().Type == token.EQ {
			p.advance() // consume EQ
			oldNoStructLit := p.noStructLit
			p.noStructLit = true
			cond, err = p.parseExpr(1)
			p.noStructLit = oldNoStructLit
			if err != nil {
				return nil, err
			}
			condPattern = pat
		} else {
			p.pos = savedPatternPos
		}
	}

	if cond == nil {
		// Suppress struct literal parsing so TYPE_IDENT { is not consumed as a struct lit.
		// e.g. if True { ... } — True is TYPE_IDENT, { starts the then-block.
		oldNoStructLit := p.noStructLit
		p.noStructLit = true
		cond, err = p.parseExpr(1)
		p.noStructLit = oldNoStructLit
		if err != nil {
			return nil, err
		}
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after if condition")
	}
	thenNode, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	then := thenNode.(*ast.Block)

	var elseNode ast.Node
	// Allow `else` to sit on a new line after the then-block's closing `}`.
	// A blank line ends the if-expression — only consume plain NEWLINEs.
	savedElsePos := p.pos
	for !p.atEnd() && p.peek().Type == token.NEWLINE {
		p.advance()
	}
	if !p.atEnd() && p.peek().Type == token.ELSE {
		p.advance() // consume ELSE
		if !p.atEnd() && p.peek().Type == token.IF {
			// else if chain
			elseStart := p.pos
			elseNode, err = p.parseIf()
			if err == nil {
				p.recordSpan(elseNode, elseStart)
			}
			if err != nil {
				return nil, err
			}
		} else if !p.atEnd() && p.peek().Type == token.LBRACE {
			elseNode, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, errorAt(tok.Line, tok.Col, "expected '{' or 'if' after else")
		}
	} else {
		p.pos = savedElsePos
	}

	return &ast.If{Cond: cond, CondPattern: condPattern, Then: then, Else: elseNode, Line: tok.Line, Col: tok.Col}, nil
}

// parseWith parses the statement `with Type.field = value`. The override
// lasts from here to the end of the enclosing block. A statement replaces one
// field; several fields are several `with` lines.
func (p *Parser) parseWith() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume WITH
	if p.atEnd() || p.peek().Line != tok.Line {
		return nil, errorAt(tok.Line, tok.Col, "expected an application field override after `with`, such as `with MyApp.logger = Silent`")
	}
	start := p.peek()
	target, err := p.parseWithTarget()
	if err != nil {
		return nil, err
	}
	if p.atEnd() || p.peek().Type != token.EQ {
		return nil, errorAt(start.Line, start.Col, "expected '=' after the application field in `with`")
	}
	p.advance() // consume EQ
	p.skipNewlines()
	if p.atEnd() {
		return nil, errorAt(tok.Line, tok.Col, "expected a value after '=' in `with`")
	}
	value, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		switch next := p.peek(); next.Type {
		case token.COMMA:
			return nil, errorAt(next.Line, next.Col, "a `with` statement replaces one field; write one `with` line per field")
		case token.LBRACE:
			if next.Line == value.LineNum() || next.Line == tok.Line {
				return nil, errorAt(next.Line, next.Col, "`with` is a statement and takes no block: the override lasts to the end of the enclosing block")
			}
		}
	}
	return &ast.With{Target: target, Value: value, Line: tok.Line, Col: tok.Col}, nil
}

// parseWithTarget parses the application field a `with` override replaces:
// a type name, optionally file-qualified, then `.field`
// (`MyApp.logger`, `app.MyApp.logger`).
func (p *Parser) parseWithTarget() (*ast.FieldAccess, error) {
	withStart := p.pos
	first := p.peek()
	var left ast.Node
	switch first.Type {
	case token.TYPE_IDENT:
		left = &ast.TypeIdent{Name: first.Lexeme, Line: first.Line, Col: first.Col}
	case token.IDENT:
		left = &ast.Ident{Name: first.Lexeme, Line: first.Line, Col: first.Col}
	default:
		return nil, errorAt(first.Line, first.Col, "expected an application field such as `MyApp.logger` after `with`")
	}
	p.advance()
	for !p.atEnd() && p.peek().Type == token.DOT {
		dot := p.peek()
		p.advance() // consume DOT
		field := p.peek()
		if field.Type != token.IDENT && field.Type != token.TYPE_IDENT {
			return nil, errorAt(dot.Line, dot.Col, "expected field name after '.'")
		}
		p.advance()
		left = &ast.FieldAccess{Object: left, Field: &ast.Ident{Name: field.Lexeme, Line: field.Line, Col: field.Col}, Line: dot.Line, Col: dot.Col}
		p.recordSpan(left, withStart)
	}
	target, ok := left.(*ast.FieldAccess)
	if !ok {
		return nil, errorAt(first.Line, first.Col, "expected an application field such as `MyApp.logger` after `with`")
	}
	return target, nil
}

// parseDefer parses `defer call(...)`, a statement that registers a
// Unit-returning call for block-exit cleanup.
func (p *Parser) parseDefer() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume DEFER
	p.skipNewlines()

	if p.atEnd() {
		line := tok.Line
		if !p.atEnd() {
			line = p.peek().Line
		}
		return nil, p.errorOnLine(line, "expected call after `defer`")
	}
	call, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	return &ast.Defer{Call: call, Line: tok.Line, Col: tok.Col}, nil
}

// parseConcurrentBlock parses `concurrent { body }` — the structured
// concurrency scope from spec §20. Called from the IDENT
// branch of parsePrimary after the caller has confirmed the current
// token's lexeme is `concurrent` and the next token is `{`; this
// function consumes the contextual keyword and the body. Body parsing
// reuses the regular block machinery; semantics (last-expression-is-
// value, lambda-scoped `return`/`?`, runtime task scoping) are
// layered on by the analyzer (analysis/checker.go) and the IR builder
// (internal/irbuild). Position info points at the `concurrent` lexeme.
func (p *Parser) parseConcurrentBlock() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume the contextual `concurrent` IDENT

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after 'concurrent'")
	}

	bodyNode, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	body := bodyNode.(*ast.Block)

	return &ast.ConcurrentBlock{
		Body: body,
		Line: tok.Line,
		Col:  tok.Col,
	}, nil
}

// parseTestDecl parses:
//
//	test "name" { ... }
//	test "name", ctx { ... }
//	tests "name" { clock Clock.Virtual  boot server.boot(startup)  setup fixture()  test "child", ctx { ... } }
func (p *Parser) parseTestDecl() (ast.Node, error) {
	tok := p.peek()
	group := tok.Type == token.TESTS
	p.advance() // consume TEST / TESTS

	if p.atEnd() || p.peek().Type != token.STRING_LITERAL {
		if group {
			return nil, errorAt(tok.Line, tok.Col, "expected tests name string after `tests`")
		}
		return nil, errorAt(tok.Line, tok.Col, "expected test name string after `test`")
	}
	nameTok := p.peek()
	name := nameTok.Lexeme
	p.advance()

	var contextPattern ast.Node
	if !group && !p.atEnd() && p.peek().Type == token.COMMA {
		p.advance() // consume COMMA
		pat, err := p.parseSinglePattern()
		if err != nil {
			return nil, err
		}
		contextPattern = pat
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		if group {
			return nil, errorAt(tok.Line, tok.Col, "expected '{' for tests body")
		}
		return nil, errorAt(tok.Line, tok.Col, "expected '{' for test body")
	}

	bodyRecoveries := p.recoveries
	var boot ast.Node
	var bootLine int
	var bootCol int
	var setup ast.Node
	var setupLine int
	var setupCol int
	var clock ast.Node
	var clockLine int
	var clockCol int
	var clockLeading []ast.Trivia
	var clockTrailing []ast.Trivia
	var body *ast.Block
	if group {
		parsed, err := p.parseTestsBody()
		if err != nil {
			return nil, err
		}
		body = parsed.block
		boot = parsed.boot
		bootLine = parsed.bootLine
		bootCol = parsed.bootCol
		setup = parsed.setup
		setupLine = parsed.setupLine
		setupCol = parsed.setupCol
		clock = parsed.clock
		clockLine = parsed.clockLine
		clockCol = parsed.clockCol
		clockLeading = parsed.clockLeading
		clockTrailing = parsed.clockTrailing
	} else {
		bodyNode, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		body = bodyNode.(*ast.Block)
	}

	testDecl := &ast.TestDecl{
		Name:           name,
		Group:          group,
		Clock:          clock,
		ClockLine:      clockLine,
		ClockCol:       clockCol,
		ClockLeading:   clockLeading,
		ClockTrailing:  clockTrailing,
		Boot:           boot,
		Setup:          setup,
		ContextPattern: contextPattern,
		Body:           body,
		Line:           tok.Line,
		Col:            tok.Col,
		NameLine:       nameTok.Line,
		NameCol:        nameTok.Col,
		BootLine:       bootLine,
		BootCol:        bootCol,
		SetupLine:      setupLine,
		SetupCol:       setupCol,
	}
	// A repaired body is not evidence of anything: the `assert` you are
	// halfway through typing is exactly what recovery replaced with an
	// error node, so this whole-declaration requirement would discard the
	// test and take its scopes with it — the same "judging a partial node
	// as if it were complete" the recovery exists to avoid. The strict
	// parsers never repair, so they always run the check.
	if !testDeclContainsAssertion(testDecl) && p.recoveries == bodyRecoveries {
		if group {
			return nil, errorAt(tok.Line, tok.Col, "tests block must contain at least one assert or refute")
		}
		return nil, errorAt(tok.Line, tok.Col, "test block must contain at least one assert or refute")
	}

	return testDecl, nil
}

func testDeclContainsAssertion(decl *ast.TestDecl) bool {
	if decl == nil || decl.Body == nil {
		return false
	}
	if !decl.Group {
		_, _, ok := firstNestedAssertion(decl.Body)
		return ok
	}
	for _, stmt := range decl.Body.Stmts {
		child, ok := stmt.(*ast.TestDecl)
		if ok && testDeclContainsAssertion(child) {
			return true
		}
	}
	return false
}

// testsBody is what a `tests "…" { }` header block yields. A struct
// rather than a return list: the list had reached nine values before
// `clock` was added, at which point every call site was positional
// guesswork.
type testsBody struct {
	block         *ast.Block
	boot          ast.Node
	bootLine      int
	bootCol       int
	setup         ast.Node
	setupLine     int
	setupCol      int
	clock         ast.Node
	clockLine     int
	clockCol      int
	clockLeading  []ast.Trivia
	clockTrailing []ast.Trivia
}

// parseTestsBody parses a group's body: at most one `clock`, one `boot` and
// one `setup` line, and its `test` blocks, in any order. The lines run in a
// fixed order whatever their position, so position means nothing; `nomi fmt`
// moves them to the top. Groups do not nest.
func (p *Parser) parseTestsBody() (testsBody, error) {
	tok := p.peek()
	p.advance() // consume LBRACE

	var children []ast.Node
	var out testsBody
	var endTrivia []ast.Trivia

	for {
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.RBRACE {
			endTrivia = leading
			break
		}

		// `clock Clock.Virtual` / `clock Clock.System` selects which clock the tests
		// below run under. Contextual keyword, like boot and setup.
		if p.peek().Type == token.IDENT && p.peek().Lexeme == "clock" {
			clockTok := p.peek()
			if out.clock != nil {
				return out, errorAt(clockTok.Line, clockTok.Col, "a `tests` group has at most one `clock` line")
			}
			p.advance() // consume contextual clock
			if p.atEnd() || p.peek().Line != clockTok.Line || !canStartRangeOperand(p.peek().Type) {
				return out, errorAt(clockTok.Line, clockTok.Col, "expected a clock after `clock`, such as `clock Clock.Virtual`")
			}
			clockNode, err := p.parseExpr(1)
			if err != nil {
				return out, err
			}
			// The expected type is known here, so `.Virtual` resolves
			// without an import — the same parse-time stamp param defaults
			// use, and for the same reason: an imported module is
			// re-parsed for evaluation and that re-parse discards whatever
			// the analyzer stamped.
			stampParamDefaultEnum(clockNode, "Clock")
			out.clock = clockNode
			out.clockLine = clockTok.Line
			out.clockCol = clockTok.Col
			out.clockLeading = leading
			out.clockTrailing = p.collectTrailingComment(clockNode.LineNum())
			continue
		}

		if p.peek().Type == token.FN && p.peekAt(1).Lexeme == "boot" {
			fnTok := p.peek()
			return out, errorAt(fnTok.Line, fnTok.Col, "a `tests` group does not define `fn boot`; it names an entry's boot on a `boot` line, such as `boot server.boot(startup)`")
		}

		// `boot server.boot(startup)` names the entry boot each test in
		// the group runs. Contextual keyword, like clock and setup.
		if p.peek().Type == token.IDENT && p.peek().Lexeme == "boot" && p.peekAt(1).Line == p.peek().Line {
			bootTok := p.peek()
			if out.boot != nil {
				return out, errorAt(bootTok.Line, bootTok.Col, "a `tests` group has at most one `boot` line")
			}
			p.advance() // consume contextual boot
			if p.atEnd() || p.peek().Line != bootTok.Line || !canStartRangeOperand(p.peek().Type) {
				return out, errorAt(bootTok.Line, bootTok.Col, "expected a call to an entry's boot after `boot`, such as `boot server.boot(startup)`")
			}
			bootNode, err := p.parseExpr(1)
			if err != nil {
				return out, err
			}
			out.boot = bootNode
			out.bootLine = bootTok.Line
			out.bootCol = bootTok.Col
			trailing := p.collectTrailingComment(bootNode.LineNum())
			if carrier, ok := bootNode.(ast.HasTrivia); ok {
				attachTrivia(carrier, leading, trailing)
			}
			continue
		}

		if p.peek().Type == token.IDENT && p.peek().Lexeme == "setup" {
			setupTok := p.peek()
			if out.setup != nil {
				return out, errorAt(setupTok.Line, setupTok.Col, "a `tests` group has at most one `setup` line")
			}
			p.advance() // consume contextual setup
			if p.atEnd() || p.peek().Line != setupTok.Line || !canStartRangeOperand(p.peek().Type) {
				return out, errorAt(setupTok.Line, setupTok.Col, "expected setup expression")
			}
			setupNode, err := p.parseExpr(1)
			if err != nil {
				return out, err
			}
			if _, ok := setupNode.(*ast.Block); !ok {
				setupNode = &ast.Block{
					Stmts: []ast.Node{
						&ast.ExprStmt{Expr: setupNode, Line: setupTok.Line, Col: setupTok.Col},
					},
					Line: setupTok.Line,
					Col:  setupTok.Col,
				}
			}
			out.setup = setupNode
			out.setupLine = setupTok.Line
			out.setupCol = setupTok.Col
			trailing := p.collectTrailingComment(setupNode.LineNum())
			if carrier, ok := setupNode.(ast.HasTrivia); ok {
				attachTrivia(carrier, leading, trailing)
			}
			continue
		}

		if p.peek().Type == token.TESTS {
			nested := p.peek()
			return out, errorAt(nested.Line, nested.Col, "a `tests` group cannot contain another `tests` group; write a sibling group instead")
		}

		if p.peek().Type != token.TEST {
			return out, errorAt(p.peek().Line, p.peek().Col, "tests body may contain only clock, boot, setup, and test declarations")
		}

		start := p.pos
		node, err := p.parseTestDecl()
		if err != nil {
			return out, err
		}
		p.recordSpan(node, start)
		trailing := p.collectTrailingComment(node.LineNum())
		attachTrivia(node.(ast.HasTrivia), leading, trailing)
		children = append(children, node)
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return out, errorAt(tok.Line, tok.Col, "expected '}' to close tests body")
	}
	closeTok := p.peek()
	p.advance() // consume RBRACE

	block := &ast.Block{
		Stmts:   children,
		Line:    tok.Line,
		Col:     tok.Col,
		EndLine: closeTok.Line,
		EndCol:  closeTok.Col,
	}
	for _, t := range endTrivia {
		block.AddTrailing(t)
	}
	out.block = block
	return out, nil
}

func (p *Parser) parseAssertion() (ast.Node, error) {
	tok := p.peek()
	refute := tok.Type == token.REFUTE
	p.advance()
	if !refute {
		if node, ok, err := p.tryParseHeadPatternAssertion(tok); ok || err != nil {
			return node, err
		}
	}
	if p.atEnd() || p.peek().Line != tok.Line || !canStartRangeOperand(p.peek().Type) {
		return &ast.Assertion{
			Refute: refute,
			Line:   tok.Line,
			Col:    tok.Col,
		}, nil
	}
	expr, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	return &ast.Assertion{
		Refute: refute,
		Expr:   expr,
		Line:   tok.Line,
		Col:    tok.Col,
	}, nil
}

func (p *Parser) tryParseHeadPatternAssertion(assertTok token.Token) (ast.Node, bool, error) {
	if p.atEnd() || p.peek().Line != assertTok.Line || !canStartPatternAssertion(p.peek().Type) {
		return nil, false, nil
	}

	savedPos := p.pos
	pat, err := p.parseSinglePattern()
	if err == nil && !p.atEnd() && p.peek().Type == token.EQ {
		p.advance() // consume EQ
		val, err := p.parseExpr(1)
		if err != nil {
			return nil, true, err
		}
		return &ast.PatternDestructure{
			Pattern:    pat,
			Value:      val,
			AssertLine: assertTok.Line,
			AssertCol:  assertTok.Col,
			Line:       assertTok.Line,
			Col:        assertTok.Col,
		}, true, nil
	}

	p.pos = savedPos
	return nil, false, nil
}

// parseBlock parses a block: { stmt; stmt; ... }
func (p *Parser) parseBlock() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume LBRACE

	var stmts []ast.Node
	var endTrivia []ast.Trivia

	for {
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.RBRACE ||
			(p.resilient && p.blockClosedByColumnOneDecl()) {
			// End-of-block trivia: comments or blank lines after the last
			// statement but before the closing `}` belong to the Block
			// itself, not to a following statement.
			endTrivia = leading
			break
		}
		stmtStart := p.pos
		node, attachedDoc, attachedLeading, err := p.parseStmt()
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
	}

	var endLine, endCol int
	if p.atEnd() || p.peek().Type != token.RBRACE {
		if !p.resilient {
			return nil, errorAt(tok.Line, tok.Col, "expected '}' to close block")
		}
		endLine, endCol = p.resilientBlockEnd(tok)
	} else {
		closeTok := p.peek()
		endLine, endCol = closeTok.Line, closeTok.Col
		p.advance() // consume RBRACE
	}

	block := &ast.Block{
		Stmts:   stmts,
		Line:    tok.Line,
		Col:     tok.Col,
		EndLine: endLine,
		EndCol:  endCol,
	}
	for _, t := range endTrivia {
		block.AddTrailing(t)
	}
	return block, nil
}

// parseCase parses a case expression:
//
//	case { branches }           — ad-hoc conditionals (no match value)
//	case expr { branches }      — value matching
func (p *Parser) parseCase() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume CASE

	var value ast.Node
	hasValue := false

	// If next token is LBRACE, it could be ad-hoc conditionals (no match value)
	// OR a map literal as the case value. Disambiguate by speculatively parsing
	// the brace's first entry as `keyExpr => …`: a top-level `=>` means a map
	// literal (any keyable expression — tuple/variant/list/struct/computed —
	// works as a key), otherwise it's ad-hoc conditionals. The `{` isn't
	// consumed at this site, so step over it (+ leading newlines) for the
	// detection and restore position before the real parse below.
	isMapLiteral := false
	if !p.atEnd() && p.peek().Type == token.LBRACE {
		savedPos := p.pos
		p.advance() // step over LBRACE for detection
		p.skipNewlines()
		isMapLiteral = p.detectMapPatternEntry()
		p.pos = savedPos
	}
	if !p.atEnd() && p.peek().Type == token.LBRACE && !isMapLiteral {
		// ad-hoc: Case.Value = nil
	} else {
		// Parse match value expression, then expect LBRACE
		var err error
		value, err = p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		hasValue = true
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after case")
	}
	p.advance() // consume LBRACE

	branches, endTrivia, err := p.parseCaseArms(hasValue)
	if err != nil {
		return nil, err
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '}' to close case expression")
	}
	p.advance() // consume RBRACE

	c := &ast.Case{Value: value, Branches: branches, Line: tok.Line, Col: tok.Col}
	for _, t := range endTrivia {
		c.AddTrailing(t)
	}
	return c, nil
}

// parseCaseArms parses case arms after an opening `{` up to, not including,
// the closing `}`. The returned trivia is what follows the last arm, which
// belongs to the node holding the arms (there is no next arm to attach to).
func (p *Parser) parseCaseArms(hasValue bool) ([]ast.CaseBranch, []ast.Trivia, error) {
	var branches []ast.CaseBranch
	for {
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.RBRACE {
			return branches, leading, nil
		}
		branch, err := p.parseCaseBranch(hasValue)
		if err != nil {
			return nil, nil, err
		}
		// Attach trivia directly to the CaseBranch itself, which embeds
		// TriviaCarrier. This keeps Pattern/Body free of formatter-specific
		// trivia that isn't semantically theirs.
		var trailing []ast.Trivia
		if branch.Body != nil {
			trailing = p.collectTrailingComment(branch.Body.LineNum())
		}
		attachTrivia(&branch, leading, trailing)
		branches = append(branches, branch)

		// Skip separators (commas) between branches; trivia is handled
		// at the top of the loop.
		for !p.atEnd() && p.peek().Type == token.COMMA {
			p.advance()
		}
	}
}

// parsePatternBindingElse parses `else { ... }` after a binding's value: a
// plain block, or case arms over the value the pattern did not match. The
// braces hold arms when their first item is `pattern ->` (or
// `pattern when`), as a case's do.
func (p *Parser) parsePatternBindingElse() (*ast.BindingElse, error) {
	elseTok := p.peek()
	p.advance() // consume ELSE
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(elseTok.Line, elseTok.Col, "expected '{' after else: a binding's else is a braced block")
	}
	if !p.bindingElseHasArms() {
		blockNode, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return &ast.BindingElse{Block: blockNode.(*ast.Block), Line: elseTok.Line, Col: elseTok.Col}, nil
	}
	lbrace := p.peek()
	p.advance() // consume LBRACE
	arms, endTrivia, err := p.parseCaseArms(true)
	if err != nil {
		return nil, err
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(elseTok.Line, elseTok.Col, "expected '}' to close else")
	}
	closeTok := p.peek()
	p.advance() // consume RBRACE
	e := &ast.BindingElse{
		Arms:       arms,
		Line:       elseTok.Line,
		Col:        elseTok.Col,
		LBraceLine: lbrace.Line,
		LBraceCol:  lbrace.Col,
		EndLine:    closeTok.Line,
		EndCol:     closeTok.Col,
	}
	for _, t := range endTrivia {
		e.AddTrailing(t)
	}
	return e, nil
}

// bindingElseHasArms reports whether the `{` at the cursor opens case arms:
// its first item is a pattern followed by `->` or `when`. It always rewinds.
func (p *Parser) bindingElseHasArms() bool {
	savedPos, savedRecoveries := p.pos, p.recoveries
	defer func() { p.pos, p.recoveries = savedPos, savedRecoveries }()
	p.advance() // step over LBRACE
	for !p.atEnd() {
		switch p.peek().Type {
		case token.NEWLINE, token.COMMENT, token.BLANK_LINE:
			p.advance()
			continue
		}
		break
	}
	if p.atEnd() || !canStartPatternAssertion(p.peek().Type) {
		return false
	}
	if _, err := p.parseSinglePattern(); err != nil || p.atEnd() {
		return false
	}
	return p.peek().Type == token.ARROW || p.peek().Type == token.WHEN
}

// atBindingElse reports whether an `else` follows a binding's value, on the
// same line or after plain newlines (not a blank line), and steps to it.
func (p *Parser) atBindingElse() bool {
	saved := p.pos
	for !p.atEnd() && p.peek().Type == token.NEWLINE {
		p.advance()
	}
	if !p.atEnd() && p.peek().Type == token.ELSE {
		return true
	}
	p.pos = saved
	return false
}

// withBindingElse finishes a destructure statement whose value was just
// parsed. Without a following `else` the statement is node. With one, the
// statement is a PatternBinding: its pattern is re-read from start, the
// statement's first token, and the value already parsed is kept.
func (p *Parser) withBindingElse(start int, node, value ast.Node, line, col int) (ast.Node, error) {
	if !p.atBindingElse() {
		return node, nil
	}
	after := p.pos
	p.pos = start
	pattern, err := p.parseSinglePattern()
	if err != nil || p.atEnd() || p.peek().Type != token.EQ {
		// `name: T = value else { ... }`: a name always matches.
		return nil, errorAt(p.tokens[after].Line, p.tokens[after].Col, "this pattern always matches; remove the else")
	}
	p.pos = after
	els, err := p.parsePatternBindingElse()
	if err != nil {
		return nil, err
	}
	return &ast.PatternBinding{Pattern: pattern, Value: value, Else: els, Line: line, Col: col}, nil
}

// tryParsePatternBinding parses `Pattern = value [else { ... }]` for a
// pattern none of the destructure statements spells (`Ok((w, h)) = ...`,
// `[first, ..rest] = ...`). It commits only when a pattern is followed by
// `=`, and otherwise rewinds.
func (p *Parser) tryParsePatternBinding() (ast.Node, bool, error) {
	start := p.peek()
	savedPos, savedRecoveries := p.pos, p.recoveries
	pattern, err := p.parseSinglePattern()
	if err != nil || p.atEnd() || p.peek().Type != token.EQ {
		p.pos, p.recoveries = savedPos, savedRecoveries
		return nil, false, nil
	}
	p.advance() // consume EQ
	value, err := p.parseExpr(1)
	if err != nil {
		return nil, true, err
	}
	n := &ast.PatternBinding{Pattern: pattern, Value: value, Line: start.Line, Col: start.Col}
	if p.atBindingElse() {
		if n.Else, err = p.parsePatternBindingElse(); err != nil {
			return nil, true, err
		}
	}
	return n, true, nil
}

// parseCaseBranch parses one branch of a case expression: pattern [when guard] -> body
func (p *Parser) parseCaseBranch(hasValue bool) (ast.CaseBranch, error) {
	line := p.peek().Line
	col := p.peek().Col
	patternStart := p.pos
	var pattern ast.Node
	var err error

	if hasValue {
		// Matching against a value: pattern can be literal, wildcard, or ident binding
		tok := p.peek()
		switch tok.Type {
		case token.UNDERSCORE:
			p.advance()
			pattern = &ast.WildcardPattern{Line: tok.Line, Col: tok.Col}
		case token.INT:
			p.advance()
			val, e := strconv.ParseInt(tok.Lexeme, 0, 64)
			if e != nil {
				return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
			}
			pattern = &ast.IntLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}
		case token.FLOAT:
			p.advance()
			val, e := strconv.ParseFloat(tok.Lexeme, 64)
			if e != nil {
				return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "invalid float %q", tok.Lexeme)
			}
			pattern = &ast.FloatLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}
		case token.DECIMAL:
			p.advance()
			pattern = &ast.DecimalLit{Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}
		case token.CODEPOINT_LITERAL:
			p.advance()
			pattern = codepointLit(tok)
		case token.MINUS:
			pattern, err = p.parseNegativeLiteralPattern()
			if err != nil {
				return ast.CaseBranch{}, err
			}
		case token.STRING_LITERAL:
			p.advance()
			pattern = &ast.StringLit{Value: tok.Lexeme, Line: tok.Line, Col: tok.Col}
			if prefixed, ok, err := p.tryParseStringPrefixPattern(pattern, tok.Line, tok.Col); err != nil {
				return ast.CaseBranch{}, err
			} else if ok {
				pattern = prefixed
			}
		case token.TRIPLE_STRING_LITERAL:
			p.advance()
			pattern = &ast.StringLit{Value: tok.Lexeme, Triple: true, Line: tok.Line, Col: tok.Col}
			if prefixed, ok, err := p.tryParseStringPrefixPattern(pattern, tok.Line, tok.Col); err != nil {
				return ast.CaseBranch{}, err
			} else if ok {
				pattern = prefixed
			}
		case token.RAW_STRING_LITERAL:
			p.advance()
			pattern = &ast.StringLit{Value: tok.Lexeme, Raw: true, Line: tok.Line, Col: tok.Col}
			if prefixed, ok, err := p.tryParseStringPrefixPattern(pattern, tok.Line, tok.Col); err != nil {
				return ast.CaseBranch{}, err
			} else if ok {
				pattern = prefixed
			}
		case token.RAW_TRIPLE_STRING_LITERAL:
			p.advance()
			pattern = &ast.StringLit{Value: tok.Lexeme, Triple: true, Raw: true, Line: tok.Line, Col: tok.Col}
			if prefixed, ok, err := p.tryParseStringPrefixPattern(pattern, tok.Line, tok.Col); err != nil {
				return ast.CaseBranch{}, err
			} else if ok {
				pattern = prefixed
			}
		case token.IDENT:
			if p.peekAt(1).Type == token.DOT && (p.peekAt(2).Type == token.TYPE_IDENT || p.peekAt(2).Type == token.IDENT) {
				p.advance()
				startTok := tok
				typeExpr, lastTok := p.parseDottedPatternType(tok)
				if !p.atEnd() && p.peek().Type == token.DOT {
					return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected variant name after '.'")
				}
				tok = lastTok

				if !p.atEnd() && p.peek().Type == token.LPAREN {
					ep, err := p.parseEnumPatternPayload(typeExpr, startTok.Line, startTok.Col)
					if err != nil {
						return ast.CaseBranch{}, err
					}
					pattern = ep
				} else if !p.atEnd() && p.peek().Type == token.LBRACKET {
					lp, err := p.parseTypePrefixedListPattern(typeExpr, startTok.Line, startTok.Col)
					if err != nil {
						return ast.CaseBranch{}, err
					}
					pattern = lp
				} else if !p.atEnd() && p.peek().Type == token.LBRACE {
					bp, err := p.parseTypePrefixedBracePattern(typeExpr, startTok.Line, startTok.Col, tok.Line)
					if err != nil {
						return ast.CaseBranch{}, err
					}
					pattern = bp
				} else {
					pattern = &ast.EnumPattern{Variant: typeExpr, Binding: "", Line: startTok.Line, Col: startTok.Col}
				}
			} else {
				p.advance()
				pattern = &ast.IdentPattern{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}
			}
		case token.LPAREN:
			p.advance() // consume LPAREN
			var patterns []ast.Node
			for {
				pat, err := p.parseSinglePattern()
				if err != nil {
					return ast.CaseBranch{}, err
				}
				patterns = append(patterns, pat)
				if p.atEnd() {
					return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected ')' in tuple pattern")
				}
				if p.peek().Type == token.RPAREN {
					p.advance()
					break
				}
				if p.peek().Type != token.COMMA {
					return ast.CaseBranch{}, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ')' in tuple pattern")
				}
				p.advance() // consume COMMA
			}
			if len(patterns) < 2 {
				// Single-element (x) is just grouping, use inner pattern
				pattern = patterns[0]
			} else {
				pattern = &ast.TuplePattern{Patterns: patterns, Line: tok.Line, Col: tok.Col}
			}
		case token.LBRACE:
			// Disambiguate: map pattern {keyExpr => pat} vs struct pattern {field, field: pat}
			p.advance() // consume LBRACE
			p.skipNewlines()

			// Speculatively parse the first entry's key as an expression; if
			// it's followed by '=>', commit to a map pattern.
			isMapPattern := !p.atEnd() && p.detectMapPatternEntry()

			if isMapPattern {
				// Map pattern: {key => pat, ...}
				var entries []ast.MapPatternEntry
				var endTrivia []ast.Trivia
				for !p.atEnd() && p.peek().Type != token.RBRACE {
					// End-of-body trivia: see parseMapLitEntries.
					tt0 := p.peek().Type
					if tt0 == token.COMMENT || tt0 == token.BLANK_LINE {
						save := p.pos
						collected := p.collectEndOfBodyTrivia()
						if !p.atEnd() && p.peek().Type == token.RBRACE {
							endTrivia = collected
							break
						}
						p.pos = save
					}
					// The key is an arbitrary expression (same parser map
					// literals use for keys), then '=>'.
					keyNode, err := p.parseExpr(1)
					if err != nil {
						return ast.CaseBranch{}, err
					}
					if p.atEnd() || p.peek().Type != token.FAT_ARROW {
						return ast.CaseBranch{}, errorAt(p.peek().Line, p.peek().Col, "expected '=>' in map pattern")
					}
					p.advance() // consume FAT_ARROW
					valPat, err := p.parseSinglePattern()
					if err != nil {
						return ast.CaseBranch{}, err
					}
					entries = append(entries, ast.MapPatternEntry{Key: keyNode, Pattern: valPat})
					// Plain separators only — preserve trivia for the
					// next iteration's EndTrivia check.
					for !p.atEnd() {
						tt := p.peek().Type
						if tt == token.COMMA || tt == token.NEWLINE || tt == token.SEMICOLON {
							p.advance()
						} else {
							break
						}
					}
				}
				if p.atEnd() || p.peek().Type != token.RBRACE {
					return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected '}' in map pattern")
				}
				p.advance() // consume RBRACE
				pattern = &ast.MapPattern{Entries: entries, EndTrivia: endTrivia, Line: tok.Line, Col: tok.Col}
			} else {
				// Anonymous struct pattern: {field, field: "literal", ...}
				var fields []ast.StructPatternField
				for !p.atEnd() && p.peek().Type != token.RBRACE {
					if p.peek().Type != token.IDENT {
						return ast.CaseBranch{}, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct pattern")
					}
					fieldTok := p.peek()
					fieldName := fieldTok.Lexeme
					p.advance()
					spf := ast.StructPatternField{Name: fieldName, NameLine: fieldTok.Line, NameCol: fieldTok.Col, Binding: fieldName, BindingLine: fieldTok.Line, BindingCol: fieldTok.Col}
					if !p.atEnd() && p.peek().Type == token.COLON {
						p.advance() // consume COLON
						if err := p.parseStructFieldValue(&spf); err != nil {
							return ast.CaseBranch{}, err
						}
					}
					fields = append(fields, spf)
					for !p.atEnd() {
						tt := p.peek().Type
						if tt == token.COMMA || tt == token.NEWLINE || tt == token.SEMICOLON {
							p.advance()
						} else {
							break
						}
					}
				}
				if p.atEnd() || p.peek().Type != token.RBRACE {
					return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected '}' in struct pattern")
				}
				p.advance() // consume RBRACE
				pattern = &ast.StructPattern{TypeName: nil, Fields: fields, Line: tok.Line, Col: tok.Col}
			}
		case token.LBRACKET:
			pat, err := p.parseSinglePattern()
			if err != nil {
				return ast.CaseBranch{}, err
			}
			pattern = pat
		case token.DOT:
			// Dot-leading variant pattern: `.X`, `.X(args)`, `.X{...}`,
			// `.X[...]`, `.X{"k" => v}`. The TypeExpr carrier is a
			// *ast.DotVariantType, telling the analyzer to resolve the
			// variant against the scrutinee's enum type.
			if p.peekAt(1).Type != token.TYPE_IDENT {
				return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected variant name after '.'")
			}
			p.advance() // consume DOT
			nameTok := p.peek()
			p.advance() // consume TYPE_IDENT
			dvt := &ast.DotVariantType{Name: nameTok.Lexeme, Line: tok.Line, Col: tok.Col}
			if !p.atEnd() && p.peek().Type == token.LPAREN {
				ep, err := p.parseEnumPatternPayload(dvt, tok.Line, tok.Col)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = ep
			} else if !p.atEnd() && p.peek().Type == token.LBRACKET {
				lp, err := p.parseTypePrefixedListPattern(dvt, tok.Line, tok.Col)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = lp
			} else if !p.atEnd() && p.peek().Type == token.LBRACE {
				bp, err := p.parseTypePrefixedBracePattern(dvt, tok.Line, tok.Col, nameTok.Line)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = bp
			} else {
				// Bare `.X` — no payload.
				pattern = &ast.EnumPattern{Variant: dvt, Binding: "", Line: tok.Line, Col: tok.Col}
			}
		case token.TYPE_IDENT:
			p.advance()

			// Check for dotted name: Shape.Rectangle{...}, Shape.Point, or a
			// dotted type name in front of the variant (Probe.Reading.Steady).
			startTok := tok // preserve start position for TypeExpr
			typeExpr, lastTok := p.parseDottedPatternType(tok)
			if !p.atEnd() && p.peek().Type == token.DOT {
				return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "expected variant name after '.'")
			}
			tok = lastTok // update tok for line/bindingCol references below

			if !p.atEnd() && p.peek().Type == token.LPAREN {
				// Enum positional pattern: Variant(...) — may destructure into a nested pattern.
				ep, err := p.parseEnumPatternPayload(typeExpr, startTok.Line, startTok.Col)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = ep
			} else if !p.atEnd() && p.peek().Type == token.LBRACKET {
				// TypeName[...] pattern — the literal-attach destructure
				// form for list-payload variants. Mirrors the type-prefixed
				// list literal `Arr[1, 2, 3]` on the construction side.
				lp, err := p.parseTypePrefixedListPattern(typeExpr, startTok.Line, startTok.Col)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = lp
			} else if !p.atEnd() && p.peek().Type == token.LBRACE {
				// TypeName{...} pattern — delegate to parseTypePrefixedBracePattern,
				// which disambiguates map-pattern vs struct-pattern by peeking at
				// the entry separator. Shared with parseSinglePattern so the same
				// shape parses identically at the top of a case arm or nested
				// inside another pattern's call-form parens (`Ok(Obj{"k" => v})`).
				bp, err := p.parseTypePrefixedBracePattern(typeExpr, startTok.Line, startTok.Col, tok.Line)
				if err != nil {
					return ast.CaseBranch{}, err
				}
				pattern = bp
			} else {
				// Bare enum pattern: just VariantName or Type.VariantName
				pattern = &ast.EnumPattern{Variant: typeExpr, Binding: "", Line: startTok.Line, Col: startTok.Col}
			}
		default:
			if tok.Problem != "" {
				return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "%s", tok.Problem)
			}
			return ast.CaseBranch{}, errorAt(tok.Line, tok.Col, "unexpected token %s %q in case pattern", tok.Type, tok.Lexeme)
		}
	} else {
		// Ad-hoc conditionals: _ is wildcard, otherwise parse as expression
		tok := p.peek()
		if tok.Type == token.UNDERSCORE {
			p.advance()
			pattern = &ast.WildcardPattern{Line: tok.Line, Col: tok.Col}
		} else {
			pattern, err = p.parseExpr(1)
			if err != nil {
				return ast.CaseBranch{}, err
			}
		}
	}
	if pattern != nil {
		p.recordSpan(pattern, patternStart)
	}

	// Optional guard: when <expr>
	var guard ast.Node
	if !p.atEnd() && p.peek().Type == token.WHEN {
		p.advance() // consume WHEN
		guard, err = p.parseExpr(1)
		if err != nil {
			return ast.CaseBranch{}, err
		}
	}

	// Expect ARROW (->)
	if p.atEnd() || p.peek().Type != token.ARROW {
		return ast.CaseBranch{}, errorAt(p.peek().Line, p.peek().Col, "expected '->' in case branch")
	}
	p.advance() // consume ARROW

	// Parse the arm body. A leading break/continue/return is a control-flow
	// node — it unwinds to the enclosing loop/lambda just as in statement
	// position; otherwise the body is an expression. The parser stays
	// permissive; the checker (analysis/iter_sensitive.go) enforces that
	// break/continue only appear inside an iteration callback.
	var body ast.Node
	switch p.peek().Type {
	case token.BREAK:
		body, err = p.parseBreak()
	case token.CONTINUE:
		body, err = p.parseContinue()
	case token.RETURN:
		body, err = p.parseReturn()
	default:
		body, err = p.parseExpr(1)
	}
	if err != nil {
		return ast.CaseBranch{}, err
	}

	endLine, endCol := tokenEnd(p.tokens[p.pos-1])
	return ast.CaseBranch{Pattern: pattern, Guard: guard, Body: body, Line: line, Col: col, EndLine: endLine, EndCol: endCol}, nil
}

// parseBlockOrLambda disambiguates between { expr } blocks and { params -> body } lambdas.
// It saves the parser position, tries to parse lambda params followed by ARROW,
// and falls back to a regular block if the pattern doesn't match.
// parseBlockOrLambda dispatches `{` to map literal, anonymous struct literal,
// or block expression. Lambdas live inside `|...|` (see parseLambda).
func (p *Parser) parseBlockOrLambda() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume LBRACE
	leading := p.collectLeadingTrivia()

	// Struct update by spread: `{..base, field: value}`. Checked BEFORE the
	// map probe, because that probe speculatively parses the first key and
	// `..base` is not an expression — before this, `{..s, f: v}` fell all
	// the way through to block parsing and reported the removed
	// open-ended-range error. A leading `..` can start nothing else: the
	// infix range operator needs a left operand, and no statement begins
	// with `..`. Same reasoning the list literal uses for `[..xs]`.
	if !p.atEnd() && p.peek().Type == token.DOTDOT {
		return p.parseAnonStructLit(tok)
	}

	// Map literal: { keyExpr => expr, ... }. The key is an arbitrary
	// expression (tuple, variant, list, struct, computed, …), so detect it
	// the way every map-entry site does — speculatively parse the first
	// entry's key and peek for `=>` (detectMapPatternEntry restores pos).
	// LBRACE + leading newlines are already consumed, so the parser is
	// positioned at the key; parseMapLit re-parses from here.
	if !p.atEnd() && p.detectMapPatternEntry() {
		return p.parseMapLit(tok)
	}

	// Anonymous struct literal: { ident: expr, ... }. A block expression can
	// also start with an annotated binding (`{ name: Type = value ... }`), so
	// look past the type annotation before committing to struct-literal parsing.
	if !p.atEnd() && p.peek().Type == token.IDENT && p.peekAt(1).Type == token.COLON && !p.startsAnnotatedBinding() {
		return p.parseAnonStructLit(tok)
	}

	// Anonymous struct with field punning: { ident, ... }
	if !p.atEnd() && p.peek().Type == token.IDENT && p.peekAt(1).Type == token.COMMA {
		return p.parseAnonStructLit(tok)
	}

	// Everything else: block expression.
	var stmts []ast.Node
	var endTrivia []ast.Trivia
	pendingLeading := leading

	for {
		leading := pendingLeading
		pendingLeading = nil
		if leading == nil {
			leading = p.collectLeadingTrivia()
		}
		if p.atEnd() || p.peek().Type == token.RBRACE ||
			(p.resilient && p.blockClosedByColumnOneDecl()) {
			endTrivia = leading
			break
		}
		stmtStart := p.pos
		node, attachedDoc, attachedLeading, err := p.parseStmt()
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
	}

	var endLine, endCol int
	if p.atEnd() || p.peek().Type != token.RBRACE {
		if !p.resilient {
			return nil, errorAt(tok.Line, tok.Col, "expected '}' to close block")
		}
		endLine, endCol = p.resilientBlockEnd(tok)
	} else {
		closeTok := p.peek()
		endLine, endCol = closeTok.Line, closeTok.Col
		p.advance() // consume RBRACE
	}

	block := &ast.Block{
		Stmts:   stmts,
		Line:    tok.Line,
		Col:     tok.Col,
		EndLine: endLine,
		EndCol:  endCol,
	}
	for _, t := range endTrivia {
		block.AddTrailing(t)
	}
	return block, nil
}

func (p *Parser) startsAnnotatedBinding() bool {
	if p.peek().Type != token.IDENT && p.peek().Type != token.UNDERSCORE {
		return false
	}
	if p.peekAt(1).Type != token.COLON {
		return false
	}
	save := p.pos
	p.advance() // binding name
	p.advance() // colon
	_, err := p.parseTypeAnnotation()
	isBinding := err == nil && !p.atEnd() && p.peek().Type == token.EQ
	p.pos = save
	return isBinding
}

// parseLambda parses a pipe-delimited lambda: |params| body. Current token
// must be the opening BAR. The body is a single expression; use a block
// expression (`{ ... }`) for multi-statement bodies.
func (p *Parser) parseLambda() (ast.Node, error) {
	return p.parseLambdaStop(false)
}

// parseLambdaStop is parseLambda with the body's extent chosen. A lambda's
// body runs to the end of its expression, so `|s| f(s) |> g()` pipes inside
// the body. Only the lambda of a `then` stage passes stopAtPipe, which ends
// its body at the next `|>` of the enclosing pipeline.
func (p *Parser) parseLambdaStop(stopAtPipe bool) (ast.Node, error) {
	startTok := p.peek()
	p.advance() // consume opening BAR

	var params []ast.Param

	// Zero-arg form: || (two consecutive BAR tokens).
	if !p.atEnd() && p.peek().Type == token.BAR {
		p.advance() // consume closing BAR
	} else {
		for {
			if p.atEnd() {
				return nil, errorAt(startTok.Line, startTok.Col, "unterminated lambda parameters")
			}
			// Lambda params share the same shape-parsing as function
			// definitions — bare name, wildcard, or an irrefutable
			// destructuring pattern, with optional `: Type` and `= default`.
			// parseParamPattern routes every shape through parseSinglePattern
			// and handles the trailing annotation/default; the loop below
			// handles the `|`/`,` separators.
			param, err := p.parseParamPattern()
			if err != nil {
				return nil, err
			}
			params = append(params, param)

			if p.atEnd() {
				return nil, errorAt(startTok.Line, startTok.Col, "unterminated lambda parameters (missing closing '|')")
			}
			if p.peek().Type == token.BAR {
				p.advance() // consume closing BAR
				break
			}
			if p.peek().Type == token.COMMA {
				p.advance() // consume COMMA
				p.skipNewlines()
				continue
			}
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or '|' after lambda parameter, got %s", p.peek().Type)
		}
	}

	// Body: a single expression, to the end of the enclosing expression, or
	// for a `then` stage to the next `|>`.
	bodyStart := p.pos
	bodyExpr, err := p.parseExprWithPipeStop(1, stopAtPipe)
	if err != nil {
		if !p.resilient {
			return nil, err
		}
		// The one expression-level recovery site. `|x| n + ` with the
		// operand still unwritten is the shape an editor sees mid-edit:
		// the cursor is INSIDE the lambda, so keeping
		// the enclosing statement is not enough — the lambda itself has
		// to survive or its parameters never reach a scope and `x` is
		// not offered. The delimiter is unambiguous here (a lambda body
		// runs to the end of the enclosing statement), which is why this
		// site is safe and a general expression-level recovery would not
		// be.
		errNode := p.recoverStmtInBlock(bodyStart, err)
		return &ast.Lambda{
			Params:  params,
			Body:    &ast.Block{Stmts: []ast.Node{errNode}, Line: errNode.Line, Col: startTok.Col, EndLine: errNode.EndLine, EndCol: errNode.EndCol},
			Line:    startTok.Line,
			Col:     startTok.Col,
			EndLine: errNode.EndLine,
			EndCol:  errNode.EndCol,
		}, nil
	}

	// Normalize body into a Block so Lambda.Body is uniformly *ast.Block.
	var body *ast.Block
	if b, ok := bodyExpr.(*ast.Block); ok {
		body = b
	} else {
		body = &ast.Block{
			Stmts: []ast.Node{&ast.ExprStmt{Expr: bodyExpr, Line: bodyExpr.LineNum()}},
			Line:  bodyExpr.LineNum(),
			Col:   startTok.Col,
		}
	}

	endLine, endCol := tokenEnd(p.tokens[p.pos-1])
	return &ast.Lambda{
		Params:  params,
		Body:    body,
		Line:    startTok.Line,
		Col:     startTok.Col,
		EndLine: endLine,
		EndCol:  endCol,
	}, nil
}

// parseAnonStructLit parses an anonymous struct literal: { field: value, ... } (LBRACE already consumed).
func (p *Parser) parseAnonStructLit(openTok token.Token) (ast.Node, error) {
	var fields []ast.StructFieldVal
	var endTrivia []ast.Trivia
	var spread ast.Node
	spreadLine, spreadCol := 0, 0
	// Struct update by spread: `{..base, field: value}`. This mirrors the
	// list literal's spread (parseListLit, above): a bare `..` at element
	// position is unambiguous against the infix range operator, because
	// the infix form requires a left operand that would already have been
	// consumed by the enclosing parseExpr.
	//
	// THE POSITION RULE IS THE OPPOSITE OF THE LIST'S, on purpose. A list
	// spread must be LAST because a List is cons cells: prepending is O(1)
	// and appending is O(n), so `[0, ..xs]` is the cheap direction. Order
	// in a record update is pure notation, and base-then-overrides is the
	// only order under which "a later field wins" is true. Hence one
	// spread, written FIRST, and the diagnostic below is the mirror of
	// "list spread `..` must be the last element".
	if !p.atEnd() && p.peek().Type == token.DOTDOT {
		spreadTok := p.peek()
		p.advance() // consume DOTDOT
		p.skipNewlines()
		head, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		spread = head
		spreadLine, spreadCol = spreadTok.Line, spreadTok.Col
		for !p.atEnd() {
			t := p.peek().Type
			if t == token.COMMA || t == token.NEWLINE || t == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
		// `{..m, "b" => 2}` — map literals keep no spread. Say so, the way
		// vector literals refuse the list spread, instead of letting it
		// reach "expected field name in struct literal".
		if !p.atEnd() && p.detectMapPatternEntry() {
			return nil, errorAt(spreadTok.Line, spreadTok.Col, "map literals do not support spread `..`; use Map.merge")
		}
	}
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Collect trivia before the next field. `}` follows → EndTrivia;
		// another field → LeadingComments on the next StructFieldVal.
		var leading []ast.Trivia
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}
		// A `..` reached HERE is either a second spread (`{..a, ..b}`) or
		// a spread after a field (`{a: 1, ..b}`). One message for both,
		// the way parseListLit gives one message for `[1, ..xs, 2]` and
		// `[1, ..xs, ..ys]`.
		if p.peek().Type == token.DOTDOT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "struct spread `..` must be the first element")
		}
		if p.peek().Type != token.IDENT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct literal")
		}
		fieldTok := p.peek()
		fieldName := fieldTok.Lexeme
		p.advance()
		// Check for field punning: Name without `:` means Name: Name
		if p.atEnd() || p.peek().Type != token.COLON {
			// Field punning — use field name as variable reference
			fields = append(fields, ast.StructFieldVal{
				Name:            fieldName,
				Value:           &ast.Ident{Name: fieldName, Line: fieldTok.Line, Col: fieldTok.Col},
				LeadingComments: leading,
				Line:            fieldTok.Line,
				Col:             fieldTok.Col,
			})
			for !p.atEnd() {
				t := p.peek().Type
				if t == token.COMMA || t == token.NEWLINE || t == token.SEMICOLON {
					p.advance()
				} else {
					break
				}
			}
			continue
		}
		p.advance() // consume COLON
		val, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		fields = append(fields, ast.StructFieldVal{Name: fieldName, Value: val, LeadingComments: leading, Line: fieldTok.Line, Col: fieldTok.Col})
		for !p.atEnd() {
			t := p.peek().Type
			if t == token.COMMA || t == token.NEWLINE || t == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(openTok.Line, openTok.Col, "expected '}' to close struct literal")
	}
	p.advance() // consume RBRACE
	return &ast.StructLit{
		TypeName:   nil,
		Fields:     fields,
		Spread:     spread,
		SpreadLine: spreadLine,
		SpreadCol:  spreadCol,
		EndTrivia:  endTrivia,
		Line:       openTok.Line,
		Col:        openTok.Col,
	}, nil
}

// parseFuncDef parses a function definition: fn name(params) [: ReturnType] { body }
func (p *Parser) parseFuncDef(public bool, allowImplQualifier bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume FN

	name, nameTok, implIface, sourceQualified, err := p.parseFunctionHeaderName(tok, "fn", allowImplQualifier)
	if err != nil {
		return nil, err
	}

	// Optional type parameters: <T, U>
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}

	// Expect LPAREN
	if p.atEnd() || p.peek().Type != token.LPAREN {
		return nil, errorAt(tok.Line, tok.Col, "expected '(' after function name")
	}
	p.advance() // consume LPAREN
	p.skipNewlines()

	// Parse parameters. App fields are read explicitly in the body as
	// `AppEnv.field`; they are not part of the parameter list.
	params, err := p.parseFuncParamList(tok)
	if err != nil {
		return nil, err
	}

	// Expect RPAREN
	if p.atEnd() || p.peek().Type != token.RPAREN {
		return nil, errorAt(tok.Line, tok.Col, "expected ')' after parameters")
	}
	p.advance() // consume RPAREN

	// Optional return type annotation: : Type.
	var returnTypeExpr ast.TypeExpr
	if !p.atEnd() && p.peek().Type == token.COLON {
		p.advance() // consume COLON
		rt, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		returnTypeExpr = rt
	}

	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}
	p.skipSignatureSeparators()
	if !p.atEnd() && p.peekIsContextual("go") {
		if p.peekAt(1).Type != token.LBRACE {
			foreignAlias, foreignName, aliasTok, goNameTok, err := p.parseGoSelectorBinding()
			if err != nil {
				return nil, err
			}
			return &ast.ExternFunc{
				Name:                     name,
				Public:                   public,
				TypeParams:               typeParams,
				Params:                   params,
				ReturnTypeExpr:           returnTypeExpr,
				WhereClauses:             whereClauses,
				ImplIface:                implIface,
				ImplIfaceSourceQualified: sourceQualified,
				Line:                     nameTok.Line,
				Col:                      nameTok.Col,
				ForeignAlias:             foreignAlias,
				ForeignName:              foreignName,
				ForeignAliasLine:         aliasTok.Line,
				ForeignAliasCol:          aliasTok.Col,
				ForeignNameLine:          goNameTok.Line,
				ForeignNameCol:           goNameTok.Col,
			}, nil
		}
		goBody, goBodyLine, goBodyCol, err := p.parseInlineGoBlock()
		if err != nil {
			return nil, err
		}
		return &ast.ExternFunc{
			Name:           name,
			Public:         public,
			TypeParams:     typeParams,
			Params:         params,
			ReturnTypeExpr: returnTypeExpr,
			WhereClauses:   whereClauses,
			Line:           nameTok.Line,
			Col:            nameTok.Col,
			GoBody:         goBody,
			GoBodyLine:     goBodyLine,
			GoBodyCol:      goBodyCol,
		}, nil
	}
	if !p.atEnd() && p.peekIsContextual("from") {
		return nil, errorAt(p.peek().Line, p.peek().Col, "Go-backed functions use `go package.Symbol` bindings")
	}

	// Parse body block
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' for function body")
	}
	bodyNode, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	body := bodyNode.(*ast.Block)

	return &ast.FuncDef{
		Name:                     name,
		Public:                   public,
		TypeParams:               typeParams,
		Params:                   params,
		ReturnTypeExpr:           returnTypeExpr,
		WhereClauses:             whereClauses,
		Body:                     body,
		ImplIface:                implIface,
		ImplIfaceSourceQualified: sourceQualified,
		Line:                     tok.Line,
		Col:                      nameTok.Col,
	}, nil
}

func (p *Parser) parseInlineGoBlock() (body string, line int, col int, err error) {
	goTok := p.peek()
	p.advance() // consume contextual `go`
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return "", 0, 0, errorAt(goTok.Line, goTok.Col, "expected '{' after 'go'")
	}
	p.advance() // consume opening {
	start := p.pos
	depth := 1
	for !p.atEnd() {
		tok := p.peek()
		switch tok.Type {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
			if depth == 0 {
				end := p.pos
				if start < end {
					line = p.tokens[start].Line
					col = p.tokens[start].Col
				} else {
					line = tok.Line
					col = tok.Col
				}
				body = renderInlineGoTokens(p.tokens[start:end])
				p.advance() // consume closing }
				return body, line, col, nil
			}
		}
		p.advance()
	}
	return "", 0, 0, errorAt(goTok.Line, goTok.Col, "expected '}' to close inline Go block")
}

func (p *Parser) parseGoBlock() (ast.Node, error) {
	goTok := p.peek()
	body, bodyLine, bodyCol, err := p.parseInlineGoBlock()
	if err != nil {
		return nil, err
	}
	return &ast.GoBlock{
		Body:     body,
		BodyLine: bodyLine,
		BodyCol:  bodyCol,
		Line:     goTok.Line,
		Col:      goTok.Col,
	}, nil
}

func (p *Parser) parseGoPackageDecl() (ast.Node, error) {
	goPkgTok := p.peek()
	p.advance() // consume contextual `gopkg`

	if p.atEnd() || p.peek().Type != token.STRING_LITERAL {
		return nil, errorAt(goPkgTok.Line, goPkgTok.Col, "expected Go import path string after `gopkg`")
	}
	pathTok := p.peek()
	importPath := pathTok.Lexeme
	p.advance()

	alias := ""
	aliasLine, aliasCol := 0, 0
	if !p.atEnd() && p.peek().Type == token.AS {
		p.advance() // consume AS
		if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.UNDERSCORE) {
			return nil, errorAt(goPkgTok.Line, goPkgTok.Col, "expected Go package alias after `as`")
		}
		aliasTok := p.peek()
		alias = aliasTok.Lexeme
		aliasLine = aliasTok.Line
		aliasCol = aliasTok.Col
		p.advance()
	}

	pathCol := pathTok.Col + 1
	if alias == "" {
		alias = inferredGoImportAlias(importPath)
		aliasLine = pathTok.Line
		aliasCol = pathCol
	}

	return &ast.ExternPackage{
		ImportPath:     importPath,
		ImportPathLine: pathTok.Line,
		ImportPathCol:  pathCol,
		Alias:          alias,
		Line:           goPkgTok.Line,
		Col:            goPkgTok.Col,
		AliasLine:      aliasLine,
		AliasCol:       aliasCol,
	}, nil
}

func (p *Parser) parseGoSelectorBinding() (string, string, token.Token, token.Token, error) {
	goTok := p.peek()
	p.advance() // consume contextual `go`

	if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.UNDERSCORE) {
		return "", "", token.Token{}, token.Token{}, errorAt(goTok.Line, goTok.Col, "expected Go package alias after `go`")
	}
	aliasTok := p.peek()
	p.advance()
	if p.atEnd() || p.peek().Type != token.DOT {
		return "", "", token.Token{}, token.Token{}, errorAt(aliasTok.Line, aliasTok.Col, "expected '.' after Go package alias")
	}
	p.advance()
	if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.TYPE_IDENT) {
		return "", "", token.Token{}, token.Token{}, errorAt(aliasTok.Line, aliasTok.Col, "expected Go symbol name after '.'")
	}
	nameTok := p.peek()
	p.advance()
	return aliasTok.Lexeme, nameTok.Lexeme, aliasTok, nameTok, nil
}

func renderInlineGoTokens(tokens []token.Token) string {
	var b strings.Builder
	line := 0
	col := 1
	for i, tok := range tokens {
		if tok.Type == token.EOF || tok.Type == token.NEWLINE {
			continue
		}
		if i == 0 {
			line = tok.Line
			col = tok.Col
		}
		for line > 0 && tok.Line > line {
			b.WriteByte('\n')
			line++
			col = 1
		}
		for tok.Col > col {
			b.WriteByte(' ')
			col++
		}
		text := inlineGoTokenText(tok)
		b.WriteString(text)
		col += len(text)
	}
	return strings.TrimSpace(b.String())
}

func inlineGoTokenText(tok token.Token) string {
	switch tok.Type {
	case token.STRING_LITERAL, token.TRIPLE_STRING_LITERAL, token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL:
		return strconv.Quote(tok.Lexeme)
	case token.CODEPOINT_LITERAL:
		// A Go rune literal over ASCII lexes as a Nomi codepoint literal,
		// whose Lexeme drops the quotes. Every other rune literal is an
		// ILLEGAL token whose Lexeme keeps them.
		return "'" + tok.Lexeme + "'"
	case token.COMMENT:
		return tok.Lexeme
	default:
		return tok.Lexeme
	}
}

func (p *Parser) parseFunctionHeaderName(tok token.Token, keyword string, allowImplQualifier bool) (string, token.Token, ast.TypeExpr, bool, error) {
	if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.TYPE_IDENT) {
		return "", token.Token{}, nil, false, errorAt(tok.Line, tok.Col, "expected function name after '%s'", keyword)
	}
	segments := []token.Token{p.peek()}
	p.advance()
	for p.peek().Type == token.DOT {
		p.advance() // consume `.`
		if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.TYPE_IDENT) {
			return "", token.Token{}, nil, false, errorAt(tok.Line, tok.Col, "expected name after '.' in %s header", keyword)
		}
		segments = append(segments, p.peek())
		p.advance()
	}
	if len(segments) == 1 {
		return segments[0].Lexeme, segments[0], nil, false, nil
	}
	methodTok := segments[len(segments)-1]
	if !allowImplQualifier {
		return "", token.Token{}, nil, false, errorAt(methodTok.Line, methodTok.Col, "qualified implementation function names are not used in source; inside an `impl ... for` block, write plain `%s %s` because the block header already names the interface", keyword, methodTok.Lexeme)
	}
	if len(segments) > 3 {
		return "", token.Token{}, nil, false, errorAt(segments[0].Line, segments[0].Col, "implementation method qualifier must be an interface type name")
	}
	iface, err := functionImplQualifierType(segments[:len(segments)-1])
	if err != nil {
		return "", token.Token{}, nil, false, err
	}
	return methodTok.Lexeme, methodTok, iface, true, nil
}

func functionImplQualifierType(parts []token.Token) (ast.TypeExpr, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("internal: empty implementation method qualifier")
	}
	last := parts[len(parts)-1]
	if last.Type != token.TYPE_IDENT {
		return nil, errorAt(last.Line, last.Col, "implementation method qualifier must end in an interface type name")
	}
	member := &ast.SimpleType{Name: last.Lexeme, Line: last.Line, Col: last.Col}
	if len(parts) == 1 {
		return member, nil
	}
	module := parts[0]
	return &ast.QualifiedType{
		Module:     module.Lexeme,
		ModuleLine: module.Line,
		ModuleCol:  module.Col,
		Member:     member,
	}, nil
}

// parseFuncParamList parses a function's parameter list (between LPAREN and
// RPAREN; this routine assumes the LPAREN has been consumed and stops before
// the RPAREN). The list may be empty.
//
// Grammar:
//
//	params := param (',' param)* ','?
//	param  := IDENT (':' type-annotation)? ('=' expr)?
//
// App fields are read explicitly in the body as `AppEnv.field`.
func (p *Parser) parseFuncParamList(tok token.Token) ([]ast.Param, error) {
	return p.parseParamSection(tok)
}

// parseParamPattern parses one parameter — bare name, wildcard, or an
// irrefutable destructuring pattern — followed by an optional `: Type`
// annotation and an optional `= default`. Shared by both the function-
// definition param loop (parseParamSection) and the lambda param loop.
//
// Strategy: defer all shape parsing to parseSinglePattern, which already
// handles every pattern form (wildcard, tuple, distinct/enum, typed/anon
// struct, map, list, literals). Trivial patterns collapse to the plain
// `Param{Name}` representation so plain params (`x`, `_`) keep today's AST
// shape; everything else is carried in Destructure. The parser deliberately
// accepts refutable shapes too (map/list/literal/multi-variant) — the checker
// is the refutability gatekeeper, not the parser.
func (p *Parser) parseParamPattern() (ast.Param, error) {
	pat, err := p.parseSinglePattern()
	if err != nil {
		return ast.Param{}, err
	}

	var param ast.Param
	switch pp := pat.(type) {
	case *ast.IdentPattern:
		param = ast.Param{Name: pp.Name, Line: pp.Line, Col: pp.Col}
	case *ast.WildcardPattern:
		param = ast.Param{Name: "_", Line: pp.Line, Col: pp.Col}
	default:
		// Destructuring pattern. The synthetic Name is the slot the argument
		// value lands in before the pattern's inner names are bound
		// (Param.Destructure is read keyed by this Name); it must
		// be unique within the param list and never collide with a real name,
		// hence the `__destr_` prefix the previous tryParse* helpers used.
		param = ast.Param{
			Name:        fmt.Sprintf("__destr_%d_%d", pat.LineNum(), patternCol(pat)),
			Destructure: pat,
			Line:        pat.LineNum(),
			Col:         patternCol(pat),
		}
	}

	// Optional type annotation: : Type
	if !p.atEnd() && p.peek().Type == token.COLON {
		p.advance() // consume COLON
		typeExpr, err := p.parseTypeAnnotation()
		if err != nil {
			return ast.Param{}, err
		}
		param.TypeAnnotation = typeExpr
	}

	// Optional default value: = expr
	if !p.atEnd() && p.peek().Type == token.EQ {
		p.advance() // consume EQ
		def, err := p.parseExpr(1)
		if err != nil {
			return ast.Param{}, err
		}
		param.Default = def
		// Dot-shorthand in a param default resolves against the param's own type
		// annotation — `name: E = .V` is exactly `E.V`, since the annotation IS
		// the expected type. Stamp the dot-variant's ResolvedEnum from the
		// annotation's base name here, at parse time, rather than leaving it to
		// the analyzer: imported modules are re-parsed for evaluation and that
		// re-parse discards the analyzer's stamp, so a parse-time resolution is
		// the one that survives. The `.V` node is kept (so the formatter
		// preserves the shorthand, like every other dot-variant site); only its
		// ResolvedEnum is filled. Covers the bare form (`.V`) and the payload
		// form (`.V(x)`, a Call wrapping the DotVariant).
		if enumName := paramAnnotationBaseName(param.TypeAnnotation); enumName != "" {
			stampParamDefaultEnum(param.Default, enumName)
		}
	}

	return param, nil
}

// paramAnnotationBaseName returns the base type name of a param annotation when
// it's a plain or generic named type (`Direction`, `Maybe<T>`), the forms a
// dot-shorthand default can resolve against. Returns "" for other type shapes
// (function, tuple, anon-struct, module-qualified), where a dot-variant default
// has no single in-scope enum name to stamp.
func paramAnnotationBaseName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	}
	return ""
}

// stampParamDefaultEnum fills the ResolvedEnum of a dot-variant param default
// (bare `.V` or payload `.V(x)`) from the param's annotation enum name, when
// not already set.
func stampParamDefaultEnum(def ast.Node, enumName string) {
	switch n := def.(type) {
	case *ast.DotVariant:
		if n.ResolvedEnum == "" {
			n.ResolvedEnum = enumName
		}
	case *ast.Call:
		if dv, ok := n.Func.(*ast.DotVariant); ok && dv.ResolvedEnum == "" {
			dv.ResolvedEnum = enumName
		}
	// Literal-attach forms — `.V{...}`, `.V[...]`, `.V{k => v}`. These
	// carry the variant name in a TypeName slot as a DotVariantType
	// rather than as a DotVariant expression, so stamping the node
	// itself reaches nothing. Left unstamped, the default parses and
	// analyses clean but constructs against no enum at all.
	case *ast.StructLit:
		stampDotVariantType(n.TypeName, enumName)
	case *ast.ListLit:
		stampDotVariantType(n.TypeName, enumName)
	case *ast.MapLit:
		stampDotVariantType(n.TypeName, enumName)
	}
}

func stampDotVariantType(te ast.TypeExpr, enumName string) {
	if dvt, ok := te.(*ast.DotVariantType); ok && dvt.ResolvedEnum == "" {
		dvt.ResolvedEnum = enumName
	}
}

// patternCol returns the column of a pattern node, for synthesizing the
// destructure slot name. Pattern nodes don't share a Col accessor interface,
// so switch on the concrete shapes parseSinglePattern can return. (The analyzer
// has a sibling, analysis.patternColOf, used for diagnostic positions; they
// differ only in the unmatched-node fallback — LineNum here, 0 there.)
func patternCol(n ast.Node) int {
	switch p := n.(type) {
	case *ast.TuplePattern:
		return p.Col
	case *ast.StructPattern:
		return p.Col
	case *ast.EnumPattern:
		return p.Col
	case *ast.MapPattern:
		return p.Col
	case *ast.ListPattern:
		return p.Col
	}
	return n.LineNum()
}

// parseParamSection parses a comma-separated section of function parameters,
// stopping at RPAREN. May be empty. Encountering a BAR mid-list emits a
// helpful error pointing at active app field reads.
func (p *Parser) parseParamSection(tok token.Token) ([]ast.Param, error) {
	var params []ast.Param

	// Empty list: caller is sitting on RPAREN right away.
	if !p.atEnd() && p.peek().Type == token.RPAREN {
		return params, nil
	}
	if !p.atEnd() && p.peek().Type == token.BAR {
		return nil, errorAt(p.peek().Line, p.peek().Col, "'|' separator in function params is not supported; read app fields as `AppEnv.field` in the body")
	}

	for {
		if p.atEnd() {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected parameter")
		}
		param, err := p.parseParamPattern()
		if err != nil {
			return nil, err
		}

		params = append(params, param)

		if p.atEnd() {
			break
		}
		switch p.peek().Type {
		case token.COMMA:
			p.advance() // consume COMMA
			p.skipNewlines()
			// Allow trailing comma: COMMA followed by RPAREN closes the section.
			if !p.atEnd() && p.peek().Type == token.RPAREN {
				return params, nil
			}
			if !p.atEnd() && p.peek().Type == token.BAR {
				return nil, errorAt(p.peek().Line, p.peek().Col, "'|' separator in function params is not supported; read app fields as `AppEnv.field` in the body")
			}
			continue
		case token.BAR:
			return nil, errorAt(p.peek().Line, p.peek().Col, "'|' separator in function params is not supported; read app fields as `AppEnv.field` in the body")
		}
		break
	}

	_ = tok
	return params, nil
}

// isTurbofishCallable reports whether `left` is a syntactic form that can
// carry explicit type arguments before a call: a bare name (`f`), a module/
// field path (`mod.f`), or a PascalCase identifier (`Static`, `Some`, struct
// constructor names). Other expressions never take a turbofish.
//
// TypeIdent is included so bare variant constructors and struct names can
// turbofish: `Static<Int>("hi")`, `Some<Int>(42)`. The trailing-`(` requirement
// in tryParseTurbofish gates this — `Static<Int> something_else` rolls back to
// comparison parsing — and the residual ambiguity (`expr < Type > (args)` as a
// chained Bool comparison) is virtually never a valid value-level construct.
func isTurbofishCallable(left ast.Node) bool {
	switch left.(type) {
	case *ast.Ident, *ast.FieldAccess, *ast.TypeIdent:
		return true
	}
	return false
}

// tryParseTurbofish speculatively parses a turbofish type-argument list
// `<Type, ...>` that is immediately followed by `(`. The current token must be
// `<`. On success it consumes through the closing `>` (leaving `(` current) and
// returns the type args; on any failure — the list doesn't parse as types, or
// isn't followed by a call — it restores the position and returns false, so the
// `<` falls through to the comparison operator.
func (p *Parser) tryParseTurbofish() ([]ast.TypeExpr, bool) {
	saved := p.pos
	p.advance() // consume <
	var typeArgs []ast.TypeExpr
	for {
		ta, err := p.parseTypeAnnotation()
		if err != nil {
			p.pos = saved
			return nil, false
		}
		typeArgs = append(typeArgs, ta)
		if p.atEnd() || p.peek().Type != token.COMMA {
			break
		}
		p.advance() // consume COMMA
	}
	if p.atEnd() || p.peek().Type != token.GT {
		p.pos = saved
		return nil, false
	}
	p.advance() // consume >
	// Commit only when the type-arg list is immediately followed by a call.
	if p.atEnd() || p.peek().Type != token.LPAREN {
		p.pos = saved
		return nil, false
	}
	return typeArgs, true
}

// parseTypeParams optionally parses <T, U, ...> type parameter list.
// Returns nil if no '<' follows. Used after definition names.
func (p *Parser) parseTypeParams() ([]ast.TypeParam, error) {
	if p.atEnd() || p.peek().Type != token.LT {
		return nil, nil
	}
	p.advance() // consume <

	var params []ast.TypeParam
	for {
		name, line, col, err := p.parseTypeParamName()
		if err != nil {
			return nil, err
		}
		if !p.atEnd() && p.peek().Type == token.COLON {
			return nil, errorAt(p.peek().Line, p.peek().Col, "inline generic bounds are not supported; declare type parameters as `<%s>` and add a `where %s: ...` clause after the signature", name, name)
		}

		params = append(params, ast.TypeParam{Name: name, Line: line, Col: col})

		if p.atEnd() || p.peek().Type != token.COMMA {
			break
		}
		p.advance() // consume COMMA
	}

	if p.atEnd() || p.peek().Type != token.GT {
		return nil, errorAt(p.peek().Line, p.peek().Col, "expected '>' to close type parameters")
	}
	p.advance() // consume >

	return params, nil
}

func (p *Parser) parseTypeParamName() (string, int, int, error) {
	if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
		line := 0
		if !p.atEnd() {
			line = p.peek().Line
		}
		return "", line, 0, p.errorOnLine(line, "expected type parameter name")
	}
	tok := p.peek()
	p.advance()
	return tok.Lexeme, tok.Line, tok.Col, nil
}

func (p *Parser) parseQualifiedTypeDeclName(keyword string) (string, token.Token, error) {
	if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
		line := 0
		if !p.atEnd() {
			line = p.peek().Line
		}
		return "", token.Token{}, p.errorOnLine(line, "expected type name after '%s'", keyword)
	}
	nameTok := p.peek()
	parts := []string{nameTok.Lexeme}
	p.advance()
	for !p.atEnd() && p.peek().Type == token.DOT {
		p.advance()
		if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
			return "", nameTok, errorAt(nameTok.Line, nameTok.Col, "expected type name after '.'")
		}
		parts = append(parts, p.peek().Lexeme)
		p.advance()
	}
	return strings.Join(parts, "."), nameTok, nil
}

func (p *Parser) parseDistinctDestructureTypePrefix() (ast.TypeExpr, string, int, int, bool) {
	if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
		return nil, "", 0, 0, false
	}
	if p.peek().Type == token.IDENT && p.peekAt(1).Type != token.DOT {
		return nil, "", 0, 0, false
	}
	first := p.peek()
	p.advance()
	parts := []string{first.Lexeme}
	memberLine := 0
	memberCol := 0
	for !p.atEnd() && p.peek().Type == token.DOT {
		p.advance()
		if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
			return nil, "", 0, 0, false
		}
		if len(parts) == 1 {
			memberLine = p.peek().Line
			memberCol = p.peek().Col
		}
		parts = append(parts, p.peek().Lexeme)
		p.advance()
	}
	name := strings.Join(parts, ".")
	if len(parts) == 1 {
		return &ast.SimpleType{Name: name, Line: first.Line, Col: first.Col}, name, first.Line, first.Col, true
	}
	member := &ast.SimpleType{Name: strings.Join(parts[1:], "."), Line: memberLine, Col: memberCol}
	return &ast.QualifiedType{
		Module:     parts[0],
		ModuleLine: first.Line,
		ModuleCol:  first.Col,
		Member:     member,
	}, name, first.Line, first.Col, true
}

// parseTypeAnnotation consumes a type like "Int", "Mod.Type", "Map<String, List<Int>>",
// or function types like "(T) -> U", "(T, T) -> T", "() -> Unit".
func canStartTypeAnnotation(t token.TokenType) bool {
	switch t {
	case token.TYPE_IDENT, token.IDENT, token.SELF, token.LPAREN, token.LBRACE:
		return true
	default:
		return false
	}
}

func (p *Parser) parseTypeAnnotation() (te ast.TypeExpr, err error) {
	start := p.pos
	defer func() {
		if err == nil && te != nil {
			p.recordSpan(te, start)
		}
	}()
	return p.parseTypeAnnotationInner()
}

func (p *Parser) parseTypeAnnotationInner() (ast.TypeExpr, error) {
	if p.atEnd() {
		return nil, fmt.Errorf("expected type annotation")
	}

	// Function type: (params) -> ReturnType
	if p.peek().Type == token.LPAREN {
		return p.parseFuncTypeAnnotation()
	}

	// Anonymous struct type: {name: Type, ...}
	// The carve-out at parseTypeDef (parser.go's LBRACE check after
	// `type Foo`) rejects bare-RHS `type Foo {...}` *before*
	// parseTypeAnnotation is called, so this branch only fires from
	// nested type slots (tuple elements, function params, generic args,
	// struct field types, etc.).
	if p.peek().Type == token.LBRACE {
		return p.parseAnonStructType()
	}

	tok := p.peek()
	// `self` is a reserved keyword in type position — denotes the
	// implementing/receiver type inside interface bodies and impl method
	// signatures. Lowercase across the language: it joins the
	// uniformly-lowercase keyword space (cf. `fn`, `pub`, `loop`, ...)
	// and lives in type position the way every other Nomi type does.
	if tok.Type == token.SELF {
		p.advance()
		return &ast.SelfType{Line: tok.Line, Col: tok.Col}, nil
	}
	if tok.Type != token.TYPE_IDENT && tok.Type != token.IDENT {
		return nil, errorAt(tok.Line, tok.Col, "expected type name, got %s", tok.Type)
	}

	name := tok.Lexeme
	nameLine := tok.Line
	nameCol := tok.Col
	p.advance()

	// Handle Mod.Type / Type.Nested.Member — qualified type reference.
	if !p.atEnd() && p.peek().Type == token.DOT {
		var memberParts []string
		var memberTok token.Token
		for !p.atEnd() && p.peek().Type == token.DOT {
			p.advance() // consume DOT
			if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
				return nil, errorAt(tok.Line, tok.Col, "expected type name after '.'")
			}
			if len(memberParts) == 0 {
				memberTok = p.peek()
			}
			memberParts = append(memberParts, p.peek().Lexeme)
			p.advance()
		}
		memberName := strings.Join(memberParts, ".")

		// Check for generic params on the final member: Mod.Type<A, B>
		if !p.atEnd() && p.peek().Type == token.LT {
			p.advance() // consume <
			param, err := p.parseTypeAnnotation()
			if err != nil {
				return nil, err
			}
			params := []ast.TypeExpr{param}
			for !p.atEnd() && p.peek().Type == token.COMMA {
				p.advance() // consume COMMA
				param, err = p.parseTypeAnnotation()
				if err != nil {
					return nil, err
				}
				params = append(params, param)
			}
			if p.atEnd() || p.peek().Type != token.GT {
				return nil, errorAt(tok.Line, tok.Col, "expected '>' to close type parameters")
			}
			p.advance() // consume >
			return &ast.QualifiedType{
				Module:     name,
				ModuleLine: nameLine,
				ModuleCol:  nameCol,
				Member:     &ast.GenericType{Name: memberName, Params: params, Line: memberTok.Line, Col: memberTok.Col},
			}, nil
		}

		return &ast.QualifiedType{
			Module:     name,
			ModuleLine: nameLine,
			ModuleCol:  nameCol,
			Member:     &ast.SimpleType{Name: memberName, Line: memberTok.Line, Col: memberTok.Col},
		}, nil
	}

	// Handle generic type parameters: Type<A, B, ...>
	if !p.atEnd() && p.peek().Type == token.LT {
		p.advance() // consume <

		param, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		params := []ast.TypeExpr{param}

		for !p.atEnd() && p.peek().Type == token.COMMA {
			p.advance() // consume COMMA
			param, err = p.parseTypeAnnotation()
			if err != nil {
				return nil, err
			}
			params = append(params, param)
		}

		if p.atEnd() || p.peek().Type != token.GT {
			return nil, errorAt(tok.Line, tok.Col, "expected '>' to close type parameters")
		}
		p.advance() // consume >

		return &ast.GenericType{Name: name, Params: params, Line: tok.Line, Col: tok.Col}, nil
	}

	return &ast.SimpleType{Name: name, Line: tok.Line, Col: tok.Col}, nil
}

// parseFuncTypeAnnotation parses function types like (T) -> U or tuple types like (T, U).
// If followed by ->, it's a function type. Otherwise, it's a tuple type.
func (p *Parser) parseFuncTypeAnnotation() (ast.TypeExpr, error) {
	tok := p.peek()
	p.advance() // consume LPAREN

	// Parse inner types
	var params []ast.TypeExpr
	first := true
	for !p.atEnd() && p.peek().Type != token.RPAREN {
		if !first {
			if p.peek().Type != token.COMMA {
				return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ')' in type")
			}
			p.advance()
		}
		paramType, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		params = append(params, paramType)
		first = false
	}

	if p.atEnd() || p.peek().Type != token.RPAREN {
		return nil, errorAt(p.peek().Line, p.peek().Col, "expected ')' in type")
	}
	p.advance() // consume RPAREN

	// If followed by ->, it's a function type
	var retType ast.TypeExpr
	if !p.atEnd() && p.peek().Type == token.ARROW {
		p.advance()
		var err error
		retType, err = p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
	}

	return &ast.FuncType{Params: params, Return: retType, Line: tok.Line, Col: tok.Col}, nil
}

// parseAnonStructType parses an anonymous struct type expression like
// `{name: String, age: Int}`. The LBRACE has not been consumed yet.
// Field names are snake_case identifiers; types are full type expressions.
// Defaults (`=`) are not allowed — a default would be runtime data, not a
// type. Adjacent fields must be separated by a comma or a newline (or
// both); run-on fields like `{a: Int b: Int}` are rejected. Trailing
// commas are permitted. Empty `{}` is permitted (note: the empty anon
// struct value `{}` currently infers to Unit and does not unify with
// this empty type).
//
// Spec §7: "Anonymous structs are structurally typed — the type is its
// shape." Two anon struct types unify iff they have the same field names
// in the same order with the same field types (see TypesEqual on
// *analysis.AnonStructType).
func (p *Parser) parseAnonStructType() (ast.TypeExpr, error) {
	tok := p.peek()
	p.advance() // consume LBRACE
	// Skip plain NEWLINE / SEMICOLON only — COMMENT / BLANK_LINE is
	// captured below so a trailing comment before `}` can land in
	// EndTrivia.
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}

	var fields []ast.StructField
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Collect trivia before the next field. If `}` follows, it's
		// EndTrivia; otherwise it becomes the next field's LeadingComments.
		var leading []ast.Trivia
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}
		// Field name
		if p.peek().Type != token.IDENT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected field name in anonymous struct type, got %s", p.peek().Type)
		}
		nameTok := p.peek()
		p.advance()

		// Colon
		if p.atEnd() || p.peek().Type != token.COLON {
			return nil, errorAt(nameTok.Line, nameTok.Col, "expected ':' after field name in anonymous struct type")
		}
		p.advance()

		// Field type
		typeExpr, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}

		// Reject defaults: `=` is not allowed in type position.
		if !p.atEnd() && p.peek().Type == token.EQ {
			return nil, errorAt(p.peek().Line, p.peek().Col, "anonymous struct types cannot have field defaults — defaults are runtime data, attached to nominal struct declarations only")
		}

		fields = append(fields, ast.StructField{
			Name:            nameTok.Lexeme,
			TypeAnnotation:  typeExpr,
			Default:         nil,
			LeadingComments: leading,
			Line:            nameTok.Line,
			Col:             nameTok.Col,
		})

		// Consume any sequence of separators (commas + newline-equivalent
		// trivia). At least one is required between adjacent fields; the
		// inner loop tolerates any mix and order. A comment on the field's
		// own line, before or after its comma, is the field's Trailing;
		// any other COMMENT / BLANK_LINE is left for the next iteration
		// (LeadingComments or EndTrivia).
		endLine := p.prevLine()
		trailing := p.collectCommentOnLine(endLine)
		sawSeparator := len(trailing) > 0
		for !p.atEnd() {
			switch p.peek().Type {
			case token.COMMA, token.NEWLINE, token.SEMICOLON:
				p.advance()
				sawSeparator = true
			case token.COMMENT:
				if trailing != nil {
					goto endSeparators
				}
				if trailing = p.collectCommentOnLine(endLine); trailing == nil {
					goto endSeparators
				}
			default:
				goto endSeparators
			}
		}
	endSeparators:
		fields[len(fields)-1].Trailing = trailing
		if !p.atEnd() && p.peek().Type != token.RBRACE && p.peek().Type != token.COMMENT && p.peek().Type != token.BLANK_LINE && !sawSeparator {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or newline between fields in anonymous struct type, got %s", p.peek().Type)
		}
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '}' to close anonymous struct type")
	}
	p.advance() // consume RBRACE

	return &ast.AnonStructType{
		Fields:    fields,
		EndTrivia: endTrivia,
		Line:      tok.Line,
		Col:       tok.Col,
	}, nil
}

// parseEnumBody parses the brace body of an enum declaration —
// newline-separated variants:
//
//	enum Name {
//	  Variant1
//	  Variant2 Type
//	  Variant3 (T1, T2)
//	  Variant4 {field: Type}
//	  embeds Other
//	}
//
// The ENUM keyword, name, and any type params have already been consumed;
// the LBRACE is the next token. Field-shaped `name: Type` items are rejected
// (structs only).
// At least one variant is required. Returns *ast.EnumDef.
//
// Variant payload syntax mirrors distinct-type declarations: after the
// variant name, an optional type expression provides the payload type
// (`Foo Int`, `Foo (Int, Int)`, `Foo Maybe<Int>`, `Foo (Int) -> Int`).
// Struct-shaped payloads use `{...}` (with a leading space). The
// paren-wrapped form `Foo(Int)` also parses because its tokens are
// indistinguishable from the parenthesized type expression `(Int)` —
// which, per the no-1-tuples rule, collapses to `Int`.
func (p *Parser) parseEnumBody(public bool, opaque bool, name string, typeParams []ast.TypeParam, whereClauses []ast.WhereConstraint, line, col int) (ast.Node, error) {
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, p.errorOnLine(line, "expected '{' after type name")
	}
	p.advance() // consume LBRACE
	// Plain separators only — preserve COMMENT / BLANK_LINE so the first
	// variant can capture them as LeadingComments (or, if `}` follows,
	// the EnumDef captures them as EndTrivia).
	p.skipSeparatorsOnly()
	var variants []ast.EnumVariant
	var items []ast.Node
	var endTrivia []ast.Trivia

	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Trivia before the next item: `}` follows → EnumDef.EndTrivia;
		// another item follows → that item's leading slot.
		var leading []ast.Trivia
		if t := p.peek().Type; t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}

		doc := p.collectDocComments()
		if err := p.rejectDecorator(); err != nil {
			return nil, err
		}
		attachedTests, attachedDoc, attachedLeading, err := p.parseAttachedTestPromptsAndDoc()
		if err != nil {
			return nil, err
		}
		doc = mergeDocComments(doc, attachedDoc)
		leading = append(leading, attachedLeading...)

		tk := p.peek()
		switch {
		case tk.Type == token.FN || tk.Type == token.PUB || p.startsHostDecl() || tk.Type == token.ONCE:
			if _, err := p.parseTypeBodyItem(); err != nil {
				return nil, err
			}
			return nil, errorAt(tk.Line, tk.Col, "enum bodies hold only variants")
		case tk.Type == token.IDENT && tk.Lexeme == "embeds" || tk.Type == token.TYPE_IDENT:
			if err := rejectAttachedTestsOnNonDeclaration(attachedTests, "an enum variant"); err != nil {
				return nil, err
			}
			v, err := p.parseEnumVariant(name)
			if err != nil {
				return nil, err
			}
			v.LeadingComments = leading
			v.Trailing = p.collectCommentOnLine(p.prevLine())
			v.Doc = doc
			variants = append(variants, v)
			if !p.atEnd() {
				next := p.peek()
				if next.Line == v.Line && next.Type == token.EQ {
					return nil, enumVariantDefaultError(next, &v)
				}
				if next.Line == v.Line && next.Type != token.RBRACE && next.Type != token.NEWLINE && next.Type != token.SEMICOLON && next.Type != token.COMMA {
					return nil, errorAt(next.Line, next.Col, "enum variants are newline- or semicolon-separated")
				}
			}
		case tk.Type == token.IDENT && p.peekAt(1).Type == token.COLON:
			return nil, errorAt(tk.Line, tk.Col, "fields are not allowed in an enum body — fields belong in a struct body")
		case tk.Type == token.TEST || tk.Type == token.TESTS || p.startsConformanceLine():
			if _, err := p.parseTypeBodyItem(); err != nil {
				return nil, err
			}
			return nil, errorAt(tk.Line, tk.Col, "enum bodies hold only variants")
		default:
			return nil, errorAt(tk.Line, tk.Col, "expected variant name or 'embeds' in enum `%s` body, got %s", name, tk.Type)
		}

		p.skipEnumVariantTrailingSeparators()
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, p.errorOnLine(line, "expected '}' to close enum definition")
	}
	p.advance() // consume RBRACE

	if len(variants) == 0 {
		return nil, p.errorOnLine(line, "enum `%s` must have at least one variant", name)
	}

	return &ast.EnumDef{Name: name, Public: public, Opaque: opaque, TypeParams: typeParams, WhereClauses: whereClauses, Variants: variants, Items: items, EndTrivia: endTrivia, Line: line, Col: col}, nil
}

// parseEnumVariant parses a single enum variant. enumName is the parent
// enum's name, used only for error messages.
func (p *Parser) parseEnumVariant(enumName string) (v ast.EnumVariant, err error) {
	start := p.pos
	defer func() { v.Span = p.spanSince(start) }()
	// Embedded variant: `embeds TypeName`
	if p.peek().Type == token.IDENT && p.peek().Lexeme == "embeds" {
		p.advance() // consume "embeds"
		if p.atEnd() || p.peek().Type != token.TYPE_IDENT {
			return ast.EnumVariant{}, errorAt(p.peek().Line, p.peek().Col, "expected type name after 'embeds'")
		}
		embTok := p.peek()
		embeddedName := embTok.Lexeme
		p.advance() // consume TYPE_IDENT
		return ast.EnumVariant{
			Name:             embeddedName,
			Kind:             "embedded",
			EmbeddedTypeExpr: &ast.SimpleType{Name: embeddedName, Line: embTok.Line, Col: embTok.Col},
			Line:             embTok.Line,
			Col:              embTok.Col,
		}, nil
	}

	// Expect variant name (TYPE_IDENT)
	if p.peek().Type != token.TYPE_IDENT {
		return ast.EnumVariant{}, errorAt(p.peek().Line, p.peek().Col, "expected variant name in enum `%s`", enumName)
	}
	variantTok := p.peek()
	variantName := variantTok.Lexeme
	p.advance() // consume TYPE_IDENT

	kind := "bare"
	var dataTypeExpr ast.TypeExpr
	var fields []ast.StructField
	var endTrivia []ast.Trivia

	// Decide payload shape by the next token.
	//   {           → struct payload (anon-struct shape, leading space optional)
	//   }, EOF,     → bare variant (no payload)
	//   NEWLINE, ;,
	//   COMMENT
	//   anything    → type-expression payload (TYPE_IDENT, IDENT, LPAREN, …)
	//   else
	// The paren-wrapped form `Foo(Type)` parses as the type expression
	// `(Type)` — per the no-1-tuples rule, that collapses to `Type`
	// (handled below). `Foo(T1, T2)` parses as a tuple type.
	//
	// A trailing COMMA also terminates a bare variant here (rather than
	// starting a payload) so the stray-comma error fires at the top of
	// the next item loop with a clear message, not as a mangled
	// payload-type parse.
	isPayloadStart := false
	if !p.atEnd() {
		t := p.peek().Type
		switch t {
		case token.RBRACE, token.NEWLINE, token.SEMICOLON, token.COMMA, token.COMMENT:
			// bare (a same-line comment ends the variant as a newline does)
		case token.EQ:
			// `North = 1`: a bare variant given a value. The separator check
			// after the variant names the mistake.
		case token.LBRACE:
			// struct payload — handled in the `else if` below
		default:
			// LPAREN, TYPE_IDENT, IDENT, …  → type expression
			isPayloadStart = true
		}
	}

	if isPayloadStart {
		kind = "positional"
		typeExpr, err := p.parseTypeAnnotation()
		if err != nil {
			return ast.EnumVariant{}, err
		}
		// Collapse a 1-element parenthesized type — `(T)` parses as
		// FuncType{Params: [T], Return: nil}, which would otherwise
		// be interpreted by the type resolver as a 1-tuple. Nomi has
		// no 1-tuples; treat `(T)` as `T`. This makes the
		// paren-wrapped `Foo(Int)` parse to the same AST as `Foo Int`.
		if ft, ok := typeExpr.(*ast.FuncType); ok && ft.Return == nil && len(ft.Params) == 1 {
			typeExpr = ft.Params[0]
		}
		dataTypeExpr = typeExpr
	} else if !p.atEnd() && p.peek().Type == token.LBRACE {
		// Struct variant: Rectangle{width: Float, height: Float}
		kind = "struct"
		p.advance() // consume LBRACE
		// Plain separators only — preserve trivia for LeadingComments /
		// EndTrivia capture below.
		p.skipSeparatorsOnly()
		for !p.atEnd() && p.peek().Type != token.RBRACE {
			// Collect trivia before the next field: `}` follows →
			// EndTrivia, real field follows → LeadingComments on the
			// next StructField.
			var leading []ast.Trivia
			t := p.peek().Type
			if t == token.COMMENT || t == token.BLANK_LINE {
				leading = p.collectEndOfBodyTrivia()
				if !p.atEnd() && p.peek().Type == token.RBRACE {
					endTrivia = leading
					break
				}
			}
			if p.peek().Type != token.IDENT {
				return ast.EnumVariant{}, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct variant")
			}
			fieldTok := p.peek()
			fieldName := fieldTok.Lexeme
			p.advance() // consume field name

			if p.atEnd() || p.peek().Type != token.COLON {
				return ast.EnumVariant{}, errorAt(p.peek().Line, p.peek().Col, "expected ':' after field name '%s'", fieldName)
			}
			p.advance() // consume COLON

			fieldTypeExpr, err := p.parseTypeAnnotation()
			if err != nil {
				return ast.EnumVariant{}, err
			}

			// Optional default value: = expr
			var defaultExpr ast.Node
			if !p.atEnd() && p.peek().Type == token.EQ {
				p.advance() // consume '='
				defaultExpr, err = p.parseExpr(1)
				if err != nil {
					return ast.EnumVariant{}, err
				}
			}

			fields = append(fields, ast.StructField{Name: fieldName, TypeAnnotation: fieldTypeExpr, Default: defaultExpr, LeadingComments: leading, Line: fieldTok.Line, Col: fieldTok.Col})

			// Skip separators. A comment on the field's own line, before
			// or after its comma, is the field's Trailing; other trivia is
			// left for the next iteration.
			endLine := p.prevLine()
			trailing := p.collectCommentOnLine(endLine)
			for !p.atEnd() {
				t := p.peek().Type
				if t == token.COMMA || t == token.NEWLINE || t == token.SEMICOLON {
					p.advance()
				} else if t == token.COMMENT && trailing == nil {
					if trailing = p.collectCommentOnLine(endLine); trailing == nil {
						break
					}
				} else {
					break
				}
			}
			fields[len(fields)-1].Trailing = trailing
		}
		if p.atEnd() || p.peek().Type != token.RBRACE {
			return ast.EnumVariant{}, errorAt(p.peek().Line, p.peek().Col, "expected '}' to close struct variant")
		}
		p.advance() // consume RBRACE
	}

	return ast.EnumVariant{
		Name:         variantName,
		Kind:         kind,
		DataTypeExpr: dataTypeExpr,
		Fields:       fields,
		EndTrivia:    endTrivia,
		Line:         variantTok.Line,
		Col:          variantTok.Col,
	}, nil
}

// skipEnumVariantTrailingSeparators consumes whitespace separators (newline,
// semicolon) that may follow a variant body. Commas are intentionally NOT
// consumed here so the next-variant loop can flag stray `,` as an error.
func (p *Parser) skipEnumVariantTrailingSeparators() {
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}
}

// parseImportStmt parses an import statement:
//
//	import math                       → ModulePath=[Ident"math"], Names=[]
//	import math as m                  → ModulePath=[Ident"math"], ModuleAlias=Ident"m"
//	import models.User                → ModulePath=[Ident"models"], Names=[TypeIdent"User"]
//	import std/io.print               → ModulePath=[Ident"std", Ident"io"], Names=[Ident"print"]
//	import std/iter.{self, Iter}  → ModulePath=[Ident"std", Ident"iter"], Names=[TypeIdent"Iter"], IncludeParent=true
//	import models.{A, B}              → ModulePath=[Ident"models"], Names=[TypeIdent"A", TypeIdent"B"]
//	import models.{A as X, B}         → ModulePath=[Ident"models"], Names=[A, B], Aliases=[X, nil]
//	import { math, models.User }      → ImportBlock with two ImportStmt entries
func (p *Parser) parseImportStmt() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume IMPORT

	if !p.atEnd() && p.peek().Type == token.IDENT && p.peek().Lexeme == "go" {
		return nil, errorAt(p.peek().Line, p.peek().Col, "Go package handles use `gopkg \"import/path\"`")
	}

	// Block form: `import { entry1, entry2, ... }` bundles multiple imports.
	// Each entry is parsed with the same logic as a standalone import.
	if !p.atEnd() && p.peek().Type == token.LBRACE {
		return p.parseImportBlock(tok)
	}

	return p.parseImportEntry(tok)
}

// parseImportEntry parses the body of an import — everything after the
// `import` keyword (or after a comma inside a block). Used both for
// standalone imports and for entries inside an import block. tok is the
// position of the leading `import` keyword (for error messages and
// stmt position); the entry's own Line/Col are taken from its first segment.
func (p *Parser) parseImportEntry(tok token.Token) (ast.Node, error) {
	if p.peek().Type == token.EXTERN {
		return nil, errorAt(p.peek().Line, p.peek().Col, "runtime-provided declarations use `host`, not `extern`")
	}
	if p.peek().Type == token.IDENT && p.peek().Lexeme == "go" &&
		(p.peekAt(1).Type == token.STRING_LITERAL ||
			p.peekAt(1).Type == token.IDENT ||
			p.peekAt(1).Type == token.UNDERSCORE ||
			p.peekAt(1).Type == token.LBRACE) {
		return nil, errorAt(p.peek().Line, p.peek().Col, "Go package handles use `gopkg \"import/path\"`")
	}

	// Expect at least one import path segment. Keywords are allowed here:
	// path segments are file names, so `std/defer` stays valid even
	// though `defer` is a statement keyword.
	if !isImportPathSegment(p.peek()) {
		return nil, errorAt(tok.Line, tok.Col, "expected import path after 'import'")
	}

	// Use the entry's first segment for its position.
	entryTok := p.peek()
	tok = token.Token{Type: tok.Type, Lexeme: tok.Lexeme, Line: entryTok.Line, Col: entryTok.Col}

	// Collect dotted path segments as positioned nodes.
	var segments []ast.Node
	segments = append(segments, p.makeImportNode(p.peek()))
	p.advance() // consume first segment

	// hasBraces tracks whether the entry used the `.{...}` selective form, so
	// the line-level `export as <name>` rename can be rejected appropriately.
	hasBraces := false
	parseImportNameChain := func() (ast.Node, error) {
		if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
			return nil, errorAt(tok.Line, tok.Col, "expected name in import list")
		}
		first := p.peek()
		last := first
		parts := []string{first.Lexeme}
		p.advance() // consume first name segment
		for !p.atEnd() && p.peek().Type == token.DOT {
			p.advance() // consume DOT
			if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
				return nil, errorAt(tok.Line, tok.Col, "expected import name after '.'")
			}
			last = p.peek()
			parts = append(parts, p.peek().Lexeme)
			p.advance() // consume selector segment
		}
		name := strings.Join(parts, ".")
		if first.Type == token.TYPE_IDENT {
			return &ast.TypeIdent{Name: name, Line: last.Line, Col: last.Col}, nil
		}
		return &ast.Ident{Name: name, Line: last.Line, Col: last.Col}, nil
	}
	parseBraceList := func() (names []ast.Node, aliases []ast.Node, exportFlags []bool, exportAliases []ast.Node, includeParent bool, selfLine int, selfCol int, err error) {
		for {
			p.skipSeparatorsOnly()
			if p.peek().Type == token.RBRACE {
				if len(names) == 0 && !includeParent {
					err = errorAt(tok.Line, tok.Col, "expected name in import list")
					return
				}
				break
			}
			if p.peek().Type == token.SELF {
				if includeParent {
					err = errorAt(p.peek().Line, p.peek().Col, "duplicate `self` in import list")
					return
				}
				includeParent = true
				selfLine = p.peek().Line
				selfCol = p.peek().Col
				p.advance() // consume SELF
				if p.peek().Type == token.AS {
					err = errorAt(p.peek().Line, p.peek().Col, "`self` in an import list is a marker, not a name; it cannot be aliased")
					return
				}
				if p.peek().Type == token.EXPORT {
					err = errorAt(p.peek().Line, p.peek().Col, "`self` in an import list cannot carry `export`; export the parent module/type via the line-level form instead")
					return
				}
				if p.peek().Type == token.COMMA {
					p.advance() // consume COMMA
					p.skipSeparatorsOnly()
					continue
				}
				break
			}
			nameNode, parseErr := parseImportNameChain()
			if parseErr != nil {
				err = parseErr
				return
			}
			names = append(names, nameNode)

			// Optional `as Alias` after the name
			var aliasNode ast.Node
			if !p.atEnd() && p.peek().Type == token.AS {
				p.advance() // consume AS
				if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
					err = errorAt(tok.Line, tok.Col, "expected alias name after 'as'")
					return
				}
				aliasNode = p.makeImportNode(p.peek())
				p.advance() // consume alias
			}
			aliases = append(aliases, aliasNode)

			// Optional per-item `export [as Pub]` modifier. Must come AFTER any
			// `as <local>` rename. Order: Name [as Local] [export [as Pub]].
			exportFlag := false
			var exportAlias ast.Node
			if !p.atEnd() && p.peek().Type == token.EXPORT {
				p.advance() // consume EXPORT
				exportFlag = true
				if !p.atEnd() && p.peek().Type == token.AS {
					p.advance() // consume AS
					if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
						err = errorAt(tok.Line, tok.Col, "expected name after 'export as'")
						return
					}
					exportAlias = p.makeImportNode(p.peek())
					p.advance() // consume export alias
				}
			}
			exportFlags = append(exportFlags, exportFlag)
			exportAliases = append(exportAliases, exportAlias)

			if p.peek().Type == token.COMMA {
				p.advance() // consume COMMA
				p.skipSeparatorsOnly()
			} else {
				break
			}
		}
		p.skipSeparatorsOnly()
		if p.peek().Type != token.RBRACE {
			err = errorAt(tok.Line, tok.Col, "expected '}' after import list")
			return
		}
		p.advance() // consume RBRACE
		return
	}
	applyLineExport := func(entries []*ast.ImportStmt, hasBraces bool) error {
		if p.atEnd() || p.peek().Type != token.EXPORT {
			return nil
		}
		exportTok := p.peek()
		p.advance() // consume EXPORT
		for _, entry := range entries {
			entry.ExportAll = true
		}
		if !p.atEnd() && p.peek().Type == token.AS {
			return errorAt(exportTok.Line, exportTok.Col, "'export as <name>' is not supported on import statements; use per-item 'export as' inside the braces")
		}
		return nil
	}
	parseSelectorClause := func() (*ast.ImportStmt, bool, error) {
		if p.peek().Type == token.LBRACE {
			p.advance() // consume LBRACE
			names, aliases, exportFlags, exportAliases, includeParent, selfLine, selfCol, err := parseBraceList()
			if err != nil {
				return nil, true, err
			}
			return &ast.ImportStmt{ModulePath: segments, FileSegments: len(segments), Names: names, Aliases: aliases, ExportFlags: exportFlags, ExportAliases: exportAliases, Braced: true, IncludeParent: includeParent, SelfLine: selfLine, SelfCol: selfCol, Line: tok.Line, Col: tok.Col}, true, nil
		}
		if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
			return nil, false, errorAt(tok.Line, tok.Col, "expected import name after ':'")
		}
		var selector []ast.Node
		selector = append(selector, p.makeImportNode(p.peek()))
		p.advance() // consume first selector segment
		for !p.atEnd() && p.peek().Type == token.DOT {
			p.advance() // consume DOT
			if p.peek().Type == token.LBRACE {
				p.advance() // consume LBRACE
				names, aliases, exportFlags, exportAliases, includeParent, selfLine, selfCol, err := parseBraceList()
				if err != nil {
					return nil, true, err
				}
				path := append(append([]ast.Node(nil), segments...), selector...)
				return &ast.ImportStmt{ModulePath: path, FileSegments: len(segments), Names: names, Aliases: aliases, ExportFlags: exportFlags, ExportAliases: exportAliases, Braced: true, IncludeParent: includeParent, SelfLine: selfLine, SelfCol: selfCol, Line: tok.Line, Col: tok.Col}, true, nil
			}
			if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
				return nil, false, errorAt(tok.Line, tok.Col, "expected import owner or name after '.'")
			}
			selector = append(selector, p.makeImportNode(p.peek()))
			p.advance() // consume selector segment
		}
		path := append(append([]ast.Node(nil), segments...), selector[:len(selector)-1]...)
		names := selector[len(selector)-1:]
		aliases := make([]ast.Node, len(names))
		exportFlags := make([]bool, len(names))
		exportAliases := make([]ast.Node, len(names))
		if !p.atEnd() && p.peek().Type == token.AS {
			p.advance() // consume AS
			if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
				return nil, false, errorAt(tok.Line, tok.Col, "expected alias name after 'as'")
			}
			aliases[0] = p.makeImportNode(p.peek())
			p.advance() // consume alias
		}
		return &ast.ImportStmt{ModulePath: path, FileSegments: len(segments), Names: names, Aliases: aliases, ExportFlags: exportFlags, ExportAliases: exportAliases, Line: tok.Line, Col: tok.Col}, false, nil
	}
	sameImportPath := func(a, b *ast.ImportStmt) bool {
		if len(a.ModulePath) != len(b.ModulePath) {
			return false
		}
		for i := range a.ModulePath {
			if ast.ImportNodeName(a.ModulePath[i]) != ast.ImportNodeName(b.ModulePath[i]) {
				return false
			}
		}
		return true
	}
	mergeSelectorEntries := func(entries []*ast.ImportStmt) []*ast.ImportStmt {
		var out []*ast.ImportStmt
		for _, entry := range entries {
			if len(out) == 0 || entry.IncludeParent || out[len(out)-1].IncludeParent || !sameImportPath(out[len(out)-1], entry) {
				out = append(out, entry)
				continue
			}
			prev := out[len(out)-1]
			prev.Names = append(prev.Names, entry.Names...)
			prev.Aliases = append(prev.Aliases, entry.Aliases...)
			prev.ExportFlags = append(prev.ExportFlags, entry.ExportFlags...)
			prev.ExportAliases = append(prev.ExportAliases, entry.ExportAliases...)
		}
		return out
	}

	var result *ast.ImportStmt
loop:
	for p.peek().Type == token.DOT || p.peek().Type == token.SLASH {
		sep := p.peek().Type
		p.advance() // consume separator

		// Check for selective import/member access. Only valid after `.`,
		// not `/` — `/` joins module-path segments, `.` is the member-access
		// boundary.
		if sep == token.DOT && p.peek().Type == token.LBRACE {
			p.advance() // consume LBRACE
			hasBraces = true
			// includeParent tracks whether the brace list carries the
			// `self` marker — `std/foo.{self, X, Y}` lifts X and Y AND
			// also binds `foo` (or for the drill-through form
			// `std/foo.Bar.{self, X}` binds `Bar`). `self` is not added
			// to Names; it's a flag on the ImportStmt. selfLine/selfCol
			// pin the marker's source position so the analyzer can
			// register a hover/go-to-def target at it.
			names, aliases, exportFlags, exportAliases, includeParent, selfLine, selfCol, err := parseBraceList()
			if err != nil {
				return nil, err
			}
			result = &ast.ImportStmt{ModulePath: segments, FileSegments: len(segments), Names: names, Aliases: aliases, ExportFlags: exportFlags, ExportAliases: exportAliases, Braced: true, IncludeParent: includeParent, SelfLine: selfLine, SelfCol: selfCol, Line: tok.Line, Col: tok.Col}
			break loop
		}
		if sep == token.DOT {
			var clauseHadBraces bool
			var err error
			result, clauseHadBraces, err = parseSelectorClause()
			if err != nil {
				return nil, err
			}
			hasBraces = hasBraces || clauseHadBraces
			break loop
		}

		// Otherwise expect another path segment. After a SLASH, any
		// identifier-shaped segment is fine (including keywords used as file
		// names).
		if sep == token.SLASH && p.peek().Type == token.LBRACE {
			return nil, p.groupedImportPathError(segments)
		}
		if !isImportPathSegment(p.peek()) {
			return nil, errorAt(tok.Line, tok.Col, "expected import path segment after '/'")
		}
		segments = append(segments, p.makeImportNode(p.peek()))
		p.advance() // consume segment
	}

	if result == nil {
		if !p.atEnd() && p.peek().Type == token.COLON {
			p.advance() // consume COLON
			var entries []*ast.ImportStmt
			for {
				entry, clauseHadBraces, err := parseSelectorClause()
				if err != nil {
					return nil, err
				}
				hasBraces = hasBraces || clauseHadBraces
				entries = append(entries, entry)
				if p.atEnd() || p.peek().Type != token.COMMA {
					break
				}
				p.advance() // consume selector-list COMMA
			}
			entries = mergeSelectorEntries(entries)
			if err := applyLineExport(entries, hasBraces); err != nil {
				return nil, err
			}
			if len(entries) == 1 {
				return entries[0], nil
			}
			return &ast.ImportBlock{Entries: entries, Line: tok.Line, Col: tok.Col}, nil
		}
	}

	if result == nil {
		// No selector was seen. This is a module import, optionally aliased:
		// `import std/io` binds `io`; `import std/io as console` binds
		// `console`.
		var moduleAlias ast.Node
		if !p.atEnd() && p.peek().Type == token.AS {
			p.advance() // consume AS
			if p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT {
				return nil, errorAt(tok.Line, tok.Col, "expected alias name after 'as'")
			}
			moduleAlias = p.makeImportNode(p.peek())
			p.advance() // consume alias
		}

		result = &ast.ImportStmt{ModulePath: segments, FileSegments: len(segments), ModuleAlias: moduleAlias, Line: tok.Line, Col: tok.Col}
	}

	// Optional line-level `export` shorthand.
	//
	//   import some_mod: a, b, c export        → ExportAll = true (re-export each)
	//   import some_mod: a, b export as foo    → ERROR (use per-item export as)
	//
	// The per-item `export` modifier inside braces (B.2) is independent: per-item
	// flags survive in ExportFlags/ExportAliases and the line-level `export` adds
	// a blanket re-export for the rest.
	if err := applyLineExport([]*ast.ImportStmt{result}, hasBraces); err != nil {
		return nil, err
	}

	return result, nil
}

func inferredGoImportAlias(importPath string) string {
	importPath = strings.TrimSuffix(importPath, "/")
	if importPath == "" {
		return ""
	}
	if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
		return importPath[idx+1:]
	}
	return importPath
}

// makeImportNode creates an *Ident or *TypeIdent from a token, preserving position.
func (p *Parser) makeImportNode(t token.Token) ast.Node {
	if t.Type == token.DOTDOT {
		return &ast.Ident{Name: t.Lexeme, Line: t.Line, Col: t.Col}
	}
	if t.Type == token.TYPE_IDENT {
		return &ast.TypeIdent{Name: t.Lexeme, Line: t.Line, Col: t.Col}
	}
	return &ast.Ident{Name: t.Lexeme, Line: t.Line, Col: t.Col}
}

func isImportPathSegment(t token.Token) bool {
	if t.Type == token.IDENT || t.Type == token.TYPE_IDENT || t.Type == token.DOTDOT {
		return true
	}
	lex := t.Lexeme
	if lex == "" {
		return false
	}
	for i := 0; i < len(lex); i++ {
		ch := lex[i]
		ok := ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9'
		if !ok {
			return false
		}
	}
	first := lex[0]
	return first == '_' || first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z'
}

// parseImportBlock parses the brace block form:
//
//	import {
//	  entry1
//	  entry2
//	}
//
// Entries are separated by newlines and terminated by `}` — one entry per
// line, no commas (the analog of Go's `import ( ... )`). Each entry has the
// same shape as a standalone import (path, optional alias, optional selective
// names). The opening `import` keyword has already been consumed by the caller
// (parseImportStmt).
//
// A comma between entries is rejected with a helpful message: commas belong to
// the per-import selective list (`mod.{a, b}` — selecting names out of a
// module), not to listing modules in a block.
func (p *Parser) parseImportBlock(importTok token.Token) (ast.Node, error) {
	p.advance() // consume {

	var entries []*ast.ImportStmt
	var endTrivia []ast.Trivia
	for {
		// Leading comments/blanks above the next entry (also consumes the
		// newline separators the old skipNewlines handled).
		leading := p.collectLeadingTrivia()
		if p.atEnd() || p.peek().Type == token.RBRACE {
			// Trivia sitting between the last entry and the closing brace has
			// no entry to attach to — stash it on the block so it survives.
			endTrivia = leading
			break
		}
		entry, err := p.parseImportEntry(importTok)
		if err != nil {
			return nil, err
		}
		var parsedEntries []*ast.ImportStmt
		switch v := entry.(type) {
		case *ast.ImportStmt:
			parsedEntries = []*ast.ImportStmt{v}
		case *ast.ImportBlock:
			parsedEntries = v.Entries
		default:
			return nil, errorAt(importTok.Line, importTok.Col, "unexpected node in import block")
		}
		if len(parsedEntries) == 0 {
			return nil, errorAt(importTok.Line, importTok.Col, "empty import entry")
		}
		trailing := p.collectTrailingComment(parsedEntries[len(parsedEntries)-1].LineNum())
		attachTrivia(parsedEntries[0], leading, nil)
		parsedEntries[len(parsedEntries)-1].Trailing = append(parsedEntries[len(parsedEntries)-1].Trailing, trailing...)
		entries = append(entries, parsedEntries...)
		if !p.atEnd() && p.peek().Type == token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "import blocks are newline-separated, not comma-separated — put each entry on its own line")
		}
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(importTok.Line, importTok.Col, "expected '}' to close import block")
	}
	p.advance() // consume }

	return &ast.ImportBlock{
		Entries:   entries,
		EndTrivia: endTrivia,
		Line:      importTok.Line,
		Col:       importTok.Col,
	}, nil
}

// parseTypeDef parses a distinct-type wrapper or zero-sized marker:
//
//	type Foo Inner   → distinct over Inner
//	type Foo (A, B)  → tuple-distinct
//	type Foo         → zero-sized
//
// Struct (`{ name: T, ... }`) and enum (`{ A | B }`) shapes have their own
// top-level keywords (`struct`, `enum`) and entry points; encountering a
// brace body here is a parse error with a hint pointing at those keywords.
//
// `opaque` is true when the declaration was prefixed with `opaque`
// (`pub opaque type Foo Int`). The flag is preserved on the TypeDef node;
// the analyzer rejects opaque-on-zero-sized at the declaration site.
func (p *Parser) parseTypeDef(public bool, opaque bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume TYPE

	name, nameTok, err := p.parseQualifiedTypeDeclName("type")
	if err != nil {
		return nil, err
	}

	// Generic distinct types are not yet supported — they would require
	// `type Foo<T> Inner<T>` shape, which the current TypeDef AST does not
	// model. Reject `<...>` here; `struct`/`enum` keywords carry generics.
	if !p.atEnd() && p.peek().Type == token.LT {
		return nil, errorAt(tok.Line, tok.Col, "type parameters on distinct `type %s` are not supported — use `struct %s<...>` or `enum %s<...>` for generic record/sum types", name, name, name)
	}

	// Check for optional inner type (must be on same line)
	var innerTypeExpr ast.TypeExpr
	if !p.atEnd() && (p.peek().Type == token.TYPE_IDENT || p.peek().Type == token.IDENT || p.peek().Type == token.LPAREN) &&
		p.peek().Line == tok.Line {
		typeExpr, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		innerTypeExpr = typeExpr
	}

	var items []ast.Node
	var endTrivia []ast.Trivia
	var closeTok token.Token
	hasBody := false
	if !p.atEnd() && p.peek().Type == token.LBRACE && p.pos > 0 && p.peek().Line == p.tokens[p.pos-1].Line {
		return nil, errorAt(p.peek().Line, p.peek().Col, "distinct `type` declarations do not take bodies; put constructors and helpers beside the type")
	}

	return &ast.TypeDef{
		Name:          name,
		Public:        public,
		Opaque:        opaque,
		InnerTypeExpr: innerTypeExpr,
		HasBody:       hasBody,
		Items:         items,
		EndTrivia:     endTrivia,
		Line:          tok.Line,
		Col:           nameTok.Col,
		EndLine:       closeTok.Line,
		EndCol:        closeTok.Col,
	}, nil
}

func (p *Parser) parseTypeDefOrForeignBinding(public bool, opaque bool) (ast.Node, error) {
	save := p.pos
	typeTok := p.peek()
	p.advance() // consume TYPE
	name, localTok, err := p.parseQualifiedTypeDeclName("type")
	if err != nil {
		return nil, err
	}
	p.skipSignatureSeparators()
	if p.peekIsContextual("go") {
		if p.peekAt(1).Type != token.LBRACE {
			foreignAlias, foreignName, aliasTok, goNameTok, err := p.parseGoSelectorBinding()
			if err != nil {
				return nil, err
			}
			return &ast.ExternType{
				Name:             name,
				Public:           public,
				Opaque:           opaque,
				Line:             typeTok.Line,
				Col:              localTok.Col,
				ForeignAlias:     foreignAlias,
				ForeignName:      foreignName,
				ForeignAliasLine: aliasTok.Line,
				ForeignAliasCol:  aliasTok.Col,
				ForeignNameLine:  goNameTok.Line,
				ForeignNameCol:   goNameTok.Col,
			}, nil
		}
		goBody, goBodyLine, goBodyCol, err := p.parseInlineGoBlock()
		if err != nil {
			return nil, err
		}
		return &ast.ExternType{
			Name:       name,
			Public:     public,
			Opaque:     opaque,
			Line:       typeTok.Line,
			Col:        localTok.Col,
			GoBody:     goBody,
			GoBodyLine: goBodyLine,
			GoBodyCol:  goBodyCol,
		}, nil
	}
	if p.peekIsContextual("from") {
		return nil, errorAt(p.peek().Line, p.peek().Col, "Go-backed types use `go package.Type` bindings")
	}
	p.pos = save
	return p.parseTypeDef(public, opaque)
}

// parseStructDef parses: [pub ][opaque ]struct Name[<TypeParams>] {fields...}.
// The STRUCT token has not been consumed yet; this function advances past
// it and delegates the brace body to parseStructBody.
func (p *Parser) parseStructDef(public bool, opaque bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume STRUCT

	name, nameTok, err := p.parseQualifiedTypeDeclName("struct")
	if err != nil {
		return nil, err
	}

	var typeParams []ast.TypeParam
	if !p.atEnd() && p.peek().Type == token.LT {
		params, err := p.parseTypeParams()
		if err != nil {
			return nil, err
		}
		typeParams = params
	}

	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after struct name")
	}
	return p.parseStructBody(public, opaque, name, typeParams, whereClauses, tok.Line, nameTok.Col)
}

// parseEnumDef parses: [pub ][opaque ]enum Name[<TypeParams>] { variant ... }.
// The ENUM token has not been consumed yet; this function advances past it
// and delegates the brace body to parseEnumBody.
func (p *Parser) parseEnumDef(public bool, opaque bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume ENUM

	name, nameTok, err := p.parseQualifiedTypeDeclName("enum")
	if err != nil {
		return nil, err
	}

	var typeParams []ast.TypeParam
	if !p.atEnd() && p.peek().Type == token.LT {
		params, err := p.parseTypeParams()
		if err != nil {
			return nil, err
		}
		typeParams = params
	}

	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after enum name")
	}
	return p.parseEnumBody(public, opaque, name, typeParams, whereClauses, tok.Line, nameTok.Col)
}

// parseStructBody parses the brace body of a struct declaration —
// newline-separated items:
//
//   - field: name: Type [= default]
//
// The keyword and name (and any type params) have already been consumed;
// the LBRACE is the next token. Returns *ast.StructDef. `opaque`
// propagates the `opaque` qualifier so the analyzer can reject
// opaque-on-struct at the declaration site (plan A.9). Enum variant-shaped
// items are rejected — they belong in enums.
func (p *Parser) parseStructBody(public bool, opaque bool, name string, typeParams []ast.TypeParam, whereClauses []ast.WhereConstraint, line, col int) (ast.Node, error) {
	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, p.errorOnLine(line, "expected '{' after type name")
	}
	p.advance() // consume LBRACE
	// Plain separators only — preserve COMMENT / BLANK_LINE so the first
	// field can capture them as LeadingComments (or the StructDef as
	// EndTrivia if the body is comment-only).
	p.skipSeparatorsOnly()

	var fields []ast.StructField
	var items []ast.Node
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Collect any COMMENT / BLANK_LINE trivia sitting before the
		// next member. If `}` follows, the trivia belongs to
		// StructDef.EndTrivia (trailing in-body); otherwise it becomes
		// the next member's LeadingComments (inter-field comment).
		var leading []ast.Trivia
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}

		// Doc comments belong to the next member.
		doc := p.collectDocComments()
		if err := p.rejectDecorator(); err != nil {
			return nil, err
		}
		attachedTests, attachedDoc, attachedLeading, err := p.parseAttachedTestPromptsAndDoc()
		if err != nil {
			return nil, err
		}
		doc = mergeDocComments(doc, attachedDoc)
		leading = append(leading, attachedLeading...)

		tk := p.peek()
		switch {
		case tk.Type == token.FN || tk.Type == token.PUB || p.startsHostDecl() || tk.Type == token.ONCE:
			if _, err := p.parseTypeBodyItem(); err != nil {
				return nil, err
			}
			return nil, errorAt(tk.Line, tk.Col, "struct bodies hold only fields")
		case tk.Type == token.TEST || tk.Type == token.TESTS || p.startsConformanceLine():
			if _, err := p.parseTypeBodyItem(); err != nil {
				return nil, err
			}
			return nil, errorAt(tk.Line, tk.Col, "struct bodies hold only fields")
		case tk.Type == token.TYPE_IDENT:
			return nil, errorAt(tk.Line, tk.Col, "variants are not allowed in a struct body — variants belong in an enum body")
		case tk.Type == token.IDENT:
			// Bare field — parsed below.
		default:
			return nil, errorAt(tk.Line, tk.Col, "expected field name in struct `%s` body, got %s", name, tk.Type)
		}

		if err := rejectAttachedTestsOnNonDeclaration(attachedTests, "a struct field"); err != nil {
			return nil, err
		}

		// Expect field name (IDENT)
		if p.atEnd() || p.peek().Type != token.IDENT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct definition")
		}
		fieldStart := p.pos
		fieldTok := p.peek()
		fieldName := fieldTok.Lexeme
		p.advance() // consume IDENT

		// Expect COLON
		if p.atEnd() || p.peek().Type != token.COLON {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ':' after field name '%s'", fieldName)
		}
		p.advance() // consume COLON

		// Parse type annotation
		fieldTypeExpr, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}

		// Optional default value: = expr
		var defaultExpr ast.Node
		if !p.atEnd() && p.peek().Type == token.EQ {
			p.advance() // consume '='
			defaultExpr, err = p.parseExpr(1)
			if err != nil {
				return nil, err
			}
		}

		fieldSpan := p.spanSince(fieldStart)
		trailing := p.collectCommentOnLine(p.prevLine())
		fields = append(fields, ast.StructField{SpanCarrier: ast.SpanCarrier{Span: fieldSpan}, Name: fieldName, TypeAnnotation: fieldTypeExpr, Default: defaultExpr, LeadingComments: leading, Trailing: trailing, Doc: doc, Line: fieldTok.Line, Col: fieldTok.Col})

		// Skip newline/semicolon separators only — items are
		// newline-separated, so a stray comma errors at the top of the
		// next iteration. Trivia (COMMENT/BLANK_LINE) after the
		// separators is handled at the top of the next iteration too.
		p.skipEnumVariantTrailingSeparators()
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, p.errorOnLine(line, "expected '}' to close struct definition")
	}
	p.advance() // consume RBRACE

	return &ast.StructDef{Name: name, Public: public, Opaque: opaque, TypeParams: typeParams, WhereClauses: whereClauses, Fields: fields, Items: items, EndTrivia: endTrivia, Line: line, Col: col}, nil
}

// parseTypeAlias parses a type alias: typealias Name Target
func (p *Parser) parseTypeAlias(public bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume TYPEALIAS

	name, nameTok, err := p.parseQualifiedTypeDeclName("typealias")
	if err != nil {
		return nil, err
	}

	// Expect target type directly after the name (no `=` separator —
	// typealias declarations mirror `type Name Inner`). Two shapes:
	//   - single type: `typealias Foo Bar`        → TargetTypeExpr
	//   - bound list:  `typealias Foo A and B`    → Bounds
	first, err := p.parseTypeAnnotation()
	if err != nil {
		return nil, err
	}

	// `and` after the first type means this is a bound alias — collect the
	// rest of the chain. The single-type form leaves Bounds empty.
	if !p.atEnd() && p.peek().Type == token.AND {
		bounds := []ast.TypeExpr{first}
		for !p.atEnd() && p.peek().Type == token.AND {
			p.advance() // consume AND
			next, err := p.parseTypeAnnotation()
			if err != nil {
				return nil, err
			}
			bounds = append(bounds, next)
		}
		return &ast.TypeAlias{
			Name:   name,
			Public: public,
			Bounds: bounds,
			Line:   tok.Line,
			Col:    nameTok.Col,
		}, nil
	}

	return &ast.TypeAlias{
		Name:           name,
		Public:         public,
		TargetTypeExpr: first,
		Line:           tok.Line,
		Col:            nameTok.Col,
	}, nil
}

// parseStructLit parses a brace literal after TypeIdent has been consumed.
//
// Two shapes are accepted, disambiguated by the entry separator:
//   - struct literal: TypeName{ field: value, ... } (uses `:`)
//   - map literal:    TypeName{ key => value, ... } (uses `=>`)
//
// Disambiguation: the first entry's key is speculatively parsed as an
// expression; if it is followed by `=>`, the whole brace is a map literal
// (composite keys — tuple/variant/list/struct/computed — included),
// otherwise it is a struct literal (fields use `:`). Empty `TypeName{}` is
// ambiguous — defaults to a struct literal here; the type checker can
// reclassify based on what TypeName resolves to (rare in practice since
// map-distinct construction with no entries is unusual).
func (p *Parser) parseStructLit(typeName ast.TypeExpr, line, col int) (ast.Node, error) {
	p.advance() // consume LBRACE
	// Use plain separator-skip (no COMMENT / BLANK_LINE) so a leading
	// trivia run before the first field can land in EndTrivia.
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}

	// Map-literal disambiguation: the first key is an arbitrary expression
	// followed by `=>` (struct fields use `:`). Speculatively parse the key
	// and peek for `=>` so composite keys (tuple/variant/list/struct/computed)
	// route to the map form, matching the anonymous-map detection.
	if !p.atEnd() && p.detectMapPatternEntry() {
		return p.parseTypePrefixedMapLit(typeName, line, col)
	}

	var fields []ast.StructFieldVal
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Collect trivia before the next field. If `}` follows, it's
		// the literal's EndTrivia; otherwise it becomes the next field's
		// LeadingComments (inter-field comment).
		var leading []ast.Trivia
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}
		// Struct update by spread has NO named form: the spread head
		// already carries the result type, so `Cfg{..base}` would repeat
		// what the compiler knows. Say that rather than "expected field
		// name", which is what a bare `..` used to get here.
		if p.peek().Type == token.DOTDOT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "struct spread `..` has no named form; write `{..base, field: value}` — the spread head carries the type")
		}
		// Expect field name (IDENT)
		if p.peek().Type != token.IDENT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct literal")
		}
		fieldTok := p.peek()
		fieldName := fieldTok.Lexeme
		p.advance() // consume IDENT

		// Check for field punning: Name without `:` means Name: Name
		if p.atEnd() || p.peek().Type != token.COLON {
			// Field punning — use field name as variable reference
			fields = append(fields, ast.StructFieldVal{
				Name:            fieldName,
				Value:           &ast.Ident{Name: fieldName, Line: fieldTok.Line, Col: fieldTok.Col},
				LeadingComments: leading,
				Line:            fieldTok.Line,
				Col:             fieldTok.Col,
			})
			for !p.atEnd() {
				tt := p.peek().Type
				if tt == token.NEWLINE || tt == token.SEMICOLON {
					p.advance()
				} else {
					break
				}
			}
			if !p.atEnd() && p.peek().Type == token.COMMA {
				p.advance()
				for !p.atEnd() {
					tt := p.peek().Type
					if tt == token.NEWLINE || tt == token.SEMICOLON {
						p.advance()
					} else {
						break
					}
				}
			}
			continue
		}
		p.advance() // consume COLON

		// Parse value expression
		val, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}

		fields = append(fields, ast.StructFieldVal{Name: fieldName, Value: val, LeadingComments: leading, Line: fieldTok.Line, Col: fieldTok.Col})

		// Skip separators (comma, newline). Keep COMMENT / BLANK_LINE
		// for the next iteration's EndTrivia check.
		for !p.atEnd() {
			tt := p.peek().Type
			if tt == token.NEWLINE || tt == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
		if !p.atEnd() && p.peek().Type == token.COMMA {
			p.advance()
			for !p.atEnd() {
				tt := p.peek().Type
				if tt == token.NEWLINE || tt == token.SEMICOLON {
					p.advance()
				} else {
					break
				}
			}
		}
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, p.errorOnLine(line, "expected '}' to close struct literal")
	}
	p.advance() // consume RBRACE

	return &ast.StructLit{TypeName: typeName, Fields: fields, EndTrivia: endTrivia, Line: line, Col: col}, nil
}

// atOperandEnd reports whether a `return`, `break` or `continue` has no
// operand: the next token ends the statement. A same-line comment ends it as
// a newline does, so `return // done` is a bare return.
func (p *Parser) atOperandEnd() bool {
	if p.atEnd() {
		return true
	}
	switch p.peek().Type {
	case token.NEWLINE, token.SEMICOLON, token.RBRACE, token.COMMENT, token.BLANK_LINE:
		return true
	}
	return false
}

// parseReturn parses a return statement: return [expr]
func (p *Parser) parseReturn() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume RETURN

	// Bare return if next token is a statement terminator
	if p.atOperandEnd() {
		return &ast.Return{Value: nil, Line: tok.Line, Col: tok.Col}, nil
	}

	value, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	return &ast.Return{Value: value, Line: tok.Line, Col: tok.Col}, nil
}

// parseBreak parses a break statement: break [expr]
func (p *Parser) parseBreak() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume BREAK

	// Bare break if next token is a statement terminator
	if p.atOperandEnd() {
		return &ast.Break{Value: nil, Line: tok.Line, Col: tok.Col}, nil
	}

	val, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	return &ast.Break{Value: val, Line: tok.Line, Col: tok.Col}, nil
}

// parseContinue parses a continue statement: continue. An expression written
// after it on the same line is kept as Continue.Value for the checker to
// reject; see ast.Continue.
func (p *Parser) parseContinue() (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume CONTINUE
	if p.atOperandEnd() || p.peek().Type == token.RPAREN || p.peek().Type == token.RBRACKET || p.peek().Type == token.COMMA {
		return &ast.Continue{Line: tok.Line, Col: tok.Col}, nil
	}
	val, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	return &ast.Continue{Value: val, Line: tok.Line, Col: tok.Col}, nil
}

// parseEnumPatternPayload parses `(` ... `)` after a variant name in a pattern context.
// Handles simple fast paths (ident binding, wildcard) and the general case of a
// recursive nested pattern (literal, tuple destructure, nested variant).
// Assumes the current token is LPAREN; consumes through the matching RPAREN.
func (p *Parser) parseEnumPatternPayload(variant ast.TypeExpr, line, col int) (ast.Node, error) {
	lparen := p.peek()
	p.advance() // consume LPAREN

	// Fast paths: Variant(ident) / Variant(_) — a single IDENT or UNDERSCORE
	// immediately followed by RPAREN. Populates Binding directly (no recursion).
	if !p.atEnd() && p.peek().Type == token.IDENT && p.peekAt(1).Type == token.RPAREN {
		bindTok := p.peek()
		p.advance() // consume IDENT
		p.advance() // consume RPAREN
		return &ast.EnumPattern{
			Variant:    variant,
			Binding:    bindTok.Lexeme,
			BindingCol: bindTok.Col,
			Line:       line,
			Col:        col,
		}, nil
	}
	if !p.atEnd() && p.peek().Type == token.UNDERSCORE && p.peekAt(1).Type == token.RPAREN {
		wcTok := p.peek()
		p.advance() // consume UNDERSCORE
		p.advance() // consume RPAREN
		// Keep an explicit WildcardPattern Payload so downstream code can
		// distinguish `Variant(_)` (data-carrying, ignored) from bare
		// `Variant` (no-data variant). Without it the AST would be
		// indistinguishable from a bare-variant pattern.
		return &ast.EnumPattern{
			Variant: variant,
			Payload: &ast.WildcardPattern{Line: wcTok.Line, Col: wcTok.Col},
			Line:    line,
			Col:     col,
		}, nil
	}

	// General case: recursively parse a full pattern for the payload.
	// Single payload (`Rect((w, h))`, `Some(x)`, `Err(_)`) is the canonical
	// shape — the variant takes one value, possibly a tuple. As a flat-
	// destructure shorthand for tuple-distinct constructors of arity N>=2,
	// `Pair(a, b, ...)` is also accepted; the comma-separated patterns are
	// packed into a TuplePattern that destructures the inner tuple. The
	// type-checker rejects the same shape against a non-tuple payload type
	// ("tuple pattern requires a tuple type, got X"), so this is safe.
	payload, err := p.parseSinglePattern()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() && p.peek().Type == token.COMMA {
		patterns := []ast.Node{payload}
		for !p.atEnd() && p.peek().Type == token.COMMA {
			p.advance() // consume COMMA
			next, err := p.parseSinglePattern()
			if err != nil {
				return nil, err
			}
			patterns = append(patterns, next)
		}
		payload = &ast.TuplePattern{
			Patterns: patterns,
			Flat:     true,
			Line:     lparen.Line,
			Col:      lparen.Col,
		}
	}
	if p.atEnd() || p.peek().Type != token.RPAREN {
		return nil, errorAt(lparen.Line, lparen.Col, "expected ')' in enum pattern")
	}
	p.advance() // consume RPAREN
	return &ast.EnumPattern{
		Variant: variant,
		Payload: payload,
		Line:    line,
		Col:     col,
	}, nil
}

func canStartPatternAssertion(t token.TokenType) bool {
	switch t {
	case token.IDENT, token.UNDERSCORE,
		token.LBRACKET, token.LBRACE, token.LPAREN, token.TYPE_IDENT, token.DOT,
		token.INT, token.FLOAT, token.DECIMAL, token.CODEPOINT_LITERAL, token.MINUS,
		token.STRING_LITERAL, token.TRIPLE_STRING_LITERAL,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL:
		return true
	default:
		return false
	}
}

// parseNegativeLiteralPattern parses a negative numeric literal pattern: a
// leading MINUS followed by an INT, FLOAT, or DECIMAL. The current token must
// be the MINUS. Mirrors the negation handling in struct-field patterns so a
// leading '-' is accepted in every pattern position the grammar allows —
// top-level case arms, tuple/list elements, and map values.
func (p *Parser) parseNegativeLiteralPattern() (ast.Node, error) {
	minusTok := p.peek()
	p.advance() // consume MINUS
	next := p.peek()
	switch next.Type {
	case token.INT:
		p.advance()
		val, err := strconv.ParseInt(next.Lexeme, 0, 64)
		if err != nil {
			return nil, errorAt(next.Line, next.Col, "invalid integer %q", next.Lexeme)
		}
		return &ast.IntLit{Value: -val, Lexeme: "-" + next.Lexeme, Line: minusTok.Line, Col: minusTok.Col}, nil
	case token.FLOAT:
		p.advance()
		val, err := strconv.ParseFloat(next.Lexeme, 64)
		if err != nil {
			return nil, errorAt(next.Line, next.Col, "invalid float %q", next.Lexeme)
		}
		return &ast.FloatLit{Value: -val, Lexeme: "-" + next.Lexeme, Line: minusTok.Line, Col: minusTok.Col}, nil
	case token.DECIMAL:
		p.advance()
		return &ast.DecimalLit{Lexeme: "-" + next.Lexeme, Line: minusTok.Line, Col: minusTok.Col}, nil
	default:
		return nil, errorAt(minusTok.Line, minusTok.Col, "expected number after '-' in pattern")
	}
}

// detectMapPatternEntry reports whether the tokens at the current position
// begin a map entry — an arbitrary key *expression* followed by '=>'. It
// speculatively parses an expression and peeks for '=>', restoring the parser
// position regardless of the outcome (the caller re-parses the key in its own
// entry loop). Shared by both sides of the map syntax:
//   - map *patterns* — disambiguate `{ keyExpr => pat }` from `{ field }` /
//     `{ field: pat }` struct patterns;
//   - map *literals* — disambiguate `{ keyExpr => val }` from blocks
//     (`{ foo() }`), anon-struct literals (`{ x: 1 }`), and case ad-hoc bodies.
//
// The key is parsed by the ordinary expression parser, so any keyable value
// (variant, tuple, list, struct, distinct, computed) is a valid key on either
// side.
func (p *Parser) detectMapPatternEntry() bool {
	savedPos := p.pos
	// This probe always rewinds, and in resilient mode the speculative
	// parse below can recover — a lambda or block expression in key
	// position. That recovery belongs to a parse being thrown away, so
	// un-count it: otherwise a probe over broken code would mark a
	// declaration damaged that the accepted parse read cleanly, and the
	// LSP would suppress that declaration's type diagnostics for no
	// reason. Restoring the counter is enough because `damaged` itself is
	// only appended once per accepted top-level declaration.
	savedRecoveries := p.recoveries
	defer func() {
		p.pos = savedPos
		p.recoveries = savedRecoveries
	}()
	if _, err := p.parseExpr(1); err != nil {
		return false
	}
	return !p.atEnd() && p.peek().Type == token.FAT_ARROW
}

// parseSinglePattern parses a single pattern element for use in compound patterns (tuples, lists, etc.).
func (p *Parser) parseSinglePattern() (node ast.Node, err error) {
	start := p.pos
	defer func() {
		if err == nil {
			p.recordSpan(node, start)
		}
	}()
	return p.parseSinglePatternInner()
}

func (p *Parser) parseSinglePatternInner() (ast.Node, error) {
	tok := p.peek()
	switch tok.Type {
	case token.UNDERSCORE:
		p.advance()
		return &ast.WildcardPattern{Line: tok.Line, Col: tok.Col}, nil
	case token.LPAREN:
		// Parenthesized pattern: grouping or tuple destructure.
		p.advance() // consume LPAREN
		var patterns []ast.Node
		for {
			pat, err := p.parseSinglePattern()
			if err != nil {
				return nil, err
			}
			patterns = append(patterns, pat)
			if p.atEnd() {
				return nil, errorAt(tok.Line, tok.Col, "expected ')' in tuple pattern")
			}
			if p.peek().Type == token.RPAREN {
				p.advance()
				break
			}
			if p.peek().Type != token.COMMA {
				return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ')' in tuple pattern")
			}
			p.advance() // consume COMMA
		}
		if len(patterns) < 2 {
			return patterns[0], nil
		}
		return &ast.TuplePattern{Patterns: patterns, Line: tok.Line, Col: tok.Col}, nil
	case token.INT:
		p.advance()
		val, err := strconv.ParseInt(tok.Lexeme, 0, 64)
		if err != nil {
			return nil, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
		}
		return &ast.IntLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil
	case token.FLOAT:
		p.advance()
		val, err := strconv.ParseFloat(tok.Lexeme, 64)
		if err != nil {
			return nil, errorAt(tok.Line, tok.Col, "invalid float %q", tok.Lexeme)
		}
		return &ast.FloatLit{Value: val, Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil
	case token.DECIMAL:
		p.advance()
		return &ast.DecimalLit{Lexeme: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil
	case token.CODEPOINT_LITERAL:
		p.advance()
		return codepointLit(tok), nil
	case token.MINUS:
		return p.parseNegativeLiteralPattern()
	case token.STRING_LITERAL:
		p.advance()
		pat := &ast.StringLit{Value: tok.Lexeme, Line: tok.Line, Col: tok.Col}
		if prefixed, ok, err := p.tryParseStringPrefixPattern(pat, tok.Line, tok.Col); err != nil {
			return nil, err
		} else if ok {
			return prefixed, nil
		}
		return pat, nil
	case token.TRIPLE_STRING_LITERAL:
		p.advance()
		pat := &ast.StringLit{Value: tok.Lexeme, Triple: true, Line: tok.Line, Col: tok.Col}
		if prefixed, ok, err := p.tryParseStringPrefixPattern(pat, tok.Line, tok.Col); err != nil {
			return nil, err
		} else if ok {
			return prefixed, nil
		}
		return pat, nil
	case token.RAW_STRING_LITERAL:
		p.advance()
		pat := &ast.StringLit{Value: tok.Lexeme, Raw: true, Line: tok.Line, Col: tok.Col}
		if prefixed, ok, err := p.tryParseStringPrefixPattern(pat, tok.Line, tok.Col); err != nil {
			return nil, err
		} else if ok {
			return prefixed, nil
		}
		return pat, nil
	case token.RAW_TRIPLE_STRING_LITERAL:
		p.advance()
		pat := &ast.StringLit{Value: tok.Lexeme, Triple: true, Raw: true, Line: tok.Line, Col: tok.Col}
		if prefixed, ok, err := p.tryParseStringPrefixPattern(pat, tok.Line, tok.Col); err != nil {
			return nil, err
		} else if ok {
			return prefixed, nil
		}
		return pat, nil
	case token.IDENT:
		if p.peekAt(1).Type == token.DOT && (p.peekAt(2).Type == token.TYPE_IDENT || p.peekAt(2).Type == token.IDENT) {
			p.advance()
			typeExpr, _ := p.parseDottedPatternType(tok)
			if !p.atEnd() && p.peek().Type == token.LPAREN {
				return p.parseEnumPatternPayload(typeExpr, tok.Line, tok.Col)
			}
			if !p.atEnd() && p.peek().Type == token.LBRACKET {
				return p.parseTypePrefixedListPattern(typeExpr, tok.Line, tok.Col)
			}
			if !p.atEnd() && p.peek().Type == token.LBRACE {
				lbraceTok := p.peek()
				return p.parseTypePrefixedBracePattern(typeExpr, tok.Line, tok.Col, lbraceTok.Line)
			}
			return &ast.EnumPattern{Variant: typeExpr, Binding: "", Line: tok.Line, Col: tok.Col}, nil
		}
		p.advance()
		return &ast.IdentPattern{Name: tok.Lexeme, Line: tok.Line, Col: tok.Col}, nil
	case token.DOT:
		// Dot-leading variant pattern in nested position (e.g. `Ok(.Obj{...})`).
		if p.peekAt(1).Type != token.TYPE_IDENT {
			return nil, errorAt(tok.Line, tok.Col, "expected variant name after '.'")
		}
		p.advance() // consume DOT
		nameTok := p.peek()
		p.advance() // consume TYPE_IDENT
		dvt := &ast.DotVariantType{Name: nameTok.Lexeme, Line: tok.Line, Col: tok.Col}
		if !p.atEnd() && p.peek().Type == token.LPAREN {
			return p.parseEnumPatternPayload(dvt, tok.Line, tok.Col)
		}
		if !p.atEnd() && p.peek().Type == token.LBRACKET {
			return p.parseTypePrefixedListPattern(dvt, tok.Line, tok.Col)
		}
		if !p.atEnd() && p.peek().Type == token.LBRACE {
			return p.parseTypePrefixedBracePattern(dvt, tok.Line, tok.Col, nameTok.Line)
		}
		return &ast.EnumPattern{Variant: dvt, Binding: "", Line: tok.Line, Col: tok.Col}, nil
	case token.TYPE_IDENT:
		p.advance()
		// Check for dotted name: Shape.Point, Error.Timeout, or a dotted type
		// name in front of the variant (Probe.Reading.Steady).
		typeExpr, _ := p.parseDottedPatternType(tok)
		// Check for enum positional pattern: Variant(...) — may be simple binding,
		// wildcard, or a nested pattern (literal, tuple destructure, another variant).
		if !p.atEnd() && p.peek().Type == token.LPAREN {
			return p.parseEnumPatternPayload(typeExpr, tok.Line, tok.Col)
		}
		// Type-prefixed list pattern: `Arr[a, b, ..rest]` — the literal-
		// attach destructure form for list-payload variants. Mirrors the
		// LBRACE map-pattern handling further up.
		if !p.atEnd() && p.peek().Type == token.LBRACKET {
			return p.parseTypePrefixedListPattern(typeExpr, tok.Line, tok.Col)
		}
		// Type-prefixed brace pattern: `Obj{"k" => v}` (map) or
		// `Point{x, y}` (struct). Same disambiguation as the top-level
		// case-arm pattern parser — shared through parseTypePrefixedBracePattern
		// so the shape parses identically nested inside another pattern
		// (e.g. `Ok(Obj{"k" => v})`).
		if !p.atEnd() && p.peek().Type == token.LBRACE {
			lbraceTok := p.peek()
			return p.parseTypePrefixedBracePattern(typeExpr, tok.Line, tok.Col, lbraceTok.Line)
		}
		// Bare enum variant (True, False, None, North, etc.)
		return &ast.EnumPattern{Variant: typeExpr, Binding: "", Line: tok.Line, Col: tok.Col}, nil
	case token.LBRACKET:
		return p.parseListPatternBody(nil, tok.Line, tok.Col)
	case token.LBRACE:
		// Brace-led inner pattern. Two shapes share this branch, disambiguated
		// by `<keyExpr> => ...` lookahead:
		//   - Anonymous map pattern `{"a" => v}` — also the call-form
		//     destructure for map-distinct types: `Kvs({"a" => v})` parses as
		//     an EnumPattern whose payload is this anonymous MapPattern; the
		//     EnumPattern branch in the checker unwraps the distinct before
		//     recursing, mirroring the literal-attach form `Kvs{"a" => v}`.
		//   - Anonymous struct pattern `{x, y}` / `{name: n}` — the param-
		//     destructure shape (`fn f({x, y}: Point)`) and any nested struct
		//     pattern (`Ok({x, y})`). Routed through the same
		//     parseTypePrefixedBracePattern (nil typeName) the case-arm parser
		//     uses, so the shape parses identically everywhere.
		if p.atEnd() {
			return nil, errorAt(tok.Line, tok.Col, "expected pattern")
		}
		return p.parseTypePrefixedBracePattern(nil, tok.Line, tok.Col, tok.Line)
	default:
		if tok.Problem != "" {
			return nil, errorAt(tok.Line, tok.Col, "%s", tok.Problem)
		}
		return nil, errorAt(tok.Line, tok.Col, "unexpected token %s in pattern", tok.Type)
	}
}

func (p *Parser) tryParseStringPrefixPattern(left ast.Node, line, col int) (ast.Node, bool, error) {
	if p.atEnd() || p.peek().Type != token.PLUS {
		return nil, false, nil
	}
	plusTok := p.peek()
	if p.peekAt(1).Type != token.IDENT {
		return nil, false, nil
	}
	p.advance()
	nameTok := p.peek()
	p.advance()
	return &ast.Binary{
		Left:  left,
		Op:    plusTok.Lexeme,
		Right: &ast.IdentPattern{Name: nameTok.Lexeme, Line: nameTok.Line, Col: nameTok.Col},
		Line:  line,
		Col:   col,
	}, true, nil
}

// --- helpers ---

func (p *Parser) peek() token.Token {
	return p.tokens[p.pos]
}

// parseDottedPatternType consumes a pattern's type prefix, which may carry a
// dotted type name in front of the variant: `Steady`, `Reading.Steady`,
// `Probe.Reading.Steady`.
//
// Everything before the last segment names the owner, because a dotted type
// name is one name whose spelling contains a dot — the owner of
// `Probe.Reading.Steady` is `Probe.Reading`, not `Probe` with `Reading` looked
// up inside it. The analyzer resolves that owner by the whole name, so the
// parser's job is only to keep the segments together.
//
// Segments are consumed only while a dot is actually followed by another
// identifier segment, so a dangling dot is left for the caller to report.
//
// Returns the type expression and the final segment's token, whose position is
// the variant's own.
func (p *Parser) parseDottedPatternType(first token.Token) (ast.TypeExpr, token.Token) {
	segments := []token.Token{first}
	for !p.atEnd() && p.peek().Type == token.DOT && (p.peekAt(1).Type == token.TYPE_IDENT || p.peekAt(1).Type == token.IDENT) {
		p.advance() // consume DOT
		segments = append(segments, p.peek())
		p.advance() // consume the segment
	}
	last := segments[len(segments)-1]
	if len(segments) == 1 {
		return &ast.SimpleType{Name: first.Lexeme, Line: first.Line, Col: first.Col}, last
	}
	owner := make([]string, 0, len(segments)-1)
	for _, seg := range segments[:len(segments)-1] {
		owner = append(owner, seg.Lexeme)
	}
	return &ast.QualifiedType{
		Module:     strings.Join(owner, "."),
		ModuleLine: first.Line,
		ModuleCol:  first.Col,
		Member:     &ast.SimpleType{Name: last.Lexeme, Line: last.Line, Col: last.Col},
	}, last
}

func (p *Parser) peekAt(offset int) token.Token {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return token.Token{Type: token.EOF}
	}
	return p.tokens[idx]
}

func (p *Parser) peekIsContextual(lexeme string) bool {
	return !p.atEnd() && p.peek().Type == token.IDENT && p.peek().Lexeme == lexeme
}

func (p *Parser) advance() {
	p.pos++
}

// tokenEnd returns the position one past the last column of a single-line
// token — its exclusive end. Used to span scope-anchoring constructs whose end
// is the last token of a body rather than a closing delimiter (lambdas,
// case-branch bodies). Multi-line tokens (triple-quoted strings) fall back to
// the token's start: a conservative under-extension that never yields a wrong
// containment (you don't autocomplete inside string content anyway). The real
// fix for that edge — end positions on string tokens — lives in the lexer and
// is not done here.
func tokenEnd(t token.Token) (line, col int) {
	if strings.ContainsRune(t.Lexeme, '\n') {
		return t.Line, t.Col
	}
	return t.Line, t.Col + len(t.Lexeme)
}

func (p *Parser) atEnd() bool {
	return p.pos >= len(p.tokens) || p.tokens[p.pos].Type == token.EOF
}

// skipNewlines steps over NEWLINE, COMMENT, and BLANK_LINE tokens. It is used
// in non-attachment contexts where trivia would otherwise disrupt parsing.
// Attachment-site code must NOT use skipNewlines before trivia collection —
// use collectLeadingTrivia() instead.
func (p *Parser) skipNewlines() {
	for !p.atEnd() {
		switch p.tokens[p.pos].Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			p.pos++
		case token.COMMENT:
			if isAttachedCommentTestStart(p.tokens[p.pos]) || isAttachedCommentTestEnd(p.tokens[p.pos]) {
				return
			}
			p.pos++
		default:
			return
		}
	}
}

// parseMapLit parses a map literal: { key => value, ... } (LBRACE already consumed).
func (p *Parser) parseMapLit(openTok token.Token) (ast.Node, error) {
	entries, endTrivia, err := p.parseMapLitEntries(openTok.Line)
	if err != nil {
		return nil, err
	}
	return &ast.MapLit{Entries: entries, EndTrivia: endTrivia, Line: openTok.Line, Col: openTok.Col}, nil
}

// parseMapPatternEntries parses `literal => pat, ...` up to and including
// the closing RBRACE for a TypeName-prefixed map pattern. Caller has
// already consumed LBRACE and the leading whitespace/newlines, plus
// committed to map-pattern shape via lookahead. Returns a MapPattern
// with the supplied TypeName attached.
func (p *Parser) parseMapPatternEntries(typeName ast.TypeExpr, line, col int) (*ast.MapPattern, error) {
	var entries []ast.MapPatternEntry
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// End-of-body trivia: see parseMapLitEntries.
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = collected
				break
			}
			p.pos = save
		}
		// The key is an arbitrary expression (parsed identically to a map
		// literal's key), then '=>'. Any keyable value works as a key.
		keyNode, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		if p.atEnd() || p.peek().Type != token.FAT_ARROW {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected '=>' in map pattern")
		}
		p.advance() // consume FAT_ARROW
		valPat, err := p.parseSinglePattern()
		if err != nil {
			return nil, err
		}
		entries = append(entries, ast.MapPatternEntry{Key: keyNode, Pattern: valPat})
		// Plain separators only — preserve COMMENT / BLANK_LINE for the
		// next iteration's EndTrivia check.
		for !p.atEnd() {
			tt := p.peek().Type
			if tt == token.COMMA || tt == token.NEWLINE || tt == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, p.errorOnLine(line, "expected '}' in map pattern")
	}
	p.advance() // consume RBRACE
	return &ast.MapPattern{TypeName: typeName, Entries: entries, EndTrivia: endTrivia, Line: line, Col: col}, nil
}

// parseTypePrefixedMapLit parses a type-prefixed map literal:
// TypeName{ key => value, ... } (LBRACE already consumed).
//
// The literal-attach construction form for map-distinct types. Mirrors
// the tuple-distinct literal-attach (`Pair(1, "x")`) — the type name
// stays attached to the brace literal in the AST so the type checker
// can validate K/V against the named map-distinct's inner Map<K, V>.
func (p *Parser) parseTypePrefixedMapLit(typeName ast.TypeExpr, line, col int) (ast.Node, error) {
	entries, endTrivia, err := p.parseMapLitEntries(line)
	if err != nil {
		return nil, err
	}
	return &ast.MapLit{TypeName: typeName, Entries: entries, EndTrivia: endTrivia, Line: line, Col: col}, nil
}

// parseTypePrefixedListPattern parses a type-prefixed list pattern:
// TypeName[pat, pat, ..rest]. The LBRACKET is the current token.
//
// Mirrors parseTypePrefixedListLit on the construction side — the
// type name stays attached to the list pattern so the checker can
// route destructuring through a list-payload variant or list-distinct
// type. The body parsing reuses parseListPatternBody, which is also
// the entry point for anonymous list patterns and for nested list
// patterns inside another pattern context.
func (p *Parser) parseTypePrefixedListPattern(typeName ast.TypeExpr, line, col int) (ast.Node, error) {
	return p.parseListPatternBody(typeName, line, col)
}

// parseTypePrefixedBracePattern parses a `TypeName{...}` pattern, with
// `TypeName` already consumed and the parser positioned at the LBRACE.
// Disambiguates between map-pattern shape (`{"key" => pat, ...}`) and
// struct-pattern shape (`{field: pat, ...}` or punned `{field, ...}`)
// by peeking at the entry separator after the first token. Used by
// both the top-level case-arm pattern parser and parseSinglePattern,
// so `Ok(Obj{"k" => v})` parses identically to top-level `Obj{"k" => v}`.
//
// startLine/startCol locate the TypeName for the resulting pattern's
// Line/Col; lbraceLine is used only for the "expected '}'" error
// message (matches the legacy error wording).
func (p *Parser) parseTypePrefixedBracePattern(typeName ast.TypeExpr, startLine, startCol, lbraceLine int) (ast.Node, error) {
	p.advance() // consume LBRACE
	p.skipNewlines()

	// Map pattern: TypeName{ keyExpr => pat, ... }
	isMapPattern := !p.atEnd() && p.detectMapPatternEntry()
	if isMapPattern {
		mp, err := p.parseMapPatternEntries(typeName, startLine, startCol)
		if err != nil {
			return nil, err
		}
		return mp, nil
	}

	// Struct pattern: TypeName{ field [: pat], ... }
	var fields []ast.StructPatternField
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		if p.peek().Type != token.IDENT {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected field name in struct pattern")
		}
		fieldTok := p.peek()
		fieldName := fieldTok.Lexeme
		p.advance()
		spf := ast.StructPatternField{Name: fieldName, NameLine: fieldTok.Line, NameCol: fieldTok.Col, Binding: fieldName, BindingLine: fieldTok.Line, BindingCol: fieldTok.Col}
		if !p.atEnd() && p.peek().Type == token.COLON {
			p.advance() // consume COLON
			if err := p.parseStructFieldValue(&spf); err != nil {
				return nil, err
			}
		}
		fields = append(fields, spf)
		// Skip comma/newlines
		for !p.atEnd() {
			tt := p.peek().Type
			if tt == token.COMMA || tt == token.NEWLINE || tt == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, p.errorOnLine(lbraceLine, "expected '}' in struct pattern")
	}
	p.advance() // consume RBRACE
	return &ast.StructPattern{
		TypeName: typeName,
		Fields:   fields,
		Line:     startLine,
		Col:      startCol,
	}, nil
}

// parseStructFieldValue parses a struct-pattern field's value (the part after
// `field:`) and stores it on spf. A bare identifier is a rename binding
// (`{x: y}` binds field x to local y); anything else is a full nested pattern
// parsed via parseSinglePattern, so struct / enum / tuple / literal / wildcard
// patterns nest arbitrarily in field position (`{inner: Inner{v: vv}}`). The
// COLON has already been consumed. Shared by the typed-struct pattern parser
// (parseTypePrefixedBracePattern) and the case-branch anonymous-struct parser.
func (p *Parser) parseStructFieldValue(spf *ast.StructPatternField) error {
	if !p.atEnd() && p.peek().Type == token.IDENT {
		valTok := p.peek()
		p.advance()
		spf.Binding = valTok.Lexeme
		spf.BindingLine = valTok.Line
		spf.BindingCol = valTok.Col
		spf.Pattern = nil
		return nil
	}
	pat, err := p.parseSinglePattern()
	if err != nil {
		return err
	}
	spf.Binding = ""
	spf.BindingLine = 0
	spf.BindingCol = 0
	spf.Pattern = pat
	return nil
}

// parseListPatternBody parses the contents of a list pattern starting
// from the LBRACKET token (which is the current token on entry). The
// typeName argument is nil for anonymous list patterns (`[a, b]`) and
// non-nil for the type-prefixed form (`Arr[a, b]`).
func (p *Parser) parseListPatternBody(typeName ast.TypeExpr, line, col int) (ast.Node, error) {
	openTok := p.peek()
	p.advance() // consume LBRACKET
	// Plain separators only — COMMENT / BLANK_LINE is captured below as
	// EndTrivia. See parseListLit for the rationale.
	p.skipSeparatorsOnly()
	var heads []ast.Node
	var endTrivia []ast.Trivia
	if !p.atEnd() && p.peek().Type == token.RBRACKET {
		p.advance()
		return &ast.ListPattern{TypeName: typeName, Heads: heads, TailSpread: nil, Line: line, Col: col}, nil
	}
	for {
		p.skipSeparatorsOnly()
		// End-of-body trivia: trivia followed only by `]` becomes EndTrivia.
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACKET {
				endTrivia = collected
				p.advance()
				return &ast.ListPattern{TypeName: typeName, Heads: heads, TailSpread: nil, EndTrivia: endTrivia, Line: line, Col: col}, nil
			}
			p.pos = save
		}
		// A trailing comma after the last head, which `nomi fmt` writes
		// when it breaks a list pattern across lines.
		if len(heads) > 0 && !p.atEnd() && p.peek().Type == token.RBRACKET {
			p.advance()
			return &ast.ListPattern{TypeName: typeName, Heads: heads, TailSpread: nil, Line: line, Col: col}, nil
		}
		// Spread tail: `..pat` with no preceding element. Patterns don't
		// have a range operator, so DOTDOT here unambiguously starts the
		// spread. Must be the last element of the pattern.
		if !p.atEnd() && p.peek().Type == token.DOTDOT {
			spreadTok := p.peek()
			p.advance() // consume DOTDOT
			p.skipNewlines()
			tail, err := p.parseSinglePattern()
			if err != nil {
				return nil, err
			}
			p.skipNewlines()
			if p.atEnd() || p.peek().Type != token.RBRACKET {
				return nil, errorAt(spreadTok.Line, spreadTok.Col, "list pattern spread `..` must be the last element")
			}
			p.advance()
			return &ast.ListPattern{TypeName: typeName, Heads: heads, TailSpread: tail, Line: line, Col: col}, nil
		}
		pat, err := p.parseSinglePattern()
		if err != nil {
			return nil, err
		}
		heads = append(heads, pat)
		p.skipSeparatorsOnly()
		if p.atEnd() {
			return nil, errorAt(openTok.Line, openTok.Col, "expected ']' in list pattern")
		}
		if p.peek().Type == token.RBRACKET {
			p.advance()
			return &ast.ListPattern{TypeName: typeName, Heads: heads, TailSpread: nil, Line: line, Col: col}, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ']' in list pattern")
		}
		p.advance()
	}
}

// parseTypePrefixedListLit parses a type-prefixed list literal:
// TypeName[item, item, ...]. The LBRACKET is the current token.
//
// Mirrors parseTypePrefixedMapLit — the type name stays attached to
// the list literal so the type checker can route construction to a
// list-payload variant or a list-distinct type. List-spread is not
// supported in the type-prefixed form (the inner literal carries the
// full sequence; spread inside a wrapper rarely makes sense).
func (p *Parser) parseTypePrefixedListLit(typeName ast.TypeExpr, line, col int) (ast.Node, error) {
	openTok := p.peek()
	p.advance() // consume LBRACKET
	// Plain separators only — COMMENT / BLANK_LINE is captured below as
	// EndTrivia. See parseListLit for the rationale.
	p.skipSeparatorsOnly()

	var items []ast.Node
	var endTrivia []ast.Trivia
	if !p.atEnd() && p.peek().Type == token.RBRACKET {
		p.advance() // consume RBRACKET
		return &ast.ListLit{TypeName: typeName, Items: items, Line: line, Col: col}, nil
	}

	for {
		p.skipSeparatorsOnly()
		// End-of-body trivia: see parseListLit.
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACKET {
				endTrivia = collected
				p.advance() // consume RBRACKET
				return &ast.ListLit{TypeName: typeName, Items: items, EndTrivia: endTrivia, Line: line, Col: col}, nil
			}
			p.pos = save
		}
		item, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		p.skipSeparatorsOnly()
		if p.atEnd() {
			return nil, errorAt(openTok.Line, openTok.Col, "expected ']' to close list literal")
		}
		if p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.ListLit{TypeName: typeName, Items: items, Line: line, Col: col}, nil
		}
		if p.peek().Type != token.COMMA {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected ',' or ']' in list literal")
		}
		p.advance() // consume COMMA
		p.skipSeparatorsOnly()
		if !p.atEnd() && p.peek().Type == token.RBRACKET {
			p.advance() // consume RBRACKET
			return &ast.ListLit{TypeName: typeName, Items: items, Line: line, Col: col}, nil
		}
	}
}

// parseMapLitEntries parses `key => value, ...` entries up to and
// including the closing RBRACE. Caller has already consumed LBRACE and
// the leading whitespace/newlines. Returns any trailing comments / blank
// lines that sit between the last entry and `}` as endTrivia so the
// caller can stash them on MapLit.EndTrivia — pre-fix the COMMENT
// terminated the loop and dropped into the "expected `}`" error path,
// which surfaced as the long-known map-trailing-comment parse failure.
func (p *Parser) parseMapLitEntries(line int) ([]ast.MapEntry, []ast.Trivia, error) {
	var entries []ast.MapEntry
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// End-of-body trivia: trivia followed only by `}` becomes
		// EndTrivia (preserved through the formatter). Trivia followed by
		// another entry token falls through; MapEntry has no
		// inter-entry trivia carrier today.
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			save := p.pos
			collected := p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = collected
				break
			}
			p.pos = save
		}
		key, err := p.parseExpr(1)
		if err != nil {
			return nil, nil, err
		}
		if p.atEnd() || p.peek().Type != token.FAT_ARROW {
			return nil, nil, errorAt(p.peek().Line, p.peek().Col, "expected '=>' in map literal")
		}
		p.advance() // consume FAT_ARROW
		val, err := p.parseExpr(1)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, ast.MapEntry{Key: key, Value: val})
		// Skip plain separators only — COMMENT / BLANK_LINE survives for
		// the next iteration's EndTrivia check.
		for !p.atEnd() {
			t := p.peek().Type
			if t == token.COMMA || t == token.NEWLINE || t == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
	}
	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, nil, p.errorOnLine(line, "expected '}' to close map literal")
	}
	p.advance() // consume RBRACE
	return entries, endTrivia, nil
}

func (p *Parser) parseInterfaceDef(public bool) (ast.Node, error) {
	tok := p.peek()
	p.advance() // consume INTERFACE

	if p.atEnd() || (p.peek().Type != token.TYPE_IDENT && p.peek().Type != token.IDENT) {
		return nil, errorAt(tok.Line, tok.Col, "expected interface name after 'interface'")
	}
	nameTok := p.peek()
	name := nameTok.Lexeme
	p.advance()

	// Optional type parameters: <T, U>
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}

	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}

	if p.atEnd() || p.peek().Type != token.LBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '{' after interface name")
	}
	p.advance() // consume LBRACE
	// Use plain separator-skip (no COMMENT/BLANK_LINE consumption) so a
	// trivia-only leading run gets a chance to land in EndTrivia at the
	// top of the loop. The previous `skipNewlines()` here would silently
	// swallow comments before the first member.
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}

	var methods []ast.InterfaceMethod
	var fields []ast.InterfaceField
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Collect trivia before the next member. `}` follows →
		// EndTrivia on the InterfaceDef; another member follows → that
		// member's LeadingComments slot (InterfaceField has a struct
		// field, InterfaceMethod uses its embedded TriviaCarrier).
		var leading []ast.Trivia
		t := p.peek().Type
		if t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}
		// Doc comments belong to the next member: inherent items
		// (FuncDef/ExternFunc) and contract members (InterfaceMethod/
		// InterfaceField) all carry a Doc slot.
		doc := p.collectDocComments()
		// `field` and `open` are contextual keywords — IDENT-lexeme
		// match inside the interface body, so they don't conflict with
		// regular identifiers anywhere else (struct fields named
		// `field`, bindings named `open`, etc.).
		switch {
		case p.peek().Type == token.IDENT && p.peek().Lexeme == "field":
			f, err := p.parseInterfaceField()
			if err != nil {
				return nil, err
			}
			f.LeadingComments = leading
			f.Trailing = p.collectCommentOnLine(p.prevLine())
			f.Doc = doc
			fields = append(fields, f)
		case p.peek().Type == token.IDENT && p.peek().Lexeme == "open":
			method, err := p.parseInterfaceMethod()
			if err != nil {
				return nil, err
			}
			for _, tr := range leading {
				method.AddLeading(tr)
			}
			method.Trailing = p.collectCommentOnLine(p.prevLine())
			method.Doc = doc
			methods = append(methods, method)
		case p.peek().Type == token.FN:
			// Plain `fn` — a contract member: signature-only = required
			// method; with a body = final default method. `pub fn` /
			// `host fn` below are the inherent-item forms. This is the
			// permanent four-way classification (spec §13).
			method, err := p.parseInterfaceMethod()
			if err != nil {
				return nil, err
			}
			for _, tr := range leading {
				method.AddLeading(tr)
			}
			method.Trailing = p.collectCommentOnLine(p.prevLine())
			method.Doc = doc
			methods = append(methods, method)
		case p.startsHostFuncDecl():
			// Host-backed default: `host fn name(...): T` — a contract
			// method whose body the host supplies. (`open host fn` enters
			// via the `open` case above.)
			method, err := p.parseInterfaceMethod()
			if err != nil {
				return nil, err
			}
			for _, tr := range leading {
				method.AddLeading(tr)
			}
			method.Trailing = p.collectCommentOnLine(p.prevLine())
			method.Doc = doc
			methods = append(methods, method)
		case p.peek().Type == token.PUB:
			// `pub` is not an interface-item modifier: a method's visibility is
			// the interface's. (The old inherent-op category `pub` marked is
			// gone — ops are defaults; host-backed defaults are `host fn`.)
			return nil, errorAt(p.peek().Line, p.peek().Col, "'pub' is not allowed on interface methods — a method's visibility is the interface's; drop 'pub' (it becomes a default), or use 'host fn' for a host-backed default")
		default:
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected 'fn', 'open fn', 'host fn', or 'field' inside interface body, got %s", p.peek().Type)
		}
		// Consume separators between members but preserve COMMENT /
		// BLANK_LINE so they can be captured at the top of the next
		// iteration (either as EndTrivia if `}` follows, or as the
		// next member's LeadingComments if a member follows).
		for !p.atEnd() {
			t := p.peek().Type
			if t == token.NEWLINE || t == token.SEMICOLON {
				p.advance()
			} else {
				break
			}
		}
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, errorAt(tok.Line, tok.Col, "expected '}' to close interface")
	}
	closeTok := p.peek()
	p.advance() // consume RBRACE

	return &ast.InterfaceDef{Name: name, Public: public, TypeParams: typeParams, WhereClauses: whereClauses, Methods: methods, Fields: fields, EndTrivia: endTrivia, Line: tok.Line, Col: nameTok.Col, EndLine: closeTok.Line, EndCol: closeTok.Col}, nil
}

// parseImplBlock parses a top-level `impl` block:
//
//	impl Display for Money { fn to_string(m: Money): String { ... } }
//	impl Iter for Box<T> { fn next(b: Box<T>): Maybe<(T, Box<T>)> { ... } }
//	impl Display for Conn
//
// Interface implementations name the interface before `for` and the receiver
// after `for`. An optional `<...>` receiver type contributes its generic
// parameters; an optional `where` clause after the receiver narrows the
// implementation. A bodyless interface impl is equivalent to an empty impl
// body, useful for marker / field-only / default-only interfaces. Interface
// implementation bodies hold `fn` / `host fn` items and inherit visibility
// from the interface.
// The cursor is at the IMPL token on entry.
func (p *Parser) parseImplBlock() (ast.Node, error) {
	implTok := p.peek()
	p.advance() // consume IMPL

	// Optional implementation-local generics header: `impl<T, U> ...`.
	generics, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}

	head, err := p.parseTypeAnnotation()
	if err != nil {
		return nil, err
	}

	if !p.atEnd() && p.peek().Type == token.FOR {
		p.advance() // consume FOR
		receiver, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}

		whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
		if err != nil {
			return nil, err
		}

		if p.atEnd() || p.peek().Type != token.LBRACE {
			if !p.atEnd() && !implBodylessBoundary(p.peek()) {
				return nil, errorAt(implTok.Line, implTok.Col, "expected '{' to open the `impl ... for` block body, or a line break after a bodyless impl declaration")
			}
			return &ast.ImplBlock{
				Interface:    head,
				Receiver:     receiver,
				Generics:     generics,
				WhereClauses: whereClauses,
				Line:         implTok.Line,
				Col:          implTok.Col,
			}, nil
		}
		items, endTrivia, closeTok, err := p.parseImplBlockBody("`impl Iface for Type` block", false)
		if err != nil {
			return nil, err
		}

		return &ast.ImplBlock{
			Interface:    head,
			Receiver:     receiver,
			Generics:     generics,
			WhereClauses: whereClauses,
			Items:        items,
			EndTrivia:    endTrivia,
			Line:         implTok.Line,
			Col:          implTok.Col,
			EndLine:      closeTok.Line,
			EndCol:       closeTok.Col,
		}, nil
	}

	if !p.atEnd() && p.peek().Type == token.LBRACE {
		items, endTrivia, closeTok, err := p.parseImplBlockBody("`impl Type` block", true)
		if err != nil {
			return nil, err
		}
		return &ast.ImplBlock{
			Receiver:  head,
			Generics:  generics,
			Items:     items,
			EndTrivia: endTrivia,
			Line:      implTok.Line,
			Col:       implTok.Col,
			EndLine:   closeTok.Line,
			EndCol:    closeTok.Col,
		}, nil
	}
	return nil, errorAt(implTok.Line, implTok.Col, "expected `{` for an inherent impl block or `for Type` in interface impl declaration")
}

func implBodylessBoundary(tok token.Token) bool {
	switch tok.Type {
	case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT, token.EOF:
		return true
	default:
		return false
	}
}

// parseImplBlockBody parses the `{ ... }` body of an impl block. The cursor is
// positioned at the LBRACE on entry; on success it is left just past the
// closing RBRACE, which is returned for end-position tracking.
func (p *Parser) parseImplBlockBody(blockNoun string, allowPub bool) ([]ast.Node, []ast.Trivia, token.Token, error) {
	var closeTok token.Token
	openTok := p.peek()
	p.advance() // consume LBRACE
	p.skipSeparatorsOnly()

	var items []ast.Node
	var endTrivia []ast.Trivia
	for !p.atEnd() && p.peek().Type != token.RBRACE {
		// Trivia before the next item: `}` follows → EndTrivia; another item
		// follows → that item's leading slot.
		var leading []ast.Trivia
		if t := p.peek().Type; t == token.COMMENT || t == token.BLANK_LINE {
			leading = p.collectEndOfBodyTrivia()
			if !p.atEnd() && p.peek().Type == token.RBRACE {
				endTrivia = leading
				break
			}
		}

		doc := p.collectDocComments()

		// The header names the single interface, so removed decorator syntax
		// inside the body is rejected with a targeted message.
		if p.peek().Type == token.AT && p.peekAt(1).Type == token.IMPL {
			return nil, nil, closeTok, errorAt(p.peek().Line, p.peek().Col, "methods inside an %s are plain `fn` items — the block header names the interface, so no `@impl` tag", blockNoun)
		}

		if err := p.rejectDecorator(); err != nil {
			return nil, nil, closeTok, err
		}
		attachedTests, attachedDoc, attachedLeading, err := p.parseAttachedTestPromptsAndDoc()
		if err != nil {
			return nil, nil, closeTok, err
		}
		doc = mergeDocComments(doc, attachedDoc)
		leading = append(leading, attachedLeading...)

		tk := p.peek()
		switch {
		case tk.Type == token.IDENT && tk.Lexeme == "field":
			return nil, nil, closeTok, errorAt(tk.Line, tk.Col, "`field` items are not allowed in an %s", blockNoun)
		case p.startsConformanceLine():
			return nil, nil, closeTok, errorAt(tk.Line, tk.Col, "an %s holds no nested conformance line", blockNoun)
		case tk.Type == token.TEST || tk.Type == token.TESTS:
			return nil, nil, closeTok, errorAt(tk.Line, tk.Col, "tests are declarations outside impl blocks")
		case tk.Type == token.ONCE || (tk.Type == token.PUB && p.peekAt(1).Type == token.ONCE):
			if !allowPub {
				return nil, nil, closeTok, errorAt(tk.Line, tk.Col, "interface impl blocks hold only `fn` / `host fn` implementation items; write `once` as a file or inherent impl declaration")
			}
			public := false
			if tk.Type == token.PUB {
				public = true
				p.advance() // consume PUB
			}
			item, err := p.parseOnceBinding(public)
			if err != nil {
				return nil, nil, closeTok, err
			}
			if err := attachTypeBodyItemMeta(item, doc, attachedTests, leading); err != nil {
				return nil, nil, closeTok, err
			}
			items = append(items, item)
		case tk.Type == token.FN || tk.Type == token.PUB || p.startsHostFuncDecl():
			item, err := p.parseImplBlockItem(allowPub, false, blockNoun)
			if err != nil {
				return nil, nil, closeTok, err
			}
			if err := attachTypeBodyItemMeta(item, doc, attachedTests, leading); err != nil {
				return nil, nil, closeTok, err
			}
			items = append(items, item)
		default:
			return nil, nil, closeTok, errorAt(tk.Line, tk.Col, "an %s body holds only `fn` / `host fn` implementation items, got %s", blockNoun, tk.Type)
		}

		p.skipEnumVariantTrailingSeparators()
	}

	if p.atEnd() || p.peek().Type != token.RBRACE {
		return nil, nil, closeTok, errorAt(openTok.Line, openTok.Col, "expected '}' to close the %s body", blockNoun)
	}
	closeTok = p.peek()
	p.advance() // consume RBRACE
	return items, endTrivia, closeTok, nil
}

// startsConformanceLine reports whether the cursor is at the head of removed
// body-level conformance syntax or a current `derive` entry. Removed old
// spellings are still *recognized* here — not to parse them, but so
// parseTypeBodyItem can route them to a targeted error pointing at the current
// syntax (rather than the generic body-loop default).
func (p *Parser) startsConformanceLine() bool {
	tk := p.peek()
	switch {
	case tk.Type == token.IMPL:
		return true
	case tk.Type == token.IDENT && tk.Lexeme == "implements":
		return true
	case tk.Type == token.IDENT && tk.Lexeme == "derive":
		return true
	case tk.Type == token.IDENT && tk.Lexeme == "derives":
		return true
	}
	return false
}

// parseTypeBodyItem rejects declaration forms that used to be admitted inside
// type bodies, with targeted current-syntax diagnostics.
func (p *Parser) parseTypeBodyItem() ([]ast.Node, error) {
	tk := p.peek()
	// `derives Iface` is a plain IDENT sequence. Give it a targeted error.
	if tk.Type == token.IDENT && tk.Lexeme == "derives" {
		return nil, errorAt(tk.Line, tk.Col, "a derived interface conformance is `derive Iface for Type`; `derives` is not a conformance keyword")
	}
	if tk.Type == token.IDENT && tk.Lexeme == "implements" {
		return nil, errorAt(tk.Line, tk.Col, "interface conformance uses `impl`, not `implements`")
	}
	if tk.Type == token.IMPL {
		return nil, errorAt(tk.Line, tk.Col, "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body")
	}
	if tk.Type == token.IDENT && tk.Lexeme == "derive" {
		if p.peekAt(1).Type == token.IMPL {
			return nil, errorAt(tk.Line, tk.Col, "a derived interface conformance is `derive Iface for Type`; `derive impl` is not a conformance keyword")
		}
		return nil, errorAt(tk.Line, tk.Col, "derive declarations are written outside the type body as `derive Iface for Type`")
	}
	if tk.Type == token.PUB && p.peekAt(1).Type == token.IMPL {
		return nil, errorAt(tk.Line, tk.Col, "implementation functions do not take `pub` — visibility comes from the implemented interface")
	}
	if tk.Type == token.TEST || tk.Type == token.TESTS {
		return nil, errorAt(tk.Line, tk.Col, "tests are declarations outside type bodies")
	}
	if tk.Type == token.ONCE || (tk.Type == token.PUB && p.peekAt(1).Type == token.ONCE) {
		return nil, errorAt(tk.Line, tk.Col, "once bindings are declarations outside type bodies")
	}
	if tk.Type == token.FN || p.startsHostDecl() || tk.Type == token.PUB {
		return nil, errorAt(tk.Line, tk.Col, "functions are declarations outside type bodies")
	}
	return nil, errorAt(tk.Line, tk.Col, "type bodies hold only shape items")
}

func (p *Parser) parseDeriveDecl() (ast.Node, error) {
	kwTok := p.peek()
	p.advance() // consume derive

	var generics []ast.TypeParam
	if !p.atEnd() && p.peek().Type == token.LT {
		params, err := p.parseTypeParams()
		if err != nil {
			return nil, err
		}
		generics = params
	}

	ifaces, err := p.parseDeriveInterfaceList(kwTok)
	if err != nil {
		return nil, err
	}
	if p.atEnd() || p.peek().Type != token.FOR {
		return nil, errorAt(kwTok.Line, kwTok.Col, "expected `for` and a receiver type in derive declaration (`derive %s for Type`)", ifaces[0].TypeString())
	}
	p.advance() // consume for
	if p.atEnd() || !canStartTypeAnnotation(p.peek().Type) {
		return nil, errorAt(kwTok.Line, kwTok.Col, "expected receiver type after `for` in derive declaration")
	}
	receiver, err := p.parseTypeAnnotation()
	if err != nil {
		return nil, err
	}
	var options ast.Node
	if !p.atEnd() && p.peek().Type == token.WITH {
		p.advance() // consume with
		if p.atEnd() {
			return nil, errorAt(kwTok.Line, kwTok.Col, "expected derive options expression after `with`")
		}
		options, err = p.parseExpr(1)
		if err != nil {
			return nil, err
		}
	}
	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() && p.peek().Type == token.LBRACE {
		return nil, errorAt(p.peek().Line, p.peek().Col, "a `derive` declaration has no body — the impl is synthesized structurally")
	}
	return &ast.ImplConformance{
		Interface:    ifaces[0],
		Interfaces:   ifaces,
		Receiver:     receiver,
		Options:      options,
		Generics:     generics,
		WhereClauses: whereClauses,
		Derive:       true,
		Line:         kwTok.Line,
		Col:          kwTok.Col,
	}, nil
}

func (p *Parser) parseDeriveInterfaceList(kwTok token.Token) ([]ast.TypeExpr, error) {
	if p.atEnd() || !canStartTypeAnnotation(p.peek().Type) {
		return nil, errorAt(kwTok.Line, kwTok.Col, "expected interface name after `derive`")
	}
	ifaceTok := p.peek()
	iface, err := p.parseTypeAnnotation()
	if err != nil {
		return nil, err
	}
	if implInterfaceTypeArgs(iface) {
		return nil, errorAt(ifaceTok.Line, ifaceTok.Col, "a `derive` declaration names the interface by base name only — no type argument (`derive Iter for List<T>`, not `derive Iter<T> for List<T>`)")
	}
	if !p.atEnd() && p.peek().Type == token.COMMA {
		return nil, errorAt(p.peek().Line, p.peek().Col, "derive declarations list one interface at a time — write `derive Iface for Type` on its own line for each interface")
	}
	return []ast.TypeExpr{iface}, nil
}

// implInterfaceTypeArgs reports whether an interface type-expr in an impl
// conformance line or impl block header carries explicit type
// arguments (`Iter<T>`, `mod.Box<T>`), seeing through module qualification.
// Mirrors analysis.implInterfaceHasTypeArgs — base names report false.
func implInterfaceTypeArgs(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.GenericType:
		return len(t.Params) > 0
	case *ast.QualifiedType:
		return implInterfaceTypeArgs(t.Member)
	}
	return false
}

// attachTypeBodyItemMeta applies the doc comment, attached tests, and leading
// trivia collected at the top of a type-body loop iteration to a parsed fn /
// extern-fn / conformance-line item.
func attachTypeBodyItemMeta(item ast.Node, doc string, attachedTests []ast.AttachedTest, leading []ast.Trivia) error {
	if len(attachedTests) > 0 {
		context := "this type-body item"
		switch item.(type) {
		case *ast.ExternFunc:
			context = "a host fn"
		case *ast.ImplConformance:
			context = "an impl conformance line"
		case *ast.ImplBlock:
			context = "an interface impl block"
		case *ast.OnceBinding:
			context = "a once binding"
		}
		if err := attachDeclarationAttachedTests(item, attachedTests, context); err != nil {
			return err
		}
	}
	if doc != "" {
		switch it := item.(type) {
		case *ast.ImplConformance:
			it.Doc = doc
		default:
			attachDoc(item, doc)
		}
	}
	if hasTrivia, ok := item.(ast.HasTrivia); ok {
		for _, tr := range leading {
			hasTrivia.AddLeading(tr)
		}
	}
	return nil
}

// parseImplBlockItem parses one declaration inside an implementation body.
// Type-body inherent functions allow ordinary `pub fn` / `pub host fn` items;
// `impl Iface for Type { ... }` bodies do not, because their functions inherit
// visibility from the interface named by the block header.
func (p *Parser) parseImplBlockItem(allowPub bool, allowImplQualifier bool, blockNoun string) (node ast.Node, err error) {
	start := p.pos
	defer func() {
		if err == nil {
			p.recordSpan(node, start)
		}
	}()
	public := false
	if p.peek().Type == token.PUB {
		if !allowPub {
			return nil, errorAt(p.peek().Line, p.peek().Col, "methods inside an %s do not take `pub` — visibility is defined by the interface", blockNoun)
		}
		public = true
		p.advance() // consume PUB
		if p.atEnd() {
			return nil, errorAt(p.peek().Line, p.peek().Col, "expected 'fn' or 'host fn' after 'pub' in %s", blockNoun)
		}
	}
	switch p.peek().Type {
	case token.FN:
		return p.parseFuncDef(public, allowImplQualifier)
	case token.IDENT:
		if p.peek().Lexeme != "host" {
			if allowPub {
				return nil, errorAt(p.peek().Line, p.peek().Col, "%s admits only 'fn' / 'pub fn' / 'host fn' / 'pub host fn' declarations here, got %s", blockNoun, p.peek().Type)
			}
			return nil, errorAt(p.peek().Line, p.peek().Col, "impl block body admits only 'fn' / 'host fn' declarations, got %s", p.peek().Type)
		}
		// Only `host fn` is admitted — `host type` is a type
		// declaration, not a method, and has no place in a block body.
		if p.peekAt(1).Type != token.FN {
			return nil, errorAt(p.peek().Line, p.peek().Col, "impl block body admits only 'host fn' (not 'host type')")
		}
		return p.parseHost(public, allowImplQualifier)
	case token.EXTERN:
		return nil, errorAt(p.peek().Line, p.peek().Col, "runtime-provided declarations use `host fn`, not `extern fn`")
	default:
		if allowPub {
			return nil, errorAt(p.peek().Line, p.peek().Col, "%s admits only 'fn' / 'pub fn' / 'host fn' / 'pub host fn' declarations here, got %s", blockNoun, p.peek().Type)
		}
		return nil, errorAt(p.peek().Line, p.peek().Col, "impl block body admits only 'fn' / 'host fn' declarations, got %s", p.peek().Type)
	}
}

// parseInterfaceField parses a `field name: Type` requirement inside an
// interface body. The cursor is positioned at the IDENT lexeme "field"
// on entry; on success it is left at the token immediately after the
// type annotation (the caller skips the trailing newline). `field` is a
// contextual keyword — only the interface body recognises it, so the
// parser-level check is on the lexeme, not on a token type.
func (p *Parser) parseInterfaceField() (f ast.InterfaceField, err error) {
	start := p.pos
	defer func() { f.Span = p.spanSince(start) }()
	fieldTok := p.peek()
	p.advance() // consume `field` IDENT

	if p.atEnd() || p.peek().Type != token.IDENT {
		return ast.InterfaceField{}, errorAt(fieldTok.Line, fieldTok.Col, "expected field name after 'field' in interface body")
	}
	nameTok := p.peek()
	name := nameTok.Lexeme
	p.advance()

	if p.atEnd() || p.peek().Type != token.COLON {
		return ast.InterfaceField{}, errorAt(fieldTok.Line, fieldTok.Col, "expected ':' after field name '%s' in interface body", name)
	}
	p.advance() // consume COLON

	typeExpr, err := p.parseTypeAnnotation()
	if err != nil {
		return ast.InterfaceField{}, err
	}

	return ast.InterfaceField{Name: name, TypeAnnotation: typeExpr, Line: nameTok.Line, Col: nameTok.Col}, nil
}

func (p *Parser) parseInterfaceMethod() (m ast.InterfaceMethod, err error) {
	start := p.pos
	defer func() { m.Span = p.spanSince(start) }()
	open := false
	var openTok token.Token
	// `open` is a contextual keyword — IDENT-lexeme match only inside
	// the interface body. Outside that context the identifier `open`
	// stays a regular binding name.
	if !p.atEnd() && p.peek().Type == token.IDENT && p.peek().Lexeme == "open" {
		open = true
		openTok = p.peek()
		p.advance() // consume `open` IDENT
	}
	// Optional `host` → host-backed default (the host provides the body).
	// Combines with `open` (`open host fn` — overridable host default).
	extern := false
	var externTok token.Token
	if !p.atEnd() && isHostToken(p.peek()) {
		extern = true
		externTok = p.peek()
		p.advance() // consume HOST
	} else if !p.atEnd() && p.peek().Type == token.EXTERN {
		return ast.InterfaceMethod{}, errorAt(p.peek().Line, p.peek().Col, "runtime-provided declarations use `host fn`, not `extern fn`")
	}
	if p.atEnd() || p.peek().Type != token.FN {
		return ast.InterfaceMethod{}, errorAt(p.peek().Line, p.peek().Col, "expected 'fn' before method name in interface")
	}
	p.advance() // consume FN

	if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.TYPE_IDENT) {
		return ast.InterfaceMethod{}, errorAt(p.peek().Line, p.peek().Col, "expected method name after 'fn' in interface")
	}
	name := p.peek().Lexeme
	line := p.peek().Line
	col := p.peek().Col
	p.advance()

	typeParams, err := p.parseTypeParams()
	if err != nil {
		return ast.InterfaceMethod{}, err
	}

	if p.atEnd() || p.peek().Type != token.LPAREN {
		return ast.InterfaceMethod{}, p.errorOnLine(line, "expected '(' after method name")
	}
	p.advance() // consume LPAREN
	p.skipNewlines()

	// Reuse the shared function-parameter parser (the same one parseFuncDef
	// uses), so interface method signatures get type annotations, trailing
	// default values, and destructuring uniformly — no bespoke param loop to
	// drift behind it. It stops at RPAREN without consuming it; the LPAREN was
	// already consumed above.
	params, err := p.parseParamSection(p.peek())
	if err != nil {
		return ast.InterfaceMethod{}, err
	}

	if p.atEnd() || p.peek().Type != token.RPAREN {
		return ast.InterfaceMethod{}, p.errorOnLine(line, "expected ')'")
	}
	p.advance()

	// Optional return type: : Type
	var returnTypeExpr ast.TypeExpr
	if !p.atEnd() && p.peek().Type == token.COLON {
		p.advance()
		rt, err := p.parseTypeAnnotation()
		if err != nil {
			return ast.InterfaceMethod{}, err
		}
		returnTypeExpr = rt
	}

	// Optional default body, optionally preceded by a method-level `where`
	// clause. Peek past NEWLINE / SEMICOLON to detect `: Int\n  { ... }` (or
	// `: Int\n  where T: C { ... }`), but only commit to the skip when a
	// `where` or LBRACE actually follows — otherwise we'd eat the trailing
	// trivia (comments / blank-lines) that the surrounding interface body
	// wants to capture as EndTrivia.
	bodyStart := p.pos
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}
	// Method-level `where` clause: `where T: A and B, K: C`. It constrains an
	// in-scope type variable (the interface's own type param or an explicit
	// method-local) for this interface method. A hard keyword, so this can
	// never be confused with a semicolon-separated member or a body statement.
	whereClauses, err := p.parseOptionalWhereClause()
	if err != nil {
		return ast.InterfaceMethod{}, err
	}
	var body ast.Node
	if !p.atEnd() && p.peek().Type == token.LBRACE {
		var err error
		body, err = p.parseBlockOrLambda()
		if err != nil {
			return ast.InterfaceMethod{}, err
		}
	} else if whereClauses == nil {
		// No body — rewind so any trailing trivia stays available for the
		// caller (parseInterfaceDef collects it as EndTrivia when it
		// precedes the closing `}`).
		p.pos = bodyStart
	}

	// A host-backed default (`host fn`) is supplied by the runtime, so it
	// must NOT carry a Nomi body.
	if extern && body != nil {
		return ast.InterfaceMethod{}, errorAt(externTok.Line, externTok.Col, "'host fn %s' is host-backed and cannot have a body", name)
	}

	// `open` names a method overridable, so it needs *some* default to
	// override: a Nomi body (`open fn ... { }`) or a host one
	// (`open host fn ...`). On a bare required method it's meaningless.
	if open && body == nil && !extern {
		return ast.InterfaceMethod{}, errorAt(openTok.Line, openTok.Col, "'open' modifier is only valid on interface default methods (those with a body or 'host'); '%s' has no default", name)
	}

	return ast.InterfaceMethod{
		Name: name, TypeParams: typeParams, Params: params, ReturnTypeExpr: returnTypeExpr, Body: body, Open: open, Extern: extern, WhereClauses: whereClauses, Line: line, Col: col,
	}, nil
}

// parseWhereClause parses a method-level `where` clause —
// `where Name: Bound [and Bound]* [, Name: Bound ...]*` — with the WHERE token
// current. Each constraint names a type variable in scope (the enclosing
// interface's type param or an explicit method-local) plus a conjunctive bound
// list, reusing the same `: A and B` form used by generic `where` clauses.
func (p *Parser) parseWhereClause() ([]ast.WhereConstraint, error) {
	p.advance() // consume WHERE
	var clauses []ast.WhereConstraint
	for {
		name, line, col, err := p.parseTypeParamName()
		if err != nil {
			return nil, p.errorOnLine(line, "expected a type variable name in `where` clause")
		}
		if p.atEnd() || p.peek().Type != token.COLON {
			return nil, p.errorOnLine(line, "expected ':' after `%s` in `where` clause", name)
		}
		p.advance() // consume COLON
		var bounds []ast.TypeExpr
		for {
			b, err := p.parseTypeAnnotation()
			if err != nil {
				return nil, err
			}
			bounds = append(bounds, b)
			if p.atEnd() || p.peek().Type != token.AND {
				break
			}
			p.advance() // consume `and`
		}
		clauses = append(clauses, ast.WhereConstraint{Name: name, Bounds: bounds, Line: line, Col: col})
		if p.atEnd() || p.peek().Type != token.COMMA {
			break
		}
		p.advance() // consume COMMA
	}
	return clauses, nil
}

func (p *Parser) parseOptionalWhereClause() ([]ast.WhereConstraint, error) {
	if p.atEnd() || p.peek().Type != token.WHERE {
		return nil, nil
	}
	whereClauses, err := p.parseWhereClause()
	if err != nil {
		return nil, err
	}
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}
	return whereClauses, nil
}

func (p *Parser) parseOptionalWhereClauseAfterHeader() ([]ast.WhereConstraint, error) {
	if p.atEnd() {
		return nil, nil
	}
	if p.peek().Type == token.WHERE {
		return p.parseOptionalWhereClause()
	}
	start := p.pos
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			break
		}
	}
	if p.atEnd() || p.peek().Type != token.WHERE {
		p.pos = start
		return nil, nil
	}
	return p.parseOptionalWhereClause()
}

func (p *Parser) skipSignatureSeparators() {
	for !p.atEnd() {
		t := p.peek().Type
		if t == token.NEWLINE || t == token.SEMICOLON {
			p.advance()
		} else {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Doc comments
// ---------------------------------------------------------------------------

// collectDocComments consumes consecutive DOC_COMMENT tokens and returns
// their text joined by newlines. Skips NEWLINEs between doc comments.
//
// Each lexeme is everything between `///` and the line's newline (the
// lexer hands us the post-marker run verbatim). We strip trailing
// whitespace, then strip a single leading space if present — that
// space is the rustdoc-style convention separator between marker and
// body. Anything PAST that single space is content and survives as
// relative indentation, which matters for ```nomi-fenced code
// examples whose case arms / nested lambda bodies depend on visible
// indent to render correctly. A line that's only whitespace (trailing
// strip eats everything) becomes a blank-paragraph marker, joined as
// an empty line in the resulting string.
func (p *Parser) collectDocComments() string {
	var lines []string
	for !p.atEnd() {
		switch p.peek().Type {
		case token.DOC_COMMENT:
			line := strings.TrimRight(p.peek().Lexeme, " \t\r")
			line = strings.TrimPrefix(line, " ")
			lines = append(lines, line)
			p.advance()
			p.skipDocCommentSeparators()
		case token.COMMENT:
			if len(lines) == 0 || !isBlankOrdinaryComment(p.peek()) || !p.blankOrdinaryCommentSeparatesDocBoundary() {
				return strings.Join(lines, "\n")
			}
			lines = append(lines, "")
			p.advance()
			p.skipDocCommentSeparators()
		default:
			if len(lines) == 0 {
				return ""
			}
			return strings.Join(lines, "\n")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (p *Parser) skipDocCommentSeparators() {
	for !p.atEnd() {
		switch p.peek().Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			p.advance()
		default:
			return
		}
	}
}

func isBlankOrdinaryComment(tok token.Token) bool {
	return tok.Type == token.COMMENT && strings.TrimSpace(tok.Lexeme) == "//"
}

func (p *Parser) blankOrdinaryCommentSeparatesDocBoundary() bool {
	for offset := 1; ; offset++ {
		tok := p.peekAt(offset)
		switch tok.Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE:
			continue
		default:
			return isDocBoundaryStart(tok)
		}
	}
}

func isDocBoundaryStart(tok token.Token) bool {
	switch tok.Type {
	case token.DOC_COMMENT,
		token.TEST_PROMPT,
		token.AT,
		token.PUB,
		token.EXTERN,
		token.FN,
		token.ONCE,
		token.TYPE,
		token.STRUCT,
		token.ENUM,
		token.TYPEALIAS,
		token.INTERFACE,
		token.IMPL,
		token.OPAQUE,
		token.TEST,
		token.TESTS:
		return true
	case token.IDENT:
		switch tok.Lexeme {
		case "field", "embeds", "open":
			return true
		}
	}
	return false
}

// annotationContext returns a parenthetical hint for binding parse errors when
// a type annotation has been consumed, so the error message can disambiguate
// between bare-binding and annotated-binding parse failures.
func annotationContext(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	return " (after type annotation)"
}

// attachDoc sets the Doc field on declaration nodes.
// attachDoc sets node's doc comment and reports whether node can carry one.
// An empty doc always attaches.
func attachDoc(node ast.Node, doc string) bool {
	if doc == "" {
		return true
	}
	switch n := node.(type) {
	case *ast.FuncDef:
		n.Doc = doc
	case *ast.ExternFunc:
		n.Doc = doc
	case *ast.StructDef:
		n.Doc = doc
	case *ast.EnumDef:
		n.Doc = doc
	case *ast.TypeDef:
		n.Doc = doc
	case *ast.ExternType:
		n.Doc = doc
	case *ast.TypeAlias:
		n.Doc = doc
	case *ast.InterfaceDef:
		n.Doc = doc
	case *ast.ImplBlock:
		n.Doc = doc
	case *ast.OnceBinding:
		n.Doc = doc
	default:
		return false
	}
	return true
}

// strayDocComment is the error for a `///` comment above a top-level item that
// takes no doc comment, such as an import or a test. Accepting it would lose
// the text: nothing reads it, and the formatter would not print it.
func strayDocComment(docTok token.Token, node ast.Node) error {
	line, col := docTok.Line, docTok.Col
	if docTok.Type != token.DOC_COMMENT {
		line, col = node.LineNum(), 1
	}
	return ParseError{Line: line, Col: col, Message: "a `///` doc comment must be followed by a declaration it documents; use `//` for an ordinary comment or `//#` for file-level text"}
}

// ---------------------------------------------------------------------------
// Once bindings
// ---------------------------------------------------------------------------

// parseOnceBinding parses file- or owner-level once bindings:
//
//	once name = expression
//	once name: T = expression
//
// The type annotation is optional — it's inferred from the value when
// omitted. `once` is the language's only "set once, never changes"
// declaration: the right-hand side is evaluated lazily on first
// access and cached forever.
func (p *Parser) parseOnceBinding(public bool) (ast.Node, error) {
	onceTok := p.peek()
	p.advance() // consume ONCE

	if p.atEnd() || (p.peek().Type != token.IDENT && p.peek().Type != token.TYPE_IDENT) {
		return nil, errorAt(onceTok.Line, onceTok.Col, "expected name after 'once'")
	}
	nameTok := p.peek()
	name := nameTok.Lexeme
	p.advance() // consume name

	var typeAnnotation ast.TypeExpr
	if !p.atEnd() && p.peek().Type == token.COLON {
		p.advance() // consume :
		te, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		typeAnnotation = te
	}

	if p.atEnd() || p.peek().Type != token.EQ {
		return nil, errorAt(onceTok.Line, onceTok.Col, "expected '=' after 'once' name")
	}
	p.advance() // consume =

	value, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}

	return &ast.OnceBinding{
		Name:           name,
		Public:         public,
		TypeAnnotation: typeAnnotation,
		Value:          value,
		Line:           onceTok.Line,
		Col:            nameTok.Col,
	}, nil
}

// ---------------------------------------------------------------------------
// Host and external declarations
// ---------------------------------------------------------------------------

func (p *Parser) parseExtern(public bool, allowImplQualifier bool) (ast.Node, error) {
	externTok := p.peek()
	p.advance() // consume "extern"

	switch p.peek().Type {
	case token.FN, token.TYPE:
		return nil, errorAt(externTok.Line, externTok.Col, "runtime-provided declarations use `host %s`, not `extern %s`", p.peek().Lexeme, p.peek().Lexeme)
	case token.STRING_LITERAL:
		return nil, errorAt(externTok.Line, externTok.Col, "Go package handles use `gopkg %q`, not `extern`", p.peek().Lexeme)
	default:
		return nil, errorAt(externTok.Line, externTok.Col, "unexpected 'extern'; runtime-provided declarations use `host`, and Go package handles use `gopkg`")
	}
}

func (p *Parser) parseHost(public bool, allowImplQualifier bool) (ast.Node, error) {
	hostTok := p.peek()
	p.advance() // consume "host"

	switch p.peek().Type {
	case token.FN:
		return p.parseExternFunc(hostTok, public, allowImplQualifier)
	case token.TYPE:
		return p.parseExternType(hostTok, public)
	default:
		return nil, errorAt(hostTok.Line, hostTok.Col, "expected 'fn' or 'type' after 'host'")
	}
}

func isHostToken(tok token.Token) bool {
	return tok.Type == token.IDENT && tok.Lexeme == "host"
}

func (p *Parser) startsHostDecl() bool {
	if !isHostToken(p.peek()) {
		return false
	}
	next := p.peekAt(1)
	return next.Type == token.FN || next.Type == token.TYPE
}

func (p *Parser) startsHostFuncDecl() bool {
	return isHostToken(p.peek()) && p.peekAt(1).Type == token.FN
}

func (p *Parser) parseExternFunc(externTok token.Token, public bool, allowImplQualifier bool) (ast.Node, error) {
	p.advance() // consume "fn"

	name, nameTok, implIface, sourceQualified, err := p.parseFunctionHeaderName(externTok, "host fn", allowImplQualifier)
	if err != nil {
		return nil, err
	}

	// Optional explicit type-param list: `<T, U>`. Generic host fns declare
	// their type parameters here; interface constraints go in a trailing
	// `where` clause.
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}

	if p.peek().Type != token.LPAREN {
		return nil, errorAt(nameTok.Line, nameTok.Col, "expected '(' after function name")
	}
	p.advance() // consume LPAREN
	p.skipNewlines()

	// Parse parameters (same logic as parseFuncDef)
	var params []ast.Param
	if !p.atEnd() && p.peek().Type != token.RPAREN {
		for {
			if p.atEnd() || p.peek().Type != token.IDENT {
				return nil, errorAt(p.peek().Line, p.peek().Col, "expected parameter name")
			}
			paramTok := p.peek()
			param := ast.Param{Name: paramTok.Lexeme, Line: paramTok.Line, Col: paramTok.Col}
			p.advance()

			// Optional type annotation: : Type
			if !p.atEnd() && p.peek().Type == token.COLON {
				p.advance()
				typeExpr, err := p.parseTypeAnnotation()
				if err != nil {
					return nil, err
				}
				param.TypeAnnotation = typeExpr
			}

			// Optional default value: = expr. Same shape as an ordinary
			// `fn` param (see parseParamPattern), including the dot-shorthand
			// stamp — `restart: Restart = .Never` resolves against the
			// annotation at parse time, because an imported module is
			// re-parsed for evaluation and that re-parse discards anything
			// the analyzer stamped.
			if !p.atEnd() && p.peek().Type == token.EQ {
				p.advance() // consume EQ
				def, err := p.parseExpr(1)
				if err != nil {
					return nil, err
				}
				param.Default = def
				if enumName := paramAnnotationBaseName(param.TypeAnnotation); enumName != "" {
					stampParamDefaultEnum(param.Default, enumName)
				}
			}

			params = append(params, param)

			if p.atEnd() || p.peek().Type != token.COMMA {
				break
			}
			p.advance() // consume COMMA
			p.skipNewlines()
			// Allow trailing comma: COMMA followed by RPAREN closes the list.
			if !p.atEnd() && p.peek().Type == token.RPAREN {
				break
			}
		}
	}

	// Expect RPAREN
	if p.atEnd() || p.peek().Type != token.RPAREN {
		return nil, errorAt(externTok.Line, externTok.Col, "expected ')' after parameters")
	}
	p.advance()

	// Optional return type: : Type
	var returnTypeExpr ast.TypeExpr
	if !p.atEnd() && p.peek().Type == token.COLON {
		p.advance()
		rt, err := p.parseTypeAnnotation()
		if err != nil {
			return nil, err
		}
		returnTypeExpr = rt
	}

	p.skipSignatureSeparators()
	whereClauses, err := p.parseOptionalWhereClause()
	if err != nil {
		return nil, err
	}

	return &ast.ExternFunc{
		Name:                     name,
		Public:                   public,
		TypeParams:               typeParams,
		Params:                   params,
		ReturnTypeExpr:           returnTypeExpr,
		WhereClauses:             whereClauses,
		ImplIface:                implIface,
		ImplIfaceSourceQualified: sourceQualified,
		Line:                     nameTok.Line,
		Col:                      nameTok.Col,
	}, nil
}

func (p *Parser) parseExternType(externTok token.Token, public bool) (ast.Node, error) {
	p.advance() // consume "type"

	name, nameTok, err := p.parseQualifiedTypeDeclName("host type")
	if err != nil {
		return nil, err
	}

	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}

	whereClauses, err := p.parseOptionalWhereClauseAfterHeader()
	if err != nil {
		return nil, err
	}

	var items []ast.Node
	var endTrivia []ast.Trivia
	var closeTok token.Token
	hasBody := false
	if !p.atEnd() && p.peek().Type == token.LBRACE && p.pos > 0 && p.peek().Line == p.tokens[p.pos-1].Line {
		return nil, errorAt(p.peek().Line, p.peek().Col, "host type declarations do not take bodies; put host functions in impl blocks or beside the type")
	}

	return &ast.ExternType{
		Name:         name,
		Public:       public,
		TypeParams:   typeParams,
		WhereClauses: whereClauses,
		HasBody:      hasBody,
		Items:        items,
		EndTrivia:    endTrivia,
		Line:         nameTok.Line,
		Col:          nameTok.Col,
		EndLine:      closeTok.Line,
		EndCol:       closeTok.Col,
	}, nil
}

// enumVariantDefaultError answers `= value` written after an enum variant.
// Only a struct-shaped variant's named fields take defaults, so the message
// points a positional payload at that form and tells a bare variant that it
// carries no value.
func enumVariantDefaultError(eq token.Token, v *ast.EnumVariant) error {
	switch v.Kind {
	case "positional":
		payload := "T"
		if v.DataTypeExpr != nil {
			payload = v.DataTypeExpr.TypeString()
		}
		return errorAt(eq.Line, eq.Col, "a positional variant payload can't have a default; name the field to give it one: `%s {<name>: %s = <value>}`", v.Name, payload)
	case "bare":
		return errorAt(eq.Line, eq.Col, "enum variant `%s` carries no value, so it can't be assigned one", v.Name)
	}
	return errorAt(eq.Line, eq.Col, "enum variants can't have defaults; give a struct-shaped variant's fields defaults instead")
}
