package irbuild

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// firstFuncDef is the entry module's top-level `fn` of that name, or nil.
func firstFuncDef(p *Program, name string) *ast.FuncDef {
	for _, n := range p.Entry().Nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == name {
			return fd
		}
	}
	return nil
}

// The type-parameter dispatch seam.
//
// Every fixture here pins the reference text as well as agreement, because a
// differential comparison passes when both sides silently produce nothing.

// TestDict_BoundsAreTheMergeOfTwoSyntaxes pins how a type parameter's bounds
// are collected, and it is not either AST field on its own.
//
// `ast.TypeParam.Bounds` carries internal/synthesized constraints; source
// constraints live in WhereClauses; and the parser REJECTS the inline spelling
// outright. So an implementation reading either field alone answers for a
// population that does not exist — which is exactly why this asserts the merge
// rather than the fields.
func TestDict_BoundsAreTheMergeOfTwoSyntaxes(t *testing.T) {
	// The inline spelling is not merely unsupported here: the front end
	// refuses it, so nothing downstream can ever see it.
	if _, err := AnalyzeSource("probe.nomi", "fn f<T: Display>(x: T): Int {\n  0\n}\n"); err == nil {
		t.Fatal("the parser accepted an inline bound; typeParamBoundsOf reads WhereClauses on the premise that it does not")
	} else if !strings.Contains(err.Error(), "inline generic bounds are not supported") {
		t.Fatalf("inline bound rejected for another reason, so this premise is unverified: %v", err)
	}
	p, err := AnalyzeSource("probe.nomi",
		"interface Shout {\n  fn shout(v: self): String\n}\n\n"+
			"fn f<T>(x: T): String where T: Shout {\n  T.shout(x)\n}\n")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	fd := firstFuncDef(p, "f")
	if fd == nil {
		t.Fatal("no FuncDef for f")
	}
	if len(fd.TypeParams) != 1 || len(fd.TypeParams[0].Bounds) != 0 {
		t.Fatalf("expected one type parameter carrying no AST-level bound, got %#v", fd.TypeParams)
	}
	got := typeParamBoundsOf(fd.TypeParams, fd.WhereClauses)
	if len(got) != 1 || got[0].name != "T" || !slices.Equal(got[0].bounds, []string{"Shout"}) {
		t.Fatalf("typeParamBoundsOf = %#v, want one T bounded by Shout", got)
	}
}
