package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A comment line between two statements leaves them two statements. It
// used to join a statement that starts with `(`, `.`, `-` or another token
// that can continue an expression onto the one before it: `f(x)`, a comment
// line, then `(x, 2)` parsed as the call `f(x)(x, 2)`. Only a `|>` continues
// an expression across a line break, with or without a comment line.
func TestParse_CommentLineDoesNotJoinStatements(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		stmts int
	}{
		"a tuple after a call":    {"fn main() {\n    f(x)\n    // note\n    (x, 2)\n}\n", 2},
		"a negation after a name": {"fn main() {\n    x\n    // note\n    -1\n}\n", 2},
		"a pipe continues":        {"fn main() {\n    x\n    // note\n    |> f()\n}\n", 1},
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := Parse(lexer.Lex(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			body := nodes[0].(*ast.FuncDef).Body
			if len(body.Stmts) != tc.stmts {
				t.Fatalf("got %d statements, want %d", len(body.Stmts), tc.stmts)
			}
		})
	}
}
