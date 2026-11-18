package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

const callDefaultsSlotsSrc = `interface Shape
    fn side(value: self) -> Int

    open fn area(value: self, scale: Int = 1, tail: Int = scale * 10) -> Int
        Shape.side(value) * scale + tail
    end
end

struct Ring
    r: Int
end

impl Shape for Ring
    fn side(g: Ring) -> Int
        g.r
    end

    fn area(g: Ring, scale: Int = g.r * 10, tail: Int) -> Int
        g.r * scale + tail
    end
end

fn via<T>(x: T) -> Int where T: Shape
    T.area(x)
end

fn both(ring: Ring) -> Int
    Shape.area(ring)
end

fn tail_only(ring: Ring) -> Int
    Shape.area(ring, 3)
end

fn main()
    _ = via(Ring{r: 1}) + both(Ring{r: 2}) + tail_only(Ring{r: 3})
end
`

// The record of a call that reaches an impl function names, per slot it
// leaves out, the declaration whose default fills it: the impl function's
// where it declares one, the interface function's where it does not (spec
// §12 *Item kinds*). A call that leaves out only slots the impl does not
// default keeps the interface's record, and so does a call on a type
// parameter, which reaches no impl the checker knows.
func TestCallDefaults_RecordEachSlotsDeclaration(t *testing.T) {
	fa, errs := checkEntry(t, callDefaultsSlotsSrc)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects the program: %v", errs)
	}
	byLine := map[int]*analysis.DefaultedCall{}
	for call, rec := range fa.CallDefaults {
		byLine[call.Line] = rec
	}
	if byLine[24] == nil || byLine[28] == nil {
		t.Fatalf("want records for T.area(x) and Shape.area(ring), got %v", byLine)
	}
	iface, _ := byLine[24].Decl.(*ast.InterfaceMethod)
	impl, _ := byLine[28].Decl.(*ast.FuncDef)
	if iface == nil || impl == nil || iface.Name != "area" || impl.Name != "area" {
		t.Fatalf("want T.area(x)'s record to name Shape's area and Shape.area(ring)'s Ring's, got %+v and %+v", byLine[24], byLine[28])
	}
	for _, w := range []struct {
		line  int
		decl  ast.Node
		slots []int
		from  []ast.Node
	}{
		{line: 24, decl: iface, slots: []int{1, 2}, from: []ast.Node{iface, iface}},
		{line: 28, decl: impl, slots: []int{1, 2}, from: []ast.Node{impl, iface}},
		{line: 32, decl: iface, slots: []int{2}, from: []ast.Node{iface}},
	} {
		rec := byLine[w.line]
		if rec == nil || rec.Decl != w.decl || len(rec.Slots) != len(w.slots) || len(rec.From) != len(w.from) {
			t.Fatalf("line %d: record %+v, want Decl %T over slots %v", w.line, rec, w.decl, w.slots)
		}
		for i, slot := range w.slots {
			if rec.Slots[i] != slot || rec.From[i] != w.from[i] {
				t.Errorf("line %d: slot %d from %T, want slot %d from %T", w.line, rec.Slots[i], rec.From[i], slot, w.from[i])
			}
			decl, params := rec.SlotSource(slot)
			if decl != w.from[i] || params[slot].Default == nil {
				t.Errorf("line %d: SlotSource(%d) is %T, with no default at the slot", w.line, slot, decl)
			}
		}
	}
}
