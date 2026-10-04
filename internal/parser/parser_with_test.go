package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An application-field read is syntactically an owner-qualified field access;
// the checker, not the parser, knows `MyApp` is an application type.
func TestParseAppFieldReadIsAFieldAccess(t *testing.T) {
	nodes := parse(t, "fn main() {\n  io.print(MyApp.config.logger)\n}")
	fn := nodes[0].(*ast.FuncDef)
	call := fn.Body.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Call)
	outer, ok := call.Args[0].(*ast.FieldAccess)
	if !ok || outer.Field.Name != "logger" {
		t.Fatalf("expected `.logger` access, got %#v", call.Args[0])
	}
	inner, ok := outer.Object.(*ast.FieldAccess)
	if !ok || inner.Field.Name != "config" {
		t.Fatalf("expected `MyApp.config`, got %#v", outer.Object)
	}
	if owner, ok := inner.Object.(*ast.TypeIdent); !ok || owner.Name != "MyApp" {
		t.Fatalf("expected the owner `MyApp`, got %#v", inner.Object)
	}
}

// `with` is a statement of its own, not an expression statement, and the
// statements after it are the block's ordinary statements.
func TestParseWithStatement(t *testing.T) {
	nodes := parse(t, "fn main() {\n  with MyApp.logger = Silent\n  run()\n}")
	fn := nodes[0].(*ast.FuncDef)
	w, ok := fn.Body.Stmts[0].(*ast.With)
	if !ok {
		t.Fatalf("expected *ast.With, got %#v", fn.Body.Stmts[0])
	}
	if w.Line != 2 || w.Col != 3 {
		t.Errorf("with at %d:%d, want 2:3", w.Line, w.Col)
	}
	if w.Target.Field.Name != "logger" || w.Target.Object.(*ast.TypeIdent).Name != "MyApp" {
		t.Errorf("target: %#v", w.Target)
	}
	if _, ok := w.Value.(*ast.TypeIdent); !ok {
		t.Errorf("value: %#v", w.Value)
	}
	if len(fn.Body.Stmts) != 2 {
		t.Errorf("body: %d statements, want 2", len(fn.Body.Stmts))
	}
}

// Several fields are several `with` lines, and a struct literal value needs
// no parentheses.
func TestParseWithSeveralStatements(t *testing.T) {
	nodes := parse(t, "fn main() {\n  with A.store = Fake{n: 1}\n  with app.A.x = 2\n  run()\n}")
	stmts := nodes[0].(*ast.FuncDef).Body.Stmts
	first, ok := stmts[0].(*ast.With)
	if !ok {
		t.Fatalf("first: %#v", stmts[0])
	}
	if _, ok := first.Value.(*ast.StructLit); !ok {
		t.Fatalf("value: %#v", first.Value)
	}
	second, ok := stmts[1].(*ast.With)
	if !ok || second.Target.Field.Name != "x" {
		t.Fatalf("second: %#v", stmts[1])
	}
	if owner, ok := second.Target.Object.(*ast.FieldAccess); !ok || owner.Field.Name != "A" {
		t.Fatalf("file-qualified owner: %#v", second.Target.Object)
	}
}

// A value may continue on the line after the `=`.
func TestParseWithValueOnTheNextLine(t *testing.T) {
	nodes := parse(t, "fn main() {\n  with A.context =\n    Context.with_timeout(A.context, d)\n  run()\n}")
	w := nodes[0].(*ast.FuncDef).Body.Stmts[0].(*ast.With)
	if _, ok := w.Value.(*ast.Call); !ok {
		t.Fatalf("value: %#v", w.Value)
	}
}

// `with` is not an expression.
func TestParseWithIsNotAnExpression(t *testing.T) {
	if _, err := Parse(lexer.Lex("fn f(): Int {\n  n = with A.x = 1\n  n\n}")); err == nil {
		t.Fatal("`with` parsed as a binding's value")
	}
}

func TestParseWithRejects(t *testing.T) {
	for src, want := range map[string]string{
		"fn main() {\n  with MyApp = 1\n}":                "application field such as `MyApp.logger`",
		"fn main() {\n  with MyApp.x\n}":                  "expected '='",
		"fn main() {\n  with\n}":                          "expected an application field override after `with`",
		"fn main() {\n  with 1 = 2\n}":                    "application field such as `MyApp.logger`",
		"fn main() {\n  with MyApp.x = 1, MyApp.y = 2\n}": "write one `with` line per field",
		"fn main() {\n  with MyApp.x = 1 { run() }\n}":    "takes no block",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}

// `$` is no longer part of the language outside strings.
func TestParseDollarIsIllegal(t *testing.T) {
	if _, err := Parse(lexer.Lex("fn main() {\n  _ = $config\n}")); err == nil {
		t.Fatal("`$config` parsed")
	}
}
