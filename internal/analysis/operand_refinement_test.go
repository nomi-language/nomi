package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// std's `/` and `%` on Int, Decimal and Duration take a NonZero divisor, and
// Int's `<<<` and `>>>` a NonNegativeInt count (spec §8 *Integer division
// and remainder*, *Bitwise operators*). A divisor or count passes when its
// type or its range proves the refinement, and is an error at the operand
// otherwise.

func operandErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.ZeroDivisorCode || e.Code == analysis.UncheckedOperandCode {
			out = append(out, e)
		}
	}
	return out
}

const operandRefinementSource = `import std::duration.{Duration, NonZeroDuration}

fn f(x: Int, z: Int, d: Decimal, e: Decimal, p: PositiveInt, n: NonZeroInt, w: NonNegativeInt, span: Duration, step: NonZeroDuration)
    dbg %s
end
`

func TestOperandRefinement_LiteralZeroDivisor(t *testing.T) {
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
		{"Int.divide", "Int.divide(x, 0)", "0", "0"},
		{"Int.remainder", "Int.remainder(x, 0)", "0", "0"},
		{"Decimal.remainder", "Decimal.remainder(d, 0d)", "0d", "0d"},
		{"interface call", "Divide.divide(x, 0)", "0", "0"},
		{"Duration by Int", "span / 0", "0", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(operandRefinementSource, "%s", tc.expr, 1)
			_, errs := checkSourceWithStdlib(src)
			errs = withoutUnusedBindingErrors(errs)
			got := operandErrors(errs)
			if len(got) != 1 {
				t.Fatalf("%s: the front end admits a literal zero divisor, or reports it %d times; errors: %v", tc.expr, len(got), errs)
			}
			if got[0].Code != analysis.ZeroDivisorCode {
				t.Errorf("code = %q, want %q", got[0].Code, analysis.ZeroDivisorCode)
			}
			want := "division by zero: the divisor is the literal " + tc.literal
			if got[0].Message != want {
				t.Errorf("message = %q, want %q", got[0].Message, want)
			}
			if col := len("    dbg ") + strings.LastIndex(tc.expr, tc.divisor) + 1; got[0].Col != col {
				t.Errorf("reported at col %d, want %d (the divisor %s)", got[0].Col, col, tc.divisor)
			}
			if len(got[0].Hints) != 1 || !strings.Contains(got[0].Hints[0], "checked first") || !strings.Contains(got[0].Hints[0], "NonZero") {
				t.Errorf("hints = %q, want one naming the check and the NonZero type", got[0].Hints)
			}
			if len(errs) != 1 {
				t.Errorf("other errors beside the zero divisor: %v", errs)
			}
		})
	}
}

func TestOperandRefinement_UncheckedOperand(t *testing.T) {
	for _, tc := range []struct{ name, expr, message, hint string }{
		{"int binding", "x / z", "`/` divides by a NonZeroInt; `z` is an Int",
			"check it first (`if z == 0 then ...`) or take a NonZeroInt"},
		{"int remainder", "x % z", "`%` divides by a NonZeroInt; `z` is an Int",
			"check it first (`if z == 0 then ...`) or take a NonZeroInt"},
		{"non-negative binding", "x / w", "`/` divides by a NonZeroInt; `w` is a NonNegativeInt",
			"check it first (`if w == 0 then ...`) or take a NonZeroInt"},
		{"decimal binding", "d / e", "`/` divides by a NonZeroDecimal; `e` is a Decimal",
			"check it first (`if e == 0d then ...`) or take a NonZeroDecimal"},
		{"expression", "x / (z - 1)", "`/` divides by a NonZeroInt, and `z - 1` is an Int",
			"bind it and check it first (`d = ...`, then `if d == 0 then ...`), or check it with `NonZeroInt.try_from(...)`"},
		{"named function", "Int.divide(x, z)", "`Int.divide` divides by a NonZeroInt; `z` is an Int",
			"check it first (`if z == 0 then ...`) or take a NonZeroInt"},
		{"duration remainder", "span % span", "`%` divides by a NonZeroDuration; `span` is a Duration",
			"check it with `NonZeroDuration.try_from(span)` or take a NonZeroDuration"},
		{"duration by int", "span / z", "`/` divides by a NonZeroInt; `z` is an Int",
			"check it first (`if z == 0 then ...`) or take a NonZeroInt"},
		{"shift left", "x <<< z", "`<<<` shifts by a NonNegativeInt; `z` is an Int",
			"check it first (`if z < 0 then ...`) or take a NonNegativeInt"},
		{"shift right", "x >>> z", "`>>>` shifts by a NonNegativeInt; `z` is an Int",
			"check it first (`if z < 0 then ...`) or take a NonNegativeInt"},
		{"negative literal count", "x <<< -1", "`<<<` shifts by a NonNegativeInt, and -1 is not one", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(operandRefinementSource, "%s", tc.expr, 1)
			_, errs := checkSourceWithStdlib(src)
			errs = withoutUnusedBindingErrors(errs)
			got := operandErrors(errs)
			if len(got) != 1 || len(errs) != 1 {
				t.Fatalf("%s: the front end admits the operand, or reports it with others; errors: %v", tc.expr, errs)
			}
			if got[0].Message != tc.message {
				t.Errorf("message = %q, want %q", got[0].Message, tc.message)
			}
			if tc.hint == "" {
				return
			}
			if len(got[0].Hints) != 1 || got[0].Hints[0] != tc.hint {
				t.Errorf("hints = %q, want %q", got[0].Hints, tc.hint)
			}
		})
	}
}

