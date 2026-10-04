package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A lambda's destructuring parameter beyond the two shapes irlambda.go
// projects itself (a flat tuple of names, a wrapping distinct's `|Dur(n)|`):
// `|Point{x, y}|`, `|Box.Extent{wide, tall}|`, `|Wrapper.Only(n)|`,
// `|Segment{from: Point{x, y: _}, to: _}|` and `|(a, (b, c))|`.
//
// These build exactly as a named function's destructuring parameter does:
// `g.destructure` resolves the irrefutable pattern against the parameter's
// kind and records its `ir.Proj` and `ir.Bind` nodes into the lambda's entry
// block through the prologue window (irparam.go), and the Binds are the names
// the body reads. A pattern `g.destructure` would refuse declines the lambda
// instead of refusing the program.

// irPatternParam is one destructuring lambda parameter the prologue builds.
type irPatternParam struct {
	pat  ast.Node
	temp ir.Temp
	k    kind
}

// irPrologueParam reports whether p destructures through the prologue rather
// than through irlambda.go's own tuple and distinct projections.
func irPrologueParam(p ast.Param, k kind) bool {
	if p.Destructure == nil {
		return false
	}
	if tp, tuple := p.Destructure.(*ast.TuplePattern); tuple {
		return !irFlatTupleParam(tp, k)
	}
	_, distinct := irDistinctParamBinding(p.Destructure, k)
	return !distinct
}

// irParamPatternKind is the kind a destructuring parameter's pattern names:
// its annotation, the type or enum its head names, or for a head-less pattern
// the type the checker recorded for it. kindInvalid otherwise. It
// reports nothing, unlike patternParamKind, because a lambda declines.
func (g *gen) irParamPatternKind(p ast.Param) kind {
	if p.TypeAnnotation != nil {
		return g.typeOf(p.TypeAnnotation)
	}
	var head ast.TypeExpr
	switch pat := p.Destructure.(type) {
	case *ast.StructPattern:
		head = pat.TypeName
	case *ast.EnumPattern:
		head = pat.Variant
	}
	if head == nil {
		if k, found := g.inferredPatternKind(p.Destructure); found {
			return k
		}
		return kindInvalid
	}
	owner, member, ok := patternHead(head)
	if !ok {
		return kindInvalid
	}
	name := owner
	if name == "" {
		name = member
	}
	if d, found := g.types[name]; found && d.lowerable {
		return named(d)
	}
	return kindInvalid
}

// patternParams builds every prologue-destructured parameter into the
// lambda's entry block and binds the names each introduces.
func (bl *irScalarBuilder) patternParams(params []irPatternParam) bool {
	g, sh := bl.g, bl.sh
	if sh.at == nil {
		// The position a held projection's Copy takes (gen.hold).
		sh.at = params[0].pat
	}
	start := len(sh.entry.Instrs())
	mark := len(g.errs)
	g.pushScope()
	closePrologue := g.irPrologueOpen(sh)
	ok := true
	for _, p := range params {
		src := expr{t: p.temp, k: p.k}
		if !g.destructure(p.pat, src, p.pat) {
			ok = false
			break
		}
	}
	closePrologue(ok)
	g.popScope()
	if len(g.errs) != mark {
		// A refusal g.destructure recorded is this lambda's decline.
		g.errs = g.errs[:mark]
		ok = false
	}
	if !ok {
		irDeclineNote("a lambda destructuring parameter the prologue did not build")
		return false
	}
	for _, in := range sh.entry.Instrs()[start:] {
		b, isBind := in.(*ir.Bind)
		if !isBind {
			continue
		}
		k := sh.frame.kindOf(b.Dst())
		if !irCallableValueKind(k) {
			irDeclineNote("a destructured lambda name's kind outside the domain: " + k.nomi())
			return false
		}
		bl.bound[b.Sym().Name()], bl.boundK[b.Sym().Name()] = b.Dst(), k
	}
	return true
}
