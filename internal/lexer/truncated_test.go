package lexer

import (
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// A source that ends inside a string literal lexes to an ILLEGAL token
// whose Problem names what is unterminated, at the literal's opening
// quote, and never panics.
func TestTruncatedStringIsUnterminated(t *testing.T) {
	cases := []struct {
		src, problem string
		col          int
	}{
		{`"abc`, `unterminated string; close it with "`, 1},
		{`x = "abc`, `unterminated string; close it with "`, 5},
		{`"`, `unterminated string; close it with "`, 1},
		{`"\`, `unterminated string; close it with "`, 1},
		{`x = "a\n`, `unterminated string; close it with "`, 5},
		{`"\$`, `invalid escape \$`, 1},
		{`"\u`, `\u escape requires braces, e.g. \u{1F600}`, 1},
		{`"\u{`, `unterminated \u{...} escape`, 1},
		{`"\u{1F6`, `unterminated \u{...} escape`, 1},
		{`x = "a${`, `unterminated ${...} in a string; close it with }`, 5},
		{`x = "a${1 + `, `unterminated ${...} in a string; close it with }`, 5},
		{`x = "a${f("b`, `unterminated string; close it with "`, 11},
		{`x = "a${1}b`, `unterminated string; close it with "`, 5},
		{"x = `abc", "unterminated raw string; close it with `", 5},
		{"`", "unterminated raw string; close it with `", 1},
		{"x = `\nabc\n", "unterminated raw string; close it with `", 5},
		{"x = \"\"\"\nabc\n", `unterminated triple-quoted string; close it with """`, 5},
		{`x = """`, `unterminated triple-quoted string; close it with """`, 5},
		{"x = \"\"\"\nabc\n\"\"", `unterminated triple-quoted string; close it with """`, 5},
		{"x = \"\"\"\na ${", `unterminated ${...} in a triple-quoted string; close it with }`, 5},
		{"x = \"\"\"\na ${b} \\", `unterminated triple-quoted string; close it with """`, 5},
		{`x = sql"abc`, `unterminated string; close it with "`, 8},
	}
	for _, c := range cases {
		var found *token.Token
		toks := Lex(c.src)
		for i := range toks {
			if toks[i].Type == token.ILLEGAL {
				found = &toks[i]
				break
			}
		}
		if found == nil {
			t.Errorf("Lex(%q) = %+v, want an ILLEGAL token", c.src, toks)
			continue
		}
		if found.Problem != c.problem || found.Line != 1 || found.Col != c.col {
			t.Errorf("Lex(%q): ILLEGAL at 1:%d with %q, want 1:%d with %q",
				c.src, found.Col, found.Problem, c.col, c.problem)
		}
	}
}

// Every prefix of a source holding each string form lexes without a panic:
// a scan that looks past a truncated construct sees the end of the source.
func TestEveryPrefixOfStringFormsLexes(t *testing.T) {
	src := "x = \"a\\n\\t\\\\\\\"\\u{1F600}\\${b}${c + \"d${e}\"}\"\n" +
		"y = `raw ${x} \\$`\n" +
		"z = \"\"\"\n  line \\${a} ${b}\n  \"\"\"\n" +
		"w = `\n  raw\n  `\n" +
		"v = sql\"select ${x}\"\n" +
		"u = '\\u{41}'\n"
	for i := 0; i <= len(src); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Lex(%q) panics: %v", src[:i], r)
				}
			}()
			Lex(src[:i])
		}()
	}
}
