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
// here. The sources with no expression the checker typed against an Iter
// are read from their kind (viewSourceOfKind): an instance of a type
// parameter bounded by Iter (analysis.IterViewBound), whose instances each
// have their own walk; a value in the body of an adapter the builder
// makes (a widened function's parameter or result, an element rebuilt at
// a wider type, a function value forwarding its parameters, each element
// `flatten` views and each result of a `flat_map` callback); a spread's
// part; and the receiver of a call its own type names
// (`Range.each_while`, `String.each_while`).

// viewSeq is the domain of a source that is a sequence already: a value
// of type `Iter<T>`, which has no view of its own.
const viewSeq analysis.IterViewDomain = 0

// viewSource is a source as the view over it walks it: its domain, the
// element it pushes, and for a type's own `impl Iter` the impl's
// functions.
type viewSource struct {
	dom  analysis.IterViewDomain
	elem kind
	user *userIterSource
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
// recorded its view. A sequence has no record and is itself, and so is a
// value in an adapter's body (unwritten) or a stand-in for a parameter
// (placed), which have no expression of their own: their kind names their
// source. A written expression with no record is no source this builder
// views.
func (bl *irScalarBuilder) viewSourceAt(at ast.Node, sk kind) (viewSource, bool) {
	if sk.tag == tagSeq || bl.unwritten {
		return bl.viewSourceOfKind(sk)
	}
	rec := bl.g.iterViewAt(at)
	if _, placed := bl.placed[at]; rec == nil && placed {
		return bl.viewSourceOfKind(sk)
	}
	if rec == nil {
		return viewSource{}, false
	}
	if rec.Domain == analysis.IterViewBound {
		return bl.viewSourceOfKind(sk)
	}
	return bl.viewSourceIn(rec.Domain, rec.Impl, sk)
}

// viewSourceFor is the source at as viewSourceAt reads it, or, for at nil,
// as its kind sk names it: a call whose callee's own type names the
// source (`Range.each_while(r, f)`), or a value with no expression.
func (bl *irScalarBuilder) viewSourceFor(at ast.Node, sk kind) (viewSource, bool) {
	if at == nil {
		return bl.viewSourceOfKind(sk)
	}
	return bl.viewSourceAt(at, sk)
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
		ok = ok && sk != kindEmptyVector
	case analysis.IterViewSet:
		vs.elem, ok = setElem(sk)
		ok = ok && sk != kindEmptySet
	case analysis.IterViewMap:
		if sk.tag == tagMap && sk != kindEmptyMap && sk.comp != nil && len(sk.comp.parts) == 2 {
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
			return viewSource{}, false
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
	if sk == kindEmptyList || sk == kindEmptySet || sk == kindEmptyVector {
		// `[]`, `[]` or `[]` entering an `Iter<T>` is built at T.
		target := bl.g.listKind(elem)
		if sk == kindEmptySet {
			target = bl.g.setKindOf(elem)
		} else if sk == kindEmptyVector {
			target = bl.g.vectorKindOf(elem)
		}
		v, k, ok := bl.coerceEmpty(at, src, sk, target)
		if !ok {
			return ir.NoTemp, false
		}
		src, sk = v, k
	}
	vs, ok := bl.viewSourceAt(at, sk)
	if !ok {
		return ir.NoTemp, false
	}
	return bl.viewAs(at, src, sk, vs, elem)
}

// viewAs views src, a value of kind sk walked as vs, as an `Iter<elem>`.
// A source whose element is not elem but becomes it (`it: Iter<Shape> =
// [circle]` for `enum Shape { embeds Circle }`) is viewed at its own
// element, and each element converted as it is read (rebuildAdapter: an
// embedded value widened, a Maybe<Circle> rebuilt as a Maybe<Shape>, a
// List<Int> viewed as an Iter<Int>, a value that enters an interface
// boxed boxed).
func (bl *irScalarBuilder) viewAs(at ast.Node, src ir.Temp, sk kind, vs viewSource, elem kind) (ir.Temp, bool) {
	want := seqKindIn(bl.g, elem)
	if sk == want {
		return src, true
	}
	view := func(over ir.IterDomain, operands ...ir.Temp) ir.Temp {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, over, operands...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: want})
		return n.Dst()
	}
	converted := func() (ir.Temp, bool) {
		from := vs.elem
		if from == elem || !irCallableValueKind(from) || bl.erasesUnboxed(elem, from) {
			return ir.NoTemp, false
		}
		fn, ok := bl.rebuildAdapter(at, from, elem)
		if !ok {
			return ir.NoTemp, false
		}
		inner := src
		if vs.dom != viewSeq {
			if inner, ok = bl.viewAs(at, src, sk, vs, from); !ok {
				return ir.NoTemp, false
			}
		}
		return bl.iterAppend(at, ir.IterMap, ir.IterOverSeq, want, inner, fn), true
	}
	switch vs.dom {
	case viewSeq:
		return converted()
	case analysis.IterViewList:
		if vs.elem == elem || bl.erasesUnboxed(elem, vs.elem) {
			// `it: Iter<Shape> = [circle]` for an interface Shape: an
			// existential is the concrete value, so each element is
			// already the interface's value and the list is viewed as it
			// is.
			return view(ir.IterOverList, src), true
		}
		return converted()
	case analysis.IterViewString:
		// `show("ab")` for `fn show(xs: Iter<Display>)`: each grapheme is
		// already the interface's value, as a list's element is; an
		// element of another kind is converted as it is read.
		if elem == kindString || bl.erasesUnboxed(elem, kindString) {
			return view(ir.IterOverString, src), true
		}
		return converted()
	case analysis.IterViewBytes:
		if elem == irByteKind() || bl.erasesUnboxed(elem, irByteKind()) {
			return view(ir.IterOverBytes, src), true
		}
		return converted()
	case analysis.IterViewRange:
		// The range walks its own element; `show(1..3)` into an
		// `Iter<Display>` reads each Int as the interface's value, and an
		// element of another kind is converted as it is read.
		own := vs.elem
		if own != elem && !bl.erasesUnboxed(elem, own) {
			return converted()
		}
		ops, ok := bl.rangeView(at, src, own)
		if !ok {
			return ir.NoTemp, false
		}
		// The view answers the Range's own known_count, so it carries the
		// element's distance function (rt.Seq's Count). One whose impl
		// inherits the None default has no count to carry.
		if bl.inheritsStepsBetween(own) {
			return view(ir.IterOverRange, ops...), true
		}
		maybeInt, shared := bl.g.sharedMaybe(kindInt)
		if !shared {
			return ir.NoTemp, false
		}
		steps, ok := bl.discreteRef(at, own, "steps_between", []kind{own, own}, maybeInt)
		if !ok {
			return ir.NoTemp, false
		}
		return view(ir.IterOverRange, append(ops, steps)...), true
	case analysis.IterViewVector:
		if v, ok := converted(); ok {
			return v, true
		}
		// sk is a lowered value, so its element is in the domain and a
		// Vector holds it.
		erased := irExistentialKind(elem) && bl.g.irErases(elem, vs.elem)
		if vs.elem != elem && !erased {
			return ir.NoTemp, false
		}
		return view(ir.IterOverVector, src), true
	case analysis.IterViewMap:
		if vs.elem == elem {
			return bl.mapView(at, src, sk, vs.elem), true
		}
		if bl.erasesUnboxed(elem, vs.elem) {
			// `count({1 => 2})` into an `Iter<Display>`: each `(K, V)`
			// pair is already the interface's value, as a list's element
			// is.
			return view(ir.IterOverMap, src), true
		}
		return converted()
	case analysis.IterViewSet:
		if v, ok := converted(); ok {
			return v, true
		}
		if vs.elem != elem {
			return ir.NoTemp, false
		}
		return view(ir.IterOverSet, src), true
	case analysis.IterViewMaybe:
		if v, ok := converted(); ok {
			return v, true
		}
		if vs.elem != elem && !(irExistentialKind(elem) && bl.g.irErases(elem, vs.elem)) {
			return ir.NoTemp, false
		}
		return view(ir.IterOverMaybe, src), true
	case analysis.IterViewImpl:
		// The view's operands are the impl's functions, whatever the
		// element: one already the interface's value is viewed as it is.
		if vs.elem == elem || bl.erasesUnboxed(elem, vs.elem) {
			return bl.userView(at, src, sk, vs.user, elem), true
		}
		return converted()
	}
	// viewSourceAt resolves a bounded type parameter's view to its
	// instance's domain, so vs.dom is one of the arms above.
	panic("viewAs: a view of domain " + strconv.Itoa(int(vs.dom)) + ", which has no arm")
}
