package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// withheldBlock is an impl of TryFrom for Code holding one function named
// fn, withheld (ast.ImplBlock.Withhold) as synthesizeRefinements withholds
// a refinement's blocks.
func withheldBlock(fn string) *ast.ImplBlock {
	ib := &ast.ImplBlock{
		Interface: &ast.SimpleType{Name: "TryFrom"},
		Receiver:  &ast.SimpleType{Name: "Code"},
		Items:     []ast.Node{&ast.FuncDef{Name: fn}},
	}
	ib.Withhold()
	return ib
}

// TestFingerprint_WithheldBlockRendersItsSetAsideParts holds the
// fingerprints to a withheld impl block: walking one does not reach into
// its unexported field (which panicked on the FuncDef there), and what it
// set aside is rendered through ImplBlock.WithheldParts, so two withheld
// blocks that differ only there fingerprint apart, and one withheld block
// apart from a plain empty inherent block. A released block's fingerprint
// is what the impl fingerprints (seenImpls) read.
func TestFingerprint_WithheldBlockRendersItsSetAsideParts(t *testing.T) {
	a, b := withheldBlock("try_from"), withheldBlock("other")
	plain := &ast.ImplBlock{Receiver: &ast.SimpleType{Name: "Code"}}
	fa, fb, fp := interfaceFingerprint([]ast.Node{a}), interfaceFingerprint([]ast.Node{b}), interfaceFingerprint([]ast.Node{plain})
	if fa == fb {
		t.Errorf("withheld blocks with different items fingerprint alike: %s", fa)
	}
	if fa == fp {
		t.Errorf("a withheld block fingerprints as a plain inherent block: %s", fa)
	}
	if implsFingerprint([]ast.Node{a}) == implsFingerprint([]ast.Node{b}) {
		t.Errorf("withheld blocks with different items have the same impls fingerprint")
	}
	if got := seenImpls(nil, []ast.Node{a}, func(string) string { return "" }); len(got) != 0 {
		t.Errorf("an unreleased block is seen as an interface impl: %v", got)
	}
	a.Release(true)
	got := seenImpls(nil, []ast.Node{a}, func(string) string { return "" })
	if len(got) != 1 {
		t.Fatalf("a released block is not seen as an interface impl: %v", got)
	}
	if released := interfaceFingerprint([]ast.Node{a}); released == fa {
		t.Errorf("releasing a block leaves its fingerprint as it was: %s", released)
	}
}
