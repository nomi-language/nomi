package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A synthesized impl's variant-payload binding is defined at its own
// position. When it shared the impl function's, the Definitions slot the
// signature check reads held the binding, so a function-typed positional
// payload (`Num ((Int) -> Int)`) was read as the synthesized `inspect`'s
// signature: "return type Int does not match interface 'Debug' return type
// String".
func TestUniversalDebug_FunctionTypedPositionalPayloadChecks(t *testing.T) {
	for _, payload := range []string{"((Int) -> Int)", "(Int) -> Int", "((Int, Int) -> Bool)", "(() -> Int)"} {
		errs := checkUniversalDebugProject(t, "enum Handler {\n  Num "+payload+"\n  Other\n}\n\nfn main() {\n  _ = Handler.Other\n}\n")
		expectNoErrs(t, errs)
	}
}

func TestUniversalDebug_PayloadBindingHasItsOwnPosition(t *testing.T) {
	nodes, err := parser.Parse(lexer.Lex("enum Handler {\n  Num Int\n  Other\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDef
	for _, n := range analysis.SynthesizeUniversalDebug(nodes) {
		if ib, ok := n.(*ast.ImplBlock); ok {
			fn = ib.Items[0].(*ast.FuncDef)
		}
	}
	if fn == nil {
		t.Fatal("no Debug impl synthesized")
	}
	var bindings int
	for _, s := range fn.Body.Stmts {
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			continue
		}
		c, ok := es.Expr.(*ast.Case)
		if !ok {
			continue
		}
		for _, br := range c.Branches {
			if p, ok := br.Pattern.(*ast.EnumPattern); ok && p.Binding != "" {
				bindings++
				if p.Line == fn.Line && p.BindingCol == fn.Col {
					t.Fatalf("binding %s is defined at the impl function's own position (%d:%d)", p.Binding, fn.Line, fn.Col)
				}
			}
		}
	}
	if bindings == 0 {
		t.Fatal("no payload binding found; the test no longer exercises anything")
	}
}
