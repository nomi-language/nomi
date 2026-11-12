package analysis

import "github.com/nomi-language/nomi/internal/ast"

// IterView is a source checked against an `Iter<T>` position
// (FileAnalysis.IterViews): the callee receives a view over the source,
// which pushes the source's elements to its consumer, not an interface
// value (spec §13 *The Iter protocol*). The IR builder builds the view
// this record names.
type IterView struct {
	// Source is the source's own type.
	Source Type
	// Elem is the element the source pushes: a List's, Vector's, Set's or
	// Range's element, a Map's `(K, V)` pair, a Maybe's payload, a
	// String's grapheme as a String, a Bytes' Byte, or the element of the
	// type's own `impl Iter`. Nil for a type parameter, whose instances
	// each push their own.
	Elem Type
	// Domain is which walk the view is.
	Domain IterViewDomain
	// Impl is the type's own `impl Iter` block for IterViewImpl, the one
	// the checker selects among its impls of Iter (implCandidates). Nil
	// for every other domain.
	Impl *ast.ImplBlock
}

// IterViewDomain is the walk a source's view is.
type IterViewDomain uint8

const (
	// IterViewList, IterViewString, IterViewBytes, IterViewRange,
	// IterViewVector, IterViewSet, IterViewMap and IterViewMaybe are std's
	// built-in sources, each walked by the machine.
	IterViewList IterViewDomain = iota + 1
	IterViewString
	IterViewBytes
	IterViewRange
	IterViewVector
	IterViewSet
	IterViewMap
	IterViewMaybe
	// IterViewImpl is a type's own `impl Iter`, a program's or std's
	// (`impl Iter for Tree`, `impl Iter for StepByRange<T, S>`): its
	// `each_while` drives the view.
	IterViewImpl
	// IterViewBound is a type parameter bounded by `Iter`: each instance
	// is one of the domains above, which the IR builder reads from the
	// instance's type.
	IterViewBound
)

// recordIterView records n, an expression of type t checked against the
// `Iter<T>` interface it, as the view its source becomes (IterViews). An
// interface value is a sequence already and has none, and so has an
// unsolved variable, whose value is never made.
func (c *checker) recordIterView(n ast.Node, it *InterfaceType, t Type) {
	view := c.iterViewOf(it, t)
	if view == nil {
		return
	}
	if c.fa.IterViews == nil {
		c.fa.IterViews = map[ast.Node]*IterView{}
	}
	c.fa.IterViews[n] = view
}

// iterViewOf is the view a value of type t is at the `Iter<T>` position
// it, nil when t is no source with a view of its own.
func (c *checker) iterViewOf(it *InterfaceType, t Type) *IterView {
	rt := resolveTypeVar(t)
	view := &IterView{Source: t}
	switch ty := rt.(type) {
	case *InterfaceType, *TypeVar, nil:
		return nil
	case *TypeParam_:
		view.Domain = IterViewBound
		return view
	case *ListType:
		view.Domain, view.Elem = IterViewList, ty.Elem
		return view
	case *MapType:
		view.Domain, view.Elem = IterViewMap, &TupleType{Elems: []Type{ty.Key, ty.Val}}
		return view
	case *PrimitiveType:
		switch ty {
		case TypeString:
			view.Domain, view.Elem = IterViewString, TypeString
			return view
		case TypeBytes:
			view.Domain, view.Elem = IterViewBytes, TypeByte
			return view
		}
	case *StructType:
		if isStdOrigin(ty.Origin) && len(ty.TypeArgs) == 1 {
			switch ty.Name {
			case "Range":
				if ty.Origin == "" || ty.Origin == "std/ranges" {
					view.Domain, view.Elem = IterViewRange, ty.TypeArgs[0]
					return view
				}
			case "Set":
				if isStdSetType(ty) {
					view.Domain, view.Elem = IterViewSet, ty.TypeArgs[0]
					return view
				}
			}
		}
	case *EnumType:
		if ty.Name == "Maybe" && (ty.Origin == "" || ty.Origin == "std/maybe") && len(ty.TypeArgs) == 1 {
			view.Domain, view.Elem = IterViewMaybe, ty.TypeArgs[0]
			return view
		}
	}
	if elem, ok := vectorElemType(rt); ok {
		view.Domain, view.Elem = IterViewVector, elem
		return view
	}
	// A type of the program's or std's with an `impl Iter` of its own.
	cands := c.implCandidates(rt, it.Name, it.Origin)
	var chosen *implCandidate
	for i, cand := range cands {
		if len(it.TypeArgs) == 1 && len(cand.args) == 1 && typesAllEqual(cand.args, it.TypeArgs) {
			chosen = &cands[i]
			break
		}
	}
	if chosen == nil && len(cands) == 1 {
		chosen = &cands[0]
	}
	if chosen == nil {
		// A generic type whose impl is bounded by a where clause, viewed
		// inside a generic body (`StepByRange{range: r, by:}` in
		// `Range.step_by`): the type parameters' bounds are the body's,
		// which implCandidates does not hold, and the type's one impl of
		// Iter is the impl.
		chosen = c.onlyIterImpl(rt, it)
	}
	if chosen == nil || chosen.info == nil {
		return nil
	}
	view.Domain, view.Impl = IterViewImpl, chosen.info.Block
	if len(chosen.args) == 1 {
		view.Elem = chosen.args[0]
	}
	return view
}

// onlyIterImpl is the one impl of the Iter interface it that the type t's
// name has, at t's type arguments, or nil when it has none or several.
func (c *checker) onlyIterImpl(t Type, it *InterfaceType) *implCandidate {
	var only *implCandidate
	for _, info := range distinctImpls(lookupImplTypeArgSets(concreteTypeName(t), it.Name, c.implTypeArgSetsContext())) {
		if !sameNominalIdentity(it.Origin, it.Name, info.Origin, it.Name) {
			continue
		}
		if only != nil {
			return nil
		}
		args := make([]Type, len(info.Args))
		for i, a := range info.Args {
			args[i] = substituteTypeParamDefs(info.TypeParamDefs, concreteTypeArgs(t), a)
		}
		only = &implCandidate{info: info, args: args}
	}
	return only
}

// recordSubtestsViews records the view each `subtests` statement's rows
// become: the statement walks them as an `Iter<T>` parameter would
// (checkSubtests), though no expected type names the Iter.
func (c *checker) recordSubtestsViews(nodes []ast.Node) {
	iter := &InterfaceType{Name: "Iter", Origin: "std/iter"}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if st, ok := n.(*ast.Subtests); ok && st.Rows != nil {
				c.recordIterView(st.Rows, iter, c.fa.ExprTypes[st.Rows])
			}
			return true
		})
	}
}
