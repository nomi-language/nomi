package parser

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/token"
)

// Source written the way another language writes it gets an error that
// says what Nomi writes instead, with the rewrite as the error's Fix. Each
// check here matches only source the parser rejects anyway, so none of
// them changes what Nomi accepts: it replaces the error a later token
// would have raised with one at the borrowed construct.

// confusedStmt is the error for a statement that starts the way another
// language starts one: `let x = 1`, `x := 1`, `def f(...)`, `x++`,
// `x += 1`, `# comment`. nil when the statement starts as Nomi's do.
func (p *Parser) confusedStmt() error {
	t, n1, n2 := p.peek(), p.peekAt(1), p.peekAt(2)
	switch t.Type {
	case token.IDENT:
		switch t.Lexeme {
		case "let", "var", "const", "val", "mut":
			return p.confusedLet(t)
		case "def", "defp", "func", "function", "fun":
			if n1.Type == token.IDENT && n1.Line == t.Line && n2.Type == token.LPAREN {
				return ParseError{Line: t.Line, Col: t.Col,
					Message: fmt.Sprintf("Nomi declares a function with `fn`, not `%s`", t.Lexeme),
					Hints:   []string{fmt.Sprintf("write `fn %s(...) -> Type { ... }`", n1.Lexeme)},
					Fix:     Replace("Write `fn`", t.Line, t.Col, t.Line, t.Col+len(t.Lexeme), "fn")}
			}
		}
		if n1.Type == token.COLON && n2.Type == token.EQ && n1.Line == t.Line && n2.Line == t.Line && n2.Col == n1.Col+1 {
			return ParseError{Line: n1.Line, Col: n1.Col,
				Message: fmt.Sprintf("Nomi binds a name with `=`, not `:=`: write `%s = ...`", t.Lexeme),
				Fix:     Replace("Write `=`", n1.Line, n1.Col, n2.Line, n2.Col+1, "=")}
		}
		if (n1.Type == token.PLUS || n1.Type == token.MINUS) && n2.Type == n1.Type &&
			n1.Line == t.Line && n1.Col == t.Col+len(t.Lexeme) && n2.Line == t.Line && n2.Col == n1.Col+1 {
			if after := p.peekAt(3); after.Line != t.Line || after.Type == token.EOF || after.Type == token.NEWLINE || after.Type == token.RBRACE {
				rewrite := fmt.Sprintf("%s = %s %s 1", t.Lexeme, t.Lexeme, n1.Lexeme)
				return ParseError{Line: t.Line, Col: t.Col,
					Message: fmt.Sprintf("values are immutable, so Nomi has no `%s%s`: bind the name again, `%s`", n1.Lexeme, n1.Lexeme, rewrite),
					Fix:     Replace("Bind it again", t.Line, t.Col, n2.Line, n2.Col+1, rewrite)}
			}
		}
		if compoundOperator(n1.Type) && n2.Type == token.EQ && n1.Line == t.Line && n2.Line == t.Line && n2.Col == n1.Col+1 {
			return ParseError{Line: n1.Line, Col: n1.Col,
				Message: fmt.Sprintf("values are immutable, so Nomi has no `%s=`: bind the name again, `%s = %s %s ...`", n1.Lexeme, t.Lexeme, t.Lexeme, n1.Lexeme),
				Fix:     Replace("Bind it again", n1.Line, n1.Col, n2.Line, n2.Col+1, fmt.Sprintf("= %s %s", t.Lexeme, n1.Lexeme))}
		}
	case token.HASH:
		if n1.Line != t.Line || n1.Type == token.EOF || n1.Type == token.NEWLINE || n1.Type == token.IDENT && n2.Type != token.DOT {
			return ParseError{Line: t.Line, Col: t.Col,
				Message: "a Nomi comment starts with `//`, not `#`",
				Hints:   []string{"`#` starts a vector or struct literal: `#[1, 2]`, `#{x: 1}`"},
				Fix:     Replace("Write `//`", t.Line, t.Col, t.Line, t.Col+1, "//")}
		}
	}
	return nil
}

