package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"strconv"
)

// A source at an `Iter<T>` position is viewed as the sequence the callee
// walks (spec §13 *The Iter protocol*). Which walk the view is (a List's,
// a String's, a Range's, a type's own `impl Iter`, ...) is the checker's
// record of the source expression (analysis.FileAnalysis.IterViews), read
// here. The record is what chooses among a type's own `impl Iter` blocks;
// a source without one is read from its kind (viewSourceOfKind), which
// names the walk of every built-in source and of a type with one impl: an
// instance of a type parameter bounded by Iter (analysis.IterViewBound),
// whose instances each have their own walk; a value in the body of an
// adapter the builder makes (a widened function's parameter or result, an
// element rebuilt at a wider type, a function value forwarding its
// parameters, each result of a `flat_map` callback); a source below the
// top of the expression's type (a field of a `Box<Vector<Int>>` entering a
// `Box<Iter<Display>>`, the error of a `?`); a spread's part; and the
// receiver of a call its own type names (`Range.each_while`,
// `String.each_while`).

// viewSeq is the domain of a source that is a sequence already: a value
// of type `Iter<T>`, which has no view of its own.
const viewSeq analysis.IterViewDomain = 0

// viewSource is a source as the view over it walks it: its domain, the
// element it pushes, the checker's type of that element where the view's
// record gives one (analysis.IterView.Elem), and for a type's own `impl
// Iter` the impl's functions.
type viewSource struct {
	dom      analysis.IterViewDomain
	elem     kind
	elemType analysis.Type
	user     *userIterSource
}

// iterViewAt is the checker's record of the view the source expression n
// becomes, nil when it has none. A copy of a written expression the
// builder lowers (a pipe stage with its subject spliced in) keeps the
// written one's position and node type, and finds the record by them; a
// name bound in place of one (holeFor) finds the written one's.
func (g *gen) iterViewAt(n ast.Node) *analysis.IterView {
	if w := g.holeFor[n]; w != nil {
		n = w
	}
	fa := g.srcFA()
	if fa == nil || n == nil || len(fa.IterViews) == 0 {
		return nil
	}
	if rec := fa.IterViews[n]; rec != nil {
		return rec
	}
	if g.iterViewsFA != fa {
		g.iterViewsFA = fa
		g.iterViewsAt = make(map[irCoercionPos]*analysis.IterView, len(fa.IterViews))
		for c, rec := range fa.IterViews {
			line, col := nodePos(c)
			g.iterViewsAt[irCoercionPos{line, col, c.NodeType()}] = rec
		}
	}
	line, col := nodePos(n)
	return g.iterViewsAt[irCoercionPos{line, col, n.NodeType()}]
}

// viewSourceAt is the source expression at, of kind sk, as the checker
// recorded its view, and with no record, as its kind names it
// (viewSourceOfKind). The record chooses among a type's `impl Iter`
// blocks; every other source's kind names its one walk. No record is
// written for a sequence, which is itself; for a value in an adapter's body
// (unwritten), a stand-in for a parameter or the receiver of a call its
// own type names (`Range.each_while(r, f)`, at nil), which have no
// expression of their own; or for an expression whose own type is no
// source and holds one below its top, which the node is only the position
// of (`open(b)` for a `b: Box<Vector<Int>>` at a `Box<Iter<Display>>`, the
// `?` of a `Result<Int, Vector<Int>>` whose boundary's error is an
// `Iter<Display>`).
func (bl *irScalarBuilder) viewSourceAt(at ast.Node, sk kind) (viewSource, bool) {
	if sk.tag != tagSeq && !bl.unwritten {
		if rec := bl.g.iterViewAt(at); rec != nil && rec.Domain != analysis.IterViewBound {
			vs, ok := bl.viewSourceIn(rec.Domain, rec.Impl, sk)
			vs.elemType = bl.g.checkerTypeArg(rec.Elem)
			return vs, ok
		}
	}
	return bl.viewSourceOfKind(sk)
}

