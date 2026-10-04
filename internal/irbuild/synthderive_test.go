package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// preludeGenFor builds a gen over one source, far enough that preludeByName is
// loadable and typeOf answers. Local rather than shared because the tests below
// need the GEN and not a Program.
func preludeGenFor(t *testing.T, src string) (*gen, *Program) {
	t.Helper()
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejected this source, so nothing below is reached; "+
			"repoint the test rather than letting it pass: %v", err)
	}
	reg := buildTypeRegistry(p)
	files := buildFileIndex(p, reg)
	gens := make([]*gen, len(p.Modules))
	for i := range p.Modules {
		gens[i] = newGen(&p.Modules[i], unitPackage(i), stdlibLowering(), files, i, reg)
	}
	reg.gens = gens
	for i := range gens {
		reg.ensureTypes(i)
	}
	files.resolveSignatures(gens)
	for _, g := range gens {
		g.declareFuncs()
	}
	return gens[0], p
}

// genericTypeNamed is the first *ast.GenericType spelled `want` under one
// top-level node, at a real-source position or a synth-band one as asked.
//
// Both halves of the test below need a node the OTHER half must not match, so
// the band is part of the search rather than checked afterwards.
func genericTypeNamed(nodes []ast.Node, want string, synth bool) *ast.GenericType {
	var found *ast.GenericType
	var walkType func(ast.TypeExpr)
	walkType = func(te ast.TypeExpr) {
		if found != nil || te == nil {
			return
		}
		switch tt := te.(type) {
		case *ast.GenericType:
			if tt.Name == want && analysis.IsSynthesizedLine(tt.Line) == synth {
				found = tt
				return
			}
			for _, p := range tt.Params {
				walkType(p)
			}
		case *ast.FuncType:
			for _, p := range tt.Params {
				walkType(p)
			}
			walkType(tt.Return)
		}
	}
	visitFunc := func(fd *ast.FuncDef) {
		for i := range fd.Params {
			walkType(fd.Params[i].TypeAnnotation)
		}
		walkType(fd.ReturnTypeExpr)
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			visitFunc(d)
		case *ast.ImplBlock:
			for _, item := range d.Items {
				if fd, isFunc := item.(*ast.FuncDef); isFunc {
					visitFunc(fd)
				}
			}
		}
		if found != nil {
			return found
		}
	}
	return found
}

// The synth-band gate is a FENCE, and this is what makes it checkable rather
// than merely stated: synthPreludeAnchor must DECLINE for a node at a real
// source position.
//
// Without this test, mutating the gate to always proceed changes no answer
// anywhere — preludeByName is shadow-safe, so a name-resolved real-source
// `Result` gives the same kind the position route gives — and the claim "nothing
// about real source changes" would be unfalsifiable. Both halves are asserted so
// the test cannot pass by declining everything.
//
// The positive half also carries a REPOINT instruction rather than a
// pass-by-silence: if the position route ever starts answering for a synth-band
// node, that is a front-end change and this whole arm should be DELETED, not
// loosened.
func TestSynthPreludeAnchorIsGatedOnTheSynthBand(t *testing.T) {
	const src = `import std/json.{FromJson, Json}

struct User {
  label: String
}

derive FromJson for User

fn f(j: Json): Result<Int, String> {
  _ = j
  Ok(1)
}

fn main() {
  _ = f(Json.Null)
}
`
	g, p := preludeGenFor(t, src)

	real := genericTypeNamed(p.Modules[0].Nodes, "Result", false)
	if real == nil {
		t.Fatal("no real-source Result<Int, String> in the tree, so the negative half is vacuous")
	}
	if _, viaPosition, ok := g.preludeAt(real.Line, real.Col); !ok || viaPosition == nil {
		t.Fatal("the position route does not answer for a real-source Result, so this test would " +
			"be asserting the wrong thing about which route is load-bearing")
	}
	if a, claimed := g.synthPreludeAnchor(real); claimed {
		t.Fatalf("synthPreludeAnchor answered %v for a REAL-SOURCE node; the band gate is what "+
			"keeps this arm off every position the front end does record", a.spec.nomi)
	}

	synth := genericTypeNamed(p.Modules[0].Nodes, "Result", true)
	if synth == nil {
		t.Fatal("derive FromJson synthesized no Result<...> annotation, so the positive half is " +
			"vacuous; the derive's shape changed and this test must be repointed")
	}
	if _, _, ok := g.preludeAt(synth.Line, synth.Col); ok {
		t.Fatal("the position route now ANSWERS for a synth-band node. That is a front-end change: " +
			"either the synth band entered fa.References or derive synthesis stopped using it. " +
			"synthderive.go's reason for existing is gone and the arm should be DELETED, not loosened")
	}
	a, claimed := g.synthPreludeAnchor(synth)
	if !claimed || a == nil || a.spec.nomi != "Result" {
		t.Fatalf("synthPreludeAnchor did not resolve the synthesized Result: claimed=%v anchor=%v",
			claimed, a)
	}
	if k := g.typeOf(synth); k == kindInvalid {
		t.Fatal("typeOf still answers kindInvalid for the synthesized annotation, so the fallback " +
			"is wired into synthPreludeAnchor but not into preludeTypeOf")
	}
}
