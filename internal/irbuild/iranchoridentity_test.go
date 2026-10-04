package irbuild

// `irTypeOf` interns an `*ir.Type` on the `kind` value, and a named type's
// kind is its `*typeDef` pointer. The std anchor families (`opaqueByDecl`,
// `preludeByDecl`, `stdEnumByDecl`, `stdStructByDecl`, `stdMarkerByDecl`) make
// that pointer process-wide: without the anchor arm in `buildTypes`, the module
// that declares `Duration` would mint its own shell and hold a different
// pointer from every module that merely mentions it.
//
// This file checks the anchor's output, not its rule: whatever
// `opaqueSpec.matches` accepted is the process-wide def afterwards.
// `TestStdStructShapeCheckRejectsADriftedDeclaration` and its siblings check
// the rule.

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestIRAnchoredTypeHasOneIdentityAcrossModules checks that the declaring
// module's `Duration` and a mentioning module's `Duration` are one
// `*typeDef`, one `kind`, and therefore one `*ir.Type`, while the shell
// `buildTypes` mints for an unanchored declaration is a second one.
//
// It reasons about one gen's table, because `ir.Table` is per gen by
// construction; the cross-gen invariant it rests on is `kind` equality, which
// is asserted on `*typeDef` pointers.
func TestIRAnchoredTypeHasOneIdentityAcrossModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	lib := std.Load()
	const declaring, nomi = "duration", "Duration"
	nodes, fa := lib.Nodes[declaring], lib.Files[declaring]
	if len(nodes) == 0 || fa == nil {
		t.Fatalf("std/%s has no analyzed nodes; the fixture this test reasons about is gone", declaring)
	}
	spec := -1
	for i := range opaqueSpecs {
		if opaqueSpecs[i].nomi == nomi {
			spec = i
		}
	}
	if spec < 0 {
		t.Fatalf("no opaque spec named %s", nomi)
	}
	shared := opaqueDefs()[spec]

	// The DECLARING module. Its own declaration must adopt the process-wide
	// def rather than getting a shell.
	var decl *ast.TypeDef
	for _, n := range nodes {
		if td, isType := n.(*ast.TypeDef); isType && td.Name == nomi {
			decl = td
		}
	}
	if decl == nil {
		t.Fatalf("std/%s does not declare %s", declaring, nomi)
	}
	dg := irAnchorGen(fa, nodes)
	if got := dg.opaqueDeclared(decl); got != spec {
		t.Fatalf("opaqueDeclared answered %d for the declaration std/%s carries, want spec %d. "+
			"buildTypes would mint a private shell, and the type would be one Nomi type with "+
			"two pointers", got, declaring, spec)
	}
	dg.buildTypes(nodes)
	if dg.types[nomi] != shared {
		t.Fatalf("the declaring module's %s is %p, the process-wide def is %p", nomi, dg.types[nomi], shared)
	}

	// A MENTIONING module reaches the same pointer through the name door.
	mentioned, mg := irAnchorMentioningGen(t, nomi)
	if mentioned != shared {
		t.Fatalf("a mentioning module resolves %s to %p, the declaring module to %p; "+
			"one Nomi type, two pointers", nomi, mentioned, shared)
	}
	if named(dg.types[nomi]) != named(mentioned) {
		t.Fatal("the two gens' kinds for one anchored type are not equal, so ir.Table's " +
			"'kind equality IS IR type identity' says nothing about this type across gens")
	}

	// The IR half, within one table because a Table is per gen: the anchored
	// pointer interns to one *ir.Type however it is reached, and the shell an
	// unanchored declaration would have produced interns to another.
	tbl := mg.irTypes()
	anchored := mg.irTypeOf(named(mentioned))
	if anchored == nil {
		t.Fatal("the anchored kind has no ir.Type")
	}
	if again := mg.irTypeOf(named(shared)); again != anchored {
		t.Fatalf("two routes to one anchored type interned two ir.Types: %p and %p", anchored, again)
	}
	before := tbl.Types()
	shell := &typeDef{nomi: nomi, decl: decl, line: decl.LineNum(), isDistinct: true, lowerable: true}
	if unanchored := mg.irTypeOf(named(shell)); unanchored == anchored {
		t.Fatal("the shell buildTypes mints for an UNANCHORED declaration interned to the " +
			"same ir.Type as the process-wide def, so this test cannot distinguish the two " +
			"cases and proves nothing about the anchor")
	}
	if tbl.Types() != before+1 {
		t.Fatalf("interning the shell moved the table from %d types to %d, want %d: the "+
			"table did not treat it as a second type", before, tbl.Types(), before+1)
	}
}

// irAnchorGen is a gen over one module's analysis and nodes, with the maps
// buildTypes needs and nothing else.
func irAnchorGen(fa *analysis.FileAnalysis, nodes []ast.Node) *gen {
	return &gen{fa: fa, nodes: nodes, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}}
}

// irAnchorMentioningGen resolves an opaque type's shared def from a module that
// MENTIONS it without declaring it, and returns that gen too.
func irAnchorMentioningGen(t *testing.T, nomi string) (*typeDef, *gen) {
	t.Helper()
	src := "import std/io\nimport std/duration.Duration\n\nfn hold(d: " + nomi + "): " + nomi + " {\n  d\n}\n\nfn main() {\n  io.print(\"x\")\n}\n"
	p, err := AnalyzeSource("iranchor_probe", src)
	if err != nil {
		t.Fatalf("analyzing the mentioning program: %v", err)
	}
	g := irAnchorGen(p.Modules[0].FA, p.Modules[0].Nodes)
	def, anchored := g.opaqueNamed(nomi)
	if !anchored {
		t.Fatalf("a module that imports and annotates %s has no anchor for it", nomi)
	}
	return def, g
}
