package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// An Int literal written where a Byte is expected is a Byte (the spec's
// "Byte literals"): `b: Byte = 0x81`, `[0x81, 0x00]` against `List<Byte>`,
// an argument to a Byte parameter, a Byte field, a returned Byte, an arm
// pattern over a Byte, and an operand of `==`, `&&&` and the other binary
// operators whose other operand is a Byte. Only a literal, or a literal
// under unary minus, takes its type from the position; an Int value never
// becomes a Byte. A literal outside 0 to 255 there is an error.

// byteRangeMessage is the error for a literal that is not a Byte value.
func byteRangeMessage(text string) string {
	return fmt.Sprintf("`%s` does not fit in a Byte (0 to 255)", text)
}

// expectsByte reports whether expected is Byte.
func expectsByte(expected Type) bool {
	return expected != nil && resolveTypeVar(expected) == TypeByte
}

// intLiteralOperand reports whether n, ungrouped, is an Int literal or a
// negated one: the expressions a Byte position types as a Byte.
func intLiteralOperand(n ast.Node) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.IntLit:
		return true
	case *ast.Unary:
		_, ok := v.Right.(*ast.IntLit)
		return ok && v.Op == "-"
	}
	return false
}

// holdsIntLiteral reports whether n is an Int literal or a negated one, or a
// collection, tuple or map literal or a call's argument list with one inside:
// an expression a type with Byte in it can change, `[0x81, 0x00]` against
// `List<Byte>` or `Some(0x81)` against `Maybe<Byte>`.
func holdsIntLiteral(n ast.Node) bool {
	if intLiteralOperand(n) {
		return true
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.NamedArg:
		return holdsIntLiteral(v.Value)
	case *ast.ListLit:
		return v.TypeName == nil && anyHoldsIntLiteral(v.Items)
	case *ast.VectorLit:
		return v.TypeName == nil && anyHoldsIntLiteral(v.Items)
	case *ast.SetLit:
		return v.TypeName == nil && anyHoldsIntLiteral(v.Items)
	case *ast.TupleLit:
		return v.TypeName == nil && anyHoldsIntLiteral(v.Items)
	case *ast.MapLit:
		if v.TypeName != nil {
			return false
		}
		for _, e := range v.Entries {
			if holdsIntLiteral(e.Key) || holdsIntLiteral(e.Value) {
				return true
			}
		}
	case *ast.Call:
		return anyHoldsIntLiteral(v.Args)
	}
	return false
}

func anyHoldsIntLiteral(items []ast.Node) bool {
	for _, item := range items {
		if holdsIntLiteral(item) {
			return true
		}
	}
	return false
}

// byteInType reports whether ty is Byte or has Byte among its element or
// type arguments, as enumInType looks for an enum.
func byteInType(ty Type, depth int) bool {
	if depth > 8 || ty == nil {
		return false
	}
	switch t := resolveTypeVar(ty).(type) {
	case *PrimitiveType:
		return t == TypeByte
	case *ListType:
		return byteInType(t.Elem, depth+1)
	case *MapType:
		return byteInType(t.Key, depth+1) || byteInType(t.Val, depth+1)
	case *TupleType:
		return anyByteInTypes(t.Elems, depth)
	case *EnumType:
		return anyByteInTypes(t.TypeArgs, depth)
	case *StructType:
		return anyByteInTypes(t.TypeArgs, depth)
	case *DistinctType:
		return anyByteInTypes(t.TypeArgs, depth)
	}
	return false
}

func anyByteInTypes(ts []Type, depth int) bool {
	for _, t := range ts {
		if byteInType(t, depth+1) {
			return true
		}
	}
	return false
}

// byteLiteralsTakeExpected reports whether a call's arguments hold an Int
// literal that the expected type, which has Byte in it, would make a Byte:
// `Some(0x81)` expected to be a `Maybe<Byte>`. The call seeds its type
// parameters from the expected type, tentatively, as it does for a
// dot-leading variant.
func byteLiteralsTakeExpected(args []ast.Node, expected Type) bool {
	return byteInType(expected, 0) && anyHoldsIntLiteral(args)
}

// unsolvedExpected reports whether expected says nothing yet: none, or an
// inference variable or type parameter not yet solved.
func unsolvedExpected(expected Type) bool {
	switch resolveTypeVar(expected).(type) {
	case nil, *TypeVar, *TypeParam_:
		return true
	}
	return false
}

