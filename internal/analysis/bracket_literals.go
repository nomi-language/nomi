package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Bracket literals.
//
// `[...]` names no collection in its tokens. A literal that names a type
// (`Ids[1]`, `Path[1]`, `.Items[1]`) is the collection that type is built
// on; one that names a std collection itself (`Vector[1]`, `Set[1]`,
// `List[1]`) is that collection; and an unnamed one is the List, Vector or
// Set its position expects, a distinct type over one of them included,
// which it then builds, and a List where nothing fixes it. The decision is
// recorded on the node (ast.ListLit.Kind) for the IR builder. A brace
// literal is a map or a struct by its first entry, which the parser reads;
// `Name{}` is decided here, by what Name declares (checkEmptyNamedMap).

// stdShapeHeadName reports whether te is written as one of the std
// collections a literal may name: `List`, `Vector`, `Set` or `Map`.
func stdShapeHeadName(te ast.TypeExpr) bool {
	st, ok := te.(*ast.SimpleType)
	if !ok {
		return false
	}
	switch st.Name {
	case "List", "Vector", "Set", "Map":
		return true
	}
	return false
}

// stdShapeHead reports whether te names a std collection in this file:
// `List`, `Vector`, `Set` or `Map` bound to std's declaration.
func (c *checker) stdShapeHead(te ast.TypeExpr) bool {
	if !stdShapeHeadName(te) {
		return false
	}
	switch t := c.reg.Lookup(te.(*ast.SimpleType).Name).(type) {
	case nil, *ListType, *MapType:
		return true
	case *DistinctType:
		return strings.HasPrefix(t.Origin, "std/")
	case *StructType:
		return strings.HasPrefix(t.Origin, "std/")
	}
	return false
}

// seqKindOf is the bracket kind of t: a Vector's, a Set's, or a List's for
// anything else.
func seqKindOf(t Type) ast.SeqKind {
	switch {
	case isVectorType(t):
		return ast.SeqVector
	case isSetType(t):
		return ast.SeqSet
	}
	return ast.SeqList
}

func isListType(t Type) bool { _, ok := resolveTypeVar(t).(*ListType); return ok }

// seqKindExpected is the kind an unnamed bracket literal takes from
// expected and the type it is checked against. When expected is a distinct
// type over a list, vector or set that this file may build, target is that
// type, which the literal builds, and inner the collection it wraps.
func (c *checker) seqKindExpected(expected Type) (kind ast.SeqKind, target *DistinctType, inner Type) {
	t := resolveTypeVar(expected)
	switch {
	case isVectorType(t):
		return ast.SeqVector, nil, t
	case isSetType(t):
		return ast.SeqSet, nil, t
	}
	if dt, ok := t.(*DistinctType); ok && c.buildsDistinct(dt) {
		if in := resolveTypeVar(distinctInnerOf(dt)); isVectorType(in) || isSetType(in) || isListType(in) {
			return seqKindOf(in), dt, in
		}
	}
	return ast.SeqList, nil, expected
}

// buildsDistinct reports whether an unnamed literal here may build dt:
// it is not opaque, or this is its defining file.
func (c *checker) buildsDistinct(dt *DistinctType) bool {
	return !dt.Opaque || c.fa != nil && c.fa.inside(dt.OwningSourceFile)
}

// namedSeqKind is the kind of the collection the type or variant a named
// bracket literal heads is built on, a List when the name says nothing
// (its check reports why).
func (c *checker) namedSeqKind(n *ast.ListLit) ast.SeqKind {
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	defer func() { c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark] }()
	et, payload, _, ok := c.resolveVariantPrefixInValuePos(n.TypeName, n.Line, n.Col)
	if !ok {
		return ast.SeqList
	}
	if et != nil {
		return seqKindOf(payload)
	}
	if dt, ok := c.reg.Lookup(n.TypeName.TypeString()).(*DistinctType); ok && dt.Inner != nil {
		return seqKindOf(dt.Inner)
	}
	return ast.SeqList
}

func isMapType(t Type) bool { _, ok := resolveTypeVar(t).(*MapType); return ok }

// namedIsMap reports whether te, the name of an empty brace literal
// `Name{}`, names std's `Map`, a distinct type over a map or a variant
// whose payload is one, without reporting anything about it.
func (c *checker) namedIsMap(te ast.TypeExpr, line, col int) bool {
	if c.stdShapeHead(te) {
		return te.TypeString() == "Map"
	}
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	defer func() { c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark] }()
	et, payload, _, ok := c.resolveVariantPrefixInValuePos(te, line, col)
	if !ok {
		return false
	}
	if et != nil {
		return isMapType(payload)
	}
	dt, ok := c.reg.Lookup(te.TypeString()).(*DistinctType)
	return ok && dt.Inner != nil && isMapType(dt.Inner)
}

