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

// withheldMember is the declaration of an impl function block d withheld
// because a call must instantiate it: its parameters, its own type
// parameters, its result annotation and body, and for an inherited interface
// default the interface method's self shape.
type withheldMember struct {
	params []ast.Param
	tps    []ast.TypeParam
	ret    ast.TypeExpr
	body   *ast.Block
	decl   *ast.FuncDef
	shape  *selfShape
	line   int
}

// withheldMember finds the declaration behind method when block d withheld it
// as a function a call instantiates (genericMethodItem): an impl member's own
// type parameters (implItemSig), an interface default's (the interface
// method's `generic interface function`), or a member whose signature wraps
// the receiver (expansiveImplItem).
func (d *implDef) withheldMember(method string) (withheldMember, bool) {
	var w withheldMember
	if d == nil || !d.lowerable || d.items[method] != nil {
		return w, false
	}
	gap, withheld := d.gaps[method]
	if !withheld || (gap.why != "generic impl function" && gap.why != "generic interface function" && gap.why != "expansive impl function") {
		return w, false
	}
	if d.decl != nil {
		for _, item := range d.decl.Items {
			if fd, ok := item.(*ast.FuncDef); ok && fd.Name == method {
				w.decl = fd
			}
		}
	}
	switch {
	case w.decl != nil:
		if w.decl.Body == nil {
			return w, false
		}
		w.params, w.tps, w.ret, w.line, w.body = w.decl.Params, w.decl.TypeParams, w.decl.ReturnTypeExpr, w.decl.Line, w.decl.Body
	case d.iface != nil && d.iface.methods[method] != nil:
		m := d.iface.methods[method]
		if m.decl == nil || m.decl.Body == nil || m.decl.Extern {
			return w, false
		}
		b, isBlock := m.decl.Body.(*ast.Block)
		if !isBlock {
			return w, false
		}
		w.params, w.tps, w.ret, w.body, w.line = m.decl.Params, m.decl.TypeParams, m.decl.ReturnTypeExpr, b, m.decl.Line
		w.shape = &m.shape
	default:
		return w, false
	}
	// An expansive member may have no type parameters of its own
	// (`fn gather(b: Box<T>): Box<List<T>>` in `impl Box<T>`): its frame is
	// the block's alone, and the key is the block and the method.
	if len(w.tps) == 0 && gap.why != "expansive impl function" {
		return w, false
	}
	return w, true
}

// supplies reports whether block d has a function `method`: an item, or a
// member withheld for a call to instantiate.
func (d *implDef) supplies(method string) bool {
	if d.items[method] != nil {
		return true
	}
	_, ok := d.withheldMember(method)
	return ok
}

// genericMethodItem is the item a call t to `method` on block d reaches when
// d withheld it as a generic impl function, instantiated at the type
// arguments the checker solved for t, or nil.
func (bl *irScalarBuilder) genericMethodItem(t *ast.Call, d *implDef, method string) *implItem {
	g := bl.g
	w, ok := d.withheldMember(method)
	if !ok {
		return nil
	}
	// Only the member's own type parameters need the checker's solved
	// signature; with none, the block's frame is the whole binding. The
	// signature at `X.grow(x)` inside a generic caller names X, not the
	// instance, so it cannot be required then.
	var here []kind
	// kindInvalid: sentinel — a result the checker left open solves nothing.
	result := kindInvalid
	if len(w.tps) > 0 {
		ft := g.checkedCallSignature(t)
		if ft == nil || len(ft.Params) != len(w.params) {
			return nil
		}
		here = make([]kind, len(ft.Params))
		for i, pt := range ft.Params {
			// kindInvalid: sentinel — an open parameter solves nothing.
			here[i] = kindInvalid
			if !irUnsolvedType(pt) {
				here[i] = g.project(pt)
			}
		}
		if ft.Return != nil && !irUnsolvedType(ft.Return) {
			result = g.project(ft.Return)
		}
	}
	return g.methodInstance(d, method, w, here, result)
}

