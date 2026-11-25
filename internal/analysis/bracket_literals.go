package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/parser"
)

// Bracket literals.
//
// `[...]` names no collection in its tokens. A literal that names a type
// (`Ids[1]`, `Path[1]`, `.Items[1]`) is the collection that type is built
// on; one that names std's `Vector` or `Set` (`Vector[1]`, `Set[1]`) is
// that collection; and an unnamed one is the List, Vector or Set its
// position expects, a distinct type over one of them included, which it
// then builds, and a List where nothing fixes it. The decision is
// recorded on the node (ast.ListLit.Kind) for the IR builder. A brace
// literal is a map or a struct by its first entry, which the parser reads;
// `Name{}` is decided here, by what Name declares (checkEmptyNamedMap).
//
// A list and a map literal have no named form, since the unnamed literal
// is one already: `List[1]`, `Map{"a" => 1}` and `Map{}` are each an error
// whose fix removes the name (reportNamedLit), and each is checked as the
// unnamed literal it would be.

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
		if in := resolveTypeVar(literalInnerOf(dt)); isVectorType(in) || isSetType(in) || isListType(in) {
			return seqKindOf(in), dt, in
		}
	}
	return ast.SeqList, nil, expected
}

// distinctLiteralType is the type of a literal that builds the distinct
// type target over the collection inner, ty its type as that collection: a
// generic target at the arguments the literal solves, so `[1, 2]` where a
// `NonEmptyList<T>` is expected, its T still open (a generic call's
// parameter), is a NonEmptyList<Int>. A literal that does not fit is
// reported where its elements are checked.
func (c *checker) distinctLiteralType(target Type, inner, ty Type) Type {
	if containsTypeVar(inner) {
		_ = c.unifyInto(inner, ty, nil)
		return resolveSolvedVars(target)
	}
	if ContainsTypeParam(inner) {
		subs := map[*TypeParam_]Type{}
		if c.unify(inner, ty, subs) == nil && len(subs) > 0 {
			return Substitute(target, subs)
		}
	}
	return target
}

// literalInnerOf is the collection an unnamed literal builds where dt is
// expected: dt's inner type, or for a subtype the base at the end of its
// chain, so `[1, 2]` builds a subtype of a subtype of a List.
func literalInnerOf(dt *DistinctType) Type {
	if subtypeOf(dt) != nil {
		return SubtypeBase(dt)
	}
	return distinctInnerOf(dt)
}

// shapeHeadSubtype is the subtype a literal headed by a std collection's
// name (`Vector[x]`) builds where expected is one, or nil.
// The head names the base the literal builds; a subtype of it is that
// base with a rule, so the literal is one as the unnamed literal is. A
// distinct type that is no subtype is not: its values are not its inner
// type's.
func (c *checker) shapeHeadSubtype(expected Type) *DistinctType {
	dt, ok := resolveTypeVar(expected).(*DistinctType)
	if !ok || subtypeOf(dt) == nil || !c.buildsDistinct(dt) {
		return nil
	}
	return dt
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
		c.reportNamedLit(n.TypeName, "map", &ast.MapLit{})
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
	ty := c.checkCollectionSubtypeLit(n, c.checkBracketLitAs(n, expected))
	c.checkLoneSpread(n, ty)
	return ty
}

// checkLoneSpread reports a list, vector or set literal n of one spread
// and nothing else whose spread already has the literal's type ty:
// values are immutable, so the literal is the spread's own value, and is
// written as it (reportEmptySpread). A spread of a subtype of ty is one
// too, since widening a value changes nothing. A literal that converts
// its spread is not: a List spread where a Vector is expected, elements
// of a subtype widened to their base (`[..ps]` with `ps` a
// `List<PositiveInt>` as a `List<Int>`), or a value of a distinct type's
// inner type, which the literal builds the distinct type from.
func (c *checker) checkLoneSpread(n *ast.ListLit, ty Type) {
	if len(n.Items) != 0 || len(n.Spreads) != 1 || ty == nil {
		return
	}
	sp := n.Spreads[0]
	spreadTy := resolveTypeVar(c.fa.ExprTypes[sp.Head])
	if spreadTy == nil {
		return
	}
	lit := resolveTypeVar(ty)
	if TypesIdentical(spreadTy, lit) || subtypeWidensTo(spreadTy, lit) {
		c.reportEmptySpread(sp, "a "+n.Kind.String()+" spread with no elements")
	}
}

// checkBracketLitAs is checkBracketLit before a subtype it builds is
// checked.
func (c *checker) checkBracketLitAs(n *ast.ListLit, expected Type) Type {
	if n.TypeName != nil {
		if name := n.TypeName.TypeString(); name != "Map" && c.stdShapeHead(n.TypeName) {
			// `Vector[1, 2]` is the vector `[1, 2]` is where one is
			// expected, whatever is.
			n.ShapeHead = true
			if name == "List" {
				bare := *n
				bare.TypeName = nil
				c.reportNamedLit(n.TypeName, "list", &bare)
			}
			n.Kind = map[string]ast.SeqKind{"List": ast.SeqList, "Vector": ast.SeqVector, "Set": ast.SeqSet}[name]
			if dt := c.shapeHeadSubtype(expected); dt != nil {
				// `Vector[x]` where a NonEmptyVector is expected builds it,
				// as `[x]` does: a subtype's value is one of its base.
				if in := resolveTypeVar(literalInnerOf(dt)); seqKindOf(in) == n.Kind && (isListType(in) || isVectorType(in) || isSetType(in)) {
					ty := c.checkSeqLit(n, n.Kind, in)
					if ty == nil {
						return nil
					}
					return c.distinctLiteralType(dt, in, ty)
				}
			}
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
		return c.distinctLiteralType(target, inner, ty)
	}
	return ty
}

// NamedLitCode tags the error for a list or map literal with std's
// `List` or `Map` in front.
const NamedLitCode = "named-collection-literal"

// reportNamedLit reports a literal of kind ("list", "map") written with
// the std collection's name in front, `List[...]` or `Map{...}`, at the
// name. bare is the literal without the name: the help shows it when it
// fits on a line, and the fix removes the name.
func (c *checker) reportNamedLit(name ast.TypeExpr, kind string, bare ast.Node) {
	e := errAt(name, "a "+kind+" literal has no name in front")
	e.Code = NamedLitCode
	head := "`" + name.TypeString() + "`"
	hint := "write the literal without " + head
	if text := format.RenderNode(bare); !strings.Contains(text, "\n") {
		hint = "write `" + text + "`"
	}
	if st, ok := name.(*ast.SimpleType); ok {
		e.Line, e.Col, e.EndLine, e.EndCol = st.Line, st.Col, st.Line, st.Col+len(st.Name)
		e.Fix = parser.Replace("Remove "+head, st.Line, st.Col, st.Line, st.Col+len(st.Name), "")
	}
	c.report(e.WithHint(hint))
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
// that collection, or an `Iter`, gives its elements a type. A literal with
// spreads is checked the same way for the three kinds (checkSeqSpreadLit).
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
			return c.checkSeqSpreadLit(n, vectorSpreads, elemTy)
		}
		if ok {
			return c.checkVectorLitExpecting(n, elemTy)
		}
		return c.checkVectorLit(n)
	case ast.SeqSet:
		elemTy, ok := setElemType(expected)
		if !ok {
			elemTy, ok = iterElemType(expected)
		}
		if len(n.Spreads) > 0 {
			if !ok {
				elemTy = nil
			}
			return c.checkSeqSpreadLit(n, setSpreads, elemTy)
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
		return c.checkSeqSpreadLit(n, listSpreads, elemTy)
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