func TestOperandRefinement_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, expr string }{
		{"float divide", "f / 0.0"},
		{"float remainder", "f % -0.0"},
		{"nonzero literal", "x / 2"},
		{"negative literal", "x % -3"},
		{"zero dividend", "0 / n"},
		{"NonZeroInt divisor", "x / n"},
		{"PositiveInt divisor", "x % p"},
		{"checked with and", "z != 0 and x % z == 0"},
		{"checked with if", "if z == 0 then 0 else x / z"},
		{"range proves it", "x / (p + w)"},
		{"square of a NonZeroInt", "x / (n * n)"},
		{"abs plus one", "x / (Int.abs(z) + 1)"},
		{"decimal literal", "d / 2.5d"},
		{"Decimal of a PositiveInt", "d / Int.to_decimal(p)"},
		{"duration unit of a literal", "span % Duration.minutes(15)"},
		{"duration typed literal", "span % Duration\"15m\""},
		{"NonZeroDuration divisor", "span % step"},
		{"duration by a literal", "span / 3"},
		{"shift by a literal", "x <<< 4"},
		{"shift by a count", "x >>> w"},
		{"shift by a length", "x <<< List.length([1])"},
		{"Int.modulo NonZeroInt", "Int.modulo(x, n)"},
		{"interface call", "Divide.divide(x, n)"},
		{"user operator", "m / 0"},
		{"user function", "split(x, 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `import std::duration.{Duration, NonZeroDuration}

struct Money
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

fn g(x: Int, z: Int, f: Float, m: Money, n: NonZeroInt, p: PositiveInt, w: NonNegativeInt, d: Decimal, span: Duration, step: NonZeroDuration)
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

// An impl may take a refinement as the right operand of `/`, `%`, `<<<` and
// `>>>`, and the operator holds its operand to it; any other operator's
// impl may not.
func TestOperandRefinement_UserImpl(t *testing.T) {
	src := `type Ticks Int

impl Divide<NonZeroInt, Ticks> for Ticks
    fn divide(Ticks(t), rhs: NonZeroInt) -> Ticks
        Ticks(t / rhs)
    end
end

fn f(t: Ticks, z: Int) -> Ticks
    _ = t / 4
    t / z
end
`
	_, errs := checkSourceWithStdlib(src)
	got := operandErrors(withoutUnusedBindingErrors(errs))
	if len(got) != 1 || got[0].Message != "`/` divides by a NonZeroInt; `z` is an Int" || got[0].Line != 11 {
		t.Fatalf("errors %v, want only `t / z` refused", errs)
	}

	_, errs = checkSourceWithStdlib(`type Ticks Int

impl Add<NonZeroInt, Ticks> for Ticks
    fn add(Ticks(t), rhs: NonZeroInt) -> Ticks
        Ticks(t + rhs)
    end
end
`)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "an impl's interface cannot take the refinement `NonZeroInt` as an argument") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the front end admits `impl Add<NonZeroInt, Ticks>`; errors: %v", errs)
	}
}

// A generic function whose bound divides by its own type does not take an
// Int, whose `/` divides by a NonZeroInt; one bounded by the divisor's type
// does.
func TestOperandRefinement_GenericBound(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn half<T>(x: T, two: T) -> T where T: Divide<T, T>
    x / two
end

fn f() -> Int
    half(10, 2)
end
`)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "Int does not implement Divide<Int, Int>") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the front end admits Int for a `Divide<T, T>` bound; errors: %v", errs)
	}

	_, errs = checkSourceWithStdlib(`fn rem<T, D>(a: T, b: D) -> T where T: Remainder<D, T>
    a % b
end

fn f() -> Int
    rem(10, NonZeroInt(4))
end
`)
	if errs = withoutUnusedBindingErrors(errs); len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
}
