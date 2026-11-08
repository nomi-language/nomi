package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// taggedLiteral lowers `Tag"text ${v}"`: each part becomes a Fragment
// variant, text as Static and a slot as Dynamic, every slot but the last
// forced, and the list goes to the tag's `from_fragments`. Only a handler this
// file's own impl block declares, or a retained stdlib body, retains; sibling
// handlers are declined.
func (bl *irScalarBuilder) taggedLiteral(t *ast.TaggedString) (ir.Temp, kind, bool, bool) {
	why := ""
	no := func() (ir.Temp, kind, bool, bool) {
		if why != "" {
			bl.g.dc.note("a typed literal: " + why)
		}
		return ir.NoTemp, kindInvalid, false, false
	}
	h, found := bl.g.literalImpl(t)
	switch {
	case !found:
		why = "no handler"
	case h.rivalled:
		why = "a rivalled handler"
	case h.unit >= 0 && h.sym == nil:
		why = "a sibling file's handler"
	case h.pkg != "" && h.unit < 0:
		why = "a Go-package handler"
	}
	if why != "" {
		return no()
	}
	fd, _ := bl.g.literalHandlerDecl(t)
	callee, why := bl.g.literalCallee(h, fd)
	if callee == nil {
		return no()
	}
	if bl.g.srcFA().CheckedLiterals[t] && !bl.g.checkedLiteralSite(t) {
		why = "a checked literal whose check does not lower"
		return no()
	}
	if len(h.params) != 1 || !irListTransportKind(h.params[0]) || !irCallableValueKind(h.result) {
		why = "a handler signature outside the retained domain"
		return no()
	}
	elem := h.params[0].comp.parts[0]
	d := elem.def
	if d == nil || d.preludeOf == nil || fragmentSpec == nil || d.preludeOf.spec != fragmentSpec || !irRetainedEnumKind(d) {
		why = "a handler element that is not a retained Fragment"
		return no()
	}
	static, dynamic, shaped := fragmentVariants(d)
	if !shaped {
		why = "a Fragment without Static and Dynamic"
		return no()
	}
	if t.Raw && bl.literalCell != t && irCheckedLiteralKind(h.result) {
		// A backtick literal whose handler can fail was checked at compile
		// time; its value is the Ok payload of its cell.
		return bl.checkedLiteral(t, h.result)
	}
	parts := fragmentParts(t.Parts)
	last := lastDynamicPart(parts)
	items := make([]ir.Temp, 0, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case ast.StringText:
			c := ir.NewString(bl.g.irNodeSpan(t), bl.f.NewTemp(), p.Value)
			bl.b.Append(c)
			v, _, _, ok := bl.variantValue(t, d, static, []ir.Temp{c.Dst()}, true)
			if !ok {
				return no()
			}
			items = append(items, v)
		case ast.StringExpr:
			want := dynamic.payloads[0].k
			v, k, mobile, ok := bl.lower(p.Expr)
			if !ok {
				return no()
			}
			if i != last && !mobile {
				mobile = true
			}
			v, k, ok = bl.coerceEmpty(p.Expr, v, k, want)
			if !ok || k != want {
				return no()
			}
			fv, _, _, ok := bl.variantValue(t, d, dynamic, []ir.Temp{v}, mobile)
			if !ok {
				return no()
			}
			items = append(items, fv)
		default:
			return no()
		}
	}
	list := ir.NewMakeList(bl.g.irNodePos(t), bl.f.NewTemp(), items, ir.NoTemp)
	bl.b.Append(list)
	bl.side(list.Dst(), irScalarSide{k: h.params[0]})
	c := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall, callee, list.Dst())
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: h.result, deferrable: true})
	return c.Dst(), h.result, false, true
}

// literalCallee is the symbol a call to the handler h, declared by fd,
// names, or nil and why it does not lower.
func (g *gen) literalCallee(h literalTarget, fd *ast.FuncDef) (*ir.Symbol, string) {
	if h.inst != nil {
		return h.inst.sym, ""
	}
	if h.item != nil {
		return g.irCalleeSym(h.item, h.label), ""
	}
	if h.unit < 0 && h.sym != nil {
		// Another file's instance impl (genericLiteralImpl).
		return h.sym, ""
	}
	if h.std != nil {
		// A stdlib handler, such as Regex's, is a retained std body called
		// as stdlibInvoke spells it.
		f := h.std
		if f.canon != nil {
			f = f.canon
		}
		if f.irBody == nil || f.rtCall != "" {
			return nil, "a stdlib handler with no retained body"
		}
		return f.irBody.Sym(), ""
	}
	if h.unit >= 0 {
		// A sibling file's handler, called by the declaring unit's symbol
		// for its retained body, as a type-qualified sibling impl call is.
		return h.sym, ""
	}
	it := g.implItemFor(fd)
	if it == nil || irImplSource(it) == nil {
		return nil, ""
	}
	return g.irCalleeSym(it, h.label), ""
}

// literalImplItem is the impl item literalImpl's local route resolves.
func (g *gen) literalImplItem(t *ast.TaggedString) *implItem {
	fd, found := g.literalHandlerDecl(t)
	if !found {
		return nil
	}
	return g.implItemFor(fd)
}

// implItemFor is the lowerable impl item of this module that declares fd, or
// nil.
func (g *gen) implItemFor(fd *ast.FuncDef) *implItem {
	if fd == nil {
		return nil
	}
	for _, d := range g.implOrder {
		if d.lowerable && d.decl != nil && implBlockDeclares(d.decl, fd) {
			return d.items[fd.Name]
		}
	}
	return nil
}
