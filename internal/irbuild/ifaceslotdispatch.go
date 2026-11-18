package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A PARAMETER DEFAULT OF AN INTERFACE FUNCTION, CHOSEN BY DISPATCH.
//
// A call that leaves a defaulted parameter out takes the default of the
// function that runs: the impl function's, when it declares one for that
// parameter, otherwise the interface's (spec §12 *Item kinds*). A call
// whose receiver's type the checker knows reaches the impl's function, and
// its record names each slot's declaration (analysis.DefaultedCall.From);
// through a type parameter the instance reaches it, and fills from its
// parameters where they declare a default (defaultHome.params). A call on
// an interface value (`s: Shape`, then `Shape.area(s)`) knows the function
// only when it runs, so it chooses the default as it chooses the function:
// by dispatch on the receiver.
//
// Each defaulted parameter of an interface function has a dispatch symbol
// of its own, interned beside the function's (irIfaceSlotDispatchSym). Each
// impl records, under it, its accessor for the slot: a function of the
// parameters before the slot answering its default. When the impl function
// declares a default there, that is the accessor its file builds for it
// (irImplOwnSlotRetain, through irSlotAccessorRetain, the builder of every
// default accessor); otherwise it is the interface's accessor at the
// impl's kinds (irIfaceSlotSym), built in the interface's file. The
// entries are extra rows of the dispatch table the function's own entries
// are in (irRecordImpls, irRecordBoxedImpls). A call in another file whose
// receiver's type is known, and a partial's supplier for an open slot,
// call the impl's accessor directly (implSlotHome).
//
// The call fills each slot it leaves out, in parameter order and after
// every written argument, with a call dispatched on the receiver to that
// slot's symbol over the slots before it (fillFromAccessors), so a default
// reading an earlier parameter reads it there, the receiver among them. A
// slot before the receiver (`fn tag(label: String = "x", target: self)`)
// has an accessor that takes the receiver after those slots, unread, so
// the call has its operand to dispatch on (keyedSlot).
//
// Whether a call dispatches is the receiver's: a call the checker resolved
// to no impl (analysis.CalleeInterface) whose receiver is an interface value
// where it is lowered (erasedInterfacePlan, dispatchedCallHome). Through a
// type parameter the instance's receiver is concrete, and the call reaches
// the impl's function.

// irSlotDispatch is one impl function's accessor for a defaulted parameter
// of its interface function: method is the slot's dispatch symbol, fn the
// accessor and owner the gen that builds it.
type irSlotDispatch struct {
	method, fn *ir.Symbol
	owner      *gen
}

// irImplSlotDispatch builds or asks for the accessor of each defaulted
// parameter of the program interface function that impl function it of d
// implements, and notes it for the dispatch entries. Called from implFunc,
// so a generic block's instance is under its frame.
func (g *gen) irImplSlotDispatch(d *implDef, it *implItem) {
	m := g.slotDispatchMethod(d, it)
	if m == nil {
		return
	}
	for slot, p := range m.decl.Params {
		if p.Default == nil {
			continue
		}
		method := g.irIfaceSlotDispatchSym(d.iface, m, slot)
		if method == nil {
			continue
		}
		fn, owner, own := g.implSlotSym(d, it, slot)
		if fn == nil {
			continue
		}
		if own {
			g.irImplOwnSlotRetain(d, it, slot, fn)
		}
		if g.slotDispatches == nil {
			g.slotDispatches = map[*implItem][]irSlotDispatch{}
		}
		g.slotDispatches[it] = append(g.slotDispatches[it], irSlotDispatch{method: method, fn: fn, owner: owner})
	}
}

// slotDispatchMethod is the interface function that impl function it of d
// implements when it has an accessor for each of that function's defaulted
// parameters (irImplSlotDispatch): a program interface's dispatchable
// function. Nil otherwise.
func (g *gen) slotDispatchMethod(d *implDef, it *implItem) *ifaceMethod {
	iface := d.iface
	if g.irMod == nil || iface == nil || iface.stdOrigin != "" || !it.lowerable {
		return nil
	}
	m := iface.methods[it.name]
	if m == nil || m.decl == nil || !m.dispatchable() || len(m.decl.Params) != len(it.params) {
		return nil
	}
	return m
}

