package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

func orPatternOf(t *testing.T, n ast.Node, alternatives int) *ast.OrPattern {
	t.Helper()
	or, ok := n.(*ast.OrPattern)
	if !ok || len(or.Alternatives) != alternatives {
		t.Fatalf("want an `or` pattern of %d alternatives, got %#v", alternatives, n)
	}
	return or
}

// `or` binds looser than every pattern construct but `as`: at the top of an
// arm it joins whole patterns, and an `as` after the last alternative names
// the whole value.
func TestOrPattern_TopOfAnArm(t *testing.T) {
	or := orPatternOf(t, caseArmPattern(t, "#.Circle{r} or #.Square{r}"), 2)
	for _, alt := range or.Alternatives {
		if _, ok := alt.(*ast.StructPattern); !ok {
			t.Fatalf("alternative: got %T", alt)
		}
	}
	if or.Line != 2 || or.Col != 5 {
		t.Fatalf("position %d:%d", or.Line, or.Col)
	}

	whole := asPatternOf(t, caseArmPattern(t, ".A(n) or .B(n) as v"), "v")
	orPatternOf(t, whole.Pattern, 2)

	or = orPatternOf(t, caseArmPattern(t, "1 or 2 or 3"), 3)
	if lit, ok := or.Alternatives[2].(*ast.IntLit); !ok || lit.Value != 3 {
		t.Fatalf("third alternative: got %#v", or.Alternatives[2])
	}
	or = orPatternOf(t, caseArmPattern(t, `"yes" or "y"`), 2)
	if _, ok := or.Alternatives[1].(*ast.StringLit); !ok {
		t.Fatalf("string alternative: got %T", or.Alternatives[1])
	}
	orPatternOf(t, caseArmPattern(t, "#(0, x) or #(x, 0)"), 2)
	orPatternOf(t, caseArmPattern(t, "[] or [_]"), 2)
	orPatternOf(t, caseArmPattern(t, "None or Some(0)"), 2)
}

// A guard after an `or` pattern applies to the whole arm, and `or` inside a
// guard is the boolean operator.
func TestOrPattern_GuardStaysBoolean(t *testing.T) {
	c, ok := parseExpr(t, "case v {\n    .A(n) or .B(n) when n > 0 or n < -9 -> 0\n    _ -> 1\n}").(*ast.Case)
	if !ok || len(c.Branches) != 2 {
		t.Fatal("want two arms")
	}
	orPatternOf(t, c.Branches[0].Pattern, 2)
	g, ok := c.Branches[0].Guard.(*ast.Binary)
	if !ok || g.Op != "or" {
		t.Fatalf("guard: got %#v", c.Branches[0].Guard)
	}

	c, ok = parseExpr(t, "case {\n    a or b -> 0\n    _ -> 1\n}").(*ast.Case)
	if !ok {
		t.Fatal("want a subject-less case")
	}
	if b, ok := c.Branches[0].Pattern.(*ast.Binary); !ok || b.Op != "or" {
		t.Fatalf("a subject-less arm's condition: got %#v", c.Branches[0].Pattern)
	}
}

// `or` nests inside every pattern that holds subpatterns.
func TestOrPattern_Nested(t *testing.T) {
	ep, ok := caseArmPattern(t, "Some(.Equilateral or .Isosceles)").(*ast.EnumPattern)
	if !ok {
		t.Fatal("want a variant pattern")
	}
	orPatternOf(t, ep.Payload, 2)

	lp, ok := caseArmPattern(t, "[')' or ']' or '}' as closer, ..rest]").(*ast.ListPattern)
	if !ok || len(lp.Heads) != 1 {
		t.Fatal("want a list pattern with one head")
	}
	orPatternOf(t, asPatternOf(t, lp.Heads[0], "closer").Pattern, 3)

	tp, ok := caseArmPattern(t, "#(1 or 2, x)").(*ast.TuplePattern)
	if !ok || len(tp.Patterns) != 2 {
		t.Fatal("want a tuple pattern")
	}
	orPatternOf(t, tp.Patterns[0], 2)

	sp, ok := caseArmPattern(t, "#P{kind: .A or .B, n}").(*ast.StructPattern)
	if !ok || len(sp.Fields) != 2 {
		t.Fatal("want a struct pattern")
	}
	orPatternOf(t, sp.Fields[0].Pattern, 2)

	sp, ok = caseArmPattern(t, "#{x: a or b}").(*ast.StructPattern)
	if !ok {
		t.Fatal("want an anonymous struct pattern")
	}
	orPatternOf(t, sp.Fields[0].Pattern, 2)

	mp, ok := caseArmPattern(t, `%{"k" => 1 or 2}`).(*ast.MapPattern)
	if !ok {
		t.Fatal("want a map pattern")
	}
	orPatternOf(t, mp.Entries[0].Pattern, 2)
}

