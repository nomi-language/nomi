package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// Nomi has no decorators. A `@name` line is a parse error naming it, wherever
// a declaration or body item may start.
func TestParseDecoratorIsRejectedEverywhere(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"function", "@uses logger\nfn f(): Int { 0 }\n",
			"line 1, col 1: `@uses` is not supported: Nomi has no decorators"},
		{"unknown name", "@inline\nfn f(): Int { 0 }\n",
			"line 1, col 1: `@inline` is not supported: Nomi has no decorators"},
		{"keyword name", "@for\nfn f(): Int { 0 }\n",
			"line 1, col 1: `@for` is not supported: Nomi has no decorators"},
		{"struct", "@uses logger\nstruct Point {\n  x: Int\n}\n",
			"line 1, col 1: `@uses` is not supported: Nomi has no decorators"},
		{"host type", "@uses logger\npub host type Tok\n",
			"line 1, col 1: `@uses` is not supported: Nomi has no decorators"},
		{"enum variant", "enum Status {\n  @uses logger\n  Active\n}\n",
			"line 2, col 3: `@uses` is not supported: Nomi has no decorators"},
		{"struct field", "struct Dog {\n  @uses logger\n  name: String\n}\n",
			"line 2, col 3: `@uses` is not supported: Nomi has no decorators"},
		{"impl item", "impl Dog {\n  @uses logger\n  fn f(d: Dog): Int { 0 }\n}\n",
			"line 2, col 3: `@uses` is not supported: Nomi has no decorators"},
		{"after an attached test", "//! assert f() == 0\n@uses logger\nfn f(): Int { 0 }\n",
			"line 2, col 1: `@uses` is not supported: Nomi has no decorators"},
		{"bare @", "@ 1\nfn f(): Int { 0 }\n",
			"line 1, col 1: unexpected '@': Nomi has no decorators"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("the parser accepts %q", tc.src)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// `uses` is an ordinary identifier.
func TestParseUsesIsAnIdentifier(t *testing.T) {
	nodes, err := Parse(lexer.Lex("fn uses(uses: Int): Int {\n  uses\n}\n"))
	if err != nil {
		t.Fatalf("the parser rejects `uses` as a name: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok || fn.Name != "uses" || len(fn.Params) != 1 || fn.Params[0].Name != "uses" {
		t.Fatalf("parsed %#v", nodes[0])
	}
}