// methodInstance is block d's withheld member w instantiated at the
// parameter and result kinds a call was typed at, which are this gen's: each
// of the member's own type parameters is solved by unifying its annotations
// against them. kindInvalid at a position solves nothing. The instance is
// interned per (block, method, arguments) and its body queued.
//
// A call in another file reaches this through the declaring gen
// (siblingMethodInstance), which hands it kinds imported into its own tables.
func (g *gen) methodInstance(d *implDef, method string, w withheldMember, here []kind, result kind) *implItem {
	solved := map[string]kind{}
	if len(w.tps) > 0 {
		if len(here) != len(w.params) {
			return nil
		}
		own := make(map[string]bool, len(w.tps))
		for _, tp := range w.tps {
			own[tp.Name] = true
		}
		for i, p := range w.params {
			// kindInvalid: sentinel — an open parameter solves nothing.
			if p.TypeAnnotation == nil || here[i] == kindInvalid {
				continue
			}
			g.unifyTypeParams(p.TypeAnnotation, here[i], own, solved)
		}
		// kindInvalid: sentinel — an open result solves nothing.
		if w.ret != nil && result != kindInvalid {
			g.unifyTypeParams(w.ret, result, own, solved)
		}
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
	args := make([]string, 0, len(w.tps))
	for _, tp := range w.tps {
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
	if w.decl != nil {
		item, gap := g.implItemSig(d, w.decl)
		if item == nil || gap.named() {
			return nil
		}
		it = item
	} else {
		it = &implItem{
			name:      method,
			params0:   w.params,
			body:      w.body,
			line:      w.line,
			result:    kindUnit,
			inherited: true,
			lowerable: true,
		}
		for i, p := range w.params {
			// kindInvalid: sentinel — no kind read yet for this parameter.
			k := kindInvalid
			if w.shape.selfTyped(i) {
				k = d.recv
			} else if p.TypeAnnotation != nil {
				k = g.typeOf(p.TypeAnnotation)
			}
			if !irCallableValueKind(k) {
				return nil
			}
			it.params = append(it.params, k)
		}
		if w.ret != nil {
			if w.shape.resultBareSelf() {
				it.result = d.recv
			} else {
				it.result = g.typeOf(w.ret)
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

// expansiveImplItem reports whether fd, a member of a block registered for an
// instance of a generic template, names that same template at an argument that
// wraps a type parameter. In `impl Result<T, E>`,
// `fn collect<T, E>(results: Iter<Result<T, E>>): Result<List<T>, E>` is one.
//
// A block registered for an instance builds every member it supplies, and
// this member's signature mints a larger instance: built for `Result<Int, E>`
// it names `Result<List<Int>, E>`, whose block builds it again for
// `Result<List<List<Int>>, E>`, without end. Withheld from the block, a call
// builds it once per solved argument tuple (genericMethodItem), and the
// instance that build mints withholds it in turn, so the chain stops after one
// step.
func (g *gen) expansiveImplItem(d *implDef, fd *ast.FuncDef) bool {
	def := d.recv.def
	if d.recv.tag != tagNamed || def == nil || def.genericOf == nil {
		return false
	}
	tpl := def.genericOf
	params := map[string]bool{}
	for _, p := range tpl.params {
		params[p] = true
	}
	for _, tp := range fd.TypeParams {
		params[tp.Name] = true
	}
	var mentions func(te ast.TypeExpr) bool
	mentions = func(te ast.TypeExpr) bool {
		switch t := te.(type) {
		case *ast.SimpleType:
			return params[t.Name]
		case *ast.GenericType:
			for _, a := range t.Params {
				if mentions(a) {
					return true
				}
			}
		case *ast.FuncType:
			for _, a := range t.Params {
				if mentions(a) {
					return true
				}
			}
			return t.Return != nil && mentions(t.Return)
		}
		return false
	}
	var expansive func(te ast.TypeExpr) bool
	expansive = func(te ast.TypeExpr) bool {
		switch t := te.(type) {
		case *ast.GenericType:
			if named, _, ok := g.genericTemplateNamed(t.Name); ok && named == tpl {
				for _, a := range t.Params {
					if _, bare := a.(*ast.SimpleType); !bare && mentions(a) {
						return true
					}
				}
			}
			for _, a := range t.Params {
				if expansive(a) {
					return true
				}
			}
		case *ast.FuncType:
			for _, a := range t.Params {
				if expansive(a) {
					return true
				}
			}
			return t.Return != nil && expansive(t.Return)
		}
		return false
	}
	for _, p := range fd.Params {
		if p.TypeAnnotation != nil && expansive(p.TypeAnnotation) {
			return true
		}
	}
	return fd.ReturnTypeExpr != nil && expansive(fd.ReturnTypeExpr)
}