// implSlotSym is impl function it's accessor for slot, a parameter its
// interface function defaults, and the gen that builds it: its own
// default's (own), which g builds as it lowers the function
// (irImplOwnSlotRetain), or the interface's at it's kinds, which the
// interface's file builds when asked (irIfaceSlotSym).
func (g *gen) implSlotSym(d *implDef, it *implItem, slot int) (sym *ir.Symbol, owner *gen, own bool) {
	m := d.iface.methods[it.name]
	key := m.shape.recvAt()
	if fd := it.decl; fd != nil && len(fd.Params) == len(it.params) && fd.Params[slot].Default != nil {
		callee := g.irCalleeSym(it, d.ownerName()+"."+it.name)
		name := d.recv.nomi() + "." + it.name
		if key < slot {
			return g.irFileSlotSym(callee, name, slot), g, true
		}
		return g.irCalleeSym(irKeyedSlotDefault{callee: callee, slot: slot, key: key}, fileSlotName(name, slot)), g, true
	}
	o, ok := g.ifaceParamDefault(m.decl.Params[slot].Default)
	if !ok || len(o.im.TypeParams) > 0 {
		return nil, nil, false
	}
	owner = g.unitGen(o.unit)
	if owner == nil {
		return nil, nil, false
	}
	if key < slot {
		key = -1
	}
	theirs := make([]kind, max(slot, key)+1)
	for i, k := range it.params[:len(theirs)] {
		ik, ok := owner.importKind(k)
		if !ok {
			return nil, nil, false
		}
		theirs[i] = ik
	}
	var frame map[string]kind
	if len(o.iface.TypeParams) > 0 {
		frame = make(map[string]kind, len(o.iface.TypeParams))
		for _, tp := range o.iface.TypeParams {
			k, bound := d.ifaceSubst[tp.Name]
			if !bound {
				return nil, nil, false
			}
			ik, ok := owner.importKind(k)
			if !ok {
				return nil, nil, false
			}
			frame[tp.Name] = ik
		}
	}
	return owner.irIfaceSlotSym(o, theirs, frame, d.ifaceSubstTypes, key), owner, false
}

// irImplOwnSlotRetain builds sym, impl function it's accessor for slot
// from its own default (implSlotSym): over the parameters before the
// slot, and the receiver too when it comes after the slot (keyedSlot).
func (g *gen) irImplOwnSlotRetain(d *implDef, it *implItem, slot int, sym *ir.Symbol) {
	fd := it.decl
	name := fileSlotName(d.recv.nomi()+"."+it.name, slot)
	if key := d.iface.methods[it.name].shape.recvAt(); key >= slot {
		kfd, kparams := keyedSlot(fd, it.params, slot, key)
		g.irSlotAccessorRetain(kfd, &fnSig{params: kparams, decl: kfd, lowerable: true}, sym, name, slot+1)
		return
	}
	g.irSlotAccessorRetain(fd, &fnSig{params: it.params, result: it.result, decl: fd, lowerable: true}, sym, name, slot)
}

// implSlotHome is the defaultHome of a call that reaches impl function it
// of d, which g's file declares, on a receiver whose type is known where
// the call is lowered: the slot a call leaves out is filled by a direct
// call of the accessor a call on an interface value dispatches to for it
// (implSlotSym), passed the receiver when the slot comes before it. Its
// accessor answers nil for a slot the interface function does not
// default, for which g builds none.
func (g *gen) implSlotHome(d *implDef, it *implItem) defaultHome {
	m := g.slotDispatchMethod(d, it)
	if m == nil {
		return defaultHome{owner: g}
	}
	recvAt := m.shape.recvAt()
	return defaultHome{
		owner: g,
		accessor: func(slot int) *ir.Symbol {
			if slot >= len(m.decl.Params) || m.decl.Params[slot].Default == nil {
				return nil
			}
			sym, _, _ := g.implSlotSym(d, it, slot)
			return sym
		},
		keyed: recvAt >= 0,
		keyAt: recvAt,
	}
}

// irKeyedSlotDefault identifies an impl function's accessor for a slot
// before the receiver, at key, which it also takes (keyedSlot).
type irKeyedSlotDefault struct {
	callee    *ir.Symbol
	slot, key int
}

// keyedSlot is the declaration and parameter kinds of the accessor for
// slot of fd that a call dispatches to when the receiver, the parameter at
// key, comes after the slot: the parameters before the slot, then the
// receiver, discarded, so the call has its operand to dispatch on, then
// the slot. The accessor is built for slot+1 of the answer.
func keyedSlot(fd *ast.FuncDef, params []kind, slot, key int) (*ast.FuncDef, []kind) {
	recv := fd.Params[key]
	recv.Name, recv.Default, recv.Destructure = "_", nil, nil
	ps := append(append(slices.Clone(fd.Params[:slot]), recv), fd.Params[slot])
	ks := append(append(slices.Clone(params[:slot]), params[key]), params[slot])
	short := *fd
	short.Params = ps
	return &short, ks
}

