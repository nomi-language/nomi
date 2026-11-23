package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// `refine` declares a refinement (spec §4 *Refinements*): its parents follow the
// name, joined by `and`, and its `when` rule is optional. A `type` over
// `A and B` wraps the intersection as its one inner type.

func TestParseRefine_KeywordAndParents(t *testing.T) {
	nodes := parse(t, "pub refine PositiveInt NonNegativeInt and NonZeroInt\n")
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("got %T, want *ast.TypeDef", nodes[0])
	}
	if !td.Refinement || !td.Public || td.Rule != nil {
		t.Fatalf("Refinement %v, Public %v, Rule %v; want a public refinement with no rule", td.Refinement, td.Public, td.Rule)
	}
	if td.InnerTypeExpr.TypeString() != "NonNegativeInt" || len(td.MoreParents) != 1 || td.MoreParents[0].TypeString() != "NonZeroInt" {
		t.Fatalf("parents %s and %v, want NonNegativeInt and [NonZeroInt]", td.InnerTypeExpr.TypeString(), td.MoreParents)
	}
}

func TestParseRefine_RuleAndWhere(t *testing.T) {
	nodes := parse(t, "refine Sorted<T> List<T> where T: Comparable when fn xs -> True\n")
	td := nodes[0].(*ast.TypeDef)
	if !td.Refinement || td.Rule == nil || len(td.WhereClauses) != 1 || len(td.TypeParams) != 1 {
		t.Fatalf("got %+v, want a generic refinement with a bound and a rule", td)
	}
	if td.RuleCol != 46 {
		t.Errorf("when at column %d, want 46", td.RuleCol)
	}
}

func TestParseRefine_TypeKeepsItsIntersection(t *testing.T) {
	for _, src := range []string{
		"type Strict NonNegativeInt and NonZeroInt\n",
		"type Email String when is_email\n",
	} {
		td := parse(t, src)[0].(*ast.TypeDef)
		if td.Refinement || len(td.MoreParents) != 0 {
			t.Errorf("%q: Refinement %v, MoreParents %v; want a type with one inner type", src, td.Refinement, td.MoreParents)
		}
	}
	td := parse(t, "type Strict NonNegativeInt and NonZeroInt\n")[0].(*ast.TypeDef)
	if conj, ok := td.InnerTypeExpr.(*ast.ConjunctionType); !ok || len(conj.Members) != 2 {
		t.Fatalf("inner %T, want the intersection of two members", td.InnerTypeExpr)
	}
}

func TestParseRefine_IntersectionInEveryTypePosition(t *testing.T) {
	src := "struct Box\n    n: NonNegativeInt and NonZeroInt\nend\n\n" +
		"fn half(n: NonNegativeInt and NonZeroInt) -> NonNegativeInt and NonZeroInt\n    n\nend\n"
	nodes := parse(t, src)
	sd := nodes[0].(*ast.StructDef)
	if _, ok := sd.Fields[0].TypeAnnotation.(*ast.ConjunctionType); !ok {
		t.Errorf("field type %T, want an intersection", sd.Fields[0].TypeAnnotation)
	}
	fd := nodes[1].(*ast.FuncDef)
	if _, ok := fd.Params[0].TypeAnnotation.(*ast.ConjunctionType); !ok {
		t.Errorf("parameter type %T, want an intersection", fd.Params[0].TypeAnnotation)
	}
	if _, ok := fd.ReturnTypeExpr.(*ast.ConjunctionType); !ok {
		t.Errorf("result type %T, want an intersection", fd.ReturnTypeExpr)
	}
}

func TestParseRefine_Rejections(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"refine Port\n", "`refine Port` names the type it narrows"},
		{"refine Port when fn p -> True\n", "`refine Port` names the type it narrows"},
	} {
		err := parseError(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %v, want one containing %q", tc.src, err, tc.want)
		}
	}
}

func TestParseRefine_IsAReservedWord(t *testing.T) {
	err := parseError(t, "fn f(refine: String) -> String\n    refine\nend\n")
	if err == nil {
		t.Fatal("`refine` as a parameter name parses; it is a reserved word")
	}
}
