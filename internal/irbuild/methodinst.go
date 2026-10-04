package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An impl function with its OWN type parameters, instantiated per call.
//
// `fn prefer<K>(_r: self, lhs: K, rhs: K): Bool where K: Comparable` (an
// interface default) and `fn choose<K>(c: ChoiceBox<T>, lhs: K, rhs: K): K`
// (an impl member) are withheld from their block when it is registered,
// because their parameters name a type nothing has bound (implItemSig's and
// inheritDefaults' `generic impl function` gap). A call knows the binding:
// the checker's instantiated signature at the call solves each method type
// parameter. So a call builds one item per (block, method, arguments) under
// the block's frame plus the solved one, as genericimpl.go's
// genericImplInst.subst was written to carry, and queues its body.
//
// The body is built after the caller's, by flushMethodInsts, for
// flushMonoInstances' reason: the call names the item's symbol, and building
// a body in the middle of another would clobber the gen's per-body state.

// methodInst is one instantiation of one generic impl function.
type methodInst struct {
	d     *implDef
	it    *implItem
	frame map[string]kind
	built bool
}

// genericMethodItem is the item a call t to `method` on block d reaches when
// d withheld it as a generic impl function, instantiated at the type
// arguments the checker solved for t, or nil.
func (bl *irScalarBuilder) genericMethodItem(t *ast.Call, d *implDef, method string) *implItem {
	g := bl.g
	if d == nil || !d.lowerable || d.items[method] != nil {
		return nil
	}
	// An impl member's own type parameters (implItemSig), or an interface
	// default's (the interface method's `generic interface function`).
	if gap, withheld := d.gaps[method]; !withheld || (gap.why != "generic impl function" && gap.why != "generic interface function") {
		return nil
	}
	var params []ast.Param
	var tps []ast.TypeParam
	var ret ast.TypeExpr
	var body *ast.Block
	var decl *ast.FuncDef
	var shape *selfShape
	line := 0
	if d.decl != nil {
		for _, item := range d.decl.Items {
			if fd, ok := item.(*ast.FuncDef); ok && fd.Name == method {
				decl = fd
			}
		}
	}
	switch {
	case decl != nil:
		params, tps, ret, line = decl.Params, decl.TypeParams, decl.ReturnTypeExpr, decl.Line
		if decl.Body == nil {
			return nil
		}
		body = decl.Body
	case d.iface != nil && d.iface.methods[method] != nil:
		m := d.iface.methods[method]
		if m.decl == nil || m.decl.Body == nil || m.decl.Extern {
			return nil
		}
		b, isBlock := m.decl.Body.(*ast.Block)
		if !isBlock {
			return nil
		}
		params, tps, ret, body, line = m.decl.Params, m.decl.TypeParams, m.decl.ReturnTypeExpr, b, m.decl.Line
		shape = &m.shape
	default:
		return nil
	}
	if len(tps) == 0 {
		return nil
	}
	ft := g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != len(params) {
		return nil
	}
	own := make(map[string]bool, len(tps))
	for _, tp := range tps {
		own[tp.Name] = true
	}
	solved := map[string]kind{}
	for i, p := range params {
		if p.TypeAnnotation == nil || irUnsolvedType(ft.Params[i]) {
			continue
		}
		g.unifyTypeParams(p.TypeAnnotation, g.project(ft.Params[i]), own, solved)
	}
	if ret != nil && !irUnsolvedType(ft.Return) {
		g.unifyTypeParams(ret, g.project(ft.Return), own, solved)
	}
	frame := map[string]kind{}
	if def := d.recv.def; def != nil && def.genericOf != nil {
		for i, p := range def.genericOf.params {
			frame[p] = def.genericArgs[i]
		}
	}
	for n, k := range d.ifaceSubst {
		frame[n] = k
	}
	args := make([]string, 0, len(tps))
	for _, tp := range tps {
		k, ok := solved[tp.Name]
		if !ok || !irCallableValueKind(k) {
			return nil
		}
		frame[tp.Name] = k
		args = append(args, k.key())
	}
	key := fmt.Sprintf("%p\x00%s\x00%s", d, method, strings.Join(args, "\x00"))
	if mi := g.methodInsts[key]; mi != nil {
		return mi.it
	}
	g.pushIfaceSubst(frame)
	defer g.popIfaceSubst()
	var it *implItem
	if decl != nil {
		item, gap := g.implItemSig(d, decl)
		if item == nil || gap.named() {
			return nil
		}
		it = item
	} else {
		it = &implItem{
			name:      method,
			params0:   params,
			body:      body,
			line:      line,
			result:    kindUnit,
			inherited: true,
			lowerable: true,
		}
		for i, p := range params {
			// kindInvalid: sentinel — no kind read yet for this parameter.
			k := kindInvalid
			if shape.selfTyped(i) {
				k = d.recv
			} else if p.TypeAnnotation != nil {
				k = g.typeOf(p.TypeAnnotation)
			}
			if !irCallableValueKind(k) {
				return nil
			}
			it.params = append(it.params, k)
		}
		if ret != nil {
			if shape.resultBareSelf() {
				it.result = d.recv
			} else {
				it.result = g.typeOf(ret)
			}
		}
		if it.result != kindUnit && !irCallableValueKind(it.result) {
			return nil
		}
	}
	if g.methodInsts == nil {
		g.methodInsts = map[string]*methodInst{}
	}
	mi := &methodInst{d: d, it: it, frame: frame}
	g.methodInsts[key] = mi
	g.methodInstQueue = append(g.methodInstQueue, mi)
	return it
}

// flushMethodInsts builds every queued method instance's body under its
// frame. A worklist: a body may reach a further instance.
func (g *gen) flushMethodInsts() {
	for i := 0; i < len(g.methodInstQueue); i++ {
		mi := g.methodInstQueue[i]
		if mi.built {
			continue
		}
		mi.built = true
		g.pushIfaceSubst(mi.frame)
		g.implFunc(mi.d, mi.it)
		g.popIfaceSubst()
	}
}