// irRecordSlotDispatch records, for the values of type typ, the entry of
// each defaulted parameter of interface function m of d that impl function
// it, lowered by owner, has an accessor for. A slot whose accessor owner
// did not retain, or a nil typ or it, is recorded unretained, as the
// function's own entry is.
func (g *gen) irRecordSlotDispatch(d *ifaceDef, m *ifaceMethod, it *implItem, owner *gen, typ *ir.Symbol) {
	if m == nil || m.decl == nil || d.stdOrigin != "" {
		return
	}
	var have []irSlotDispatch
	if it != nil && owner != nil {
		have = owner.slotDispatches[it]
	}
	for slot, p := range m.decl.Params {
		if p.Default == nil {
			continue
		}
		method := g.irIfaceSlotDispatchSym(d, m, slot)
		if method == nil {
			continue
		}
		var fn *ir.Symbol
		for _, e := range have {
			if e.method == method && e.owner.retains(e.fn) {
				fn = e.fn
			}
		}
		if typ == nil || fn == nil {
			g.irMod.ImplementUnretained(method)
			continue
		}
		g.irMod.Implement(method, typ, fn)
	}
}

// dispatchedCallHome is the defaultHome of a call of interface function im
// whose receiver, the operand at im's self slot, is an interface value
// (slotDispatchHome): a named-argument call that leaves a slot out before
// one it names. ok is false for any other receiver.
func (bl *irScalarBuilder) dispatchedCallHome(im *ast.InterfaceMethod, kinds []kind, temps []ir.Temp) (defaultHome, bool) {
	recvAt := selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt()
	if recvAt < 0 || recvAt >= len(temps) || temps[recvAt] == ir.NoTemp {
		return defaultHome{}, false
	}
	recv := kinds[recvAt]
	if recv.tag != tagIface || recv.iface == nil {
		return defaultHome{}, false
	}
	m := recv.iface.methods[im.Name]
	if m == nil {
		return defaultHome{}, false
	}
	return bl.g.slotDispatchHome(recv.iface, m, recvAt), true
}

// reachedImplHome is the defaultHome of a call of interface function im
// whose receiver, the operand at im's self slot, has a type known where
// the call is lowered: that of the impl function it reaches, this file's
// (reachedImplItem) or another file's (siblingImplMemberBy), whose
// accessors it calls (implSlotHome), with its parameters
// (defaultHome.params) and their kinds in this file. ok is false when the
// builder finds none.
func (bl *irScalarBuilder) reachedImplHome(im *ast.InterfaceMethod, kinds []kind, temps []ir.Temp) (defaultHome, []kind, bool) {
	if it := bl.reachedImplItem(im, kinds, temps); it != nil && it.impl != nil {
		home := bl.g.implSlotHome(it.impl, it)
		home.params = irImplSource(it).Params
		return home, it.params, true
	}
	recvAt := selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt()
	if recvAt < 0 || recvAt >= len(kinds) {
		return defaultHome{}, nil, false
	}
	recv := kinds[recvAt]
	if recv.tag != tagNamed || recv.def == nil || recv.def.decl == nil {
		return defaultHome{}, nil, false
	}
	site, params, _, owning := bl.g.siblingImplMemberBy(recv.def.decl, im.Name, "", nil)
	if site == nil || site.iface == "" || site.item.impl == nil || len(params) != len(kinds) {
		return defaultHome{}, nil, false
	}
	home := site.defaultHome(owning)
	home.params = irImplSource(site.item).Params
	return home, params, true
}

// slotDispatchHome is the defaultHome of a call of interface function m
// of d on an interface value, whose receiver is operand recvAt: each slot
// it leaves out is filled by a call dispatched on the receiver.
func (g *gen) slotDispatchHome(d *ifaceDef, m *ifaceMethod, recvAt int) defaultHome {
	return defaultHome{
		accessor: func(slot int) *ir.Symbol {
			return g.irIfaceSlotDispatchSym(d, m, slot)
		},
		keyed:      true,
		dispatched: true,
		keyAt:      recvAt,
	}
}
