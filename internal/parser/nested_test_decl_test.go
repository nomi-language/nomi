package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `test` or `group` is a declaration of the file (a `test` also of a
// group), and a `gopkg` serves the file's top-level `go` bindings. Each one
// inside a body is refused where it starts. The strict parse reports it,
// and the resilient parse reports it once and keeps the declaration, so
// the rest of the body still parses.
func TestParseTopLevelOnlyDeclarationsInABody(t *testing.T) {
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"test in a test", "test \"outer\" {\n    test \"inner\" { assert 1 == 1 }\n    assert 1 == 1\n}\n", nestedTestMessage, 2, 5},
		{"test in a function", "fn f() -> Int {\n    test \"inner\" { assert 1 == 1 }\n    1\n}\n", nestedTestMessage, 2, 5},
		{"test in a lambda", "fn main() {\n    g = fn x: Int -> {\n        test \"inner\" { assert 1 == 1 }\n        x\n    }\n}\n", nestedTestMessage, 3, 9},
		{"test in an if block", "test \"outer\" {\n    if True {\n        test \"inner\" { assert 1 == 1 }\n    }\n    assert 1 == 1\n}\n", nestedTestMessage, 3, 9},
		{"test in a case arm", "test \"outer\" {\n    case 1 {\n        1 -> { test \"x\" { assert 1 == 1 } }\n        _ -> {}\n    }\n    assert 1 == 1\n}\n", nestedTestMessage, 3, 16},
		{"test in a group's test", "group \"g\" {\n    test \"a\" {\n        test \"inner\" { assert 1 == 1 }\n        assert 1 == 1\n    }\n}\n", nestedTestMessage, 3, 9},
		{"test in a group's setup", "group \"g\" {\n    setup {\n        test \"x\" { assert 1 == 1 }\n        1\n    }\n\n    test \"a\", n { assert n == 1 }\n}\n", nestedTestMessage, 3, 9},
		{"test in a once", "once v = {\n    test \"inner\" { assert 1 == 1 }\n    1\n}\n", nestedTestMessage, 2, 5},
		{"test in an impl function", "struct S {\n    x: Int\n}\nimpl S {\n    fn get(s: S) -> Int {\n        test \"x\" { assert 1 == 1 }\n        s.x\n    }\n}\n", nestedTestMessage, 6, 9},
		{"test in an attached prompt", "//! test \"x\" { assert 1 == 1 }\n//! assert answer() == 42\n//\nfn answer() -> Int {\n    42\n}\n", nestedTestMessage, 1, 5},
		{"group in a test", "test \"outer\" {\n    group \"g\" {\n        test \"a\" { assert 1 == 1 }\n    }\n    assert 1 == 1\n}\n", nestedGroupMessage, 2, 5},
		{"group in a function", "fn main() {\n    group \"g\" {\n        test \"a\" { assert 1 == 1 }\n    }\n}\n", nestedGroupMessage, 2, 5},
		{"gopkg in a function", "fn main() {\n    gopkg \"strings\"\n}\n", "a `gopkg` declaration belongs at the top level of a file, beside the `go` bindings that use it; it cannot appear inside a body", 2, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(c.src))
			if err == nil {
				t.Fatalf("Parse admits %q", c.src)
			}
			var pe ParseError
			if !errors.As(err, &pe) || pe.Message != c.want || pe.Line != c.line || pe.Col != c.col {
				t.Fatalf("Parse error %v, want %q at %d:%d", err, c.want, c.line, c.col)
			}

			_, errs, _ := ParseResilient(lexer.Lex(c.src))
			if len(errs) != 1 || errs[0].Message != c.want || errs[0].Line != c.line || errs[0].Col != c.col {
				t.Fatalf("ParseResilient errors %v, want only %q at %d:%d", errs, c.want, c.line, c.col)
			}
		})
	}
}

// The words keep their ordinary meaning in a body: `group` and `gopkg` are
// names there unless a string follows on the line.
func TestParseGroupAndGopkgAreNamesInABody(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n    group = 3\n    g = group + 1\n}\n",
		"fn main() {\n    x = group(1)\n}\n",
		"fn f(group: Int) -> Int {\n    group\n}\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

// An attached prompt's lines are a test body, so `subtests` is read as the
// statement it is in a `test` block.
func TestParseSubtestsInAnAttachedPrompt(t *testing.T) {
	src := "//! subtests [1, 2], n { assert n > 0 }\n//\nfn answer() -> Int {\n    42\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok || len(fd.AttachedTests) != 1 {
		t.Fatalf("expected `answer` with one attached test, got %#v", nodes[0])
	}
	if _, ok := fd.AttachedTests[0].Body.Stmts[0].(*ast.Subtests); !ok {
		t.Fatalf("the prompt's statement is %T, want *ast.Subtests", fd.AttachedTests[0].Body.Stmts[0])
	}
}
