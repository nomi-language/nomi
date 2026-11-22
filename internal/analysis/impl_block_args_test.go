package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

const implBlockArgsSrc = `interface Gets<T>
    fn get(s: self) -> T
end

interface Tagged<T>
    fn tag(s: self) -> Int
end

struct Box
    n: Int
end

impl Gets for Box
    fn get(b: Box) -> Int
        b.n
    end
end

impl Tagged for Box
    fn tag(b: Box) -> Int
        b.n
    end
end

interface Shown
    fn shown(s: self) -> String
end

impl Shown for Box
    fn shown(Box) -> String
        "box"
    end
end
`

// TestImplBlockTypeArgs_SolvedAndOpen: a header that writes no type
// arguments has them recorded from its items' signatures (Gets's T is
// Int), and a parameter no item names is marked open (Tagged's T), which
// the IR builder refuses rather than reading the interface's own
// parameter as an argument.
func TestImplBlockTypeArgs_SolvedAndOpen(t *testing.T) {
	fa, nodes := checkNodes(t, implBlockArgsSrc)
	blocks := map[string]*ast.ImplBlock{}
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok {
			name, _ := ib.Interface.(*ast.SimpleType)
			if name != nil {
				blocks[name.Name] = ib
			}
		}
	}
	gets := ImplBlockTypeArgs(fa, blocks["Gets"])
	if gets == nil || len(gets.Args) != 1 || gets.Args[0].String() != "Int" || len(gets.Open) != 0 {
		t.Fatalf("Gets for Box: want Args [Int] and nothing open, got %+v", gets)
	}
	tagged := ImplBlockTypeArgs(fa, blocks["Tagged"])
	if tagged == nil || len(tagged.Open) != 1 || !tagged.Open[0] {
		t.Fatalf("Tagged for Box: want its T open, got %+v", tagged)
	}
}

// TestImplBlockTypeArgs_VerifierHoldsTheRecord: the verifier passes the
// blocks while the record is there and reports each when it is not.
func TestImplBlockTypeArgs_VerifierHoldsTheRecord(t *testing.T) {
	fa, nodes := checkNodes(t, implBlockArgsSrc)
	count := func() int {
		n := 0
		for _, v := range VerifyElaboration(fa, nodes) {
			if v.Category == ElabImplArgsUnrecorded {
				n++
			}
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("recorded impl blocks are reported %d times", n)
	}
	for _, byIface := range fa.ImplBlocks {
		for iface, blocks := range byIface {
			for _, b := range blocks {
				if iface == "Gets" || iface == "Tagged" {
					b.Args = nil
				}
			}
		}
	}
	if n := count(); n != 2 {
		t.Fatalf("want both generic blocks reported once their arguments are gone, got %d", n)
	}
	blocks := 0
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Interface != nil {
			blocks++
		}
	}
	fa.ImplBlocks = nil
	missing := 0
	for _, v := range VerifyElaboration(fa, nodes) {
		if v.Category == ElabImplIfaceUnrecorded {
			missing++
		}
	}
	if missing != blocks {
		t.Fatalf("want each of the %d impl blocks reported once no record is left, got %d", blocks, missing)
	}
}

// TestImplBlockTypeArgs_EveryBlockNamesItsInterface: every impl block has
// a record of the interface it implements, by its declaration's name and
// origin, one of an interface with no type parameters included. The
// derived and universal blocks std and the corpus hold are held by
// TestElaborationGate (vmhost).
func TestImplBlockTypeArgs_EveryBlockNamesItsInterface(t *testing.T) {
	fa, nodes := checkNodes(t, implBlockArgsSrc)
	got := map[string]*ImplTypeArgs{}
	for _, n := range nodes {
		ib, ok := n.(*ast.ImplBlock)
		if !ok || ib.Interface == nil {
			continue
		}
		rec := ImplBlockTypeArgs(fa, ib)
		if rec == nil {
			t.Fatalf("impl %s for %s has no record", ib.Interface.TypeString(), ib.Receiver.TypeString())
		}
		got[ib.Interface.TypeString()] = rec
	}
	if r := got["Shown"]; r == nil || r.Interface != "Shown" || r.Origin != fa.Origin || !r.Std().IsZero() || r.Args != nil {
		t.Fatalf("Shown for Box: want the program's Shown with no arguments, got %+v", r)
	}
	if r := got["Gets"]; r == nil || r.Interface != "Gets" || len(r.Args) != 1 {
		t.Fatalf("Gets for Box: want Gets at one argument, got %+v", r)
	}
}
