package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// An impl function with its OWN type parameters, instantiated per call.
//
// `fn prefer<K>(_r: self, lhs: K, rhs: K) -> Bool where K: Comparable` (an
// interface default) and `fn choose<K>(c: ChoiceBox<T>, lhs: K, rhs: K) -> K`
// (an impl member) are withheld from their block when it is registered,
// because their parameters name a type nothing has bound (implItemSig's and
// inheritDefaults' `generic impl function` gap). A call knows the binding:
// the checker records the type argument it instantiated the call at for
// each method type parameter (calleeTypeArgs). So a call builds one item per
// (block, method, arguments) under the block's frame plus those, as
// genericimpl.go's genericImplInst.subst was written to carry, and queues
// its body. A type argument inference left open at the call, which no value
// depends on (`Box.flat(Box.Full(None))`), is read as the type the checker
// closed it as (analysis.TypeVar.Closed), as the call's operands are.
//
// The block's frame is its instance's. A type-qualified call names the
// instance the checker instantiated the block's own parameters at
// (templateCallInstance), not the receiver operand's: `Box.flat(b)` with
// `fn flat(b: Box<Maybe<T>>) -> Maybe<T>` in `impl Box<T>` over a
// `Box<Maybe<Int>>` is `Box<Int>`'s.
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
	// accessorName is set when another file's call omits a defaulted
	// parameter of this instance whose default it cannot lower itself
	// (methodInstAccessors): the instance's default accessors are then
	// built under its frame, named as a call site names them.
	accessorName   string
	accessorsBuilt bool
}

// withheldMember is the declaration of an impl function block d withheld
// because a call must instantiate it: its parameters, its own type
// parameters, its result annotation and body, and for an inherited interface
// default the interface method's self shape.
type withheldMember struct {
	params []ast.Param
	tps    []ast.TypeParam
	where  []ast.WhereConstraint
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
	if !withheld || (gap.why != "generic impl function" && gap.why != gapGenericIfaceFunc && gap.why != "expansive impl function") {
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
		w.where = w.decl.WhereClauses
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
		w.where = m.decl.WhereClauses
		w.shape = &m.shape
	default:
		return w, false
	}
	// An expansive member may have no type parameters of its own
	// (`fn gather(b: Box<T>) -> Box<List<T>>` in `impl Box<T>`): its frame is
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

// typeParamNames is w's own type parameters' names, in declaration order.
func (w withheldMember) typeParamNames() []string {
	names := make([]string, len(w.tps))
	for i, tp := range w.tps {
		names[i] = tp.Name
	}
	return names
}

// genericMethodItem is the item a call t to `method` on block d reaches when
// d withheld it as a generic impl function, instantiated at the type
// arguments the checker instantiated t at (calleeTypeArgs), or nil.
func (bl *irScalarBuilder) genericMethodItem(t *ast.Call, d *implDef, method string) *implItem {
	w, ok := d.withheldMember(method)
	if !ok {
		return nil
	}
	args, ok := bl.g.calleeTypeArgs(t.Func, w.typeParamNames())
	if !ok {
		return nil
	}
	return bl.g.methodInstance(d, method, w, args)
}

// methodInstance is block d's withheld member w instantiated at args, its
// own type parameters' arguments in declaration order, which are this
// gen's kinds, under the block's own frame. The instance is interned per
// (block, method, arguments) and its body queued.
//
// A call in another file reaches this through the declaring gen
// (siblingMethodInstance), which hands it kinds imported into its own tables.
func (g *gen) methodInstance(d *implDef, method string, w withheldMember, own []kind) *implItem {
	if len(own) != len(w.tps) {
		return nil
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
	for i, tp := range w.tps {
		frame[tp.Name] = own[i]
		args = append(args, own[i].key())
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
			impl:      d,
			instKey:   g.substLinkKey(),
			name:      method,
			params0:   w.params,
			body:      w.body,
			line:      w.line,
			result:    kindUnit,
			inherited: true,
			lowerable: true,
		}
		// A `self` inside a parameter or result type (`(self, U)`) is the
		// implementor, as in inheritedDefaultSig.
		prevSelf := g.defaultSelf
		g.defaultSelf = &irDefaultSelf{iface: d.iface, recv: d.recv}
		defer func() { g.defaultSelf = prevSelf }()
		for i, p := range w.params {
			// kindInvalid: sentinel — no kind read yet for this parameter.
			k := kindInvalid
			if w.shape.selfTyped(i) {
				k = d.recv
			} else if p.TypeAnnotation != nil || p.Default != nil {
				k = g.paramKind(p)
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
		if !mi.built {
			mi.built = true
			g.pushIfaceSubst(mi.frame)
			g.implFunc(mi.d, mi.it)
			g.popIfaceSubst()
		}
		if mi.accessorName != "" && !mi.accessorsBuilt {
			mi.accessorsBuilt = true
			g.pushIfaceSubst(mi.frame)
			g.methodInstSlotDefaults(mi)
			g.popIfaceSubst()
		}
	}
}

// methodInstAccessors asks g, the gen that declares the instance it, for an
// accessor per defaulted parameter of it (irFileSlotRetain), so a call in
// another file can fill a default that names what only this file can lower
// (fillFromAccessors). name is the callee name the call site gives the instance.
// It answers the instance's callee symbol, or nil when it is no instance of
// a declared member. A request after the instance was built queues it again,
// so the next flush builds the accessors.
func (g *gen) methodInstAccessors(it *implItem, name string) *ir.Symbol {
	if it == nil || it.decl == nil || it.inherited {
		return nil
	}
	for _, mi := range g.methodInsts {
		if mi.it != it {
			continue
		}
		if mi.accessorName == "" {
			mi.accessorName = name
			if mi.built {
				g.methodInstQueue = append(g.methodInstQueue, mi)
			}
		}
		return g.irCalleeSym(it, name)
	}
	return nil
}

// methodInstSlotDefaults builds mi's default accessors, under its frame.
func (g *gen) methodInstSlotDefaults(mi *methodInst) {
	it := mi.it
	fd := it.decl
	if g.irMod == nil || fd == nil || len(it.params) != len(fd.Params) || !it.lowerable {
		return
	}
	callee := g.irCalleeSym(it, mi.accessorName)
	sig := &fnSig{params: it.params, result: it.result, decl: fd, lowerable: true}
	for slot, p := range fd.Params {
		if p.Default != nil {
			g.irFileSlotRetain(fd, sig, callee, mi.accessorName, slot)
		}
	}
}

// expansiveImplItem reports whether fd, a member of a block registered for an
// instance of a generic template, names that same template at an argument that
// wraps a type parameter. In `impl Result<T, E>`,
// `fn collect<T, E>(results: Iter<Result<T, E>>) -> Result<List<T>, E>` is one.
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
