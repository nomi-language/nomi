package analysis

import "github.com/nomi-language/nomi/internal/ast"

// collectionLitKind names the std collection an untyped collection literal
// builds: "Seq" for `[...]`, spreads included, which is the List, Vector or
// Set its position expects, "Map" for `{k => v}` and `{}`, and the
// collection a std head names, `Vector[...]` or `Set[...]`. It is "" for
// any other expression, a literal naming another type (`Tags[...]`) among
// them, since that one has the type it names.
func collectionLitKind(n ast.Node) string {
	switch v := ungroupExpr(n).(type) {
	case *ast.ListLit:
		if v.TypeName == nil {
			return "Seq"
		}
		switch name := v.TypeName.TypeString(); name {
		case "List", "Vector", "Set":
			return name
		}
	case *ast.MapLit:
		if v.TypeName == nil {
			return "Map"
		}
	}
	return ""
}

// collectionKind names the std collection t is, as collectionLitKind
// names a literal's, or "" when t is none of them.
func collectionKind(t Type) string {
	t = resolveTypeVar(t)
	if _, ok := t.(*ListType); ok {
		return "List"
	}
	if _, ok := t.(*MapType); ok {
		return "Map"
	}
	if _, ok := vectorElemType(t); ok {
		return "Vector"
	}
	if _, ok := setElemType(t); ok {
		return "Set"
	}
	return ""
}

// checkCollectionOperands checks the operands of a collection `+` or `-`
// when exactly one of them is a collection literal (spec §8 *Arithmetic
// operators*): the other operand is checked first, and the literal is built
// as that operand's type when the other is the same collection, as a
// literal is where an annotation expects the collection (spec §5 *Type
// argument conversion*). So `[(Route`GET /`, home)] + routes` with
// `routes: List<(Route<Unit>, Handler)>` is a `List<(Route<Unit>, Handler)>`, its
// `home` converting as it is built, in either order. The two operands of a
// `+` have one type, so the literal takes no type of its own the other
// would then have to equal. ok is false when the operands are not of that
// shape, and the caller checks them as it always does.
func (c *checker) checkCollectionOperands(n *ast.Binary) (left, right Type, ok bool) {
	if n.Op != "+" && n.Op != "-" {
		return nil, nil, false
	}
	leftLit, rightLit := collectionLitKind(n.Left), collectionLitKind(n.Right)
	switch {
	case rightLit != "" && leftLit == "":
		left = c.checkNode(n.Left)
		right = c.checkCollectionLitAgainst(n.Right, rightLit, left)
		return left, right, true
	case leftLit != "" && rightLit == "":
		right = c.checkNode(n.Right)
		left = c.checkCollectionLitAgainst(n.Left, leftLit, right)
		return left, right, true
	}
	return nil, nil, false
}

// checkCollectionLitAgainst checks the collection literal lit, of the kind
// collectionLitKind names, with other as its expected type when other is a
// collection of that kind with no open part. When the literal cannot be
// built as other (`[1] + words`), it is checked on its own instead, so the
// operator reports the mismatch of the two types it always has.
func (c *checker) checkCollectionLitAgainst(lit ast.Node, kind string, other Type) Type {
	other = resolveTypeVar(other)
	ok := collectionKind(other) == kind
	if kind == "Seq" {
		switch collectionKind(other) {
		case "List", "Vector", "Set":
			ok = true
		}
	}
	if other == nil || !ok || !concreteExpected(other) {
		return c.checkNode(lit)
	}
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	t := c.checkNodeExpecting(lit, other)
	if len(c.errors) == mark {
		return t
	}
	c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
	return c.checkNode(lit)
}
