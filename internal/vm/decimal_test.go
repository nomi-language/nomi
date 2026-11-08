package vm

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// decimalLit parses a decimal literal's token the way the machine's constants
// do, through rt.ParseDecimalLexeme.
func decimalLit(t *testing.T, lexeme string) rt.Decimal {
	t.Helper()
	d, err := rt.ParseDecimalLexeme(lexeme)
	if err != nil {
		t.Fatalf("malformed decimal literal %q: %v", lexeme, err)
	}
	return d
}

func TestDecimal_OperandAndLiteralBoundaries(t *testing.T) {
	pos := ir.At("decimal.nomi", 19, 1)
	f := ir.NewFunc(pos, "decimal")
	a, b, out := f.NewTemp(), f.NewTemp(), f.NewTemp()
	arithmetic := ir.NewArith(pos, out, ir.OpAdd, ir.DecimalArith(), a, b)
	comparison := ir.NewCompare(pos, out, ir.OpEq, ir.ValDecimal, a, b)
	for _, bad := range []ir.Temp{a, b} {
		fr := testFrame(f, nil)
		fr.write(a, decimalLit(t, "1.5d"))
		fr.write(b, decimalLit(t, "1.5d"))
		fr.write(bad, int64(1))
		if err := new(Machine).arith(fr, arithmetic); err == nil || !strings.Contains(err.Error(), "operand is int64") {
			t.Fatalf("arithmetic operand %v: %v", bad, err)
		}
		if err := new(Machine).compareInstr(fr, comparison); err == nil || !strings.Contains(err.Error(), "comparison's operands") {
			t.Fatalf("comparison operand %v: %v", bad, err)
		}
	}
	if _, err := constValue(ir.NewDecimal(pos, a, "1..5d")); err == nil {
		t.Fatal("malformed exact-text literal admitted")
	}
}

// Decimal `%` is exact, and a zero divisor is Int's division-by-zero fault
// at the operator's line.
func TestDecimal_RemainderAndItsZeroDivisor(t *testing.T) {
	pos := ir.At("decimal.nomi", 19, 1)
	f := ir.NewFunc(pos, "remainder")
	a, b, out := f.NewTemp(), f.NewTemp(), f.NewTemp()
	fr := testFrame(f, nil)
	fr.write(a, decimalLit(t, "10.5d"))
	fr.write(b, decimalLit(t, "3d"))
	n := ir.NewArith(pos, out, ir.OpRem, ir.DecimalArith(), a, b)
	if err := new(Machine).arith(fr, n); err != nil {
		t.Fatalf("10.5d %% 3d: %v", err)
	}
	if got, _ := fr.read(out); got.(rt.Decimal).Display() != "1.5" {
		t.Fatalf("10.5d %% 3d = %v, want 1.5", got)
	}
	fr.write(b, decimalLit(t, "0.0d"))
	err := new(Machine).arith(fr, n)
	var fault *Fault
	if !errors.As(err, &fault) || err.Error() != rt.DivByZeroError(19).Error() {
		t.Fatalf("remainder by zero: %v", err)
	}
}
