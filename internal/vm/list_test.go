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

func TestListDebugUsesElementDebugRatherThanInspect(t *testing.T) {
	pos := ir.At("list.nomi", 1, 1)
	fn := ir.NewFunc(pos, "render")
	list := fn.AddParam(ir.NewSymbol("list"), ir.ValContainer)
	fn.SetType(list, ir.NewListType(ir.StringType))
	block := fn.NewBlock(pos, "entry")
	result := fn.NewTemp()
	block.Append(ir.NewRenderDebug(pos, result, list))
	block.SetTerm(ir.NewReturn(pos, result))
	mod := ir.NewModule("list")
	mod.AddFunc(fn)
	if err := ir.LintModule(mod); err != nil {
		t.Fatal(err)
	}
	input := rtList("a\nb\t\"\\z")
	got, err := vm.New(mod, io.Discard).Run("render", input)
	if err != nil {
		t.Fatal(err)
	}
	const want = "[\"a\nb\t\\\"\\\\z\"]"
	if got != want {
		t.Fatalf("Debug = %q, want %q", got, want)
	}
	if rt.RowText(input) == want {
		t.Fatal("witness does not distinguish Debug from Inspect")
	}
}
