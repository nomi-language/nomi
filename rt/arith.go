package rt

import (
	"fmt"
	"math"
)

// Nomi's `Int` is `int64` and it traps. Go's int64 wraps silently, so this file
// is the difference between the two: a wrong answer here is a wrong number,
// printed without complaint, in a program that looks fine. Which operator
// checks what, and the fault text, live here once.
//
// The predicates are exported for internal/vm, which boxes each fault itself
// over its own operator enum — an embedder wants the Int methods in
// std/int.nomi.

// AddOverflows reports whether a+b leaves the signed 64-bit range.
func AddOverflows(a, b int64) bool {
	return (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b)
}

// SubOverflows reports whether a-b leaves the signed 64-bit range.
func SubOverflows(a, b int64) bool {
	return (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b)
}

// MulOverflows reports whether a*b leaves the signed 64-bit range.
func MulOverflows(a, b int64) bool {
	if a == 0 || b == 0 {
		return false
	}
	// MinInt64 has no positive magnitude, so it only multiplies safely by 1.
	if a == math.MinInt64 {
		return b != 1
	}
	if b == math.MinInt64 {
		return a != 1
	}
	// Neither operand is MinInt64, so the division below can't itself overflow.
	p := a * b
	return p/b != a
}

// QuoOverflows reports whether a/b leaves the signed 64-bit range.
//
// Exactly one pair does: MinInt64 / -1 is MaxInt64+1, which is unrepresentable.
// Go wraps it back to MinInt64 rather than panicking, so it has to be named
// explicitly — this is the division case a backend forgets.
func QuoOverflows(a, b int64) bool { return a == math.MinInt64 && b == -1 }

// OverflowError is the error a trapping Int operation reports. op is spelled the
// way the source spells it ("+", "-", "*", "/").
func OverflowError(line int, op string, a, b int64) error {
	return positioned(line, overflowText(line, op, a, b))
}

// DivByZeroError is the error `/` and `%` report for a zero divisor. It carries
// no operands: which value was zero is not in doubt.
func DivByZeroError(line int) error { return positioned(line, divByZeroText(line)) }

func overflowText(line int, op string, a, b int64) string {
	return fmt.Sprintf("line %d: integer overflow: %d %s %d", line, a, op, b)
}

func divByZeroText(line int) string {
	return fmt.Sprintf("line %d: division by zero", line)
}

// divByZeroMessage is divByZeroText without its position. A host function's
// trap carries no line and the VM writes the call's in front, so
// `Decimal.divide_exact` trapping with it reads as Int's `/` does.
// TestDivByZeroMessageIsTheIntText holds the two to one text.
const divByZeroMessage = "division by zero"

// A fault's line is the source line of the operator, passed in by the caller
// rather than recovered from the frame or the Go stack. That is what makes
// `fn add(a, b) { a + b }` report the line of `a + b` and not the line of the
// call: a fault blames the operation.

// NegFloat is Nomi's unary `-` on Float: the sign flipped, so `-0.0` is
// negative zero and `-(-0.0)` positive zero. `0 - a` is not the same
// operation — it answers +0.0 for a zero operand — which is why the VM calls
// this rather than subtracting.
func NegFloat(a float64) float64 { return -a }

// --- wrapping Int arithmetic ----------------------------------------------

// Nomi's opt-in modular arithmetic: `Int.wrapping_add`/`_sub`/`_mul`, and
// ast.Binary.Wrapping, which the `@derive Hashable` hash-mix sets on nodes it
// synthesizes. These must not trap, since that is why they exist, and
// a backend that lowered a wrapping node to the trapping helper above would turn
// a deliberate wrap into a spurious crash.
//
// Division and modulo have no wrapping form. A wrapping node carrying one is
// the checked operator: the VM falls through to it, so MinInt64 / -1 traps
// there as it does anywhere else.

// WrapAddInt is modular `+`.
func WrapAddInt(a, b int64) int64 { return a + b }

// WrapSubInt is modular `-`.
func WrapSubInt(a, b int64) int64 { return a - b }

