package vm

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestRTIdentity_ConstructionAndProjectionShareTheirOperands holds three
// sharing properties over the machine's own rt values, where they are
// observable: `Run` converts its result to a `value`, so a caller outside the
// package sees an equal value and not the same one.
//
//   - a list construction's typed tail is the operand, not a copy;
//   - a list suffix projection is the operand's own cell;
//   - `try`'s propagated operand is the operand itself.
func TestRTIdentity_ConstructionAndProjectionShareTheirOperands(t *testing.T) {
	pos := ir.At("identity.nomi", 1, 1)
	mod := ir.NewModule("identity.nomi")

	prepend := ir.NewFunc(pos, "prepend")
	tail := prepend.AddParam(ir.NewSymbol("tail"), ir.ValContainer)
	prepend.SetType(tail, ir.NewListType(ir.IntType))
	b := prepend.NewBlock(pos, "entry")
	head := prepend.NewTemp()
	b.Append(ir.NewInt(pos, head, 1))
	built := prepend.NewTemp()
	prepend.SetType(built, ir.NewListType(ir.IntType))
	b.Append(ir.NewMakeList(pos, built, []ir.Temp{head}, tail))
	b.SetTerm(ir.NewReturn(pos, built))
	mod.AddFunc(prepend)

	suffix := ir.NewFunc(pos, "suffix")
	xs := suffix.AddParam(ir.NewSymbol("xs"), ir.ValContainer)
	sb := suffix.NewBlock(pos, "entry")
	rest := suffix.NewTemp()
	sb.Append(ir.NewProjSuffix(pos, rest, xs, 1, ir.ValContainer))
	sb.SetTerm(ir.NewReturn(pos, rest))
	mod.AddFunc(suffix)

	unwrap := ir.NewFunc(pos, "unwrap")
	operand := unwrap.AddParam(ir.NewSymbol("operand"), ir.ValVariant)
	ub := unwrap.NewBlock(pos, "entry")
	ub.Append(ir.NewTry(pos, operand, "try operand"))
	answer := ir.NewInt(pos, unwrap.NewTemp(), 42)
	ub.Append(answer)
	ub.SetTerm(ir.NewReturn(pos, answer.Dst()))
	mod.AddFunc(unwrap)

	m := New(mod, io.Discard)
	shared := listOf([]any{int64(2), int64(3)})
	got, err := m.run("prepend", []any{shared})
	if err != nil {
		t.Fatal(err)
	}
	if xs, ok := got.(*list); !ok || xs.Tail != shared || xs.Len != 3 {
		t.Fatalf("list construction copied its tail: %#v", got)
	}

	cell := cons(int64(2), nil)
	whole := cons(int64(1), cell)
	if got, err := m.run("suffix", []any{whole}); err != nil || got != any(cell) {
		t.Fatalf("suffix lost sharing: %v, %v", got, err)
	}

	for _, v := range []any{noneValue, errValue(int64(7))} {
		got, err := m.run("unwrap", []any{v})
		if err != nil {
			t.Fatal(err)
		}
		if got != v {
			t.Fatalf("try propagated a copy of its operand: %#v", got)
		}
	}
}
