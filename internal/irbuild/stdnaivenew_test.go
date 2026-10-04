package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestDefaultedHostFnArmStillEvaluates is the POSITIVE CONTROL for
// stdCandidateFor's `case fd == nil && defaulted > 0` arm, and it exists because
// that arm has NO std members: a std function with a defaulted parameter is
// Nomi-bodied, so the declaration is the one authority for the default (as
// `NaiveDateTime.new` is). Declarations that match the arm's
// description but are generic or out of the subset land on `case generic:` or
// `case blocked:` first.
//
// A checked reason with no current user is a guard that cannot fire. So the arm
// is exercised DIRECTLY, as TestKeyCollapseReasonStillEvaluates does for its
// reason: the next `pub host fn` in std that grows a trailing default must still
// be refused rather than silently filled from a declaration the Go
// implementation never reads.
func TestDefaultedHostFnArmStillEvaluates(t *testing.T) {
	intType := func() ast.TypeExpr { return &ast.SimpleType{Name: "Int"} }
	params := []ast.Param{
		{Name: "a", TypeAnnotation: intType()},
		{Name: "b", TypeAnnotation: intType(), Default: &ast.Block{}},
	}
	// fd == nil is what makes it a `host fn`: there is no Nomi body to hang a
	// default on.
	c := stdCandidateFor("probe", "Thing", "", "", "make", params, intType(),
		true, false, false, nil, stdAnchors{})
	const want = "stdlib host function with a defaulted parameter"
	if c.f.why != want {
		t.Errorf("a host fn with a trailing default is classified %q, want %q; the arm "+
			"has no std member left, so this is the only thing keeping it armed",
			c.f.why, want)
	}

	// The negative direction, which is what would rot silently: the SAME
	// declaration with a body must NOT hit the arm. Otherwise the arm could be
	// refusing on `defaulted > 0` alone and every Nomi-bodied default in std
	// would be refused with it, including `NaiveDateTime.new`.
	withBody := stdCandidateFor("probe", "Thing", "", "", "make", params, intType(),
		true, false, false, &ast.FuncDef{Name: "make", Params: params,
			Body: &ast.Block{}}, stdAnchors{})
	if withBody.f.why == want {
		t.Errorf("a Nomi-bodied function with the same trailing default is also refused "+
			"%q; the arm is meant to turn on `fd == nil`, not on the default alone", want)
	}
}
