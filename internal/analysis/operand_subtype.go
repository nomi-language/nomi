package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// Operators whose right operand is a subtype (spec §8 *Integer division and
// remainder*, §8 *Bitwise operators*). std's `/` and `%` on Int, Decimal and
// Duration take a NonZero divisor (`impl Divide<NonZeroInt, Int> for Int`,
// `impl Remainder<NonZeroDuration, Duration> for Duration`), and Int's `<<<`
// and `>>>` a NonNegativeInt count, so a checked program never divides by
// zero or shifts by a negative count. The impl is chosen by the right
// operand's base, as an operand of a subtype is its base's value
// (selectOperatorImpl, subtypeOperandFits), and the operand is then held to
// the subtype: it passes when its type is the subtype or below it, or its
// range proves the subtype's rules (provenSubtype: a non-zero literal, a
// binding checked first, arithmetic that rules zero out). Otherwise it is an
// error at the operand that says to check it first or take the subtype. A
// user's impl may take a subtype of its own the same way: `impl
// Remainder<NonZeroMinutes, Minutes> for Minutes`.
//
// The VM's division-by-zero and negative-shift traps stay as a backstop
// that no checked program reaches.

// operandSubtypeInterfaces are the operator interfaces whose right operand an
// impl may declare as a subtype (subtype_check.go lifts the rule that an
// impl's interface takes no subtype argument for them).
var operandSubtypeInterfaces = map[string]bool{
	"Divide": true, "Remainder": true, "ShiftLeft": true, "ShiftRight": true,
}

// ZeroDivisorCode tags the error for a divisor written as a literal zero.
const ZeroDivisorCode = "literal-zero-divisor"

// UncheckedOperandCode tags the error for a right operand of `/`, `%`,
// `<<<` or `>>>` that is not shown to be the subtype the operator takes.
const UncheckedOperandCode = "unchecked-operand"

// subtypeOperandFits reports whether an impl whose right operand is want
// serves a right operand of type right: want is a subtype and right is of
// its base. Whether the operand's value is a want is checkSubtypeOperand's.
func subtypeOperandFits(want, right Type) bool {
	if subtypeOf(want) == nil || right == nil {
		return false
	}
	return TypesEqual(SubtypeBase(want), SubtypeBase(right))
}

// checkSubtypeOperand holds operand, the right operand of op (an operator,
// or the name of its function, `Int.divide`), to want, the subtype the
// selected impl takes. It reports false when it reported an error.
func (c *checker) checkSubtypeOperand(operand ast.Node, op string, want Type) bool {
	if operand == nil || subtypeOf(want) == nil {
		return true
	}
	have := c.fa.ExprTypes[operand]
	if have == nil {
		have = c.fa.ExprTypes[ungroupExpr(operand)]
	}
	if have == nil || isInfallibleLike(have) {
		return true
	}
	if _, open := resolveTypeVar(have).(*TypeVar); open {
		return true
	}
	if TypesIdentical(have, want) || subtypeWidensTo(have, want) || c.provenSubtype(operand, have, want) != nil {
		return true
	}
	if lit, typed := ungroupExpr(operand).(*ast.TaggedString); typed && subtypeLiteral(lit) && TypesIdentical(have, SubtypeBase(want)) {
		// `span % Duration"15m"`: a typed literal is checked against the
		// subtype's rules when the program is compiled, as one is where a
		// subtype is expected.
		c.recordSubtypeLiteral(lit, want)
		return true
	}
	line, col := exprStartLineCol(operand)
	if col <= 0 {
		// A capture's `&1` has no expression start of its own.
		line, col = startOf(operand)
	}
	if line <= 0 || IsSynthesizedLine(line) {
		return false
	}
	c.report(c.operandSubtypeError(operand, op, have, want, line, col))
	return false
}