// viewSourceIn is a value of kind sk in the domain dom the checker
// recorded, false when sk is not that domain's kind: a kind the builder
// does not represent as the domain's walk.
func (bl *irScalarBuilder) viewSourceIn(dom analysis.IterViewDomain, impl *ast.ImplBlock, sk kind) (viewSource, bool) {
	vs := viewSource{dom: dom}
	ok := false
	switch dom {
	case analysis.IterViewList:
		if sk.tag == tagList && sk.comp != nil && len(sk.comp.parts) == 1 {
			vs.elem, ok = sk.comp.parts[0], true
		}
	case analysis.IterViewString:
		vs.elem, ok = kindString, sk == kindString
	case analysis.IterViewBytes:
		vs.elem, ok = irByteKind(), sk == irBytesKind()
	case analysis.IterViewRange:
		vs.elem, ok = irIterRangeElem(sk)
	case analysis.IterViewVector:
		vs.elem, ok = vectorElem(sk)
	case analysis.IterViewSet:
		vs.elem, ok = setElem(sk)
	case analysis.IterViewMap:
		if sk.tag == tagMap && sk.comp != nil && len(sk.comp.parts) == 2 {
			vs.elem, ok = bl.g.tupleKind([]kind{sk.comp.parts[0], sk.comp.parts[1]}), true
		}
	case analysis.IterViewMaybe:
		vs.elem, ok = maybeIterElem(sk)
	case analysis.IterViewImpl:
		if u, found := bl.g.userIterImplByBlock(sk, impl); found {
			vs.user, vs.elem = bl.userSource(sk, u)
			ok = vs.user != nil
		}
	}
	if !ok {
		return viewSource{}, false
	}
	return vs, true
}

// viewSourceOfKind is a value of kind sk as a source, read from the kind:
// a sequence, a type parameter's instance, or a value with no expression
// the checker typed against an Iter.
func (bl *irScalarBuilder) viewSourceOfKind(sk kind) (viewSource, bool) {
	if sk.tag == tagSeq {
		if sk.comp == nil || len(sk.comp.parts) != 1 {
			panic("irbuild: viewSourceOfKind: a sequence kind is interned with its one element")
		}
		return viewSource{dom: viewSeq, elem: seqElem(sk)}, true
	}
	var dom analysis.IterViewDomain
	switch {
	case sk.tag == tagList:
		dom = analysis.IterViewList
	case sk == kindString:
		dom = analysis.IterViewString
	case sk == irBytesKind():
		dom = analysis.IterViewBytes
	case sk.tag == tagMap:
		dom = analysis.IterViewMap
	default:
		if _, ok := irIterRangeElem(sk); ok {
			dom = analysis.IterViewRange
		} else if _, ok := vectorElem(sk); ok {
			dom = analysis.IterViewVector
		} else if _, ok := setElem(sk); ok {
			dom = analysis.IterViewSet
		} else if _, ok := maybeIterElem(sk); ok {
			dom = analysis.IterViewMaybe
		} else if u, ok := bl.g.userIterImplOf(sk); ok {
			vs := viewSource{dom: analysis.IterViewImpl}
			vs.user, vs.elem = bl.userSource(sk, u)
			return vs, vs.user != nil
		}
	}
	if dom == 0 {
		return viewSource{}, false
	}
	return bl.viewSourceIn(dom, nil, sk)
}

// seqView is src, the value of the source expression at of kind sk, as a
// lowered `Iter<elem>`: the view iterCall builds over its source, for a
// source entering a declared `Iter<T>` position (a parameter, a field, a
// payload, an annotated binding). A sequence of that element passes
// unchanged. The element may be a function: the views read elements
// without looking at them.
func (bl *irScalarBuilder) seqView(at ast.Node, src ir.Temp, sk, elem kind) (ir.Temp, bool) {
	if !irCallableValueKind(elem) && elem != kindUnit {
		return ir.NoTemp, false
	}
	if sk == seqKindIn(bl.g, elem) {
		return src, true
	}
	vs, ok := bl.viewSourceAt(at, sk)
	if !ok {
		return ir.NoTemp, false
	}
	return bl.viewAs(at, src, sk, vs, elem)
}

