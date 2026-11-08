package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A Nomi-bodied default beside a row's functions leaves the anchor as it
// was: the default takes no table slot and is lowered in its declaring
// module (stdifacedefault.go), so Display keeps rt's table. A host-backed
// default has no body to lower there and no impl writes it, so it drops the
// anchor, as any other shape drift does.
func TestStdIface_NomiDefaultKeepsTheAnchor(t *testing.T) {
	g := stdIfaceGen(t, stdIfaceProbeSource)
	sym := resolveSymbol(g.fa.ModuleScope.Lookup("Display"))
	if sym == nil {
		t.Fatal("Display is not in the probe's module scope")
	}
	decl, ok := sym.Node.(*ast.InterfaceDef)
	if !ok {
		t.Fatal("Display does not resolve to its declaration")
	}
	anchors := stdAnchorsOf(g.fa)
	spec := &stdIfaceSpecs[0]
	if spec.nomi != "Display" || !spec.matches(decl, anchors) {
		t.Fatal("std's own Display does not anchor, so the cases below say nothing")
	}
	shout := ast.InterfaceMethod{
		Name:   "shout",
		Params: []ast.Param{{Name: "value", TypeAnnotation: &ast.SelfType{}}},
	}
	withDefault := *decl
	nomi := shout
	nomi.Body, nomi.Open = &ast.Block{}, true
	withDefault.Methods = append([]ast.InterfaceMethod{nomi}, decl.Methods...)
	if !spec.matches(&withDefault, anchors) {
		t.Error("a Nomi-bodied default ahead of to_string dropped Display's anchor")
	}
	withHost := *decl
	host := shout
	host.Extern = true
	withHost.Methods = append(append([]ast.InterfaceMethod{}, decl.Methods...), host)
	if spec.matches(&withHost, anchors) {
		t.Error("a host-backed default kept Display's anchor")
	}
}
