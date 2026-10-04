package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

func TestCodepointLiteralExpression(t *testing.T) {
	cases := []struct {
		src    string
		value  rune
		lexeme string
	}{
		{`'a'`, 'a', `a`},
		{`'\''`, '\'', `\'`},
		{`'\u{7F}'`, 0x7F, `\u{7F}`},
	}
	for _, c := range cases {
		lit, ok := parseExpr(t, c.src).(*ast.CodepointLit)
		if !ok {
			t.Fatalf("Parse(%s): expected *ast.CodepointLit", c.src)
		}
		if lit.Value != c.value || lit.Lexeme != c.lexeme || lit.Line != 1 || lit.Col != 1 {
			t.Errorf("Parse(%s) = %+v, want value %q lexeme %q at 1:1", c.src, lit, c.value, c.lexeme)
		}
	}
}

func TestCodepointLiteralRange(t *testing.T) {
	r, ok := parseExpr(t, `'a'..='z'`).(*ast.RangeLit)
	if !ok {
		t.Fatalf("expected *ast.RangeLit")
	}
	if s, ok := r.Start.(*ast.CodepointLit); !ok || s.Value != 'a' {
		t.Errorf("start = %#v", r.Start)
	}
	if e, ok := r.End.(*ast.CodepointLit); !ok || e.Value != 'z' {
		t.Errorf("end = %#v", r.End)
	}
}

func TestCodepointLiteralPatterns(t *testing.T) {
	c, ok := parseExpr(t, "case cp {\n    'a' -> 1\n    '\\n' -> 2\n    _ -> 3\n}").(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case")
	}
	if p, ok := c.Branches[0].Pattern.(*ast.CodepointLit); !ok || p.Value != 'a' {
		t.Errorf("arm 0 pattern = %#v", c.Branches[0].Pattern)
	}
	if p, ok := c.Branches[1].Pattern.(*ast.CodepointLit); !ok || p.Value != '\n' {
		t.Errorf("arm 1 pattern = %#v", c.Branches[1].Pattern)
	}

	// Nested positions go through parsePattern.
	c, ok = parseExpr(t, "case x {\n    Some('a') -> 1\n    ('b', _) -> 2\n    _ -> 3\n}").(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case")
	}
	ep, ok := c.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("arm 0 = %#v", c.Branches[0].Pattern)
	}
	if p, ok := ep.Payload.(*ast.CodepointLit); !ok || p.Value != 'a' {
		t.Errorf("Some payload = %#v", ep.Payload)
	}
	tp, ok := c.Branches[1].Pattern.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("arm 1 = %#v", c.Branches[1].Pattern)
	}
	if p, ok := tp.Patterns[0].(*ast.CodepointLit); !ok || p.Value != 'b' {
		t.Errorf("tuple component = %#v", tp.Patterns[0])
	}
}

// A malformed literal reports the lexer's diagnosis as the parse error, in
// expression, case-arm and nested pattern position alike.
func TestCodepointLiteralErrorsReportTheLexerDiagnosis(t *testing.T) {
	cases := []struct {
		src  string
		line int
		col  int
		msg  string
	}{
		{"x = 'ab'", 1, 5, `codepoint literal 'ab' holds more than one character; write "ab" as a string`},
		{"x = ''", 1, 5, `empty codepoint literal ''; a codepoint literal holds exactly one ASCII character`},
		{"x = 'é'", 1, 5, `codepoint literal 'é' is U+00E9, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "é" as a string, or use Codepoint.from_int for another scalar value`},
		{"x = 'a", 1, 5, `unterminated codepoint literal; close it with '`},
		{`x = '\q'`, 1, 5, `invalid escape \q in codepoint literal; the escapes are \n, \t, \\, \' and \u{...}`},
		{"case c {\n    'ab' -> 1\n    _ -> 2\n}", 2, 5, `codepoint literal 'ab' holds more than one character; write "ab" as a string`},
		{"case c {\n    Some('é') -> 1\n    _ -> 2\n}", 2, 10, `codepoint literal 'é' is U+00E9, not ASCII: a codepoint literal is ASCII only (U+0000–U+007F); write "é" as a string, or use Codepoint.from_int for another scalar value`},
	}
	for _, c := range cases {
		_, err := Parse(lexer.Lex(c.src))
		if err == nil {
			t.Errorf("Parse(%q): no error, want %q", c.src, c.msg)
			continue
		}
		var pe ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q): error %T %v is not a ParseError", c.src, err, err)
			continue
		}
		if pe.Line != c.line || pe.Col != c.col || pe.Message != c.msg {
			t.Errorf("Parse(%q):\n got %d:%d %q\nwant %d:%d %q", c.src, pe.Line, pe.Col, pe.Message, c.line, c.col, c.msg)
		}
	}
}

// Inline Go is lexed with Nomi's lexer, so a Go rune literal over ASCII
// arrives as a codepoint literal and must render with its quotes.
func TestInlineGoKeepsRuneLiterals(t *testing.T) {
	got := renderInlineGoTokens(lexer.Lex(`r := 'a'; q := '\''; e := 'é'`))
	want := `r := 'a'; q := '\''; e := 'é'`
	if got != want {
		t.Errorf("renderInlineGoTokens = %q, want %q", got, want)
	}
}
