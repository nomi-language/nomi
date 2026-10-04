package irbuild

import (
	"testing"
)

// The bare `once` read is an edge in the graph, asserted on the INDEX rather
// than through lowering, because a lowering could decline while the edge was
// still missing. Here left.nomi reads right.nomi's `once` bare and
// nothing else connects them, so the edge is present or it is not.
func TestSiblingOnce_BareReadIsAReferenceEdge(t *testing.T) {
	p, err := Analyze(fixture("once_sibling_cycle/main.nomi"))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	unit := map[string]int{}
	for i := range p.Modules {
		unit[p.Modules[i].Name] = i
	}
	left, hasLeft := unit["left"]
	right, hasRight := unit["right"]
	if !hasLeft || !hasRight {
		t.Fatalf("fixture no longer has both files: %v", unit)
	}
	x := buildFileIndex(p, buildTypeRegistry(p))
	if !x.reaches[left][right] {
		t.Fatal("left reads right's `once` through a bare selective import, and the " +
			"reference graph records no edge — so the import cycle is invisible")
	}
	// And the other direction, through the ordinary call, so the pair really is
	// a cycle and the refusal above is not resting on one edge.
	if !x.reaches[right][left] {
		t.Fatal("right calls left's function and the graph records no edge")
	}
}
