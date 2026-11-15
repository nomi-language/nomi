package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A literal zero divisor of Int's and Decimal's `/` and `%`, and of the std
// functions that divide, is a compile error at the divisor (spec §8
// *Integer division and remainder*).

func zeroDivisorErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.ZeroDivisorCode {
			out = append(out, e)
		}
	}
	return out
}

func TestZeroDivisor_Rejected(t *testing.T) {
	// divisor is the divisor as written, where the error is reported, and
	// literal is how the message spells it.
	for _, tc := range []struct{ name, expr, divisor, literal string }{
		{"int divide", "x / 0", "0", "0"},
		{"int remainder", "x % 0", "0", "0"},
		{"parenthesized", "x / (0)", "(0)", "0"},
		{"negative zero", "x / -0", "-0", "-0"},
		{"negative parenthesized", "x % (-0)", "(-0)", "-0"},
		{"hex zero", "x / 0x0", "0x0", "0x0"},
		{"underscored zero", "x / 0_0", "0_0", "0_0"},
		{"refined dividend", "p / 0", "0", "0"},
		{"decimal divide", "d / 0d", "0d", "0d"},
		{"decimal remainder", "d % 0.00d", "0.00d", "0.00d"},
		{"negative decimal zero", "d / -0.0d", "-0.0d", "-0.0d"},
		{"Int.modulo", "Int.modulo(x, 0)", "0", "0"},
		{"Int.floor_div", "Int.floor_div(x, 0)", "0", "0"},
		{"Int.floor_div piped", "x |> Int.floor_div(0)", "0", "0"},
		{"Int.modulo named", "Int.modulo(x, y: 0)", "0", "0"},
		{"Int.divide", "Int.divide(x, 0)", "0", "0"},
		{"Int.remainder", "Int.remainder(x, 0)", "0", "0"},
		{"Decimal.divide", "Decimal.divide(d, 0d)", "0d", "0d"},
		{"Decimal.divide rounded", "Decimal.divide(d, 0d, 2, RoundingMode.HalfEven)", "0d", "0d"},
		{"Decimal.remainder", "Decimal.remainder(d, 0d)", "0d", "0d"},
		{"Decimal.divide_exact", "Decimal.divide_exact(d, 0.0d)", "0.0d", "0.0d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn f(x: Int, d: Decimal, p: PositiveInt)\n    dbg " + tc.expr + "\nend\n"
			if strings.Contains(tc.expr, "RoundingMode") {
				src = "import std::decimal.RoundingMode\n\n" + src
			}
			_, errs := checkSourceWithStdlib(src)
			errs = withoutUnusedBindingErrors(errs)
			got := zeroDivisorErrors(errs)
			if len(got) != 1 {
				t.Fatalf("%s: the front end admits a literal zero divisor, or reports it %d times; errors: %v", tc.expr, len(got), errs)
			}
			want := "division by zero: the divisor is the literal " + tc.literal
			if got[0].Message != want {
				t.Errorf("message = %q, want %q", got[0].Message, want)
			}
			if col := len("    dbg ") + strings.LastIndex(tc.expr, tc.divisor) + 1; got[0].Col != col {
				t.Errorf("reported at col %d, want %d (the divisor %s)", got[0].Col, col, tc.divisor)
			}
			if len(got[0].Hints) != 1 || !strings.Contains(got[0].Hints[0], "still traps") || !strings.Contains(got[0].Hints[0], "NonZero") {
				t.Errorf("hints = %q, want one naming the run-time trap and the NonZero type", got[0].Hints)
			}
			if len(errs) != 1 {
				t.Errorf("other errors beside the zero divisor: %v", errs)
			}
		})
	}
}

func TestZeroDivisor_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, expr string }{
		{"float divide", "f / 0.0"},
		{"float remainder", "f % -0.0"},
		{"variable divisor", "x / z"},
		{"once divisor", "x / zero"},
		{"expression divisor", "x / (z - z)"},
		{"nonzero divisor", "x / 2"},
		{"zero dividend", "0 / x"},
		{"Int.modulo variable", "Int.modulo(x, z)"},
		{"Int.modulo zero dividend", "Int.modulo(0, x)"},
		{"interface call", "Divide.divide(x, 0)"},
		{"user operator", "m / 0"},
		{"user function", "split(x, 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `struct Money
    cents: Int
end

impl Divide<Int, Money> for Money
    fn divide(lhs: Money, rhs: Int) -> Money
        Money{cents: if rhs == 0 then 0 else lhs.cents / rhs}
    end
end

fn split(a: Int, b: Int) -> Int
    a + b
end

once zero = 0

fn f(x: Int, z: Int, f: Float, m: Money)
    dbg ` + tc.expr + `
end
`
			_, errs := checkSourceWithStdlib(src)
			if errs = withoutUnusedBindingErrors(errs); len(errs) != 0 {
				t.Fatalf("%s: errors %v", tc.expr, errs)
			}
		})
	}
}
