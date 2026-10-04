package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestIterProtocolShapeMatchesStdSource asserts the anchor IS built.
//
// loadIter refuses every `Iter` call when the declaration's shape disagrees
// with the push protocol. That is the right failure, but a silent one: every
// `Iter` call is declined and nothing names the cause. This guard turns a std
// edit that breaks the shape into a named failure.
func TestIterProtocolShapeMatchesStdSource(t *testing.T) {
	p, err := AnalyzeSource("main", "fn main() {\n  _ = 1\n}\n")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	fa := p.Entry().FA
	if fa == nil || fa.ModuleScope == nil {
		t.Fatal("the entry has no module scope, so no anchor can be established")
	}
	sym := fa.ModuleScope.Lookup("Iter")
	if sym == nil || sym.Resolved == nil {
		t.Fatal("`Iter` is not reachable through the prelude")
	}
	decl, isDecl := sym.Resolved.Node.(*ast.InterfaceDef)
	if !isDecl {
		t.Fatalf("`Iter` resolves to %T, not an interface declaration", sym.Resolved.Node)
	}
	if !iterProtocolMatches(decl) {
		t.Fatal("std/iter.nomi's `Iter` does not have the push-protocol shape rt/seq.go implements; " +
			"rt.Seq's closure field and Iter.count's O(1) answer are both stated against it")
	}
}