// confusedLet is the error for `let x = 1` and its relatives (`let mut`,
// `var`, `const`, `val`) when a name and `=` or `:` follow: in a body a
// binding is `x = 1`, and at the top level a value is `once x = 1`.
func (p *Parser) confusedLet(t token.Token) error {
	i := 1
	if next := p.peekAt(1); next.Type == token.IDENT && next.Lexeme == "mut" && next.Line == t.Line {
		i = 2
	}
	name, after := p.peekAt(i), p.peekAt(i+1)
	if name.Type != token.IDENT || name.Line != t.Line || (after.Type != token.EQ && after.Type != token.COLON) {
		return nil
	}
	spelled := t.Lexeme
	if i == 2 {
		spelled += " mut"
	}
	if p.blockDepth == 0 {
		return ParseError{Line: t.Line, Col: t.Col,
			Message: fmt.Sprintf("Nomi has no `%s`: a file-level value is `once %s = ...`", spelled, name.Lexeme),
			Hints:   []string{"a `once` is computed on its first read and kept"},
			Fix:     Replace("Write `once`", t.Line, t.Col, name.Line, name.Col, "once ")}
	}
	return ParseError{Line: t.Line, Col: t.Col,
		Message: fmt.Sprintf("Nomi has no `%s`: a binding is `%s = ...`", spelled, name.Lexeme),
		Hints:   []string{"values are immutable; a later `" + name.Lexeme + " = ...` binds the name again"},
		Fix:     Replace("Remove `"+spelled+"`", t.Line, t.Col, name.Line, name.Col, "")}
}

func compoundOperator(tt token.TokenType) bool {
	switch tt {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT:
		return true
	}
	return false
}

