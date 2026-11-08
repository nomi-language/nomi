package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// tupleIndexOrdinals reads the number after a field-access dot, `t.0`, as
// the ordinal names a tuple's items are read by (`t.first`), for the error
// that says so. The lexer reads `t.1.0`'s `1.0` as one Float, which is two
// indices. Each index is plain decimal digits with no leading zero; an
// index past the last ordinal has none. ok is false when tok is no index at
// all (`t.1e3`, `t.0x1`, `t.01`), which is the plain field-name error.
func tupleIndexOrdinals(tok token.Token) (names []string, ok bool, past int) {
	if tok.Type != token.INT && tok.Type != token.FLOAT {
		return nil, false, 0
	}
	parts := strings.Split(tok.Lexeme, ".")
	if len(parts) > 2 {
		return nil, false, 0
	}
	for _, s := range parts {
		if s == "" || (len(s) > 1 && s[0] == '0') || strings.TrimLeft(s, "0123456789") != "" {
			return nil, false, 0
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			return nil, false, 0
		}
		name, named := ast.TupleOrdinal(i)
		if !named {
			return nil, true, i + 1
		}
		names = append(names, name)
	}
	return names, true, 0
}

// isTupleIndexToken reports whether tok after a dot is a number written as
// a tuple index, `.0` or `.1.0`.
func isTupleIndexToken(tok token.Token) bool {
	_, ok, _ := tupleIndexOrdinals(tok)
	return ok
}

// tupleIndexError reports `t.0` (receiver is what precedes the dot, "t";
// empty in an accessor) and returns the ordinal names to read in its place,
// so the parse goes on as if they were written. ok is false for an index
// past the last ordinal, which has no name to go on with.
func (p *Parser) tupleIndexError(receiver string, tok token.Token) (names []string, err error) {
	names, ok, past := tupleIndexOrdinals(tok)
	if !ok {
		return nil, errorAt(tok.Line, tok.Col, "expected field name after '.'")
	}
	last, _ := ast.TupleOrdinal(ast.TupleOrdinalCount - 1)
	if past > 0 {
		return nil, ParseError{Line: tok.Line, Col: tok.Col,
			Message: fmt.Sprintf("tuple items are read by ordinal name, `.first` to `.%s`, and item %d has none", last, past),
			Hints:   []string{"bind the items with a pattern, `(a, b, ...)`, or make the tuple a struct with named fields"}}
	}
	written := "." + strings.Join(names, ".")
	p.softError(ParseError{Line: tok.Line, Col: tok.Col,
		Message: fmt.Sprintf("tuple items are read by ordinal name, not by number: write `%s%s`", receiver, written),
		Hints:   []string{fmt.Sprintf("a tuple's items are `.first`, `.second`, `.third`, ... up to `.%s`", last)},
		Fix:     Replace("Write `"+written+"`", tok.Line, tok.Col, tok.Line, tok.Col+len(tok.Lexeme), strings.Join(names, "."))})
	return names, nil
}

// receiverSpelling is how the error for `t.0` names the value before the
// dot: a name or a capture placeholder as written, anything else not at all.
func receiverSpelling(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.PlaceholderParam:
		return "&" + strconv.Itoa(v.N)
	case *ast.FieldAccess:
		if v.Field != nil {
			if r := receiverSpelling(v.Object); r != "" {
				return r + "." + v.Field.Name
			}
		}
	}
	return ""
}