// intLiteralText is how a diagnostic spells the literal.
func intLiteralText(n *ast.IntLit) string {
	if n.Lexeme != "" {
		return n.Lexeme
	}
	return n.Decimal()
}

// checkByteLiteral types node, an Int literal or a negated one (already
// ungrouped), as a Byte, and reports a value outside 0 to 255.
func (c *checker) checkByteLiteral(node ast.Node) Type {
	switch n := node.(type) {
	case *ast.IntLit:
		if n.Big != nil || n.Value < 0 || n.Value > 255 {
			c.addError(n.Line, n.Col, byteRangeMessage(intLiteralText(n)))
		}
		c.registerByteHover(n)
	case *ast.Unary:
		lit := n.Right.(*ast.IntLit)
		c.fa.recordExprType(lit, TypeByte)
		if lit.Big == nil && lit.Value == 0 {
			c.addError(n.Line, n.Col, "a Byte literal has no sign: write `0` for `-0`")
			break
		}
		c.addError(n.Line, n.Col, byteRangeMessage("-"+intLiteralText(lit)))
	}
	return TypeByte
}

// checkBytePattern checks an Int literal pattern over a Byte value. The
// pattern is recorded as a Byte, the value the arm compares with.
func (c *checker) checkBytePattern(n *ast.IntLit) {
	if n.Big != nil || n.Value < 0 || n.Value > 255 {
		c.addError(n.Line, n.Col, byteRangeMessage(intLiteralText(n)))
	}
	c.registerByteHover(n)
	c.fa.recordExprType(n, TypeByte)
}

// registerByteHover makes a hover on a Byte literal name its type, which
// the spelling does not, unless an argument's hover already holds the spot.
func (c *checker) registerByteHover(n *ast.IntLit) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, taken := c.fa.References[pos]; taken || n.Line <= 0 {
		return
	}
	text := intLiteralText(n)
	c.fa.References[pos] = &Symbol{
		Name: text,
		Kind: SymbolLiteral,
		Pos:  pos,
		Type: TypeByte,
		Span: len(text),
	}
}

// checkByteOperands checks the operands of a binary operator that is not a
// comparison or a pipe when exactly one of them is an Int literal: the other
// operand is checked first, and a Byte there makes the literal a Byte, so
// `b &&& 0x7F` and `0x80 ||| b` are Bytes. ok is false otherwise, and the
// caller checks the operands as it always does.
func (c *checker) checkByteOperands(n *ast.Binary) (left, right Type, ok bool) {
	leftLit, rightLit := intLiteralOperand(n.Left), intLiteralOperand(n.Right)
	switch {
	case rightLit && !leftLit:
		left = c.checkNode(n.Left)
		right = c.checkLiteralOperand(n.Right, left)
		return left, right, true
	case leftLit && !rightLit:
		right = c.checkNode(n.Right)
		left = c.checkLiteralOperand(n.Left, right)
		return left, right, true
	}
	return nil, nil, false
}

// checkLiteralOperand checks an operand that holds an Int literal against
// the other operand's type when that type has Byte in it (`b &&& 0x7F`,
// `bytes == [0x81, 0x00]`), and on its own otherwise.
func (c *checker) checkLiteralOperand(operand ast.Node, other Type) Type {
	if byteInType(other, 0) {
		return c.checkOperandAgainst(operand, other)
	}
	return c.checkNode(operand)
}

// checkLiteralComparison checks a comparison's operands when one of them
// holds an Int literal and neither takes its type from its position
// otherwise: the right one against the left (checkLiteralOperand) when it
// holds one, as in `[b, 0x42] == [0x41, 0x42]`, else the left one against the
// right, checked first. ok is false when neither holds a literal.
func (c *checker) checkLiteralComparison(n *ast.Binary) (left, right Type, ok bool) {
	switch {
	case holdsIntLiteral(n.Right):
		left = c.checkNode(n.Left)
		right = c.checkLiteralOperand(n.Right, left)
		return left, right, true
	case holdsIntLiteral(n.Left):
		right = c.checkNode(n.Right)
		left = c.checkLiteralOperand(n.Left, right)
		return left, right, true
	}
	return nil, nil, false
}
