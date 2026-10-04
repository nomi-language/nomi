package vm

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestCompareList_RejectsNonListOperands(t *testing.T) {
	pos := ir.At("compare.nomi", 1, 1)
	f := ir.NewFunc(pos, "equal")
	a, b, out := f.NewTemp(), f.NewTemp(), f.NewTemp()
	n := ir.NewCompare(pos, out, ir.OpEq, ir.ValContainer, a, b)
	for _, bad := range []ir.Temp{a, b} {
		fr := testFrame(f, nil)
		fr.write(a, listOf([]any{int64(1)}))
		fr.write(b, listOf([]any{int64(1)}))
		fr.write(bad, int64(1))
		if err := new(Machine).compareInstr(fr, n); err == nil || !strings.Contains(err.Error(), "comparison's operands") {
			t.Fatalf("bad operand %v: %v", bad, err)
		}
	}
}
