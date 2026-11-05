package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// parseEmbedDecl parses `[pub] embed name `patterns“ with the cursor after
// the name (ast.EmbedDecl). It is the keyword-declaration rule with no
// parameters, no result type and a body the parser splits into patterns:
// white space separates them, and a pattern holding a space is written in
// double quotes. What each pattern matches is the front end's to find.
func (p *Parser) parseEmbedDecl(kwTok, nameTok token.Token, public bool) (ast.Node, error) {
	decl := &ast.EmbedDecl{
		Name:     nameTok.Lexeme,
		Public:   public,
		Line:     kwTok.Line,
		Col:      kwTok.Col,
		NameLine: nameTok.Line,
		NameCol:  nameTok.Col,
	}
	shape := "it is `embed name `pattern``, with one or more file patterns between the backticks"
	if p.atEnd() {
		return nil, errorAt(nameTok.Line, nameTok.Col+len(nameTok.Lexeme), "an `embed` names the files it holds: %s", shape)
	}
	start := p.pos
	switch tok := p.peek(); tok.Type {
	case token.LPAREN:
		return nil, errorAt(tok.Line, tok.Col, "an `embed` takes no parameters: %s", shape)
	case token.ARROW:
		return nil, errorAt(tok.Line, tok.Col, "an `embed` is an `embed.Files`, so it is written with no `->` type: %s", shape)
	case token.RAW_STRING_LITERAL:
		p.advance()
		decl.Body = &ast.StringLit{Value: tok.Lexeme, Raw: true, Line: tok.Line, Col: tok.Col}
		p.recordSpan(decl.Body, start)
	case token.RAW_TRIPLE_STRING_LITERAL:
		p.advance()
		decl.Body = &ast.StringLit{Value: tok.Lexeme, Triple: true, Raw: true, Line: tok.Line, Col: tok.Col, EndLine: tok.EndLine, EndCol: tok.EndCol}
		decl.BodyCol = tok.Indent + 1
		p.recordSpan(decl.Body, start)
	case token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
		return nil, errorAt(tok.Line, tok.Col-len(tok.Tag), "an `embed` body is a list of file patterns and takes no tag: remove `%s`", tok.Tag)
	case token.STRING_LITERAL, token.TRIPLE_STRING_LITERAL, token.STRING_START, token.TRIPLE_STRING_START,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL, token.TAGGED_STRING_START, token.TAGGED_TRIPLE_STRING_START:
		return nil, errorAt(tok.Line, tok.Col-len(tok.Tag), "the patterns of an `embed` are a raw string: write them between backticks")
	case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT, token.EOF:
		return nil, errorAt(nameTok.Line, nameTok.Col+len(nameTok.Lexeme), "an `embed` names the files it holds: %s", shape)
	default:
		return nil, errorAt(tok.Line, tok.Col, "unexpected %s in an `embed` declaration: %s", tok.Lexeme, shape)
	}
	if !p.atEnd() {
		switch tok := p.peek(); tok.Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT, token.EOF:
		default:
			return nil, errorAt(tok.Line, tok.Col, "unexpected %s in an `embed` declaration: %s", tok.Lexeme, shape)
		}
	}
	patterns, err := embedPatterns(decl)
	if err != nil {
		return nil, err
	}
	decl.Patterns = patterns
	return decl, nil
}

// embedPatterns splits an `embed` body into its patterns.
func embedPatterns(decl *ast.EmbedDecl) ([]ast.EmbedPattern, error) {
	text := decl.Body.Value
	var out []ast.EmbedPattern
	for i := 0; i < len(text); {
		c := text[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		line, col := embedBodyPosition(decl, i)
		if c == '"' {
			end := strings.IndexAny(text[i+1:], "\"\n")
			if end < 0 || text[i+1+end] != '"' {
				return nil, errorAt(line, col, "this `embed` pattern's `\"` is never closed: a quoted pattern ends with `\"` on its line")
			}
			pattern := text[i+1 : i+1+end]
			next := i + 1 + end + 1
			if next < len(text) && !isEmbedSpace(text[next]) {
				return nil, errorAt(line, col, "a quoted `embed` pattern ends at its closing `\"`: put a space after it")
			}
			if pattern == "" {
				return nil, errorAt(line, col, "an `embed` pattern is empty")
			}
			out = append(out, ast.EmbedPattern{Text: pattern, Line: line, Col: col, Width: next - i})
			i = next
			continue
		}
		j := i
		for j < len(text) && !isEmbedSpace(text[j]) {
			j++
		}
		out = append(out, ast.EmbedPattern{Text: text[i:j], Line: line, Col: col, Width: j - i})
		i = j
	}
	if len(out) == 0 {
		return nil, errorAt(decl.Body.Line, decl.Body.Col, "an `embed` names at least one file pattern between its backticks")
	}
	return out, nil
}

func isEmbedSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// embedBodyPosition is the source line and column of byte offset of an
// `embed` body's text, as KeywordDecl.BodyPosition places a keyword
// declaration's.
func embedBodyPosition(decl *ast.EmbedDecl, offset int) (line, col int) {
	body := decl.Body
	if !body.Triple {
		return body.Line, body.Col + 1 + offset
	}
	before := body.Value[:offset]
	nl := strings.Count(before, "\n")
	last := strings.LastIndexByte(before, '\n')
	return body.Line + 1 + nl, decl.BodyCol + (offset - last - 1)
}
