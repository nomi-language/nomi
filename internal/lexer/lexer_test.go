package lexer

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/token"
	"reflect"
	"testing"
)

// helper to extract just types from tokens
func types(tokens []token.Token) []token.TokenType {
	tt := make([]token.TokenType, len(tokens))
	for i, t := range tokens {
		tt[i] = t.Type
	}
	return tt
}

// helper to check token list matches expected type/lexeme pairs.
// Tag is checked only when the expected token has a non-empty Tag —
// callers omit it for non-tagged token kinds.
func assertTokens(t *testing.T, source string, expected []token.Token) {
	t.Helper()
	tokens := Lex(source)
	if len(tokens) != len(expected) {
		t.Fatalf("Lex(%q): expected %d tokens, got %d\ntokens: %v", source, len(expected), len(tokens), tokens)
	}
	for i, exp := range expected {
		got := tokens[i]
		if got.Type != exp.Type {
			t.Errorf("token[%d]: expected type %s, got %s (lexeme=%q)", i, exp.Type, got.Type, got.Lexeme)
		}
		if exp.Lexeme != "" && got.Lexeme != exp.Lexeme {
			t.Errorf("token[%d]: expected lexeme %q, got %q", i, exp.Lexeme, got.Lexeme)
		}
		if exp.Tag != "" && got.Tag != exp.Tag {
			t.Errorf("token[%d]: expected tag %q, got %q", i, exp.Tag, got.Tag)
		}
		if exp.Line != 0 && got.Line != exp.Line {
			t.Errorf("token[%d]: expected line %d, got %d", i, exp.Line, got.Line)
		}
		if exp.Col != 0 && got.Col != exp.Col {
			t.Errorf("token[%d]: expected col %d, got %d", i, exp.Col, got.Col)
		}
	}
}

func tok(typ token.TokenType, lexeme string) token.Token {
	return token.Token{Type: typ, Lexeme: lexeme}
}

// tokTagged is the typed-literal counterpart to tok: builds an
// expected token with both Lexeme (body) and Tag (identifier) set.
func tokTagged(typ token.TokenType, tag, lexeme string) token.Token {
	return token.Token{Type: typ, Lexeme: lexeme, Tag: tag}
}

func tokAt(typ token.TokenType, lexeme string, line, col int) token.Token {
	return token.Token{Type: typ, Lexeme: lexeme, Line: line, Col: col}
}

// --- Single-character operators ---

