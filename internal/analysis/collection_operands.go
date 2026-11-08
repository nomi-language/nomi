package analysis

import "github.com/nomi-language/nomi/internal/ast"

// collectionLitKind names the std collection an untyped collection literal
// builds: "List" for `[...]`, "Vector" for `#[...]`, "Set" for `%[...]` and
// "Map" for `%{...}`, spreads included. It is "" for any other expression,
// a literal with a type name (`Tags[...]`) among them, since that one has
// the type it names.
func collectionLitKind(n ast.Node) string {
	switch v := ungroupExpr(n).(type) {
	case *ast.ListLit:
		if v.TypeName == nil {
			return "List"
		}
	case *ast.ListSpreadLit:
		return "List"
	case *ast.VectorLit:
		if v.TypeName == nil {
			return "Vector"
		}
	case *ast.SetLit:
		if v.TypeName == nil {
			return "Set"
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
	if other == nil || collectionKind(other) != kind || !concreteExpected(other) {
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

// checkTypedListSpreads checks, before anything else in the list literal n,
// each spread whose head has a type of its own: not a collection literal,
// not a form that takes its type from its position (needsExpectedType),
// and not the bare `..` of an update. pre holds their types by head, for
// checkListSpread to use in place of checking them again. built is the
// element type of the first of them with no open part, which the literal's
// elements are built as (checkSpreadLitElem), or nil.
func (c *checker) checkTypedListSpreads(n *ast.ListSpreadLit) (pre map[ast.Node]Type, built Type) {
	if len(n.Items) == 0 || ast.UpdateHole(n) != nil {
		return nil, nil
	}
	for _, sp := range n.Spreads {
		if collectionLitKind(sp.Head) != "" || needsExpectedType(sp.Head) {
			continue
		}
		if pre == nil {
			pre = map[ast.Node]Type{}
		}
		t := c.checkNode(sp.Head)
		pre[sp.Head] = t
		if built != nil || t == nil {
			continue
		}
		if e, ok := c.spreadElemType(t); ok && concreteExpected(e) {
			built = e
		}
	}
	return pre, built
}

// checkSpreadLitElem checks item, an element of a list literal with
// spreads, after elements and spreads whose joined type is have. When a
// spread fixed built (checkTypedListSpreads), the element is built as one
// of built, as an element is where an annotation expects the list, and its
// type is built; when it cannot be, it is checked as any element is
// (checkLitElem), so the literal reports the mismatch it always has.
func (c *checker) checkSpreadLitElem(pos *litPosition, item ast.Node, have, built Type) Type {
	if built != nil {
		mark, undeterminedMark := len(c.errors), len(c.undetermined)
		t := c.checkNodeExpecting(item, built)
		if len(c.errors) == mark && t != nil && c.litElemFits(built, t, item) {
			pos.started = true
			return built
		}
		c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
	}
	return c.checkLitElem(pos, item, have)
}
