package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A file in the brace-bodied syntax fails at the `{` that ends its first
// header line, with the error that says a body is closed by `end`, and a fix
// that removes the brace, whatever later error the brace would have caused.
func TestBraceBody_IsTheFirstError(t *testing.T) {
	cases := map[string]struct {
		src       string
		line, col int
		fixed     string // the header line after the fix
	}{
		"a function":   {"fn f() -> Int {\n    1\n}\n", 1, 15, "fn f() -> Int"},
		"an if":        {"fn f(c: Bool) -> Int\n    if c {\n        1\n    } else {\n        2\n    }\nend\n", 2, 10, "    if c"},
		"a case":       {"fn f(x: Int) -> Int\n    case x {\n        1 -> 2\n        _ -> 3\n    }\nend\n", 2, 12, "    case x"},
		"a struct":     {"struct P {\n    x: Int\n}\n", 1, 10, "struct P"},
		"an import":    {"import {\n    std::json\n}\n\nfn main()\nend\n", 1, 8, "import"},
		"a test":       {"test \"t\" {\n    assert True\n}\n", 1, 10, "test \"t\""},
		"a later body": {"fn g() -> Int\n    1\nend\n\nfn f() -> Int {\n    1\n}\n", 5, 15, "fn f() -> Int"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(c.src))
			pe, ok := err.(ParseError)
			if !ok || pe.Message != braceBodyMessage || pe.Line != c.line || pe.Col != c.col {
				t.Fatalf("Parse error = %#v, want %q at %d:%d", err, braceBodyMessage, c.line, c.col)
			}
			if pe.Fix == nil || len(pe.Fix.Edits) != 1 {
				t.Fatalf("no fix: %#v", pe)
			}
			e := pe.Fix.Edits[0]
			line := splitLines(c.src)[c.line-1]
			got := line[:e.Start.Col-1] + e.Text + line[e.End.Col-1:]
			if got != c.fixed {
				t.Errorf("fixed header = %q, want %q", got, c.fixed)
			}
			_, errs := ParseAll(lexer.Lex(c.src))
			if len(errs) == 0 || errs[0].Message != braceBodyMessage {
				t.Errorf("ParseAll's first error = %v, want the brace-body error", errs)
			}
			_, errs = ParseWithRecovery(lexer.Lex(c.src))
			if len(errs) == 0 || errs[0].Message != braceBodyMessage {
				t.Errorf("ParseWithRecovery's first error = %v, want the brace-body error", errs)
			}
		})
	}
}

// A `{` that ends a line and opens data is not a body: a file that parses
// never reports it, and one whose error is elsewhere keeps that error.
func TestBraceBody_DataBraceIsNotABody(t *testing.T) {
	ok := "fn f() -> {x: Int}\n    p = {\n        x: 1,\n    }\n    p\nend\n"
	if _, err := Parse(lexer.Lex(ok)); err != nil {
		t.Fatalf("Parse rejected a struct literal over lines: %v", err)
	}
	bad := "fn f() -> Int\n    p = {\n        x: 1,\n    }\n    p +\nend\n"
	_, err := Parse(lexer.Lex(bad))
	if pe, isPE := err.(ParseError); !isPE || pe.Message == braceBodyMessage {
		t.Fatalf("Parse error = %#v, want the file's own error", err)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
