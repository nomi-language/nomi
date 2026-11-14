package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A refinement's `when` rule (spec §4 *Refinement types*): after a `type`'s
// inner type, and after a struct's `end`, on that line.

func TestParseRefinement_TypeWithLambdaRule(t *testing.T) {
	nodes := parse(t, "pub type Port Int when fn port -> port >= 1 and port <= 65535\n")
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("got %T, want *ast.TypeDef", nodes[0])
	}
	if _, isLambda := td.Rule.(*ast.Lambda); !isLambda {
		t.Fatalf("Rule = %T, want *ast.Lambda", td.Rule)
	}
	if td.RuleLine != 1 || td.RuleCol != 19 {
		t.Errorf("when at %d:%d, want 1:19", td.RuleLine, td.RuleCol)
	}
}

func TestParseRefinement_TypeWithNamedRule(t *testing.T) {
	nodes := parse(t, "type Username String when is_username\n")
	td := nodes[0].(*ast.TypeDef)
	if id, ok := td.Rule.(*ast.Ident); !ok || id.Name != "is_username" {
		t.Fatalf("Rule = %#v, want the Ident is_username", td.Rule)
	}
}

func TestParseRefinement_StructAfterEnd(t *testing.T) {
	sd := parseSingleStructDef(t, "struct Span\n    low: Int\n    high: Int\nend when fn s -> s.low <= s.high\n")
	if sd.Rule == nil || sd.RuleLine != 4 || sd.RuleCol != 5 {
		t.Fatalf("Rule = %#v at %d:%d, want a rule at 4:5", sd.Rule, sd.RuleLine, sd.RuleCol)
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
	} {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want %q", tc.src, err, tc.want)
		}
	}
}
