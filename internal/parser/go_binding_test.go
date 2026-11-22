package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// Nomi source binds no Go code, so every form that would (a `gopkg` handle,
// a `go alias.Symbol` after a function's or a type's header, a `go { ... }`
// block, a `go "path"` import) is one error at its `go` or `gopkg`, with a
// hint naming what reaches code outside Nomi.
func TestParseRejectsGoBindings(t *testing.T) {
	cases := []struct {
		src       string
		line, col int
	}{
		{"gopkg \"strings\" as strs\n", 1, 1},
		{"gopkg \"strings\"\n", 1, 1},
		{"fn main()\n    gopkg \"strings\"\nend\n", 2, 5},
		{"fn upper(s: String) -> String go strs.ToUpper\n", 1, 31},
		{"pub fn upper(s: String) -> String go strs.ToUpper\n", 1, 35},
		{"export fn up(x: String) -> String go s.ToUpper\n", 1, 35},
		{"fn f() -> Int go\n  return 1\nend", 1, 15},
		{"pub fn f(x: Int) -> Int\n  go { return x }", 2, 3},
		{"impl Box\n    fn origin() -> Int go ffi.Origin\nend\n", 2, 24},
		{"opaque type RawBox go ffi.Box\n", 1, 20},
		{"pub opaque type Regex go ffi.Regex\n", 1, 23},
		{"type Conn go {\n  db *sql.DB\n}", 1, 11},
		{"go {\n  var x = 1\n}", 1, 1},
		{"import go \"net/http\"\n", 1, 8},
		{"import go http \"net/http\"\n", 1, 8},
		{"import\n  go _ \"net/http\"\nend\n", 2, 3},
	}
	for _, c := range cases {
		_, err := Parse(lexer.Lex(c.src))
		if err == nil {
			t.Errorf("Parse(%q) admits a Go binding", c.src)
			continue
		}
		var pe ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q): error %T %v is not a ParseError", c.src, err, err)
			continue
		}
		if pe.Line != c.line || pe.Col != c.col || pe.Message != GoBindingMessage || len(pe.Hints) != 1 || pe.Hints[0] != goBindingHint {
			t.Errorf("Parse(%q):\n got %d:%d %q %q\nwant %d:%d %q %q", c.src, pe.Line, pe.Col, pe.Message, pe.Hints, c.line, c.col, GoBindingMessage, goBindingHint)
		}
	}
}