// confusedOperand is the error for an operand written as another language
// writes it: `match x { ... }`, `{x: 1}`, `{"k": v}`,
// `%User{name: "a"}`, `%{x: 1}`, `vec![1, 2]`. nil when the operand starts
// as Nomi's do.
func (p *Parser) confusedOperand() error {
	if err := p.confusedCapture(); err != nil {
		return err
	}
	t, n1, n2 := p.peek(), p.peekAt(1), p.peekAt(2)
	switch t.Type {
	case token.IDENT:
		switch t.Lexeme {
		case "match", "switch":
			if n1.Line == t.Line && startsScrutinee(n1.Type) {
				return ParseError{Line: t.Line, Col: t.Col,
					Message: fmt.Sprintf("Nomi matches with `case`, not `%s`: write `case x { pattern -> value }`", t.Lexeme),
					Hints:   []string{"each arm is `pattern -> value`, one per line"},
					Fix:     Replace("Write `case`", t.Line, t.Col, t.Line, t.Col+len(t.Lexeme), "case")}
			}
		case "vec":
			if n1.Type == token.BANG && n2.Type == token.LBRACKET && n1.Line == t.Line && n1.Col == t.Col+3 && n2.Col == n1.Col+1 {
				return ParseError{Line: t.Line, Col: t.Col,
					Message: "Nomi has no macros: a list is `[1, 2]`, and a vector `#[1, 2]`",
					Fix:     Replace("Write a list literal", t.Line, t.Col, n2.Line, n2.Col, "")}
			}
		}
	case token.TYPE_IDENT:
		if n1.Type == token.LBRACE && n1.Line == t.Line {
			if t.Lexeme == "Map" && p.mapEntryAt(2) {
				return ParseError{Line: t.Line, Col: t.Col,
					Message: "a map literal is `%{key => value}`: `Map` names the type, and the literal starts with `%`",
					Fix:     Replace("Write `%{`", t.Line, t.Col, t.Line, t.Col+len(t.Lexeme), "%")}
			}
		}
	case token.LBRACE:
		if p.structFieldAt(1) {
			return ParseError{Line: t.Line, Col: t.Col,
				Message: "a struct literal starts with `#`: write `#{x: 1}`",
				Hints:   []string{"a plain `{` opens a block"},
				Fix:     Replace("Write `#{`", t.Line, t.Col, t.Line, t.Col, "#")}
		}
		if (n1.Type == token.STRING_LITERAL || n1.Type == token.STRING) && n2.Type == token.COLON {
			fix := &Fix{Title: "Write a map literal", Edits: []Edit{{Start: Pos{t.Line, t.Col}, End: Pos{t.Line, t.Col}, Text: "%"}}}
			for _, c := range p.colonsAtDepthOne(0) {
				fix.Edits = append(fix.Edits, Edit{Start: Pos{c.Line, c.Col}, End: Pos{c.Line, c.Col + 1}, Text: " =>"})
			}
			return ParseError{Line: t.Line, Col: t.Col,
				Message: "a map literal is `%{\"k\" => v}`: it starts with `%`, and `=>` joins each key to its value",
				Fix:     fix}
		}
		if p.commaListOnLine(0) {
			return ParseError{Line: t.Line, Col: t.Col,
				Message: "a plain `{` opens a block: a tuple is `(a, b)`, and a set is `%[a, b]`",
				Hints:   []string{"a list is `[a, b]`, a vector `#[a, b]`"}}
		}
	case token.PERCENT:
		at := 1
		if n1.Type == token.TYPE_IDENT {
			at = 2
		}
		if open := p.peekAt(at); open.Type == token.LBRACE && open.Col == p.peekAt(at-1).Col+len(p.peekAt(at-1).Lexeme) && p.structFieldAt(at+1) {
			if at == 2 {
				return ParseError{Line: t.Line, Col: t.Col,
					Message: fmt.Sprintf("a struct literal is its name and its fields: write `%s{...}`; `%%` opens a map, whose entries are `key => value`", n1.Lexeme),
					Fix:     Replace("Write `"+n1.Lexeme+"{`", t.Line, t.Col, t.Line, t.Col+1, "")}
			}
			return ParseError{Line: t.Line, Col: t.Col,
				Message: "a map's entries are `key => value`: write `%{\"x\" => 1}`, or `#{x: 1}` for a struct with field `x`",
				Fix:     Replace("Write a struct literal `#{`", t.Line, t.Col, t.Line, t.Col+1, "#")}
		}
	}
	return nil
}

// startsScrutinee reports whether a token of type tt can start the value
// `match` or `switch` is followed by, in another language's spelling.
func startsScrutinee(tt token.TokenType) bool {
	switch tt {
	case token.IDENT, token.TYPE_IDENT, token.INT, token.STRING, token.STRING_LITERAL, token.HASH:
		return true
	}
	return false
}

// structFieldAt reports whether the tokens from offset read as a struct
// field and its value, `name: value`, where the value is not a type: the
// start of another language's struct or object literal. A typed binding
// `name: Type = v` starts the same way with a type after the colon.
func (p *Parser) structFieldAt(offset int) bool {
	name, colon, value := p.peekAt(offset), p.peekAt(offset+1), p.peekAt(offset+2)
	if name.Type != token.IDENT || colon.Type != token.COLON || colon.Line != name.Line || value.Line != name.Line {
		return false
	}
	switch value.Type {
	case token.INT, token.FLOAT, token.DECIMAL, token.STRING, token.STRING_LITERAL, token.STRING_START,
		token.LBRACKET, token.MINUS, token.CODEPOINT_LITERAL:
		return true
	case token.IDENT:
		// `name: mod.Type` is a typed binding; `name: value` is not.
		return p.peekAt(offset+3).Type != token.DOT
	}
	return false
}

// mapEntryAt reports whether the tokens from offset read as a map entry,
// `key => value`, with a literal or a name as the key.
func (p *Parser) mapEntryAt(offset int) bool {
	key := p.peekAt(offset)
	switch key.Type {
	case token.STRING, token.STRING_LITERAL, token.INT, token.IDENT:
		return p.peekAt(offset+1).Type == token.FAT_ARROW
	}
	return false
}

