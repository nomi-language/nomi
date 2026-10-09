package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// isComparisonOp reports whether op compares two operands of one type:
// `==`, `!=`, `<`, `>`, `<=`, `>=`.
func isComparisonOp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

// checkComparisonOperands checks the two operands of a comparison. Both
// operands have one type (spec §22), so each is the other's expected type:
// in `k == .Deposit` the right operand resolves against `k`'s enum, and in
// `.Deposit == k` the left one does. The operand whose type depends on its
// position is checked second, against the type of the other. The expected
// type of the comparison itself (Bool, or Unit at a statement) is never an
// operand's: it is cleared for both.
//
// When both operands are dot-leading forms (`.A == .B`), neither names the
// enum, and one error at the operator asks for one to be qualified in place
// of an error at each.
func (c *checker) checkComparisonOperands(n *ast.Binary) (left, right Type) {
	prevAt, prevEnum := c.expectedAt, c.expectedEnum
	c.expectedAt, c.expectedEnum = nil, nil
	defer func() { c.expectedAt, c.expectedEnum = prevAt, prevEnum }()

	leftHead, rightHead := positionTypedHead(n.Left), positionTypedHead(n.Right)
	if leftHead != nil && rightHead != nil && (isDotLeading(leftHead) || isDotLeading(rightHead)) {
		name := dotLeadingName(leftHead)
		if name == "" {
			name = dotLeadingName(rightHead)
		}
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"neither operand of `%s` names its enum; qualify one variant with the enum's name (write `E.%s` for `.%s`)",
			n.Op, name, name))
		mark := len(c.errors)
		c.checkNode(n.Left)
		c.checkNode(n.Right)
		c.dropHeadErrors(mark, leftHead, rightHead)
		return nil, nil
	}

	leftNeeds := leftHead != nil || needsExpectedType(n.Left)
	rightNeeds := rightHead != nil || needsExpectedType(n.Right)
	if leftNeeds && !rightNeeds {
		right = c.checkNode(n.Right)
		left = c.checkOperandAgainst(n.Left, right)
		return left, right
	}
	left = c.checkNode(n.Left)
	if rightNeeds {
		right = c.checkOperandAgainst(n.Right, left)
	} else {
		right = c.checkNode(n.Right)
	}
	return left, right
}

// checkOperandAgainst checks a comparison operand with the other operand's
// type as its expected type, when that type is known.
func (c *checker) checkOperandAgainst(operand ast.Node, other Type) Type {
	other = resolveTypeVar(other)
	if _, unsolved := other.(*TypeVar); unsolved || other == nil {
		return c.checkNode(operand)
	}
	return c.checkNodeExpecting(operand, other)
}

// positionTypedHead returns the node that takes its type from its position
// when operand is one of the forms that do: a dot-leading variant in any of
// its spellings (`.A`, `.A(x)`, `.A{f: x}`, `.A[x]`, `.A{"k" => v}`), or a
// brace literal with no type name and no spread (`{x: 1, y: 2}`). It returns
// nil for any other operand.
func positionTypedHead(operand ast.Node) ast.Node {
	switch v := ungroupExpr(operand).(type) {
	case *ast.DotVariant:
		return v
	case *ast.Call:
		if dv, ok := v.Func.(*ast.DotVariant); ok {
			return dv
		}
	case *ast.StructLit:
		if dvt, ok := v.TypeName.(*ast.DotVariantType); ok {
			return dvt
		}
		if v.TypeName == nil && v.Spread == nil {
			return v
		}
	case *ast.ListLit:
		if dvt, ok := v.TypeName.(*ast.DotVariantType); ok {
			return dvt
		}
	case *ast.MapLit:
		if dvt, ok := v.TypeName.(*ast.DotVariantType); ok {
			return dvt
		}
	}
	return nil
}

func isDotLeading(head ast.Node) bool {
	switch head.(type) {
	case *ast.DotVariant, *ast.DotVariantType:
		return true
	}
	return false
}

func dotLeadingName(head ast.Node) string {
	switch h := head.(type) {
	case *ast.DotVariant:
		return h.Name
	case *ast.DotVariantType:
		return h.Name
	}
	return ""
}

// dropHeadErrors removes the errors reported since mark at a dot-leading
// head's own position: the "no determinable enum type" each one reports
// when nothing names its enum, which the comparison's own error replaces.
// Errors inside the operands (a payload, a field value) stay.
func (c *checker) dropHeadErrors(mark int, heads ...ast.Node) {
	type pos struct{ line, col int }
	at := map[pos]bool{}
	for _, h := range heads {
		switch v := h.(type) {
		case *ast.DotVariant:
			at[pos{v.Line, v.Col}] = true
		case *ast.DotVariantType:
			at[pos{v.Line, v.Col}] = true
		}
	}
	kept := c.errors[:mark]
	for _, e := range c.errors[mark:] {
		if !at[pos{e.Line, e.Col}] {
			kept = append(kept, e)
		}
	}
	c.errors = kept
}