// WrapMulInt is modular `*`.
func WrapMulInt(a, b int64) int64 { return a * b }

// --- Float arithmetic ------------------------------------------------------

// FloatModuloText is the fault `%` on Float reports. Exported for the reason
// DecimalModuloText below is: a caller that needs the string rather
// than the trap (internal/vm does) calls this instead of retyping it.
func FloatModuloText(line int) string {
	return fmt.Sprintf("line %d: modulo not supported on floats", line)
}

// FloatModuloError is FloatModuloText as the fault the VM returns.
func FloatModuloError(line int) error { return positioned(line, FloatModuloText(line)) }

// --- Decimal arithmetic ----------------------------------------------------
//
// `+ - *` are always exact and can never fail: the representation is
// arbitrary-precision base-10, so a sum's scale is the larger of its operands'
// and a product's is the sum of them. There is deliberately no wrapping form
// and no overflow check, which is why the type exists beside Int.
//
// There is no `/`. Whether an exact quotient terminates depends on the
// divisor's value, so an operator that trapped on `10d / 3d` would pass tests
// that divide by 2 and stop in production on 3. Division is `Decimal.divide`,
// which names its rounding, or `Decimal.divide_exact` (decimalstd.go), which
// answers None for a quotient that does not terminate.
//
// ModDecimal carries the same `line int` as the Int family and for the same
// reason: a fault blames the operator, so the caller passes the operator's
// own line. Its fault text lives here once. `internal/vm`'s
// `TestTrapText_OneHomePerText` derives every `line %d:` text this package
// declares and fails if the VM spells one a second time.

// DecimalModuloText is the fault `%` on Decimal reports. The checker rejects it,
// so this is the runtime backstop, kept because it costs one function.
func DecimalModuloText(line int) string {
	return fmt.Sprintf("line %d: modulo is not defined on Decimal", line)
}

// AddDecimal is Nomi's `+` on Decimal. Exact; never fails.
func AddDecimal(a, b Decimal) Decimal { return a.Add(b) }

// SubDecimal is Nomi's `-` on Decimal. Exact; never fails.
func SubDecimal(a, b Decimal) Decimal { return a.Sub(b) }

// MulDecimal is Nomi's `*` on Decimal. Exact; never fails.
func MulDecimal(a, b Decimal) Decimal { return a.Mul(b) }

// NegDecimal is Nomi's unary `-` on Decimal.
//
// Total, unlike Int's unary `-`, which is checked subtraction from zero so
// that -(MinInt64) traps. Decimal is arbitrary-precision: there is no magnitude it
// cannot negate, so there is nothing to check. Scale is preserved and negating
// zero stays zero, which is why this is Negate rather than `0d - v` — the
// latter would be correct too, and would allocate a zero to do it.
func NegDecimal(v Decimal) Decimal { return v.Negate() }

// ModDecimal is Nomi's `%` on Decimal, which is not defined: it always traps.
func ModDecimal(a, b Decimal, line int) Decimal {
	trapAt(line, DecimalModuloText(line))
	return Decimal{}
}

// EqDecimal is Nomi's `==` on Decimal, and it is scale-insensitive: `1.50d`
// equals `1.5d`.
//
// Routed through Compare rather than comparing mantissa and scale, because
// equality must agree with HashDecimal below — and a pair that compares equal
// while hashing differently is a wrong map bucket rather than a wrong answer,
// which is far harder to see. The same rule std/decimal.nomi's `impl Equatable
// for Decimal` documents, implemented once.
func EqDecimal(a, b Decimal) bool { return a.Compare(b) == 0 }

// HashDecimal is Nomi's `Hashable.hash` for Decimal.
//
// The normalized form is hashed, which is what makes `1.50d` and `1.5d` land in
// one bucket and is therefore the other half of EqDecimal's contract. Built
// from HashString and HashMix rather than a private FNV so that this package
// has one hashing family.
func HashDecimal(d Decimal) uint64 {
	n := d.Normalize()
	return HashMix(uint64(StringHash(n.MantissaOrZero().String())), uint64(uint32(n.Scale)))
}