// colonsAtDepthOne are the `:` tokens directly inside the brace at offset,
// not inside a nested bracket, up to its closing brace.
func (p *Parser) colonsAtDepthOne(offset int) []token.Token {
	var out []token.Token
	depth := 0
	for i := offset; ; i++ {
		tok := p.peekAt(i)
		switch tok.Type {
		case token.EOF:
			return nil
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth--
			if depth == 0 {
				return out
			}
		case token.COLON:
			if depth == 1 {
				out = append(out, tok)
			}
		}
	}
}

// commaListOnLine reports whether the brace at offset holds a comma at its
// own depth and closes on its line: `{a, b}` or `{1, 2}`, another
// language's tuple or set.
func (p *Parser) commaListOnLine(offset int) bool {
	open := p.peekAt(offset)
	depth, comma := 0, false
	for i := offset; ; i++ {
		tok := p.peekAt(i)
		if tok.Type == token.EOF || tok.Line != open.Line {
			return false
		}
		switch tok.Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth--
			if depth == 0 {
				return comma
			}
		case token.COMMA:
			if depth == 1 {
				comma = true
			}
		case token.ARROW, token.FAT_ARROW, token.COLON, token.EQ:
			if depth == 1 {
				return false
			}
		}
	}
}

// confusedCapture is the error for a capture written as Elixir's
// formatter writes one, `& &1.amount` or `& &1 * 2`: in Nomi an
// expression capture holds its body in parentheses, `&(&1 * 2)`, and a
// body that only reads a field of `&1` is the field accessor `.amount`.
// The body runs to the first `,` or closing bracket at its own depth on
// the `&`'s line. nil when the `&` is not one the lexer refused for its
// space, or no `&1` follows it.
func (p *Parser) confusedCapture() error {
	amp, first := p.peek(), p.peekAt(1)
	if amp.Type != token.ILLEGAL || amp.Lexeme != "&" || first.Type != token.PLACEHOLDER || first.Line != amp.Line {
		return nil
	}
	depth, end := 0, 1
	for i := 1; ; i++ {
		tok := p.peekAt(i)
		if tok.Type == token.EOF || tok.Type == token.NEWLINE || tok.Line != amp.Line {
			break
		}
		switch tok.Type {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			depth--
		case token.COMMA:
			if depth == 0 {
				depth = -1
			}
		}
		if depth < 0 {
			break
		}
		end = i
	}
	last := p.peekAt(end)
	endCol := last.Col + len(last.Lexeme)
	accessor := ""
	if first.Lexeme == "&1" && end >= 3 && (end-1)%2 == 0 {
		for i := 2; i <= end; i += 2 {
			dot, name := p.peekAt(i), p.peekAt(i+1)
			if dot.Type != token.DOT || name.Type != token.IDENT {
				accessor = ""
				break
			}
			accessor += "." + name.Lexeme
		}
	}
	if accessor != "" {
		return ParseError{Line: amp.Line, Col: amp.Col,
			Message: fmt.Sprintf("a function that only reads a field is the field accessor `%s`", accessor),
			Hints:   []string{"an expression capture holds its body in parentheses: `&(&1 * 2)`"},
			Fix:     Replace("Write `"+accessor+"`", amp.Line, amp.Col, last.Line, endCol, accessor)}
	}
	return ParseError{Line: amp.Line, Col: amp.Col,
		Message: "an expression capture holds its body in parentheses: write `&(...)`, as in `&(&1 * 2)`",
		Fix: &Fix{Title: "Add the parentheses", Edits: []Edit{
			{Start: Pos{amp.Line, amp.Col}, End: Pos{first.Line, first.Col}, Text: "&("},
			{Start: Pos{last.Line, endCol}, End: Pos{last.Line, endCol}, Text: ")"},
		}}}
}
