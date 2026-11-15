package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A refinement's `when` rule (spec §4 *Subtypes*): after a `type`'s
// inner type, and after a struct's `end`, on that line.

func TestParseRefinement_TypeWithLambdaRule(t *testing.T) {
	nodes := parse(t, "pub subtype Port Int when fn port -> port >= 1 and port <= 65535\n")
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("got %T, want *ast.TypeDef", nodes[0])
	}
	if _, isLambda := td.Rule.(*ast.Lambda); !isLambda {
		t.Fatalf("Rule = %T, want *ast.Lambda", td.Rule)
	}
	if td.RuleLine != 1 || td.RuleCol != 22 {
		t.Errorf("when at %d:%d, want 1:22", td.RuleLine, td.RuleCol)
	}
}

func TestParseRefinement_TypeWithNamedRule(t *testing.T) {
	nodes := parse(t, "subtype Username String when is_username\n")
	td := nodes[0].(*ast.TypeDef)
	if id, ok := td.Rule.(*ast.Ident); !ok || id.Name != "is_username" {
		t.Fatalf("Rule = %#v, want the Ident is_username", td.Rule)
	}
}

func TestParseRefinement_StructRuleEndsTheBody(t *testing.T) {
	sd := parseSingleStructDef(t, "struct Span\n    low: Int\n    high: Int\n    when fn s -> s.low <= s.high\nend\n")
	if sd.Rule == nil || sd.RuleLine != 4 || sd.RuleCol != 5 {
		t.Fatalf("Rule = %#v at %d:%d, want a rule at 4:5", sd.Rule, sd.RuleLine, sd.RuleCol)
	}
}

func TestParseRefinement_StructRuleOverSeveralLines(t *testing.T) {
	sd := parseSingleStructDef(t, "struct Span\n    low: Int\n    high: Int\n\n    // the order\n    when fn s ->\n        s.low <= s.high\n    end // closes the rule\nend\n")
	if _, ok := sd.Rule.(*ast.Lambda); !ok || sd.RuleLine != 6 {
		t.Fatalf("Rule = %T at line %d, want a lambda at line 6", sd.Rule, sd.RuleLine)
	}
	if len(sd.Fields) != 2 || len(sd.RuleLeading) != 2 || len(sd.RuleTrailing) != 1 {
		t.Fatalf("fields %d, rule leading %d, rule trailing %d; want 2, 2 and 1", len(sd.Fields), len(sd.RuleLeading), len(sd.RuleTrailing))
	}
}

func TestParseRefinement_PlainDeclarationsHaveNoRule(t *testing.T) {
	nodes := parse(t, "type Id Int\nstruct P\n    x: Int\nend\n")
	if nodes[0].(*ast.TypeDef).Rule != nil || nodes[1].(*ast.StructDef).Rule != nil {
		t.Fatal("a declaration without `when` has a rule")
	}
}

func TestParseRefinement_Rejections(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"type Marker when fn x -> True\n", "zero-sized `type Marker` has no value for a `when` rule to check"},
		{"type Port Int when\n", "`when` on a type declaration takes the rule its values satisfy"},
		{"struct Span\n    low: Int\nend when fn s -> True\n", "a struct's `when` rule goes inside its body, after its fields"},
		{"struct Span end when fn s -> True\n", "a struct's `when` rule goes inside its body, after its fields"},
		{"struct Span\n    when fn s -> True\n    low: Int\nend\n", "a struct's `when` rule comes after every field"},
		{"struct Span\n    low: Int\n    when fn s -> True\n    when fn s -> False\nend\n", "a struct has one `when` rule"},
		{"struct Span\n    low: Int\n    when\nend\n", "`when` on a type declaration takes the rule its values satisfy"},
	} {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want %q", tc.src, err, tc.want)
		}
	}
}

func TestParseRefinement_SeveralParents(t *testing.T) {
	nodes := parse(t, "pub subtype PositiveInt NonNegativeInt and NonZeroInt when fn n -> n > 0\n")
	td := nodes[0].(*ast.TypeDef)
	first, ok := td.InnerTypeExpr.(*ast.SimpleType)
	if !ok || first.Name != "NonNegativeInt" {
		t.Fatalf("InnerTypeExpr = %#v, want NonNegativeInt", td.InnerTypeExpr)
	}
	if len(td.MoreParents) != 1 || td.MoreParents[0].TypeString() != "NonZeroInt" {
		t.Fatalf("MoreParents = %#v, want [NonZeroInt]", td.MoreParents)
	}
	// Without `when` the conjunction is the inner type.
	plain := parse(t, "type Shape Priced and Costed\n")[0].(*ast.TypeDef)
	if _, conj := plain.InnerTypeExpr.(*ast.ConjunctionType); !conj || len(plain.MoreParents) != 0 {
		t.Fatalf("type Shape Priced and Costed = %#v, want a conjunction inner type", plain)
	}
}
