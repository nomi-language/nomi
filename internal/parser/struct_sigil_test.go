package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `{` starts a struct with no name wherever a value, a type or a pattern
// starts, and an import's member list after a module path. The two
// readings are told apart by position.
func TestStructSigil_EachPositionParses(t *testing.T) {
	for _, src := range []string{
		"import std::io.{self, print}\n",
		"fn f({w:, h:}: {w: Int, h: Int}) -> Int\n    w * h\nend\n",
		"fn main()\n    p = {x: 1}\n    q = {..p, x: 2}\n    {x:} = q\nend\n",
		"fn main()\n    r: Maybe<{id: Int}> = Some({id: 1})\n    _ = case r\n        Some({id: 1}) -> 1\n        _ -> 0\n    end\nend\n",
		"fn main()\n    r = 1..begin\n        3\n    end\nend\n",
		"test \"rows\"\n    [{n: 1}] |> subtests {n:}\n        assert n == 1\n    end\nend\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

// A line that starts with `{` is a statement of its own, as one that
// starts with `.Variant` is.
func TestStructSigil_ALineStartIsANewStatement(t *testing.T) {
	nodes := parse(t, "fn main()\n    x = a\n    {y: 1}\nend\n")
	fn := nodes[0].(*ast.FuncDef)
	if n := len(fn.Body.Stmts); n != 2 {
		t.Fatalf("the body holds %d statements, want 2", n)
	}
	stmt, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("the second statement is %T, want an expression", fn.Body.Stmts[1])
	}
	if lit, ok := stmt.Expr.(*ast.StructLit); !ok || lit.TypeName != nil {
		t.Fatalf("the second statement is %T, want an anonymous struct literal", stmt.Expr)
	}
}

func TestStructSigil_Refusals(t *testing.T) {
	// A struct has no `#`.
	wantParseError(t, "fn main()\n    p = #{x: 1}\nend\n", "a struct is written without `#`")
	wantParseError(t, "fn f(p: #{x: Int}) -> Int\n    p.x\nend\n", "an anonymous struct type is written without `#`")
	wantParseError(t, "fn main()\n    _ = case p\n        #{x} -> x\n    end\nend\n", "a struct is written without `#`")
	// After a value, `.` takes a field name.
	wantParseError(t, "fn main()\n    b = a.{x}\nend\n", "a field name follows `.` after a value")
	// The dot and the brace are written together.
	wantParseError(t, "fn main()\n    p = . {x: 1}\nend\n", "expected a variant or field name after '.'")
}
