package vm

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// TestInt_AZeroDivisorIsTheBackstopFaultWhateverTheDividend: `/` and `%`
// take a NonZeroInt, so a checked program never divides an Int by zero; the
// machine still checks, and a zero divisor reaching an ir.Arith is the
// division-by-zero fault at the operator's line, for a dividend of any size.
func TestInt_AZeroDivisorIsTheBackstopFaultWhateverTheDividend(t *testing.T) {
	big, ok := rt.ParseIntValue("1000000000000000000000000000000")
	if !ok {
		t.Fatal("10^30 does not parse")
	}
	for _, op := range []ir.ArithOp{ir.OpDiv, ir.OpRem} {
		for _, dividend := range []rt.Value{int64(7), big} {
			pos := ir.At("main.nomi", 5, 1)
			f := ir.NewFunc(pos, "divide")
			a, b, out := f.NewTemp(), f.NewTemp(), f.NewTemp()
			fr := testFrame(f, nil)
			fr.write(a, dividend)
			fr.write(b, int64(0))
			err := new(Machine).arith(fr, ir.NewArith(pos, out, op, ir.IntArith(ir.OverflowExact), a, b))
			if err == nil || err.Error() != rt.DivByZeroError(5).Error() {
				t.Fatalf("%v by zero of %v: %v", op, dividend, err)
			}
		}
	}
}
