package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestWrittenImplMembers_RanksBlocksAsTheChecker holds the builder's index
// of impl functions to the checker's ranking of impl blocks
// (analysis.BestImplPrecedence), over blocks the front end wrote: a
// written `impl Debug`, a `derive Debug`, the name-only Debug of a
// module-level opaque type, and that of an opaque type declared in a body
// (FileAnalysis.BlockDebug), which registerBlockLocalDebug registers. The
// block-local one ranks as a module-level one does, below a written and a
// derived block.
func TestWrittenImplMembers_RanksBlocksAsTheChecker(t *testing.T) {
	const src = `import std::io

struct Written
    n: Int
end

impl Debug for Written
    fn inspect(_w: Written) -> String
        "written"
    end
end

struct Derived
    n: Int
end

derive Debug for Derived

opaque struct Universal
    n: Int
end

fn main()
    opaque struct Local
        n: Int
    end

    io.print(Debug.inspect(Local{n: 1}))
    io.print(Debug.inspect(Written{n: 1}))
    io.print(Debug.inspect(Derived{n: 1}))
    io.print(Debug.inspect(Universal{n: 1}))
end
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects this program, so nothing below asserts anything: %v", err)
	}
	m := p.Modules[0]
	blocks := map[string]*ast.ImplBlock{}
	for _, n := range m.Nodes {
		ib, ok := n.(*ast.ImplBlock)
		if !ok || analysis.TypeExprBaseName(ib.Interface) != "Debug" {
			continue
		}
		blocks[analysis.TypeExprBaseName(ib.Receiver)] = ib
	}
	for _, ib := range m.FA.BlockDebug {
		blocks["Local"] = ib
	}
	for _, name := range []string{"Written", "Derived", "Universal", "Local"} {
		if blocks[name] == nil {
			t.Fatalf("the front end wrote no Debug block for %s", name)
		}
	}
	if !isSynthesized(blocks["Local"]) {
		t.Errorf("the universal Debug of a block-local type is outside the synthesized band, so implDef.synth calls it written")
	}
	site := func(name string) implMemberSite { return implMemberSite{block: blocks[name]} }
	for _, c := range []struct {
		sites []string
		want  string
	}{
		{[]string{"Local", "Written"}, "Written"},
		{[]string{"Local", "Derived"}, "Derived"},
		{[]string{"Universal", "Derived"}, "Derived"},
		{[]string{"Universal", "Derived", "Written"}, "Written"},
	} {
		var sites []implMemberSite
		for _, name := range c.sites {
			sites = append(sites, site(name))
		}
		got := writtenImplMembers(sites)
		if len(got) != 1 || got[0].block != blocks[c.want] {
			t.Errorf("among %v the builder keeps %d sites, want only %s's", c.sites, len(got), c.want)
		}
		if want := analysis.ImplPrecedence(blocks[c.want]); analysis.ImplPrecedence(got[0].block) != want {
			t.Errorf("among %v the builder keeps a block the checker ranks differently", c.sites)
		}
	}
	if got := writtenImplMembers([]implMemberSite{site("Local"), site("Universal")}); len(got) != 2 {
		t.Errorf("a block-local and a module-level universal Debug rank alike, but the builder keeps %d of the two", len(got))
	}
}