func TestSingleCharOperators(t *testing.T) {
	assertTokens(t, "+", []token.Token{
		tok(token.PLUS, "+"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "-", []token.Token{
		tok(token.MINUS, "-"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "*", []token.Token{
		tok(token.STAR, "*"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "/", []token.Token{
		tok(token.SLASH, "/"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "%", []token.Token{
		tok(token.PERCENT, "%"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "!", []token.Token{
		tok(token.BANG, "!"),
		tok(token.EOF, ""),
	})
	assertTokens(t, ".", []token.Token{
		tok(token.DOT, "."),
		tok(token.EOF, ""),
	})
	assertTokens(t, ",", []token.Token{
		tok(token.COMMA, ","),
		tok(token.EOF, ""),
	})
	assertTokens(t, ":", []token.Token{
		tok(token.COLON, ":"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "(", []token.Token{
		tok(token.LPAREN, "("),
		tok(token.EOF, ""),
	})
	assertTokens(t, ")", []token.Token{
		tok(token.RPAREN, ")"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "[", []token.Token{
		tok(token.LBRACKET, "["),
		tok(token.EOF, ""),
	})
	assertTokens(t, "]", []token.Token{
		tok(token.RBRACKET, "]"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "{", []token.Token{
		tok(token.LBRACE, "{"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "}", []token.Token{
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "@", []token.Token{
		tok(token.AT, "@"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "#", []token.Token{
		tok(token.HASH, "#"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "#[]", []token.Token{
		tok(token.HASH, "#"),
		tok(token.LBRACKET, "["),
		tok(token.RBRACKET, "]"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "#{}", []token.Token{
		tok(token.HASH, "#"),
		tok(token.LBRACE, "{"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

// --- Decorator marker ---

func TestDecoratorAt(t *testing.T) {
	// `uses` is an ordinary identifier; `@` stays a token so the parser can
	// refuse a decorator line by name.
	assertTokens(t, "@uses uses", []token.Token{
		tok(token.AT, "@"),
		tok(token.IDENT, "uses"),
		tok(token.IDENT, "uses"),
		tok(token.EOF, ""),
	})
	// `@derive Formatted` — non-keyword decorator name + type-ident arg.
	assertTokens(t, "@derive Formatted", []token.Token{
		tok(token.AT, "@"),
		tok(token.IDENT, "derive"),
		tok(token.TYPE_IDENT, "Formatted"),
		tok(token.EOF, ""),
	})
	// `@derive eq, hash` — non-keyword decorator name + ident args.
	assertTokens(t, "@derive eq, hash", []token.Token{
		tok(token.AT, "@"),
		tok(token.IDENT, "derive"),
		tok(token.IDENT, "eq"),
		tok(token.COMMA, ","),
		tok(token.IDENT, "hash"),
		tok(token.EOF, ""),
	})
}

// --- Two-character operators ---

func TestTwoCharOperators(t *testing.T) {
	assertTokens(t, "++", []token.Token{
		tok(token.PLUS, "+"),
		tok(token.PLUS, "+"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "==", []token.Token{
		tok(token.EQEQ, "=="),
		tok(token.EOF, ""),
	})
	assertTokens(t, "!=", []token.Token{
		tok(token.BANGEQ, "!="),
		tok(token.EOF, ""),
	})
	assertTokens(t, "<=", []token.Token{
		tok(token.LTEQ, "<="),
		tok(token.EOF, ""),
	})
	assertTokens(t, ">=", []token.Token{
		tok(token.GTEQ, ">="),
		tok(token.EOF, ""),
	})
	assertTokens(t, "<", []token.Token{
		tok(token.LT, "<"),
		tok(token.EOF, ""),
	})
	assertTokens(t, ">", []token.Token{
		tok(token.GT, ">"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "=", []token.Token{
		tok(token.EQ, "="),
		tok(token.EOF, ""),
	})
	assertTokens(t, "|>", []token.Token{
		tok(token.PIPE, "|>"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "->", []token.Token{
		tok(token.ARROW, "->"),
		tok(token.EOF, ""),
	})
}

// --- Integer literals ---

func TestIntegerLiterals(t *testing.T) {
	assertTokens(t, "42", []token.Token{
		tok(token.INT, "42"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "1_000", []token.Token{
		tok(token.INT, "1_000"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "0xFF", []token.Token{
		tok(token.INT, "0xFF"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "0b1010", []token.Token{
		tok(token.INT, "0b1010"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "0o77", []token.Token{
		tok(token.INT, "0o77"),
		tok(token.EOF, ""),
	})
}

// --- Float literals ---

func TestFloatLiterals(t *testing.T) {
	assertTokens(t, "3.14", []token.Token{
		tok(token.FLOAT, "3.14"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "1.0e10", []token.Token{
		tok(token.FLOAT, "1.0e10"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "2.5E-3", []token.Token{
		tok(token.FLOAT, "2.5E-3"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "1.0e+5", []token.Token{
		tok(token.FLOAT, "1.0e+5"),
		tok(token.EOF, ""),
	})
}

// --- Decimal literals ---

func TestDecimalLiterals(t *testing.T) {
	// Fractional mantissa + 'd' → one DECIMAL token carrying the full lexeme.
	assertTokens(t, "1.50d", []token.Token{
		tok(token.DECIMAL, "1.50d"),
		tok(token.EOF, ""),
	})
	// Bare integer mantissa + 'd' → scale-0 DECIMAL (not INT + ident).
	assertTokens(t, "5d", []token.Token{
		tok(token.DECIMAL, "5d"),
		tok(token.EOF, ""),
	})
	// Underscore separators are allowed, matching Int/Float.
	assertTokens(t, "1_000.00d", []token.Token{
		tok(token.DECIMAL, "1_000.00d"),
		tok(token.EOF, ""),
	})

	// 1.50d is ONE DECIMAL token, never FLOAT followed by ident `d`.
	dec := Lex("1.50d")
	if len(dec) != 2 || dec[0].Type != token.DECIMAL {
		t.Fatalf("1.50d: expected single DECIMAL token, got %v", dec)
	}

	// `d` followed by an identifier char is NOT a suffix: 1.50days stays
	// FLOAT `1.50` + ident `days`.
	assertTokens(t, "1.50days", []token.Token{
		tok(token.FLOAT, "1.50"),
		tok(token.IDENT, "days"),
		tok(token.EOF, ""),
	})

	// `5d` is DECIMAL, `5days` is INT + ident.
	assertTokens(t, "5days", []token.Token{
		tok(token.INT, "5"),
		tok(token.IDENT, "days"),
		tok(token.EOF, ""),
	})

	// A `d` after an exponent is NOT a decimal suffix (no exponent notation in
	// decimal literals): 1.5e3d lexes as FLOAT `1.5e3` then a separate ident `d`.
	assertTokens(t, "1.5e3d", []token.Token{
		tok(token.FLOAT, "1.5e3"),
		tok(token.IDENT, "d"),
		tok(token.EOF, ""),
	})

	// Decimals participate in arithmetic expressions.
	assertTokens(t, "1.50d + 1.5d", []token.Token{
		tok(token.DECIMAL, "1.50d"),
		tok(token.PLUS, "+"),
		tok(token.DECIMAL, "1.5d"),
		tok(token.EOF, ""),
	})

	// Uppercase 'D' is accepted too (case-insensitive, like 0x/0X, e/E):
	// 1.50D and 5D lex as single DECIMAL tokens carrying the full lexeme.
	assertTokens(t, "1.50D", []token.Token{
		tok(token.DECIMAL, "1.50D"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "5D", []token.Token{
		tok(token.DECIMAL, "5D"),
		tok(token.EOF, ""),
	})

	// Capital `D` followed by ident chars is NOT a suffix: 1.50Days stays
	// FLOAT `1.50` + ident `Days` (the !isIdentPart guard still applies).
	// `Days` is uppercase-start, so it lexes as TYPE_IDENT — the point is it
	// is a separate ident token, never folded into a DECIMAL.
	assertTokens(t, "1.50Days", []token.Token{
		tok(token.FLOAT, "1.50"),
		tok(token.TYPE_IDENT, "Days"),
		tok(token.EOF, ""),
	})
}

// --- String literals (no interpolation) ---

func TestStringLiteral(t *testing.T) {
	assertTokens(t, `"hello"`, []token.Token{
		tok(token.STRING_LITERAL, "hello"),
		tok(token.EOF, ""),
	})
}

func TestStringWithEscapes(t *testing.T) {
	assertTokens(t, `"hello\nworld"`, []token.Token{
		tok(token.STRING_LITERAL, "hello\nworld"),
		tok(token.EOF, ""),
	})
	assertTokens(t, `"tab\there"`, []token.Token{
		tok(token.STRING_LITERAL, "tab\there"),
		tok(token.EOF, ""),
	})
	assertTokens(t, `"back\\slash"`, []token.Token{
		tok(token.STRING_LITERAL, "back\\slash"),
		tok(token.EOF, ""),
	})
	assertTokens(t, `"say\"hi\""`, []token.Token{
		tok(token.STRING_LITERAL, `say"hi"`),
		tok(token.EOF, ""),
	})
}

// --- String interpolation ---

func TestStringInterpolation(t *testing.T) {
	// "hello ${name}"
	assertTokens(t, `"hello ${name}"`, []token.Token{
		tok(token.STRING_START, "hello "),
		tok(token.IDENT, "name"),
		tok(token.STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestStringInterpolationMultiple(t *testing.T) {
	// "${a} and ${b}"
	assertTokens(t, `"${a} and ${b}"`, []token.Token{
		tok(token.STRING_START, ""),
		tok(token.IDENT, "a"),
		tok(token.STRING_PART, " and "),
		tok(token.IDENT, "b"),
		tok(token.STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestStringInterpolationWithTrailingText(t *testing.T) {
	// "hi ${name}!"
	assertTokens(t, `"hi ${name}!"`, []token.Token{
		tok(token.STRING_START, "hi "),
		tok(token.IDENT, "name"),
		tok(token.STRING_END, "!"),
		tok(token.EOF, ""),
	})
}

func TestStringInterpolationNestedBraces(t *testing.T) {
	// "value ${f(x)}" — the interpolation contains parens but no nested braces
	assertTokens(t, `"value ${f(x)}"`, []token.Token{
		tok(token.STRING_START, "value "),
		tok(token.IDENT, "f"),
		tok(token.LPAREN, "("),
		tok(token.IDENT, "x"),
		tok(token.RPAREN, ")"),
		tok(token.STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestSingleLineBackslashBraceLiteral(t *testing.T) {
	// `\{` is not a recognized escape; the strict lexer rejects it. A brace
	// needs no escaping in a string — write `{` directly.
	if !lexHasIllegal(`"a\{b"`) {
		t.Error(`expected ILLEGAL for unknown escape \{`)
	}
}

func TestSingleLineHashBraceInterpolation(t *testing.T) {
	assertTokens(t, `"x = ${y}"`, []token.Token{
		tok(token.STRING_START, "x = "),
		tok(token.IDENT, "y"),
		tok(token.STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestSingleLineLiteralBrace(t *testing.T) {
	assertTokens(t, `"a {b} c"`, []token.Token{
		tok(token.STRING_LITERAL, "a {b} c"),
		tok(token.EOF, ""),
	})
}

func TestSingleLineDoubleHashLiteral(t *testing.T) {
	assertTokens(t, `"cost: #5"`, []token.Token{
		tok(token.STRING_LITERAL, "cost: #5"),
		tok(token.EOF, ""),
	})
}

func TestSingleLineBareHash(t *testing.T) {
	assertTokens(t, `"price #5 USD"`, []token.Token{
		tok(token.STRING_LITERAL, "price #5 USD"),
		tok(token.EOF, ""),
	})
}

func TestSingleLineHashBraceAtStart(t *testing.T) {
	assertTokens(t, `"${y} suffix"`, []token.Token{
		tok(token.STRING_START, ""),
		tok(token.IDENT, "y"),
		tok(token.STRING_END, " suffix"),
		tok(token.EOF, ""),
	})
}

// --- Triple-quoted strings ---

func TestTripleQuotedBasic(t *testing.T) {
	src := "\"\"\"\n    hello\n    world\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "hello\nworld"),
		tok(token.EOF, ""),
	})
}

func TestTripleQuotedNoEscapeProcessing(t *testing.T) {
	src := "\"\"\"\n    hello\\nworld\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "hello\\nworld"),
		tok(token.EOF, ""),
	})
}

func TestTripleQuotedWithInterpolation(t *testing.T) {
	src := "\"\"\"\n    hello ${name}\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_START, "hello "),
		tok(token.IDENT, "name"),
		tok(token.TRIPLE_STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestTripleQuotedIndentStripping(t *testing.T) {
	src := "\"\"\"\n        SELECT *\n        FROM users\n        \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "SELECT *\nFROM users"),
		tok(token.EOF, ""),
	})
}

func TestTripleQuotedSingleLine(t *testing.T) {
	// Content on same line as opening — no leading newline to strip
	src := "\"\"\"hello\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "hello"),
		tok(token.EOF, ""),
	})
}

func TestTripleHashBraceInterpolation(t *testing.T) {
	src := "\"\"\"\n    x = ${y}\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_START, "x = "),
		tok(token.IDENT, "y"),
		tok(token.TRIPLE_STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestTripleLiteralBrace(t *testing.T) {
	src := "\"\"\"\n    {body}\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "{body}"),
		tok(token.EOF, ""),
	})
}

func TestTripleEscapedInterpolation(t *testing.T) {
	// \${ → literal ${
	src := "\"\"\"\n    cost: \\${price}\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "cost: ${price}"),
		tok(token.EOF, ""),
	})
}

func TestTripleStripsLeadingNewline(t *testing.T) {
	// Newline immediately after the opening """ is stripped from the body.
	src := "\"\"\"\nbody\n\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "body"),
		tok(token.EOF, ""),
	})
}

func TestTripleNoLeadingNewline(t *testing.T) {
	// No newline immediately after opening; body starts with text on the same line.
	src := `"""body"""`
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "body"),
		tok(token.EOF, ""),
	})
}

func TestTripleEmptyBodyWithNewline(t *testing.T) {
	// Leading newline stripped; closing """ sits on the next line with no content.
	src := "\"\"\"\n\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, ""),
		tok(token.EOF, ""),
	})
}

func TestTripleStripsTrailingNewline(t *testing.T) {
	// Newline before the closing """ (with no indent) is stripped.
	src := "\"\"\"\nline1\nline2\n\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "line1\nline2"),
		tok(token.EOF, ""),
	})
}

func TestTripleEmptyBody(t *testing.T) {
	// """""" with no body and no leading newline.
	src := `""""""`
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, ""),
		tok(token.EOF, ""),
	})
}

// --- Triple-quoted min-indent semantics (Java JEP 378 style) ---

func TestTripleMinIndentUnderIndentedLine(t *testing.T) {
	// SELECT at 2 spaces, FROM at 4 spaces, closing at 4 spaces.
	// Min across non-blank body lines + closing = 2.
	// Strip 2 from each: SELECT flush left, FROM keeps 2 leading spaces.
	src := "\"\"\"\n  SELECT *\n    FROM users\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "SELECT *\n  FROM users"),
		tok(token.EOF, ""),
	})
}

func TestTripleMinIndentClosingDeeperThanBody(t *testing.T) {
	// Body lines flush left, closing at 2 spaces.
	// Min = 0 (from body lines). Strip nothing.
	src := "\"\"\"\nfoo\nbar\n  \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "foo\nbar"),
		tok(token.EOF, ""),
	})
}

func TestTripleMinIndentBlankLineBetween(t *testing.T) {
	// Blank line in the middle does not contribute to the min calc.
	// Body at 4, closing at 4 → strip 4.
	src := "\"\"\"\n    foo\n\n    bar\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "foo\n\nbar"),
		tok(token.EOF, ""),
	})
}

func TestTripleMinIndentBlankLineWithSomeWhitespace(t *testing.T) {
	// Blank line carries 1 space (less than the 4-space baseline). The
	// space is stripped (rather than leaking into the body) because
	// stripLinesIndent removes up to len(baseline) matching chars.
	src := "\"\"\"\n    foo\n \n    bar\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "foo\n\nbar"),
		tok(token.EOF, ""),
	})
}

func TestTripleMinIndentClosingFlushLeft(t *testing.T) {
	// Closing-delimiter line is flush left; even if body lines are
	// indented, the closing line forces a 0-space baseline.
	src := "\"\"\"\n    foo\n    bar\n\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "    foo\n    bar"),
		tok(token.EOF, ""),
	})
}

func TestTripleMinIndentTabsAndSpaces(t *testing.T) {
	// All non-blank lines start with the same tab indent → strip the tab.
	src := "\"\"\"\n\tfoo\n\tbar\n\t\"\"\""
	assertTokens(t, src, []token.Token{
		tok(token.TRIPLE_STRING_LITERAL, "foo\nbar"),
		tok(token.EOF, ""),
	})
}

// --- Raw strings (`...`) ---

func TestRawSingleLineBasic(t *testing.T) {
	assertTokens(t, "`hello`", []token.Token{
		tok(token.RAW_STRING_LITERAL, "hello"),
		tok(token.EOF, ""),
	})
}

func TestRawSingleLineHashBraceLiteral(t *testing.T) {
	// `${name}` inside a raw string is verbatim text — no interpolation
	// parsing, no `#` doubling.
	assertTokens(t, "`price: ${VAR}`", []token.Token{
		tok(token.RAW_STRING_LITERAL, "price: ${VAR}"),
		tok(token.EOF, ""),
	})
}

func TestRawSingleLineBackslashLiteral(t *testing.T) {
	// `\d` is a literal backslash + literal `d`.
	assertTokens(t, "`\\d{3,4}`", []token.Token{
		tok(token.RAW_STRING_LITERAL, `\d{3,4}`),
		tok(token.EOF, ""),
	})
}

func TestRawSingleLineEscapedDollarLiteral(t *testing.T) {
	// `\${` in raw form is verbatim — the backslash is kept.
	assertTokens(t, "`\\${already}`", []token.Token{
		tok(token.RAW_STRING_LITERAL, "\\${already}"),
		tok(token.EOF, ""),
	})
}

func TestRawTripleBasic(t *testing.T) {
	src := "`\n    hello\n    world\n    `"
	assertTokens(t, src, []token.Token{
		tok(token.RAW_TRIPLE_STRING_LITERAL, "hello\nworld"),
		tok(token.EOF, ""),
	})
}

func TestRawTripleHashBraceLiteral(t *testing.T) {
	// `${f}` inside a raw multi-line string is verbatim — no interpolation
	// parsing.
	src := "`\n    echo ${f}\n    `"
	assertTokens(t, src, []token.Token{
		tok(token.RAW_TRIPLE_STRING_LITERAL, "echo ${f}"),
		tok(token.EOF, ""),
	})
}

func TestRawTripleEscapedDollarLiteral(t *testing.T) {
	// `\${` in multi-line raw form is verbatim — the backslash is kept.
	src := "`\n    cost: \\${price}\n    `"
	assertTokens(t, src, []token.Token{
		tok(token.RAW_TRIPLE_STRING_LITERAL, "cost: \\${price}"),
		tok(token.EOF, ""),
	})
}

func TestRawTripleRegexBackslash(t *testing.T) {
	// Regex with `\d` survives verbatim.
	src := "`\n    \\d{3,4}\n    `"
	assertTokens(t, src, []token.Token{
		tok(token.RAW_TRIPLE_STRING_LITERAL, "\\d{3,4}"),
		tok(token.EOF, ""),
	})
}

func TestRawTripleIndentStripping(t *testing.T) {
	// Raw triple respects the same min-indent rules as plain triple.
	src := "`\n        SELECT *\n        FROM users\n        `"
	assertTokens(t, src, []token.Token{
		tok(token.RAW_TRIPLE_STRING_LITERAL, "SELECT *\nFROM users"),
		tok(token.EOF, ""),
	})
}

func TestRawBacktickNoClosingDelimiterIsIllegal(t *testing.T) {
	tokens := Lex("`x")
	if len(tokens) < 1 {
		t.Fatalf("expected at least one token")
	}
	if tokens[0].Type != token.ILLEGAL {
		t.Errorf("expected ILLEGAL for unterminated raw string, got %s", tokens[0].Type)
	}
}

// lexHasIllegal reports whether lexing src produces any ILLEGAL token.
func lexHasIllegal(src string) bool {
	for _, tok := range Lex(src) {
		if tok.Type == token.ILLEGAL {
			return true
		}
	}
	return false
}

func TestString_UnknownEscapeIsIllegal(t *testing.T) {
	// `\q` is not a recognized escape; it must be rejected, not silently kept
	// as the two literal characters `\` and `q`.
	if !lexHasIllegal(`"a\qb"`) {
		t.Error(`expected ILLEGAL for unknown escape \q`)
	}
}

func TestString_MalformedUnicodeEscapeIsIllegal(t *testing.T) {
	if !lexHasIllegal("\"\\uABCD\"") {
		t.Error(`expected ILLEGAL for brace-less \u (Nomi uses \u{...})`)
	}
	if !lexHasIllegal(`"\u{zz}"`) {
		t.Error(`expected ILLEGAL for non-hex \u{zz}`)
	}
	if !lexHasIllegal(`"\u{}"`) {
		t.Error(`expected ILLEGAL for empty \u{}`)
	}
}

func TestString_ValidEscapesAreNotIllegal(t *testing.T) {
	// Guard: every supported escape must still lex cleanly.
	if lexHasIllegal(`"a\nb\tc\\d\"e\u{1F600}"`) {
		t.Error("valid escapes should not produce ILLEGAL")
	}
	// U+10FFFF is the highest valid scalar value.
	if lexHasIllegal(`"\u{10FFFF}"`) {
		t.Error(`\u{10FFFF} should be valid`)
	}
}

func TestString_SurrogateEscapeIsIllegal(t *testing.T) {
	// \u{D800}..\u{DFFF} are surrogate code points, not Unicode scalar values;
	// reject them rather than silently coercing to U+FFFD (as Rust does).
	if !lexHasIllegal(`"\u{D800}"`) {
		t.Error(`expected ILLEGAL for surrogate escape \u{D800}`)
	}
	if !lexHasIllegal(`"\u{DFFF}"`) {
		t.Error(`expected ILLEGAL for surrogate escape \u{DFFF}`)
	}
}

// A \u{...} escape takes 1-6 hex digits naming a scalar value in every string
// form that processes escapes (plain, interpolated, typed), with the problem
// codepoint literals report, since both decode it with strlit.
func TestString_UnicodeEscapeTakesOneToSixHexDigits(t *testing.T) {
	digits := `invalid \u{...} code point: %s; a \u{...} escape takes 1 to 6 hex digits`
	cases := []struct{ escape, problem string }{
		{`\u{}`, fmt.Sprintf(digits, "")},
		{`\u{0000041}`, fmt.Sprintf(digits, "0000041")},
		{`\u{1234567}`, fmt.Sprintf(digits, "1234567")},
		{`\u{zz}`, fmt.Sprintf(digits, "zz")},
		{`\u{110000}`, `invalid \u{...} code point: 110000; the largest code point is 10FFFF`},
		{`\u{D800}`, `invalid \u{...} code point: D800; D800 to DFFF are surrogates, not Unicode scalar values`},
		{`\u{DFFF}`, `invalid \u{...} code point: DFFF; D800 to DFFF are surrogates, not Unicode scalar values`},
	}
	forms := []struct{ name, open, close string }{
		{"plain", `"a`, `b"`},
		{"interpolated", `"${x}a`, `b"`},
		{"interpolated tail", `"a${x}`, `b"`},
		{"typed", `Sql"a`, `b"`},
		{"typed interpolated", `Sql"${x}a`, `b"`},
	}
	for _, f := range forms {
		for _, c := range cases {
			src := f.open + c.escape + f.close
			var got *token.Token
			toks := Lex(src)
			for i := range toks {
				if toks[i].Type == token.ILLEGAL {
					got = &toks[i]
					break
				}
			}
			if got == nil {
				t.Errorf("%s: Lex(%q) has no ILLEGAL token", f.name, src)
				continue
			}
			if got.Problem != c.problem {
				t.Errorf("%s: Lex(%q) problem:\n got %q\nwant %q", f.name, src, got.Problem, c.problem)
			}
		}
	}
	for _, ok := range []string{`"\u{0}"`, `"\u{41}"`, `"\u{000041}"`, `"\u{10FFFF}"`, `"${x}\u{1F600}"`, `Sql"\u{E9}"`} {
		if lexHasIllegal(ok) {
			t.Errorf("Lex(%q) has an ILLEGAL token; 1 to 6 hex digits are valid", ok)
		}
	}
	// Triple-quoted and raw strings process no escapes, so the text stays.
	for _, raw := range []string{"\"\"\"\n  \\u{0000041}\n  \"\"\"", "`\\u{0000041}`"} {
		if lexHasIllegal(raw) {
			t.Errorf("Lex(%q) has an ILLEGAL token; this form processes no escapes", raw)
		}
	}
}

func TestBareTildeIsIllegal(t *testing.T) {
	// `~` is not a Nomi token.
	tokens := Lex(`~`)
	if len(tokens) < 1 {
		t.Fatalf("expected at least one token")
	}
	if tokens[0].Type != token.ILLEGAL {
		t.Errorf("expected ILLEGAL for bare `~`, got %s", tokens[0].Type)
	}
	if tokens[0].Lexeme != "~" {
		t.Errorf("expected `~` lexeme, got %q", tokens[0].Lexeme)
	}
}

// --- Keywords ---

func TestKeywords(t *testing.T) {
	assertTokens(t, "true", []token.Token{
		tok(token.IDENT, "true"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "false", []token.Token{
		tok(token.IDENT, "false"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "if", []token.Token{
		tok(token.IF, "if"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "else", []token.Token{
		tok(token.ELSE, "else"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "and", []token.Token{
		tok(token.AND, "and"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "or", []token.Token{
		tok(token.OR, "or"),
		tok(token.EOF, ""),
	})
}

func TestNewKeywords(t *testing.T) {
	assertTokens(t, "fn", []token.Token{
		tok(token.FN, "fn"),
		tok(token.EOF, ""),
	})
	// "Fn" is no longer a keyword — it lexes as TYPE_IDENT
	assertTokens(t, "Fn", []token.Token{
		tok(token.TYPE_IDENT, "Fn"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "return", []token.Token{
		tok(token.RETURN, "return"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "case", []token.Token{
		tok(token.CASE, "case"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "when", []token.Token{
		tok(token.WHEN, "when"),
		tok(token.EOF, ""),
	})
}

func TestUnderscore(t *testing.T) {
	assertTokens(t, "_", []token.Token{
		tok(token.UNDERSCORE, "_"),
		tok(token.EOF, ""),
	})
	// _foo should still be IDENT
	assertTokens(t, "_foo", []token.Token{
		tok(token.IDENT, "_foo"),
		tok(token.EOF, ""),
	})
}

func TestFuncDeclaration(t *testing.T) {
	assertTokens(t, "fn add(x) { x }", []token.Token{
		tok(token.FN, "fn"),
		tok(token.IDENT, "add"),
		tok(token.LPAREN, "("),
		tok(token.IDENT, "x"),
		tok(token.RPAREN, ")"),
		tok(token.LBRACE, "{"),
		tok(token.IDENT, "x"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

// --- Identifiers ---

func TestIdentifiers(t *testing.T) {
	assertTokens(t, "foo", []token.Token{
		tok(token.IDENT, "foo"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "snake_case", []token.Token{
		tok(token.IDENT, "snake_case"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "_private", []token.Token{
		tok(token.IDENT, "_private"),
		tok(token.EOF, ""),
	})
}

func TestTypeIdentifiers(t *testing.T) {
	assertTokens(t, "Foo", []token.Token{
		tok(token.TYPE_IDENT, "Foo"),
		tok(token.EOF, ""),
	})
}

func TestLexTypeKeyword(t *testing.T) {
	tokens := Lex("type Id Int")
	if len(tokens) < 3 {
		t.Fatalf("expected at least 3 tokens, got %d", len(tokens))
	}
	if tokens[0].Type != token.TYPE {
		t.Errorf("expected TYPE token, got %s %q", tokens[0].Type, tokens[0].Lexeme)
	}
	if tokens[0].Lexeme != "type" {
		t.Errorf("expected lexeme 'type', got %q", tokens[0].Lexeme)
	}
}

func TestLexTypeKeywordCapitalizedIsIdent(t *testing.T) {
	tokens := Lex("Type Id Int")
	if len(tokens) < 3 {
		t.Fatalf("expected at least 3 tokens, got %d", len(tokens))
	}
	// "Type" is no longer a keyword — it lexes as a TYPE_IDENT
	if tokens[0].Type != token.TYPE_IDENT {
		t.Errorf("expected TYPE_IDENT token, got %s %q", tokens[0].Type, tokens[0].Lexeme)
	}
	if tokens[0].Lexeme != "Type" {
		t.Errorf("expected lexeme 'Type', got %q", tokens[0].Lexeme)
	}
}

// --- Whitespace and comments ---

func TestWhitespaceSkipped(t *testing.T) {
	assertTokens(t, "a   b", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestLineComment(t *testing.T) {
	assertTokens(t, "a // comment", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.COMMENT, "// comment"),
		tok(token.EOF, ""),
	})
}

func TestLineCommentFollowedByNewline(t *testing.T) {
	assertTokens(t, "a // comment\nb", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.COMMENT, "// comment"),
		tok(token.NEWLINE, "\n"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

// --- Newlines ---

func TestNewlineToken(t *testing.T) {
	assertTokens(t, "a\nb", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.NEWLINE, "\n"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestConsecutiveNewlinesCollapse(t *testing.T) {
	// A run of 2+ newlines collapses to NEWLINE + a single BLANK_LINE
	assertTokens(t, "a\n\n\nb", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.NEWLINE, "\n"),
		tok(token.BLANK_LINE, ""),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestBlankLineAfterGenericType(t *testing.T) {
	assertTokens(t, "typealias A List<User>\n\ntypealias B List<User>", []token.Token{
		tok(token.TYPEALIAS, "typealias"),
		tok(token.TYPE_IDENT, "A"),
		tok(token.TYPE_IDENT, "List"),
		tok(token.LT, "<"),
		tok(token.TYPE_IDENT, "User"),
		tok(token.GT, ">"),
		tok(token.NEWLINE, "\n"),
		tok(token.BLANK_LINE, ""),
		tok(token.TYPEALIAS, "typealias"),
		tok(token.TYPE_IDENT, "B"),
		tok(token.TYPE_IDENT, "List"),
		tok(token.LT, "<"),
		tok(token.TYPE_IDENT, "User"),
		tok(token.GT, ">"),
		tok(token.EOF, ""),
	})
}

func TestLeadingNewlinesSkipped(t *testing.T) {
	// Leading newlines before any real token should not emit NEWLINE
	assertTokens(t, "\n\na", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedAfterOperator(t *testing.T) {
	// After +, newline should be suppressed (Go-style)
	assertTokens(t, "a +\nb", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.PLUS, "+"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedAfterComma(t *testing.T) {
	assertTokens(t, "a,\nb", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.COMMA, ","),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedAfterEq(t *testing.T) {
	assertTokens(t, "x =\n42", []token.Token{
		tok(token.IDENT, "x"),
		tok(token.EQ, "="),
		tok(token.INT, "42"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedAfterLParen(t *testing.T) {
	assertTokens(t, "f(\na)", []token.Token{
		tok(token.IDENT, "f"),
		tok(token.LPAREN, "("),
		tok(token.IDENT, "a"),
		tok(token.RPAREN, ")"),
		tok(token.EOF, ""),
	})
}

func TestNewlineEmittedAfterRParen(t *testing.T) {
	assertTokens(t, "f()\nb", []token.Token{
		tok(token.IDENT, "f"),
		tok(token.LPAREN, "("),
		tok(token.RPAREN, ")"),
		tok(token.NEWLINE, "\n"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestNewlineEmittedAfterRBracket(t *testing.T) {
	assertTokens(t, "[1]\nb", []token.Token{
		tok(token.LBRACKET, "["),
		tok(token.INT, "1"),
		tok(token.RBRACKET, "]"),
		tok(token.NEWLINE, "\n"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedBeforeRParen(t *testing.T) {
	assertTokens(t, "f(\na\n)", []token.Token{
		tok(token.IDENT, "f"),
		tok(token.LPAREN, "("),
		tok(token.IDENT, "a"),
		tok(token.RPAREN, ")"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedBeforeRBracket(t *testing.T) {
	assertTokens(t, "[1,\n2\n]", []token.Token{
		tok(token.LBRACKET, "["),
		tok(token.INT, "1"),
		tok(token.COMMA, ","),
		tok(token.INT, "2"),
		tok(token.RBRACKET, "]"),
		tok(token.EOF, ""),
	})
}

func TestNewlineNotSuppressedBeforeRBrace(t *testing.T) {
	assertTokens(t, "a\n}", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.NEWLINE, "\n"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

func TestNewlineSuppressedBeforePipe(t *testing.T) {
	// |> at start of next line should suppress the NEWLINE
	assertTokens(t, "a\n|> b", []token.Token{
		tok(token.IDENT, "a"),
		tok(token.PIPE, "|>"),
		tok(token.IDENT, "b"),
		tok(token.EOF, ""),
	})
}

func TestAttachedTestPromptLexesBodyAsCode(t *testing.T) {
	assertTokens(t, "//! assert answer() == 42", []token.Token{
		tok(token.TEST_PROMPT, "//!"),
		tok(token.ASSERT, "assert"),
		tok(token.IDENT, "answer"),
		tok(token.LPAREN, "("),
		tok(token.RPAREN, ")"),
		tok(token.EQEQ, "=="),
		tok(token.INT, "42"),
		tok(token.EOF, ""),
	})
}

func TestAttachedTestPromptStripsFromTripleStringBodyLines(t *testing.T) {
	tokens := Lex(`//! assert Toml.text(
//!   Toml"""
//!   [module]
//!   name = "demo"
//!   """
//! ) == "ok"`)

	for _, tok := range tokens {
		if tok.Type == token.TAGGED_TRIPLE_STRING_LITERAL {
			if tok.Tag != "Toml" {
				t.Fatalf("tag = %q, want Toml", tok.Tag)
			}
			if tok.Lexeme != "[module]\nname = \"demo\"" {
				t.Fatalf("triple string body = %q", tok.Lexeme)
			}
			return
		}
	}
	t.Fatalf("expected tagged triple string token, got %v", tokens)
}

func TestNewlineSuppressedAfterArrow(t *testing.T) {
	assertTokens(t, "->\na", []token.Token{
		tok(token.ARROW, "->"),
		tok(token.IDENT, "a"),
		tok(token.EOF, ""),
	})
}

// --- Position tracking ---

func TestPositionTracking(t *testing.T) {
	tokens := Lex("a b")
	if tokens[0].Line != 1 || tokens[0].Col != 1 {
		t.Errorf("expected a at 1:1, got %d:%d", tokens[0].Line, tokens[0].Col)
	}
	if tokens[1].Line != 1 || tokens[1].Col != 3 {
		t.Errorf("expected b at 1:3, got %d:%d", tokens[1].Line, tokens[1].Col)
	}
}

func TestPositionMultiline(t *testing.T) {
	tokens := Lex("a\nb")
	// a at 1:1, NEWLINE at 1:2, b at 2:1
	if tokens[2].Line != 2 || tokens[2].Col != 1 {
		t.Errorf("expected b at 2:1, got %d:%d", tokens[2].Line, tokens[2].Col)
	}
}

// --- Mixed expressions ---

func TestMixedExpression(t *testing.T) {
	assertTokens(t, "x = 42 + y", []token.Token{
		tok(token.IDENT, "x"),
		tok(token.EQ, "="),
		tok(token.INT, "42"),
		tok(token.PLUS, "+"),
		tok(token.IDENT, "y"),
		tok(token.EOF, ""),
	})
}

func TestPipeExpression(t *testing.T) {
	assertTokens(t, "x |> f", []token.Token{
		tok(token.IDENT, "x"),
		tok(token.PIPE, "|>"),
		tok(token.IDENT, "f"),
		tok(token.EOF, ""),
	})
}

func TestArrowExpression(t *testing.T) {
	assertTokens(t, "(x) -> x", []token.Token{
		tok(token.LPAREN, "("),
		tok(token.IDENT, "x"),
		tok(token.RPAREN, ")"),
		tok(token.ARROW, "->"),
		tok(token.IDENT, "x"),
		tok(token.EOF, ""),
	})
}

// --- Edge cases ---

func TestEmptyInput(t *testing.T) {
	assertTokens(t, "", []token.Token{
		tok(token.EOF, ""),
	})
}

func TestIllegalCharacter(t *testing.T) {
	tokens := Lex("~")
	if tokens[0].Type != token.ILLEGAL {
		t.Errorf("expected ILLEGAL for ~, got %s", tokens[0].Type)
	}
}

func TestEmptyString(t *testing.T) {
	assertTokens(t, `""`, []token.Token{
		tok(token.STRING_LITERAL, ""),
		tok(token.EOF, ""),
	})
}

// --- IsComplete ---

func TestIsCompleteSimpleExpr(t *testing.T) {
	if !IsComplete("x + 1") {
		t.Error("expected complete")
	}
}

func TestIsCompleteEmpty(t *testing.T) {
	if !IsComplete("") {
		t.Error("expected complete for empty input")
	}
}

func TestIsCompleteUnclosedParen(t *testing.T) {
	if IsComplete("f(a, b") {
		t.Error("expected incomplete for unclosed paren")
	}
}

func TestIsCompleteUnclosedBrace(t *testing.T) {
	if IsComplete("if x > 0 {") {
		t.Error("expected incomplete for unclosed brace")
	}
}

func TestIsCompleteUnclosedBracket(t *testing.T) {
	if IsComplete("[1, 2,") {
		t.Error("expected incomplete for unclosed bracket")
	}
}

func TestIsCompleteTrailingOperator(t *testing.T) {
	if IsComplete("x +") {
		t.Error("expected incomplete for trailing operator")
	}
}

func TestIsCompleteTrailingPipe(t *testing.T) {
	if IsComplete("[1, 2, 3] |>") {
		t.Error("expected incomplete for trailing pipe")
	}
}

func TestIsCompleteTrailingEq(t *testing.T) {
	if IsComplete("x =") {
		t.Error("expected incomplete for trailing eq")
	}
}

func TestIsCompleteTrailingComma(t *testing.T) {
	if IsComplete("f(a,") {
		t.Error("expected incomplete for trailing comma")
	}
}

func TestIsCompleteBalancedBraces(t *testing.T) {
	if !IsComplete("if x > 0 { 1 } else { 2 }") {
		t.Error("expected complete for balanced braces")
	}
}

func TestLexBareQuestionMarkIsIllegal(t *testing.T) {
	// `?` glued to an identifier tail is part of that identifier (predicate
	// convention — see TestLexPredicateIdentifier). A `?` NOT preceded by an
	// identifier (bare, or after whitespace) is still not a valid token — it
	// lexes to ILLEGAL.
	tokens := Lex("?")
	if tokens[0].Type != token.ILLEGAL || tokens[0].Lexeme != "?" {
		t.Errorf("expected ILLEGAL `?` token for bare `?`, got %s %q", tokens[0].Type, tokens[0].Lexeme)
	}

	// `x ?` (whitespace separated) — `x` is an IDENT, then a stray ILLEGAL `?`.
	spaced := Lex("x ?")
	if spaced[0].Type != token.IDENT || spaced[0].Lexeme != "x" {
		t.Errorf("expected IDENT `x`, got %s %q", spaced[0].Type, spaced[0].Lexeme)
	}
	if spaced[1].Type != token.ILLEGAL || spaced[1].Lexeme != "?" {
		t.Errorf("expected ILLEGAL `?` after whitespace, got %s %q", spaced[1].Type, spaced[1].Lexeme)
	}
}

func TestLexPredicateIdentifier(t *testing.T) {
	// A single trailing `?` glued to an identifier tail is part of the
	// identifier (predicate naming, e.g. `empty?`).
	for _, src := range []string{"empty?", "starts_with?"} {
		tokens := Lex(src)
		if tokens[0].Type != token.IDENT || tokens[0].Lexeme != src {
			t.Errorf("Lex(%q): expected IDENT %q, got %s %q", src, src, tokens[0].Type, tokens[0].Lexeme)
		}
	}

	// `contains?(x)` — the `?` belongs to the ident, then the call paren.
	assertTokens(t, "contains?(x)", []token.Token{
		tok(token.IDENT, "contains?"),
		tok(token.LPAREN, "("),
		tok(token.IDENT, "x"),
		tok(token.RPAREN, ")"),
		tok(token.EOF, ""),
	})

	// Exactly one, trailing only: `foo?bar` is two adjacent idents `foo?` and
	// `bar` (the `?` does not glue them); `foo??` is `foo?` then a stray `?`.
	assertTokens(t, "foo?bar", []token.Token{
		tok(token.IDENT, "foo?"),
		tok(token.IDENT, "bar"),
		tok(token.EOF, ""),
	})
	dbl := Lex("foo??")
	if dbl[0].Type != token.IDENT || dbl[0].Lexeme != "foo?" {
		t.Errorf("expected IDENT `foo?`, got %s %q", dbl[0].Type, dbl[0].Lexeme)
	}
	if dbl[1].Type != token.ILLEGAL || dbl[1].Lexeme != "?" {
		t.Errorf("expected trailing ILLEGAL `?` for `foo??`, got %s %q", dbl[1].Type, dbl[1].Lexeme)
	}
}

func TestLexTryKeyword(t *testing.T) {
	// Bare `try` is the keyword; `try_send` is a single identifier (longest match).
	tokens := Lex("try")
	if tokens[0].Type != token.TRY {
		t.Fatalf("expected TRY keyword for bare `try`, got %s", tokens[0].Type)
	}
	idents := Lex("try_send")
	if idents[0].Type != token.IDENT || idents[0].Lexeme != "try_send" {
		t.Errorf("expected IDENT `try_send`, got %s %q", idents[0].Type, idents[0].Lexeme)
	}
}

func TestFatArrow(t *testing.T) {
	assertTokens(t, `"a" => 1`, []token.Token{
		tok(token.STRING_LITERAL, "a"),
		tok(token.FAT_ARROW, "=>"),
		tok(token.INT, "1"),
		tok(token.EOF, ""),
	})
}

func TestInterfaceKeyword(t *testing.T) {
	assertTokens(t, "interface Foo", []token.Token{
		tok(token.INTERFACE, "interface"),
		tok(token.TYPE_IDENT, "Foo"),
		tok(token.EOF, ""),
	})
}

func TestImplKeyword(t *testing.T) {
	assertTokens(t, "type Foo impl Bar", []token.Token{
		tok(token.TYPE, "type"),
		tok(token.TYPE_IDENT, "Foo"),
		tok(token.IMPL, "impl"),
		tok(token.TYPE_IDENT, "Bar"),
		tok(token.EOF, ""),
	})
}

func TestImportKeyword(t *testing.T) {
	assertTokens(t, "import Http", []token.Token{
		tok(token.IMPORT, "import"),
		tok(token.TYPE_IDENT, "Http"),
		tok(token.EOF, ""),
	})
}

func TestImportDottedPath(t *testing.T) {
	assertTokens(t, "import Http.Request", []token.Token{
		tok(token.IMPORT, "import"),
		tok(token.TYPE_IDENT, "Http"),
		tok(token.DOT, "."),
		tok(token.TYPE_IDENT, "Request"),
		tok(token.EOF, ""),
	})
}

func TestImportSelective(t *testing.T) {
	assertTokens(t, "import Http.{Request, Response}", []token.Token{
		tok(token.IMPORT, "import"),
		tok(token.TYPE_IDENT, "Http"),
		tok(token.DOT, "."),
		tok(token.LBRACE, "{"),
		tok(token.TYPE_IDENT, "Request"),
		tok(token.COMMA, ","),
		tok(token.TYPE_IDENT, "Response"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

// --- Unicode escape sequences ---

func TestUnicodeEscape(t *testing.T) {
	tokens := Lex(`"\u{0041}"`)
	if len(tokens) < 1 || tokens[0].Lexeme != "A" {
		t.Errorf("expected 'A', got %q (tokens: %v)", tokens[0].Lexeme, tokens)
	}
}

func TestUnicodeEscapeEmoji(t *testing.T) {
	tokens := Lex(`"\u{1F600}"`)
	if len(tokens) < 1 || tokens[0].Lexeme != "😀" {
		t.Errorf("expected emoji, got %q", tokens[0].Lexeme)
	}
}

func TestUnicodeEscapeCafe(t *testing.T) {
	tokens := Lex(`"\u{00E9}"`)
	if len(tokens) < 1 || tokens[0].Lexeme != "é" {
		t.Errorf("expected é, got %q", tokens[0].Lexeme)
	}
}

func TestUnicodeEscapeInString(t *testing.T) {
	tokens := Lex(`"caf\u{00E9}"`)
	if len(tokens) < 1 || tokens[0].Lexeme != "café" {
		t.Errorf("expected café, got %q", tokens[0].Lexeme)
	}
}

func TestLexTypealiasKeyword(t *testing.T) {
	tokens := Lex("typealias Name String")
	if len(tokens) < 4 {
		t.Fatalf("expected at least 4 tokens, got %d", len(tokens))
	}
	if tokens[0].Type != token.TYPEALIAS {
		t.Errorf("expected TYPEALIAS token, got %s %q", tokens[0].Type, tokens[0].Lexeme)
	}
}

func TestIsCompleteMultilineAccumulated(t *testing.T) {
	if !IsComplete("[1, 2, 3] |>\n  Iter.map(|x| x * 2)") {
		t.Error("expected complete for accumulated multiline pipe")
	}
}

func TestHostKeyword(t *testing.T) {
	assertTokens(t, "host fn", []token.Token{
		tok(token.IDENT, "host"),
		tok(token.FN, "fn"),
		tok(token.EOF, ""),
	})
}

func TestDocComment(t *testing.T) {
	tokens := Lex("/// This is a doc comment\nfn Foo() {}")
	if tokens[0].Type != token.DOC_COMMENT {
		t.Errorf("expected DOC_COMMENT, got %s", tokens[0].Type)
	}
	if tokens[0].Lexeme != " This is a doc comment" {
		t.Errorf("expected doc text, got %q", tokens[0].Lexeme)
	}
}

func TestDocCommentMultiLine(t *testing.T) {
	tokens := Lex("/// Line one\n/// Line two\nfn Foo() {}")
	if tokens[0].Type != token.DOC_COMMENT {
		t.Errorf("expected DOC_COMMENT, got %s", tokens[0].Type)
	}
	if tokens[1].Type != token.DOC_COMMENT {
		t.Errorf("expected DOC_COMMENT, got %s", tokens[1].Type)
	}
}

func TestRegularCommentNotDocComment(t *testing.T) {
	tokens := Lex("// regular comment\nfn Foo() {}")
	// Regular comments emit COMMENT tokens (distinct from DOC_COMMENT)
	if tokens[0].Type != token.COMMENT {
		t.Errorf("expected COMMENT, got %s", tokens[0].Type)
	}
	if tokens[0].Lexeme != "// regular comment" {
		t.Errorf("expected lexeme %q, got %q", "// regular comment", tokens[0].Lexeme)
	}
}

func TestExportKeyword(t *testing.T) {
	assertTokens(t, "export { foo }", []token.Token{
		tok(token.EXPORT, "export"),
		tok(token.LBRACE, "{"),
		tok(token.IDENT, "foo"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

func TestPubKeyword(t *testing.T) {
	assertTokens(t, "pub fn foo", []token.Token{
		tok(token.PUB, "pub"),
		tok(token.FN, "fn"),
		tok(token.IDENT, "foo"),
		tok(token.EOF, ""),
	})
}

func TestOnceKeyword(t *testing.T) {
	assertTokens(t, "once x: Int = 0", []token.Token{
		tok(token.ONCE, "once"),
		tok(token.IDENT, "x"),
		tok(token.COLON, ":"),
		tok(token.TYPE_IDENT, "Int"),
		tok(token.EQ, "="),
		tok(token.INT, "0"),
		tok(token.EOF, ""),
	})
}

func TestReadsIsPlainIdentifier(t *testing.T) {
	// `reads` is not a keyword. Lexes as IDENT.
	assertTokens(t, "reads {config}", []token.Token{
		tok(token.IDENT, "reads"),
		tok(token.LBRACE, "{"),
		tok(token.IDENT, "config"),
		tok(token.RBRACE, "}"),
		tok(token.EOF, ""),
	})
}

func TestFourSlashesIsRegularComment(t *testing.T) {
	tokens := Lex("//// four slashes\nfn Foo() {}")
	// Four slashes is a regular comment, not a doc comment
	if tokens[0].Type != token.COMMENT {
		t.Errorf("expected COMMENT (four-slash comment), got %s", tokens[0].Type)
	}
}

func TestLex_EmitsBlankLine(t *testing.T) {
	toks := Lex("x = 1\n\n\ny = 2\n")
	var kinds []token.TokenType
	for _, t := range toks {
		kinds = append(kinds, t.Type)
	}
	// Expect exactly one BLANK_LINE between the two statements
	count := 0
	for _, k := range kinds {
		if k == token.BLANK_LINE {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want 1 BLANK_LINE, got %d in %v", count, kinds)
	}
}

func TestLex_SingleNewlineNoBlankLine(t *testing.T) {
	toks := Lex("x = 1\ny = 2\n")
	for _, tk := range toks {
		if tk.Type == token.BLANK_LINE {
			t.Fatalf("unexpected BLANK_LINE in %v", toks)
		}
	}
}

func TestLex_PreservesLineComment(t *testing.T) {
	toks := Lex("x = 1 // a note\n")
	var kinds []token.TokenType
	for _, t := range toks {
		kinds = append(kinds, t.Type)
	}
	// Expect: IDENT EQ INT COMMENT NEWLINE EOF
	want := []token.TokenType{
		token.IDENT, token.EQ, token.INT, token.COMMENT, token.NEWLINE, token.EOF,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %v, want %v", kinds, want)
	}

	// Comment lexeme should include the `//` and text, without trailing newline
	var c token.Token
	for _, t := range toks {
		if t.Type == token.COMMENT {
			c = t
			break
		}
	}
	if c.Lexeme != "// a note" {
		t.Fatalf("lexeme = %q, want %q", c.Lexeme, "// a note")
	}
}

func TestLex_TrailingCommentDoesNotBreakPipeContinuation(t *testing.T) {
	src := "x // note\n  |> f\n"
	toks := Lex(src)
	// Expect no NEWLINE between IDENT/COMMENT and PIPE — the comment-then-pipe
	// should still suppress the newline like a bare pipe continuation would.
	for i, tk := range toks {
		if tk.Type == token.NEWLINE && i+1 < len(toks) {
			next := toks[i+1]
			if next.Type == token.PIPE {
				t.Fatalf("NEWLINE appeared before PIPE; continuation broken: %v", toks)
			}
		}
	}
}

func TestLex_RangeOperators(t *testing.T) {
	// `..` is the half-open range operator and `..=` is the inclusive form.
	// They must lex as single tokens, distinct from a plain DOT or a DOT
	// followed by an EQ. A trailing `=` only joins when paired with `..`.
	assertTokens(t, "..", []token.Token{
		tok(token.DOTDOT, ".."),
		tok(token.EOF, ""),
	})
	assertTokens(t, "..=", []token.Token{
		tok(token.DOTDOTEQ, "..="),
		tok(token.EOF, ""),
	})
	assertTokens(t, "1..5", []token.Token{
		tok(token.INT, "1"),
		tok(token.DOTDOT, ".."),
		tok(token.INT, "5"),
		tok(token.EOF, ""),
	})
	assertTokens(t, "1..=5", []token.Token{
		tok(token.INT, "1"),
		tok(token.DOTDOTEQ, "..="),
		tok(token.INT, "5"),
		tok(token.EOF, ""),
	})
	// A bare `.` followed by `=` stays as two tokens.
	assertTokens(t, ".=", []token.Token{
		tok(token.DOT, "."),
		tok(token.EQ, "="),
		tok(token.EOF, ""),
	})
}

// --- Typed literals ---

func TestTaggedSingleLine(t *testing.T) {
	// Sql"SELECT 1" → TAGGED_STRING_LITERAL with Tag="Sql".
	assertTokens(t, `Sql"SELECT 1"`, []token.Token{
		tokTagged(token.TAGGED_STRING_LITERAL, "Sql", "SELECT 1"),
		tok(token.EOF, ""),
	})
}

func TestTaggedSingleLineWithInterp(t *testing.T) {
	// Sql"id = ${id}" → START(Tag="Sql","id = "), IDENT("id"), END("")
	assertTokens(t, `Sql"id = ${id}"`, []token.Token{
		tokTagged(token.TAGGED_STRING_START, "Sql", "id = "),
		tok(token.IDENT, "id"),
		tok(token.STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestTaggedTriple(t *testing.T) {
	// Sql"""\n    SELECT 1\n    """ → TAGGED_TRIPLE_STRING_LITERAL.
	src := "Sql\"\"\"\n    SELECT 1\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tokTagged(token.TAGGED_TRIPLE_STRING_LITERAL, "Sql", "SELECT 1"),
		tok(token.EOF, ""),
	})
}

func TestTaggedTripleWithInterp(t *testing.T) {
	// Sql"""\n    SELECT * WHERE id = ${id}\n    """
	src := "Sql\"\"\"\n    SELECT * WHERE id = ${id}\n    \"\"\""
	assertTokens(t, src, []token.Token{
		tokTagged(token.TAGGED_TRIPLE_STRING_START, "Sql", "SELECT * WHERE id = "),
		tok(token.IDENT, "id"),
		tok(token.TRIPLE_STRING_END, ""),
		tok(token.EOF, ""),
	})
}

func TestRawTaggedSingleLine(t *testing.T) {
	// Regex`\d+` → RAW_TAGGED_STRING_LITERAL with Tag="Regex". The
	// `\d` survives verbatim (raw means no escape processing).
	assertTokens(t, "Regex`\\d+`", []token.Token{
		tokTagged(token.RAW_TAGGED_STRING_LITERAL, "Regex", `\d+`),
		tok(token.EOF, ""),
	})
}

func TestRawTaggedTriple(t *testing.T) {
	// Bash`\n    echo ${HOME}\n    ` → RAW_TAGGED_TRIPLE_STRING_LITERAL
	// with Tag="Bash". `${HOME}` is verbatim text (raw strings never
	// interpolates).
	src := "Bash`\n    echo ${HOME}\n    `"
	assertTokens(t, src, []token.Token{
		tokTagged(token.RAW_TAGGED_TRIPLE_STRING_LITERAL, "Bash", "echo ${HOME}"),
		tok(token.EOF, ""),
	})
}

func TestSpaceBetweenTagAndQuoteIsNotTagged(t *testing.T) {
	// `Sql "x"` (with a space) is NOT a typed literal — the TYPE_IDENT
	// and STRING_LITERAL are separate tokens. This pins down the
	// "immediately adjacent" rule.
	assertTokens(t, `Sql "x"`, []token.Token{
		tok(token.TYPE_IDENT, "Sql"),
		tok(token.STRING_LITERAL, "x"),
		tok(token.EOF, ""),
	})
}

func TestSnakeIdentAdjacentToStringIsNotTagged(t *testing.T) {
	// `sql"x"` — a snake_case identifier adjacent to a string opener is
	// NOT a typed literal (tags are PascalCase type names). It lexes as
	// an ordinary IDENT followed by a separate STRING_LITERAL, which the
	// parser then rejects as two adjacent primaries.
	assertTokens(t, `sql"x"`, []token.Token{
		tok(token.IDENT, "sql"),
		tok(token.STRING_LITERAL, "x"),
		tok(token.EOF, ""),
	})
}

func TestKeywordAdjacentToStringIsNotTagged(t *testing.T) {
	// `if"x"` — `if` is a keyword, not a valid tag name. The lexer
	// emits the keyword and the string as separate tokens (no tagged
	// kind). Whether the parser then accepts the resulting sequence is
	// a separate concern; the lexer's job is to refuse to coerce
	// keywords into tag names.
	assertTokens(t, `if"x"`, []token.Token{
		tok(token.IF, "if"),
		tok(token.STRING_LITERAL, "x"),
		tok(token.EOF, ""),
	})
}

func TestTypeIdentAdjacentToStringIsTagged(t *testing.T) {
	// `SQL"x"` — a PascalCase identifier immediately adjacent to a string
	// opener is a TAGGED_STRING_LITERAL with Tag="SQL". Typed literals are
	// PascalCase-only at the lexer level; the analyzer's
	// resolveTaggedStringTag requires the tag to name a type with an
	// `impl Literal for <Tag>` block.
	tokens := Lex(`SQL"x"`)
	if len(tokens) < 1 || tokens[0].Type != token.TAGGED_STRING_LITERAL {
		t.Fatalf("expected TAGGED_STRING_LITERAL for PascalCase tag, got %v", tokens)
	}
	if tokens[0].Tag != "SQL" || tokens[0].Lexeme != "x" {
		t.Errorf("expected Tag=SQL Lexeme=x, got %+v", tokens[0])
	}
}

func TestTaggedTripleNoInterpInRawForm(t *testing.T) {
	// Inside a raw-tagged triple, `${x}` and `\${` are verbatim. Mirrors
	// the untagged multi-line raw rule.
	src := "Bash`\n    cost: \\${price}\n    `"
	assertTokens(t, src, []token.Token{
		tokTagged(token.RAW_TAGGED_TRIPLE_STRING_LITERAL, "Bash", "cost: \\${price}"),
		tok(token.EOF, ""),
	})
}
