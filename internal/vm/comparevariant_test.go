package vm

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestCompareVariant_RejectsNonVariantOperands(t *testing.T) {
	pos := ir.At("compare.nomi", 1, 1)
	f := ir.NewFunc(pos, "equal")
	a, b, out := f.NewTemp(), f.NewTemp(), f.NewTemp()
	n := ir.NewCompare(pos, out, ir.OpEq, ir.ValVariant, a, b)
	for _, bad := range []ir.Temp{a, b} {
		fr := testFrame(f, nil)
		fr.write(a, noneValue)
		fr.write(b, noneValue)
		fr.write(bad, int64(1))
		if err := new(Machine).compareInstr(fr, n); err == nil || !strings.Contains(err.Error(), "comparison's operands") {
			t.Fatalf("bad operand %v: %v", bad, err)
		}
	}
}