// operandSubtypeError is the error for operand, of type have, where op takes
// a want.
func (c *checker) operandSubtypeError(operand ast.Node, op string, have, want Type, line, col int) TypeError {
	wantName := formatTypeForError(want)
	base := SubtypeBase(want)

	verb := "divides by"
	if op == "<<<" || op == ">>>" || op == "Int.shift_left" || op == "Int.shift_right" {
		verb = "shifts by"
	}
	fam := familyOfBase(base)
	if lit := ungroupExpr(operand); fam != nil && isNumericLiteral(lit) {
		text := RenderLiteral(lit)
		if r, ok := literalRange(lit, fam); ok && r.known {
			if v, point := r.isPoint(); point && v.Sign() == 0 && verb == "divides by" {
				return TypeError{Line: line, Col: col, Code: ZeroDivisorCode,
					Message: fmt.Sprintf("division by zero: the divisor is the literal %s", text)}.
					WithHint(fmt.Sprintf("`%s` %s a %s, and %s is zero; a divisor computed while the program runs is checked first (`if d == %s then ...`) or taken as a %s", op, verb, wantName, text, zeroLiteral(fam), wantName))
			}
		}
		return TypeError{Line: line, Col: col, Code: UncheckedOperandCode,
			Message: fmt.Sprintf("`%s` %s a %s, and %s is not one", op, verb, wantName, text)}
	}
	haveName := formatTypeForError(have)
	check := checkFirstCondition(fam, want)
	name := identText(ungroupExpr(operand))
	if fam != nil && verb == "divides by" {
		if v, point := c.exprRange(operand, fam).isPoint(); point && v.Sign() == 0 {
			// `zero = 0` then `7 % zero`: no check can make it pass.
			what := "this divisor is always 0"
			if name != "" {
				what = fmt.Sprintf("`%s` is 0 here", name)
			}
			return TypeError{Line: line, Col: col, Code: ZeroDivisorCode,
				Message: "division by zero: " + what}.
				WithHint(fmt.Sprintf("`%s` %s a %s", op, verb, wantName))
		}
	}
	if name != "" && fam == intFamily {
		if v, point := c.exprRange(operand, fam).isPoint(); point {
			// `n = 0 - 1` then `1 <<< n`: n is one value, which no check
			// makes a want.
			return TypeError{Line: line, Col: col, Code: UncheckedOperandCode,
				Message: fmt.Sprintf("`%s` %s a %s; `%s` is %s here", op, verb, wantName, name, v.RatString())}
		}
	}
	if name != "" {
		e := TypeError{Line: line, Col: col, Code: UncheckedOperandCode,
			Message: fmt.Sprintf("`%s` %s a %s; `%s` is %s %s", op, verb, wantName, name, ArticleFor(haveName), haveName)}
		if check != "" {
			return e.WithHint(fmt.Sprintf("check it first (`if %s then ...`) or take a %s", fmt.Sprintf(check, name), wantName))
		}
		return e.WithHint(fmt.Sprintf("check it with `%s.try_from(%s)` or take a %s", wantName, name, wantName))
	}
	noun := "divisor"
	if verb == "shifts by" {
		noun = "count"
	}
	what := "this " + noun
	if text := RenderLiteral(ungroupExpr(operand)); len(text) <= 40 {
		what = "`" + text + "`"
	}
	e := TypeError{Line: line, Col: col, Code: UncheckedOperandCode,
		Message: fmt.Sprintf("`%s` %s a %s, and %s is %s %s", op, verb, wantName, what, ArticleFor(haveName), haveName)}
	if check != "" {
		return e.WithHint(fmt.Sprintf("bind it and check it first (`d = ...`, then `if %s then ...`), or check it with `%s.try_from(...)`", fmt.Sprintf(check, "d"), wantName))
	}
	return e.WithHint(fmt.Sprintf("check it with `%s.try_from(...)`, an `Option<%s>`", wantName, wantName))
}

// checkFirstCondition is the condition, with %s for the binding, whose
// failing branch leaves and so narrows a value of fam to want: `d == 0`
// for a NonZeroInt, `n < 0` for a NonNegativeInt. Empty when no constant
// of fam's base is written as a literal (Duration).
func checkFirstCondition(fam *numericFamily, want Type) string {
	if fam == nil || fam == durationFamily {
		return ""
	}
	zero := zeroLiteral(fam)
	dt := subtypeOf(want)
	if dt == nil || dt.Origin != fam.origin {
		return ""
	}
	switch dt.Name {
	case fam.nonZero:
		return "%s == " + zero
	case fam.nonNegative:
		return "%s < " + zero
	case fam.positive:
		return "%s <= " + zero
	}
	return ""
}

// zeroLiteral is how a zero of fam's base is written.
func zeroLiteral(fam *numericFamily) string {
	switch fam {
	case decimalFamily:
		return "0d"
	case floatFamily:
		return "0.0"
	}
	return "0"
}

// isNumericLiteral reports whether n is a numeric literal, possibly
// negated.
func isNumericLiteral(n ast.Node) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.Unary:
		return v.Op == "-" && isNumericLiteral(v.Right)
	case *ast.IntLit, *ast.FloatLit:
		return true
	case *ast.DecimalLit:
		_, err := rt.ParseDecimalLexeme(v.Lexeme)
		return err == nil
	}
	return false
}

// operatorCallName is how an operator function's call names it in an
// error: `Int.divide`, `Divide.divide`, or the bare method.
func operatorCallName(fn ast.Node) string {
	switch f := fn.(type) {
	case *ast.FieldAccess:
		if f.Field == nil {
			return ""
		}
		if obj := identText(ungroupExpr(f.Object)); obj != "" {
			return obj + "." + f.Field.Name
		}
		return f.Field.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}
