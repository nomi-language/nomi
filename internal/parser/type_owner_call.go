package parser

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A call's owner is a name: `User.from_json(json)`, `Box.new(1)`. A type
// written with its arguments in that place, `List<String>.from_json(json)`,
// reads as the comparisons `List < String > .from_json(json)`, which the
// checker would reject with a comparison mismatch at the `<`, and
// `Map<String, Int>.from_json(j)` does not parse at all. typeOwnerCall
// reports both at the type instead, with the call Nomi writes.
//
// It matches a type name (`List`, or a file's `json.Json`), a `<` list that
// parses as types, its `>`, and `.name(` right after. As comparisons that
// source compares a Bool with a field accessor's call, so no program means
// it.

// typeOwnerCall is the error for `Type<Args>.name(...)`, where left is the
// owner and the current token its `<`. nil when the tokens after it are not
// that shape; the position is left where it was either way.
func (p *Parser) typeOwnerCall(left ast.Node) error {
	owner, start, ok := typeOwnerName(left)
	if !ok {
		return nil
	}
	lt := p.peek()
	saved := p.pos
	defer func() { p.pos = saved }()
	p.advance() // consume <
	argsStart := p.pos
	for {
		if _, err := p.parseTypeAnnotation(); err != nil {
			return nil
		}
		p.skipBreaksBefore(token.GT)
		if p.atEnd() || p.peek().Type != token.COMMA {
			break
		}
		p.advance() // consume COMMA
	}
	if p.atEnd() || p.peek().Type != token.GT {
		return nil
	}
	typeArgs := p.tokenText(argsStart, p.pos)
	p.advance() // consume >
	if p.peek().Type != token.DOT || p.peekAt(1).Type != token.IDENT || p.peekAt(2).Type != token.LPAREN {
		return nil
	}
	name := p.peekAt(1)
	method := name.Lexeme
	callArgs := "..."
	if end, ok := p.closingParen(p.pos + 2); ok {
		callArgs = p.tokenText(p.pos+3, end)
	}
	typ := owner + "<" + typeArgs + ">"
	pe := ParseError{Line: lt.Line, Col: lt.Col,
		Message: fmt.Sprintf("`%s.%s(...)` names a type with arguments as a call's owner, which Nomi reads as comparisons", typ, method),
		Hints:   []string{typeOwnerHelp(owner, typ, method, callArgs)}}
	if method == "from_json" && typeArgs != "..." {
		pe.Fix = Replace("Write `FromJson.from_json<"+typ+">`", start.Line, start.Col,
			name.Line, name.Col+len(name.Lexeme), "FromJson.from_json<"+typ+">")
	}
	return pe
}

// typeOwnerHelp says how to write the call to method of typ, whose
// arguments are callArgs.
func typeOwnerHelp(owner, typ, method, callArgs string) string {
	if method == "from_json" {
		return fmt.Sprintf("name the target as a type argument, `FromJson.from_json<%s>(%s)`, or annotate the result: `value: Result<%s, Json.ShapeError> = FromJson.from_json(%s)`",
			typ, callArgs, typ, callArgs)
	}
	return fmt.Sprintf("name the type without its arguments, `%s.%s(%s)`, and annotate the result as `%s` if they are not inferred",
		owner, method, callArgs, typ)
}

// typeOwnerName is the source of a type name that can own a call, `List`
// or `json.Json`, and where it starts. ok is false for anything else, a
// value among them.
func typeOwnerName(left ast.Node) (string, Pos, bool) {
	switch n := left.(type) {
	case *ast.TypeIdent:
		return n.Name, Pos{n.Line, n.Col}, true
	case *ast.FieldAccess:
		obj, isIdent := n.Object.(*ast.Ident)
		if isIdent && n.Field != nil && n.Field.Name != "" && unicode.IsUpper(rune(n.Field.Name[0])) {
			return obj.Name + "." + n.Field.Name, Pos{obj.Line, obj.Col}, true
		}
	}
	return "", Pos{}, false
}

// closingParen is the index of the `)` that closes the `(` at open, if it
// closes.
func (p *Parser) closingParen(open int) (int, bool) {
	depth := 0
	for i := open; i < len(p.tokens); i++ {
		switch p.tokens[i].Type {
		case token.LPAREN:
			depth++
		case token.RPAREN:
			depth--
			if depth == 0 {
				return i, true
			}
		case token.EOF:
			return 0, false
		}
	}
	return 0, false
}

// tokenText is the source of the tokens from index from up to to, spaced
// as written, when they are names, punctuation and integers on one line;
// "..." for anything else, whose lexeme may not be its source.
func (p *Parser) tokenText(from, to int) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		t := p.tokens[i]
		switch t.Type {
		case token.IDENT, token.TYPE_IDENT, token.INT, token.DOT, token.COMMA,
			token.LT, token.GT, token.LPAREN, token.RPAREN:
		default:
			return "..."
		}
		if i > from {
			prev := p.tokens[i-1]
			if prev.Line != t.Line {
				return "..."
			}
			if t.Col > prev.Col+len(prev.Lexeme) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(t.Lexeme)
	}
	return b.String()
}
