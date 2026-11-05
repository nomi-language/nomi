package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irElseless is the region of an `if` with no `else`, or an `else if`
// chain with no final `else`, that the checker typed `Maybe<T>`
// (analysis.FileAnalysis.ElselessIfs): each arm's value is wrapped in
// `Some`, and the missing `else` writes `None`.
type irElseless struct {
	head  *ast.If
	maybe kind
	inner kind
	some  *variantDef
	none  *variantDef
	// links are the chain's `if`s, head included: the else arm that is one
	// continues the region, and any other nested `if` is a value of its own.
	links map[*ast.If]bool
}

// elselessType is the Maybe type the checker gave t, an `if` with no
// `else` whose branches have a value, or nil. A pipe's `if` stage is
// lowered from a copy of its node (irpipe.go), which splicedIfs maps back.
func (g *gen) elselessType(t *ast.If) analysis.Type {
	if orig, ok := g.splicedIfs[t]; ok {
		t = orig
	}
	fa := g.srcFA()
	if fa == nil {
		return nil
	}
	return fa.ElselessIfs[t]
}

// How ifRegion lowers an `if`, as enterElseless decides.
const (
	// elselessNo: t is no elseless region's head, or sig already lowers it.
	elselessNo = iota
	// elselessArms: each arm wraps its value, with the signature answered.
	elselessArms
	// elselessValue: the region's result is not a Maybe (an interface
	// the Maybe enters, `ToHtml` in an `Html` literal's slot), so t is
	// lowered as a Maybe value and that value enters the result.
	elselessValue
	// elselessDeclined: the Maybe has no kind here.
	elselessDeclined
)

// enterElseless is how ifRegion lowers t under sig, and for elselessArms
// the signature t's arms are lowered with.
func (bl *irScalarBuilder) enterElseless(t *ast.If, sig irFuncSig) (irFuncSig, int) {
	if (sig.elseless != nil && sig.elseless.head == t) || sig.testArms {
		// A test body's statement `if` discards its value, so its arms
		// are test statements whatever their type.
		return sig, elselessNo
	}
	ty := bl.g.elselessType(t)
	if ty == nil {
		return sig, elselessNo
	}
	maybe := sig.result
	if sig.inferResult {
		if analysis.ResolveTypeVar(ty) == nil || irUnsolvedType(ty) {
			bl.g.dc.note("an `if` without `else` whose Maybe the checker left open")
			return sig, elselessDeclined
		}
		maybe = bl.g.project(ty)
	} else if !irMaybeKind(maybe) {
		return sig, elselessValue
	}
	if !irMaybeKind(maybe) || len(maybe.def.preludeArgs) != 1 {
		bl.g.dc.note("an `if` without `else` whose value is not a Maybe here: " + maybe.nomi())
		return sig, elselessDeclined
	}
	some, none := maybe.def.variant("Some"), maybe.def.variant("None")
	if some == nil || none == nil || len(some.payloads) != 1 {
		bl.g.dc.note("an `if` without `else` whose Maybe has no Some and None: " + maybe.nomi())
		return sig, elselessDeclined
	}
	es := &irElseless{head: t, maybe: maybe, inner: maybe.def.preludeArgs[0], some: some, none: none, links: map[*ast.If]bool{}}
	for l := t; l != nil; {
		es.links[l] = true
		next, isIf := l.Else.(*ast.If)
		if !isIf {
			break
		}
		l = next
	}
	sig.inferResult, sig.result, sig.elseless = false, maybe, es
	return sig, elselessArms
}

// elselessAsValue lowers t, an `if` without `else` whose region's result
// is an interface its Maybe enters, as a Maybe value of its own, which
// then enters the result as any value entering it does.
func (bl *irScalarBuilder) elselessAsValue(t *ast.If, sig irFuncSig) (kind, bool) {
	val, k, _, ok := bl.inferredRegionValue(t, false)
	if !ok {
		return kindInvalid, false
	}
	have := k
	if val, k, ok = bl.coerceEmpty(t, val, k, sig.result); !ok || k != sig.result {
		bl.g.dc.note("an `if` without `else` whose " + have.nomi() + " does not enter " + sig.result.nomi())
		return kindInvalid, false
	}
	bl.resultCopy(t, val)
	return k, true
}

// elselessNone writes `None` to the region's result from els, the edge of
// an `if` with no `else` that no branch took, and jumps to exit.
func (bl *irScalarBuilder) elselessNone(els, exit *ir.Block, t *ast.If, es *irElseless) kind {
	prev := bl.b
	bl.b = els
	v, k, _, _ := bl.variantValue(t, es.maybe.def, es.none, nil, true)
	bl.resultCopy(t, v)
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(t), exit.ID()))
	bl.b = prev
	return k
}

// elselessArmRegion reports whether an elseless region's arm that is the
// region nested lowers into the region's result directly, as a link of its
// chain does. Any other `if` or `case` is a value the arm wraps whole.
func elselessArmRegion(nested ast.Node, es *irElseless) bool {
	t, isIf := nested.(*ast.If)
	return isIf && es.links[t]
}

// elselessSome lowers body, an arm of an elseless region, as the Maybe's
// payload and wraps it in `Some`.
func (bl *irScalarBuilder) elselessSome(body ast.Node, es *irElseless) (ir.Temp, kind, bool) {
	val, k, _, ok := bl.lowerWant(body, es.inner)
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	if val, k, ok = bl.coerceEmpty(body, val, k, es.inner); !ok || k != es.inner {
		if ok {
			bl.g.dc.note("an arm of an `if` without `else` whose kind is not the Maybe's payload: " +
				k.nomi() + " vs " + es.inner.nomi())
		}
		return ir.NoTemp, kindInvalid, false
	}
	v, mk, _, _ := bl.variantValue(body, es.maybe.def, es.some, []ir.Temp{val}, false)
	return v, mk, true
}
