package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A PARAMETER DEFAULT OF AN INTERFACE FUNCTION, FILLED IN ANOTHER FILE.
//
// `fn describe(s: self, prefix: String = default_prefix()) -> String { ... }`
// in shape.nomi: a call in main.nomi that leaves `prefix` out, whether it
// reaches an implementing block's function (`Square.describe(sq)`), an
// erased receiver's dispatch (`Shape.describe(s)`) or a named-argument
// call, fills the slot from the interface's declaration, which is Nomi
// written in shape.nomi. `default_prefix` may be that file's private
// function, and main.nomi may bind the name to something else.
//
// So the slot is filled as a sibling file's `fn` default is
// (siblingdefault.go): when every name the default mentions resolves to the
// same declaration from both files, it is lowered at the call, reading the
// checker's answers about its nodes from the declaring file's analysis
// (lowerNamesFrom). Otherwise the declaring file's gen builds an accessor
// for the slot, taking the parameters before it at the kinds the call
// passes, and the call calls it. An accessor is keyed by the interface
// function's declaration, the slot and the kinds up to it, built on request
// (flushIfaceSlotDefaults) and linked by the declaration's linkage name
// with those kinds and the slot.

// ifaceParamDefault is where one parameter default of an interface
// function is declared: the declaring file's unit, the function and the
// parameter's slot.
type ifaceParamDefault struct {
	unit  int
	iface *ast.InterfaceDef
	im    *ast.InterfaceMethod
	slot  int
}

// irIfaceSlotDefault identifies one accessor: the interface function, the
// slot, and the kinds of the parameters before it and of the slot, spelled
// portably.
type irIfaceSlotDefault struct {
	im    *ast.InterfaceMethod
	slot  int
	kinds string
}

// irIfaceSlotReq is one queued accessor, its kinds in the declaring gen's.
type irIfaceSlotReq struct {
	key    irIfaceSlotDefault
	sym    *ir.Symbol
	name   string
	params []kind
	built  bool
}

// ifaceParamDefaultOf reports where default node d is declared when it is a
// parameter default of an interface function another file of the program
// declares.
func (g *gen) ifaceParamDefaultOf(d ast.Node) (ifaceParamDefault, bool) {
	o, ok := g.ifaceParamDefault(d)
	return o, ok && o.unit != g.fileUnit
}

// ifaceParamDefault reports where default node d is declared when it is a
// parameter default of an interface function of the program's files.
func (g *gen) ifaceParamDefault(d ast.Node) (ifaceParamDefault, bool) {
	r := g.reg
	if r == nil || d == nil {
		return ifaceParamDefault{}, false
	}
	if r.ifaceParamDefaults == nil {
		r.ifaceParamDefaults = map[ast.Node]ifaceParamDefault{}
		for unit, o := range r.gens {
			if o == nil || o.stdModule != "" || o.programStd {
				continue
			}
			for _, n := range o.nodes {
				id, ok := n.(*ast.InterfaceDef)
				if !ok {
					continue
				}
				for i := range id.Methods {
					im := &id.Methods[i]
					for slot, p := range im.Params {
						if p.Default != nil {
							r.ifaceParamDefaults[p.Default] = ifaceParamDefault{unit: unit, iface: id, im: im, slot: slot}
						}
					}
				}
			}
		}
	}
	o, ok := r.ifaceParamDefaults[d]
	return o, ok
}

// ifaceSlotDefault fills slot i of a call from the parameter default o
// declares, with args holding the slots before it at the kinds params give:
// lowered here when its names mean here what they mean there, else a call
// of the declaring gen's accessor.
func (bl *irScalarBuilder) ifaceSlotDefault(o ifaceParamDefault, p ast.Param, params []kind, args []ir.Temp, i int) (ir.Temp, kind, bool) {
	g := bl.g
	owner := g.unitGen(o.unit)
	if owner == nil {
		return ir.NoTemp, kindInvalid, false
	}
	if defaultNamesAgree(p.Default, owner, g) {
		defer g.lowerNamesFrom(o.unit)()
		src, k, _, ok := bl.lowerWant(p.Default, params[i])
		return src, k, ok
	}
	if !irCallOperandKind(params[i]) {
		return ir.NoTemp, kindInvalid, false
	}
	theirs := make([]kind, i+1)
	for j, k := range params[:i+1] {
		ik, ok := owner.importKind(k)
		if !ok {
			return ir.NoTemp, kindInvalid, false
		}
		theirs[j] = ik
	}
	sym := owner.irIfaceSlotSym(o, theirs)
	pos := g.irNodePos(p.Default)
	operands := make([]ir.Temp, i)
	for j, t := range args[:i] {
		if !irHeldValue(bl.f, t, bl.sides) {
			cp := ir.NewCopy(pos, bl.f.NewTemp(), t)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: params[j], copy: irCopyForce})
			t = cp.Dst()
			args[j] = t
		}
		operands[j] = t
	}
	c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, operands...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: params[i]})
	cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
	bl.b.Append(cp)
	bl.side(cp.Dst(), irScalarSide{k: params[i], copy: irCopyForce})
	return cp.Dst(), params[i], true
}

// defaultNamesAgree reports whether every name default d mentions resolves
// to the same declaration in owning's file and in g's, so lowering it in g
// means what it means where it was written.
func defaultNamesAgree(d ast.Node, owning, g *gen) bool {
	names := map[string]bool{}
	walkNames(d, names, pickAnyName)
	for name := range names {
		there, here := resolvedTypeSymbol(owning.fa, name), resolvedTypeSymbol(g.fa, name)
		if (there == nil) != (here == nil) || (there != nil && there.Node != here.Node) {
			return false
		}
	}
	return true
}

// irIfaceSlotSym is the accessor symbol for slot o of an interface function
// this gen's file declares, over params, the kinds of the parameters up to
// and including the slot, queued for flushIfaceSlotDefaults the first time
// any file asks. g is the DECLARING gen.
func (g *gen) irIfaceSlotSym(o ifaceParamDefault, params []kind) *ir.Symbol {
	key := irIfaceSlotDefault{im: o.im, slot: o.slot, kinds: portableArgsKey(params)}
	if r := g.ifaceSlotDefaults[key]; r != nil {
		return r.sym
	}
	name := o.im.Name + " default " + strconv.Itoa(o.slot+1)
	r := &irIfaceSlotReq{key: key, sym: g.irCalleeSym(key, name), name: name, params: params}
	if g.ifaceSlotDefaults == nil {
		g.ifaceSlotDefaults = map[irIfaceSlotDefault]*irIfaceSlotReq{}
	}
	g.ifaceSlotDefaults[key] = r
	g.ifaceSlotQueue = append(g.ifaceSlotQueue, r)
	return r.sym
}

// flushIfaceSlotDefaults builds every queued accessor not yet built.
// Building one can queue another, which the index loop picks up.
func (g *gen) flushIfaceSlotDefaults() {
	for i := 0; i < len(g.ifaceSlotQueue); i++ {
		if r := g.ifaceSlotQueue[i]; !r.built {
			r.built = true
			im := r.key.im
			fd := &ast.FuncDef{Name: im.Name, Params: im.Params, Line: im.Line, Col: im.Col}
			sig := &fnSig{params: r.params, decl: fd, lowerable: true}
			g.irSlotAccessorRetain(fd, sig, r.sym, r.name, r.key.slot)
		}
	}
}

// ifaceSlotDefaultsPending reports whether an accessor is queued and
// unbuilt.
func (g *gen) ifaceSlotDefaultsPending() bool {
	for _, r := range g.ifaceSlotQueue {
		if !r.built {
			return true
		}
	}
	return false
}