// Parentheses group alternatives; a parenthesized `or` among alternatives is
// spliced, so `(A or B) or C` is the pattern `A or B or C`. An `as` names one
// alternative only inside parentheses.
func TestOrPattern_Grouping(t *testing.T) {
	orPatternOf(t, caseArmPattern(t, "(.A or .B) or .C"), 3)
	orPatternOf(t, caseArmPattern(t, ".A or (.B or .C)"), 3)
	or := orPatternOf(t, caseArmPattern(t, "(.A(n) as a) or (.B(n) as a)"), 2)
	asPatternOf(t, or.Alternatives[0], "a")
	asPatternOf(t, or.Alternatives[1], "a")

	_, err := Parse(lexer.Lex("case v {\n    .A(n) as a or .B(n) -> 0\n}"))
	if err == nil || !strings.Contains(err.Error(), "an `as` names the whole `or` pattern") {
		t.Fatalf("`as` before `or`: got %v", err)
	}
	_, err = Parse(lexer.Lex("case v {\n    .A or -> 0\n}"))
	if err == nil || !strings.Contains(err.Error(), "expected a pattern after `or`") {
		t.Fatalf("`or` with nothing after it: got %v", err)
	}
}

// A line that ends in `or` continues on the next, and a comment after the
// `or` is the next alternative's.
func TestOrPattern_BrokenAcrossLines(t *testing.T) {
	c, ok := parseExpr(t, "case v {\n    .A or\n        .B or // last\n        .C -> 0\n}").(*ast.Case)
	if !ok || len(c.Branches) != 1 {
		t.Fatal("want one arm")
	}
	or := orPatternOf(t, c.Branches[0].Pattern, 3)
	lead := or.Alternatives[2].(ast.HasTrivia).GetLeading()
	if len(lead) != 1 || lead[0].Text != "// last" {
		t.Fatalf("the comment after `or`: got %#v", lead)
	}
}

func TestOrPattern_StatementAndParameterPositions(t *testing.T) {
	pb := parsePatternBinding(t, ".A(n) or .B(n) = e else { return 0 }")
	orPatternOf(t, pb.Pattern, 2)
	if pb.Else == nil {
		t.Fatal("want the else")
	}
	pb = parsePatternBinding(t, ".A(n) or .B(n) as v = e")
	orPatternOf(t, asPatternOf(t, pb.Pattern, "v").Pattern, 2)

	ifNode, ok := parseExpr(t, "if Some(1 or 2) = m { 0 } else { 1 }").(*ast.If)
	if !ok || ifNode.CondPattern == nil {
		t.Fatal("want a pattern if")
	}

	f, ok := parse(t, "fn f(.A(n) or .B(n): E) -> Int { n }")[0].(*ast.FuncDef)
	if !ok || len(f.Params) != 1 {
		t.Fatal("want a function with one parameter")
	}
	orPatternOf(t, f.Params[0].Destructure, 2)

	ifNode, ok = parseExpr(t, "if a or b { 0 } else { 1 }").(*ast.If)
	if !ok || ifNode.CondPattern != nil {
		t.Fatal("`if a or b` is a boolean condition")
	}
}
