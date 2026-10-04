package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

func parsePatternBinding(t *testing.T, source string) *ast.PatternBinding {
	t.Helper()
	nodes := parse(t, source)
	if len(nodes) != 1 {
		t.Fatalf("Parse(%q): expected 1 statement, got %d", source, len(nodes))
	}
	pb, ok := nodes[0].(*ast.PatternBinding)
	if !ok {
		t.Fatalf("Parse(%q): expected *ast.PatternBinding, got %T", source, nodes[0])
	}
	return pb
}

func TestPatternBindingElse_Block(t *testing.T) {
	pb := parsePatternBinding(t, "Some(email) = user.email else {\n    return Err(\"none\")\n}")
	ep, ok := pb.Pattern.(*ast.EnumPattern)
	if !ok || ep.Binding != "email" {
		t.Fatalf("pattern: got %#v", pb.Pattern)
	}
	if _, ok := pb.Value.(*ast.FieldAccess); !ok {
		t.Fatalf("value: got %T", pb.Value)
	}
	if pb.Else == nil || pb.Else.Block == nil || pb.Else.Arms != nil {
		t.Fatalf("else: want a plain block, got %#v", pb.Else)
	}
	if _, ok := pb.Else.Block.Stmts[0].(*ast.Return); !ok {
		t.Fatalf("else block: got %T", pb.Else.Block.Stmts[0])
	}
	if pb.Line != 1 || pb.Col != 1 || pb.Else.Line != 1 || pb.Else.Col != 26 {
		t.Fatalf("positions: binding %d:%d, else %d:%d", pb.Line, pb.Col, pb.Else.Line, pb.Else.Col)
	}
}

func TestPatternBindingElse_Arms(t *testing.T) {
	pb := parsePatternBinding(t, "Ok(user) = load(id) else {\n    Err(NotFound) -> return 1\n    Err(e) when e == 2 -> 8080\n}")
	if pb.Else == nil || pb.Else.Block != nil || len(pb.Else.Arms) != 2 {
		t.Fatalf("else: want two arms, got %#v", pb.Else)
	}
	if _, ok := pb.Else.Arms[0].Body.(*ast.Return); !ok {
		t.Fatalf("first arm body: got %T", pb.Else.Arms[0].Body)
	}
	if pb.Else.Arms[1].Guard == nil {
		t.Fatalf("second arm: want a guard")
	}
	if pb.Else.LBraceLine != 1 || pb.Else.LBraceCol != 26 || pb.Else.EndLine != 4 || pb.Else.EndCol != 1 {
		t.Fatalf("braces: { at %d:%d, } at %d:%d", pb.Else.LBraceLine, pb.Else.LBraceCol, pb.Else.EndLine, pb.Else.EndCol)
	}
}

// A block whose first item is not `pattern ->` is a plain block, whatever
// the item looks like.
func TestPatternBindingElse_BlockStartingWithAPatternShape(t *testing.T) {
	for _, src := range []string{
		"Ok((w, h)) = parse(text) else { (80, 24) }",
		"Some(e) = m else { \"none\" }",
		"Some(e) = m else {\n    x = 1\n    \"${x}\"\n}",
		"Some(e) = m else { continue }",
	} {
		pb := parsePatternBinding(t, src)
		if pb.Else == nil || pb.Else.Block == nil {
			t.Errorf("%q: want a plain else block, got %#v", src, pb.Else)
		}
	}
}

// Patterns no destructure statement spells take the general path, with or
// without else; the checker decides whether the pattern can fail.
func TestPatternBindingElse_GeneralPatterns(t *testing.T) {
	for src, pattern := range map[string]string{
		"Ok((w, h)) = parse(text) else { (80, 24) }":       "*ast.EnumPattern",
		"[first, ..rest] = xs else { return 0 }":           "*ast.ListPattern",
		".Valid{addr, score} = check(x) else { return 0 }": "*ast.StructPattern",
		"Ok(Some(n)) = r":                    "*ast.EnumPattern",
		"(Some(a), b) = p else { return 0 }": "*ast.TuplePattern",
		"{\"k\" => v} = m else { return 0 }": "*ast.MapPattern",
		"x = 5 else { return 0 }":            "*ast.IdentPattern",
		"(a, b) = p else { return 0 }":       "*ast.TuplePattern",
	} {
		pb := parsePatternBinding(t, src)
		if got := typeName(pb.Pattern); got != pattern {
			t.Errorf("%q: pattern %s, want %s", src, got, pattern)
		}
	}
}

func typeName(n ast.Node) string {
	switch n.(type) {
	case *ast.EnumPattern:
		return "*ast.EnumPattern"
	case *ast.ListPattern:
		return "*ast.ListPattern"
	case *ast.StructPattern:
		return "*ast.StructPattern"
	case *ast.TuplePattern:
		return "*ast.TuplePattern"
	case *ast.MapPattern:
		return "*ast.MapPattern"
	case *ast.IdentPattern:
		return "*ast.IdentPattern"
	}
	return "other"
}

// `else` may sit on the line after the value, as an if's may.
func TestPatternBindingElse_ElseOnNextLine(t *testing.T) {
	pb := parsePatternBinding(t, "Some(e) = m\nelse { return 0 }")
	if pb.Else == nil || pb.Else.Line != 2 {
		t.Fatalf("else: got %#v", pb.Else)
	}
}

// Without else, the destructure statements keep their own nodes.
func TestPatternBindingElse_DestructuresWithoutElseUnchanged(t *testing.T) {
	for src, want := range map[string]string{
		"Some(x) = m":  "*ast.DistinctDestructure",
		"(a, b) = p":   "*ast.TupleDestructure",
		"{a, b} = p":   "*ast.StructDestructure",
		"x = 5":        "*ast.Binding",
		"io.print(x)":  "*ast.ExprStmt",
		"[1, 2] |> f":  "*ast.ExprStmt",
		"{ x }":        "*ast.ExprStmt",
		"Foo{a: 1}":    "*ast.ExprStmt",
		"a == b":       "*ast.ExprStmt",
		"\"s\" |> f()": "*ast.ExprStmt",
	} {
		nodes := parse(t, src)
		if len(nodes) != 1 {
			t.Fatalf("%q: %d statements", src, len(nodes))
		}
		got := ""
		switch nodes[0].(type) {
		case *ast.DistinctDestructure:
			got = "*ast.DistinctDestructure"
		case *ast.TupleDestructure:
			got = "*ast.TupleDestructure"
		case *ast.StructDestructure:
			got = "*ast.StructDestructure"
		case *ast.Binding:
			got = "*ast.Binding"
		case *ast.ExprStmt:
			got = "*ast.ExprStmt"
		case *ast.PatternBinding:
			got = "*ast.PatternBinding"
		}
		if got != want {
			t.Errorf("%q: got %T, want %s", src, nodes[0], want)
		}
	}
}

func TestPatternBindingElse_ParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"x: Int = 5 else { return 0 }":          "this pattern always matches; remove the else",
		"Some(x) = m else return 0":             "expected '{' after else: a binding's else is a braced block",
		"Some(x) = m else {\n    Err(e) -> 1\n": "expected '}' to close else",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got error %v, want one containing %q", src, err, want)
		}
	}
}
