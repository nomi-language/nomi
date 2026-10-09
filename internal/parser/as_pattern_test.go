package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// caseArmPattern parses `case v { <arm> -> 0 }` and returns the arm's pattern.
func caseArmPattern(t *testing.T, arm string) ast.Node {
	t.Helper()
	c, ok := parseExpr(t, "case v {\n    "+arm+" -> 0\n}").(*ast.Case)
	if !ok || len(c.Branches) != 1 {
		t.Fatalf("%q: want one case arm", arm)
	}
	return c.Branches[0].Pattern
}

func asPatternOf(t *testing.T, n ast.Node, name string) *ast.AsPattern {
	t.Helper()
	a, ok := n.(*ast.AsPattern)
	if !ok || a.Name != name {
		t.Fatalf("want `... as %s`, got %#v", name, n)
	}
	return a
}

// `as` binds looser than every other pattern construct: at the top of an arm
// it names the whole value, and inside a payload the payload.
func TestAsPattern_BindsLooserThanEveryOtherConstruct(t *testing.T) {
	whole := asPatternOf(t, caseArmPattern(t, "Ok(x) as r"), "r")
	if ep, ok := whole.Pattern.(*ast.EnumPattern); !ok || ep.Binding != "x" {
		t.Fatalf("inner: got %#v", whole.Pattern)
	}
	if whole.Line != 2 || whole.Col != 5 || whole.NameLine != 2 || whole.NameCol != 14 {
		t.Fatalf("positions: pattern %d:%d, name %d:%d", whole.Line, whole.Col, whole.NameLine, whole.NameCol)
	}

	ep, ok := caseArmPattern(t, "Ok(Tx{kind: .Deposit} as t)").(*ast.EnumPattern)
	if !ok {
		t.Fatal("want the variant pattern outermost")
	}
	inner := asPatternOf(t, ep.Payload, "t")
	if _, ok := inner.Pattern.(*ast.StructPattern); !ok {
		t.Fatalf("payload's pattern: got %T", inner.Pattern)
	}

	prefix := asPatternOf(t, caseArmPattern(t, `"/users/" + id as path`), "path")
	if b, ok := prefix.Pattern.(*ast.Binary); !ok || b.Op != "+" {
		t.Fatalf("string prefix: got %#v", prefix.Pattern)
	}
}

func TestAsPattern_EveryNestedPosition(t *testing.T) {
	tp, ok := caseArmPattern(t, "(Some(_) as a, [x, ..rest] as xs)").(*ast.TuplePattern)
	if !ok || len(tp.Patterns) != 2 {
		t.Fatal("want a two-element tuple pattern")
	}
	asPatternOf(t, tp.Patterns[0], "a")
	asPatternOf(t, tp.Patterns[1], "xs")

	lp, ok := caseArmPattern(t, "[Some(n) as first, .._]").(*ast.ListPattern)
	if !ok {
		t.Fatal("want a list pattern")
	}
	asPatternOf(t, lp.Heads[0], "first")
	mp, ok := caseArmPattern(t, `{"k" => Ok(v) as found}`).(*ast.MapPattern)
	if !ok {
		t.Fatal("want a map pattern")
	}
	asPatternOf(t, mp.Entries[0].Pattern, "found")
	sp, ok := caseArmPattern(t, "Line{from: Point{x, y} as start}").(*ast.StructPattern)
	if !ok {
		t.Fatal("want a struct pattern")
	}
	asPatternOf(t, sp.Fields[0].Pattern, "start")
}

func TestAsPattern_ChainsParse(t *testing.T) {
	outer := asPatternOf(t, caseArmPattern(t, "Some(n) as a as b"), "b")
	asPatternOf(t, outer.Pattern, "a")
}

func TestAsPattern_StatementAndParameterPositions(t *testing.T) {
	pb := parsePatternBinding(t, "(a, b) as pair = make()")
	asPatternOf(t, pb.Pattern, "pair")

	pb = parsePatternBinding(t, "Some(n) as m = find() else { return 0 }")
	asPatternOf(t, pb.Pattern, "m")
	if pb.Else == nil {
		t.Fatal("want the else")
	}

	f, ok := parse(t, "fn f(Point{x, y} as p: Point): Int { x }")[0].(*ast.FuncDef)
	if !ok || len(f.Params) != 1 {
		t.Fatal("want a function with one parameter")
	}
	asPatternOf(t, f.Params[0].Destructure, "p")
	if f.Params[0].TypeAnnotation == nil {
		t.Fatal("want the parameter's annotation")
	}

	ifNode, ok := parseExpr(t, "if Ok(v) as r = load() { v } else { 0 }").(*ast.If)
	if !ok {
		t.Fatal("want an if")
	}
	asPatternOf(t, ifNode.CondPattern, "r")

	pd, ok := parse(t, "test \"t\" {\n    assert Some(n) as m = find()\n}")[0].(*ast.TestDecl).Body.Stmts[0].(*ast.PatternDestructure)
	if !ok {
		t.Fatal("want an assert pattern")
	}
	asPatternOf(t, pd.Pattern, "m")
}

func TestAsPattern_NameMustBeABindingName(t *testing.T) {
	for _, src := range []string{
		"case v {\n    Ok(x) as Big -> 0\n}",
		"case v {\n    Ok(x) as _ -> 0\n}",
		"case v {\n    Ok(x) as -> 0\n}",
		"case v {\n    Ok(x) as (r) -> 0\n}",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil {
			t.Errorf("%q: want a parse error", src)
			continue
		}
		if !strings.Contains(err.Error(), "expected a binding name after `as` in a pattern") || !strings.Contains(err.Error(), "line 2, col 11") {
			t.Errorf("%q: got %v", src, err)
		}
	}
}
