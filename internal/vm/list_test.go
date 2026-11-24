package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestListConstructionSharesTailAndChecksItsShape(t *testing.T) {
	pos := ir.At("list.nomi", 1, 1)
	fn := ir.NewFunc(pos, "prepend")
	tail := fn.AddParam(ir.NewSymbol("tail"), ir.ValContainer)
	fn.SetType(tail, ir.NewListType(ir.IntType))
	block := fn.NewBlock(pos, "entry")
	head := fn.NewTemp()
	block.Append(ir.NewInt(pos, head, 1))
	result := fn.NewTemp()
	fn.SetType(result, ir.NewListType(ir.IntType))
	block.Append(ir.NewMakeList(pos, result, []ir.Temp{head}, tail))
	block.SetTerm(ir.NewReturn(pos, result))
	mod := ir.NewModule("list")
	mod.AddFunc(fn)
	if err := ir.LintModule(mod); err != nil {
		t.Fatal(err)
	}
	machine := vm.New(mod, io.Discard)
	suffix := rtList(int64(2), int64(3))
	got, err := machine.Run("prepend", suffix)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.(*rt.List[any])
	if !ok || list.Tail != suffix || list.Len != 3 || rt.DisplayText(list) != "[1, 2, 3]" {
		t.Fatalf("constructed list does not share its typed tail: %#v", got)
	}
	got, err = machine.Run("prepend", (*rt.List[any])(nil))
	if err != nil || rt.DisplayText(got) != "[1]" {
		t.Fatalf("empty tail: %v, %v", got, err)
	}
	if _, err := machine.Run("prepend", int64(2)); err == nil || !strings.Contains(err.Error(), "not a List") {
		t.Fatalf("invalid tail: %v", err)
	}
}

// TestListDebugUsesElementDebugRatherThanInspect: an ir.Render at
// RenderDebug renders by Debug, the list and each element, and not by the
// assertion row's renderer (rt.RowText), which is one call away in the
// package this machine renders with and gives a wrong string with no
// error. The witness is a value whose row and Debug differ
// (internal/ir/render.go records which).
func TestListDebugUsesElementDebugRatherThanInspect(t *testing.T) {
	pos := ir.At("list.nomi", 1, 1)
	fn := ir.NewFunc(pos, "render")
	list := fn.AddParam(ir.NewSymbol("list"), ir.ValContainer)
	fn.SetType(list, ir.NewListType(ir.NewStructType(ir.NewSymbol("shapes.Point"))))
	block := fn.NewBlock(pos, "entry")
	result := fn.NewTemp()
	block.Append(ir.NewRenderDebug(pos, result, list))
	block.SetTerm(ir.NewReturn(pos, result))
	mod := ir.NewModule("list")
	mod.AddFunc(fn)
	if err := ir.LintModule(mod); err != nil {
		t.Fatal(err)
	}
	// A struct declared with its fields out of name order separates the
	// two, both for the list and for its element: its Debug keeps the
	// declaration order, `Point{y: 2, x: 1}`, and its row sorts the fields
	// by name. A String or a Decimal would not, since a row's leaves are
	// Debug's.
	point := rt.NewStructDesc("shapes.Point", []rt.FieldSpec{{Name: "y", Type: rt.SlotInt}, {Name: "x", Type: rt.SlotInt}}).
		Make(int64(2), int64(1))
	input := rtList(point)
	got, err := vm.New(mod, io.Discard).Run("render", input)
	if err != nil {
		t.Fatal(err)
	}
	const want = "[Point{y: 2, x: 1}]"
	if got != want {
		t.Fatalf("Debug = %q, want %q", got, want)
	}
	if rt.RowText(input) == want {
		t.Fatal("witness does not distinguish Debug from Inspect")
	}
}
