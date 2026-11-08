package parser_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// bindingValue parses `fn main() { x = <expr> }` and answers the value.
func bindingValue(t *testing.T, expr string) ast.Node {
	t.Helper()
	nodes, errs := parser.ParseWithRecovery(lexer.Lex("fn main() {\n    x = " + expr + "\n}\n"))
	if len(errs) > 0 {
		t.Fatalf("parse %q: %v", expr, errs)
	}
	fn := nodes[0].(*ast.FuncDef)
	return fn.Body.Stmts[0].(*ast.Binding).Value
}

// `as` binds like `|>` and reads left to right with it, and a comparison's
// or `and`'s operand holds it.
func TestAscriptionPrecedence(t *testing.T) {
	if a, ok := bindingValue(t, "xs |> Iter.to_list() as List<Int>").(*ast.Ascription); !ok {
		t.Errorf("a pipeline then `as`: want the ascription on top, got %T", a)
	} else if b, ok := a.Value.(*ast.Binary); !ok || b.Op != "|>" {
		t.Errorf("the ascribed value is %T, want the pipeline", a.Value)
	}
	if b, ok := bindingValue(t, "a == b as Int").(*ast.Binary); !ok || b.Op != "==" {
		t.Errorf("a comparison: want `==` on top")
	} else if _, ok := b.Right.(*ast.Ascription); !ok {
		t.Errorf("`==`'s right operand is %T, want the ascription", b.Right)
	}
	if b, ok := bindingValue(t, "p and q as Bool").(*ast.Binary); !ok || b.Op != "and" {
		t.Errorf("`and`: want `and` on top")
	}
	if a, ok := bindingValue(t, "1 + 2 as Int").(*ast.Ascription); !ok {
		t.Errorf("arithmetic: want the ascription on top")
	} else if _, ok := a.Value.(*ast.Binary); !ok {
		t.Errorf("the ascribed value is %T, want `1 + 2`", a.Value)
	}
}

func TestAscriptionChainIsRefused(t *testing.T) {
	_, errs := parser.ParseWithRecovery(lexer.Lex("fn main() {\n    x = 1 as Int as Int\n}\n"))
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "`as` can't follow another `as`: an expression has one ascribed type") {
		t.Fatalf("want the chained-`as` error, got %v", errs)
	}
}