// checkEmptyNamedMap types `Name{}` when Name is a type over a map: the
// declaration, not the tokens, says that the braces hold no fields of a
// struct but the entries of an empty map. ok is false for any other
// literal, which is a struct's.
func (c *checker) checkEmptyNamedMap(n *ast.StructLit) (Type, bool) {
	if n.TypeName == nil || len(n.Fields) > 0 || len(n.Positional) > 0 || n.Spread != nil || !c.namedIsMap(n.TypeName, n.Line, n.Col) {
		return nil, false
	}
	n.EmptyMap = true
	if c.stdShapeHead(n.TypeName) {
		return &MapType{Key: c.freshTypeVar(), Val: c.freshTypeVar()}, true
	}
	t, ok := c.resolveShapeTarget(n.TypeName, n.Line, n.Col, n.TypeName.TypeString()+"{}", "map", "Map<K, V>", isMapType, true)
	if !ok {
		return nil, true
	}
	return c.shapeTargetResult(t), true
}

// checkBracketLit types a bracket literal where expected is the type its
// position expects, nil for none, and records its kind.
func (c *checker) checkBracketLit(n *ast.ListLit, expected Type) Type {
	if n.TypeName != nil {
		if name := n.TypeName.TypeString(); name != "Map" && c.stdShapeHead(n.TypeName) {
			// `Vector[1, 2]` is the vector `[1, 2]` is where one is
			// expected, whatever is.
			n.ShapeHead = true
			n.Kind = map[string]ast.SeqKind{"List": ast.SeqList, "Vector": ast.SeqVector, "Set": ast.SeqSet}[name]
			return c.checkSeqLit(n, n.Kind, expected)
		}
		n.Kind = c.namedSeqKind(n)
		switch n.Kind {
		case ast.SeqVector:
			return c.checkNamedVectorLit(n)
		case ast.SeqSet:
			return c.checkNamedSetLit(n)
		}
		return c.checkListLit(n)
	}
	kind, target, inner := c.seqKindExpected(expected)
	if kind == ast.SeqList && target == nil && len(n.Spreads) > 0 && !isListType(expected) {
		if _, iter := iterElemType(expected); !iter {
			kind = c.spreadKind(n)
		}
	}
	n.Kind = kind
	ty := c.checkSeqLit(n, kind, inner)
	if target != nil && ty != nil {
		return target
	}
	return ty
}

// spreadKind is the kind of an unnamed bracket literal with spreads whose
// position expects no collection: the collection its first spread with a
// type of its own is, when that is a Vector or a Set (`[..v, x]` with `v`
// a vector is one), else a List. The spread is checked again with the
// literal, so what this check reports is dropped.
func (c *checker) spreadKind(n *ast.ListLit) ast.SeqKind {
	for _, sp := range n.Spreads {
		if _, bare := sp.Head.(*ast.CurrentValue); bare || needsExpectedType(sp.Head) {
			continue
		}
		mark, undeterminedMark := len(c.errors), len(c.undetermined)
		t := c.checkNode(sp.Head)
		c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
		return seqKindOf(t)
	}
	return ast.SeqList
}

// checkSeqLit types the literal n as a collection of kind, where expected
// is the type its position expects, nil for none. Only an expected type of
// that collection, or an `Iter`, gives its elements a type.
func (c *checker) checkSeqLit(n *ast.ListLit, kind ast.SeqKind, expected Type) Type {
	switch kind {
	case ast.SeqVector:
		elemTy, ok := vectorElemType(expected)
		if !ok {
			elemTy, ok = iterElemType(expected)
		}
		if len(n.Spreads) > 0 {
			if !ok {
				elemTy = nil
			}
			return c.checkVectorUpdateLit(n, elemTy)
		}
		if ok {
			return c.checkVectorLitExpecting(n, elemTy)
		}
		return c.checkVectorLit(n)
	case ast.SeqSet:
		if len(n.Spreads) > 0 {
			if !isSetType(expected) {
				expected = nil
			}
			return c.checkSetUpdateLit(n, expected)
		}
		elemTy, ok := setElemType(expected)
		if !ok {
			elemTy, ok = iterElemType(expected)
		}
		if ok {
			return c.checkSetLitExpecting(n, elemTy)
		}
		return c.checkSetLit(n)
	}
	var elemTy Type
	if lt, ok := resolveTypeVar(expected).(*ListType); ok {
		elemTy = lt.Elem
	} else if e, ok := iterElemType(expected); ok {
		// A collection literal is an `Iter` of its element type, so an
		// expected `Iter<T>` pushes T into the items as `List<T>` would:
		// `Iter.to_map([(.North, .Cave)])` expected to be
		// `Map<Direction, Place>` checks each tuple against
		// `(Direction, Place)`.
		elemTy = e
	}
	if len(n.Spreads) > 0 {
		// The heads of `[h, ..rest]` are elements and the tail a list of
		// them, so an expected list pushes its element type into each head
		// (`[.Red, ..rest]` against `List<Color>`) and the whole into the
		// tail.
		if elemTy != nil {
			return c.checkListSpreadLitExpecting(n, elemTy)
		}
		return c.checkListSpreadLit(n)
	}
	if elemTy != nil {
		// Heterogeneous list literals need the expected element type pushed
		// down so `embeds` subtype coercion works at element granularity:
		// `[circle, rectangle, Shape.Point]: List<Shape>` accepts each
		// element where Shape is the unifying enum.
		return c.checkListLitExpecting(n, elemTy)
	}
	return c.checkListLit(n)
}
