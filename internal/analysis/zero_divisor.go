package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// Dividing by a literal zero (spec §8 *Integer division and remainder*).
// Int's and Decimal's `/` and `%`, and the std functions that divide by an
// argument, trap on a zero divisor when the program runs. A divisor written
// as a literal zero (`0`, `-0`, `0x0`, `(0)`, `0d`, `0.00d`) traps every
// time the expression runs, so the compiler reports it. Only the literal is
// checked: a binding, a `once` or an expression whose value is zero keeps
// the run-time trap, so whether a program compiles never turns on how far
// the compiler evaluates it. Float's `/` and `%` give an infinity or NaN
// and never trap, and an operator a user's impl declares is the impl's
// business, so neither is checked. A call through the interface
// (`Divide.divide(x, 0)`) resolves to no std function here and keeps the
// run-time trap too.

// ZeroDivisorCode tags the error.
const ZeroDivisorCode = "literal-zero-divisor"

// zeroDivisorFuncs is each std function that traps on a zero divisor, keyed
// "Owner.name", with the module that declares its owner, the divisor's
// argument (counted with a piped value as 0) and the divisor's type. A call
// through the interface (`Divide.divide(a, 0)`) keeps the run-time trap.
var zeroDivisorFuncs = map[string]struct {
	module  string
	divisor iterArg
	of      Type
}{
	"Int.divide":           {"int", iterArg{1, "rhs"}, TypeInt},
	"Int.remainder":        {"int", iterArg{1, "rhs"}, TypeInt},
	"Int.modulo":           {"int", iterArg{1, "y"}, TypeInt},
	"Int.floor_div":        {"int", iterArg{1, "y"}, TypeInt},
	"Decimal.divide":       {"decimal", iterArg{1, "b"}, TypeDecimal},
	"Decimal.remainder":    {"decimal", iterArg{1, "rhs"}, TypeDecimal},
	"Decimal.divide_exact": {"decimal", iterArg{1, "b"}, TypeDecimal},
}

// checkZeroDivisors reports each Int or Decimal division, remainder or
// dividing std call in nodes whose divisor is a literal zero. It runs once
// the file's expression types are closed.
func (c *checker) checkZeroDivisors(nodes []ast.Node) {
	if c.fa == nil {
		return
	}
	piped := map[*ast.Call]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Binary:
				switch node.Op {
				case "/", "%":
					if base := intOrDecimal(c.fa.ExprTypes[node.Left]); base != nil {
						c.reportZeroDivisor(node.Right, base)
					}
				case "|>":
					if call, ok := node.Right.(*ast.Call); ok {
						piped[call] = true
						c.checkZeroDivisorCall(call, node.Left)
					}
				}
			case *ast.Call:
				if !piped[node] {
					c.checkZeroDivisorCall(node, nil)
				}
			}
			return true
		})
	}
}

// checkZeroDivisorCall reports call when it calls a std function that
// divides by an argument and that argument is a literal zero.
func (c *checker) checkZeroDivisorCall(call *ast.Call, piped ast.Node) {
	sym := (&iterSensitivity{fa: c.fa}).resolveIterCallbackSymbol(call.Func)
	if sym == nil || sym.Kind != SymbolFunction {
		return
	}
	f, ok := zeroDivisorFuncs[sym.OwningType+"."+sym.Name]
	if !ok || !isStdOwnerFunction(c.fa, sym, sym.OwningType, f.module) {
		return
	}
	if divisor := iterCallArgs(call, piped).at(f.divisor); divisor != nil {
		c.reportZeroDivisor(divisor, f.of)
	}
}

// intOrDecimal is Int or Decimal when t is one, or a refinement of one, and
// nil otherwise: the dividend types whose `/` and `%` are std's and trap.
func intOrDecimal(t Type) Type {
	if t == nil {
		return nil
	}
	t = RefinementBase(resolveTypeVar(ResolveClosed(t)))
	for _, base := range []Type{TypeInt, TypeDecimal} {
		if TypesIdentical(t, base) {
			return base
		}
	}
	return nil
}

// reportZeroDivisor reports divisor when it is a literal zero of type t, an
// Int or a Decimal. A literal of another type is a type mismatch, which the
// checker has reported already.
func (c *checker) reportZeroDivisor(divisor ast.Node, t Type) {
	if t == nil || !literalZero(divisor, t) || !TypesIdentical(intOrDecimal(c.fa.ExprTypes[divisor]), t) {
		return
	}
	line, col := startOf(divisor)
	if line <= 0 || IsSynthesizedLine(line) {
		return
	}
	c.report(TypeError{
		Line:    line,
		Col:     col,
		Code:    ZeroDivisorCode,
		Message: fmt.Sprintf("division by zero: the divisor is the literal %s", RenderLiteral(ungroupExpr(divisor))),
	}.WithHint(fmt.Sprintf(
		"dividing by 0 always traps, so this is caught before the program runs; a divisor computed while it runs still traps when it is zero unless it is a NonZero%s (`NonZero%s.try_from(d)`)",
		t, t)))
}

// literalZero reports whether n, ungrouped and with any minus signs taken
// off, is a literal of t, an Int or a Decimal, whose value is zero: `0`,
// `0x0` or `0_0` for Int, and `0d` or `0.00d` for Decimal. A literal with
// no `d` is never a Decimal (spec §3 *Decimal literals*).
func literalZero(n ast.Node, t Type) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.Unary:
		return v.Op == "-" && literalZero(v.Right, t)
	case *ast.IntLit:
		return t == TypeInt && v.Value == 0 && (v.Big == nil || v.Big.Sign() == 0)
	case *ast.DecimalLit:
		d, err := rt.ParseDecimalLexeme(v.Lexeme)
		return t == TypeDecimal && err == nil && d.Sign() == 0
	}
	return false
}