// viewAs views src, a value of kind sk walked as vs, as an `Iter<elem>`.
// Whether the source's own elements are elem's already is one test for
// every domain: `it: Iter<Shape> = [circle]` for an interface Shape,
// `show(1..3)`, `show("ab")`, `show(a_set)` or `count({1 => 2})` into an
// `Iter<Display>` walk the source as it is, since an existential is the
// concrete value. A source whose element is not elem but becomes it (`it:
// Iter<Shape> = [circle]` for `enum Shape { embeds Circle }`) is viewed at
// its own element, and each element converted as it is read
// (rebuildAdapter: an embedded value widened, a Maybe<Circle> rebuilt as a
// Maybe<Shape>, a List<Int> viewed as an Iter<Int>, a value that enters an
// interface boxed boxed). A domain's own part is only the view over it
// (viewOwn), so no domain decides what enters an interface.
func (bl *irScalarBuilder) viewAs(at ast.Node, src ir.Temp, sk kind, vs viewSource, elem kind) (ir.Temp, bool) {
	want := seqKindIn(bl.g, elem)
	if sk == want {
		return src, true
	}
	own, from := vs.dom != viewSeq, vs.elem
	asItIs := from == elem || bl.erasesUnboxed(elem, from)
	if !asItIs && !irCallableValueKind(from) {
		// An element no adapter takes is elem's when it is the interface's
		// or the conjunction's value all the same.
		if !irHeldKind(elem) || !bl.g.irErases(elem, from, nil) {
			return ir.NoTemp, false
		}
		asItIs = true
	}
	if asItIs {
		if !own {
			// A sequence of another kind whose elements are elem's: the
			// caller relabels it (relabels).
			return ir.NoTemp, false
		}
		return bl.viewOwn(at, src, sk, vs, elem)
	}
	fn, ok := bl.rebuildAdapter(at, from, elem)
	if !ok {
		return ir.NoTemp, false
	}
	if own {
		if src, ok = bl.viewOwn(at, src, sk, vs, from); !ok {
			return ir.NoTemp, false
		}
	}
	return bl.iterAppend(at, ir.IterMap, ir.IterOverSeq, want, src, fn), true
}

// viewOwn views src, a collection of kind sk walked as vs, as an
// `Iter<elem>` over the elements it holds, which viewAs found to be elem's
// already: the domain's own view and nothing else.
func (bl *irScalarBuilder) viewOwn(at ast.Node, src ir.Temp, sk kind, vs viewSource, elem kind) (ir.Temp, bool) {
	want := seqKindIn(bl.g, elem)
	view := func(over ir.IterDomain, operands ...ir.Temp) (ir.Temp, bool) {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, over, operands...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: want})
		return n.Dst(), true
	}
	switch vs.dom {
	case analysis.IterViewList:
		return view(ir.IterOverList, src)
	case analysis.IterViewString:
		return view(ir.IterOverString, src)
	case analysis.IterViewBytes:
		return view(ir.IterOverBytes, src)
	case analysis.IterViewVector:
		return view(ir.IterOverVector, src)
	case analysis.IterViewSet:
		return view(ir.IterOverSet, src)
	case analysis.IterViewMaybe:
		return view(ir.IterOverMaybe, src)
	case analysis.IterViewMap:
		if vs.elem == elem {
			return bl.mapView(at, src, sk, vs.elem), true
		}
		// `count({1 => 2})` into an `Iter<Display>`: each `(K, V)` pair is
		// already the interface's value.
		return view(ir.IterOverMap, src)
	case analysis.IterViewImpl:
		// The view's operands are the impl's functions, whatever the
		// element.
		return bl.userView(at, src, sk, vs.user, elem), true
	case analysis.IterViewRange:
		own := vs.elem
		if own == kindInt {
			// std's Int `compare`, `next` and `steps_between` are the
			// consumer's own (rt.IntRangeSeq).
			return view(ir.IterOverIntRange, src)
		}
		if own == codepointKind() {
			// std's Codepoint impls are the consumer's own
			// (rt.CodepointRangeSeq).
			return view(ir.IterOverCodepointRange, src)
		}
		ops, ok := bl.rangeView(at, src, own, vs.elemType)
		if !ok {
			return ir.NoTemp, false
		}
		// The view answers the Range's own known_count, so it carries the
		// element's distance function (rt.Seq's Count). One whose impl
		// inherits the None default has no count to carry.
		if bl.inheritsStepsBetween(own) {
			return view(ir.IterOverRange, ops...)
		}
		maybeInt, shared := bl.g.sharedMaybe(kindInt)
		if !shared {
			panic("irbuild: viewOwn: std's shared Maybe<Int> is always built")
		}
		steps, ok := bl.discreteRef(at, own, "steps_between", []kind{own, own}, maybeInt)
		if !ok {
			return ir.NoTemp, false
		}
		return view(ir.IterOverRange, append(ops, steps)...)
	}
	// viewSourceAt resolves a bounded type parameter's view to its
	// instance's domain, so vs.dom is one of the arms above.
	panic("viewOwn: a view of domain " + strconv.Itoa(int(vs.dom)) + ", which has no arm")
}
