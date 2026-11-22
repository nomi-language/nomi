package irbuild

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A PARAMETER DEFAULT OF AN INTERFACE FUNCTION, FILLED IN ANOTHER FILE.
//
// `fn describe(s: self, prefix: String = default_prefix()) -> String { ... }`
// in shape.nomi: a call in main.nomi that leaves `prefix` out and reaches
// an implementing block's function that declares no default of its own
// (`Square.describe(sq)`, or a named-argument call) fills the slot from the
// interface's declaration, which is Nomi written in shape.nomi.
// `default_prefix` may be that file's private function, and main.nomi may
// bind the name to something else. A call on an interface value dispatches
// to the impl's accessor (ifaceslotdispatch.go), which for such an impl is
// the accessor built here at its kinds.
//
// So the slot is filled as a sibling file's `fn` default is
// (siblingdefault.go): when every name the default mentions resolves to the
// same declaration from both files, it is lowered at the call, reading the
// checker's answers about its nodes from the declaring file's analysis
// (lowerNamesFrom). Otherwise the declaring file's gen builds an accessor
// for the slot, taking the parameters before it at the kinds the call
// passes, and the call calls it. An accessor is keyed by the interface
// function's declaration, the slot, the kinds up to it and, for a generic
// interface or function, the type arguments the call binds, built on
// request (flushIfaceSlotDefaults) and linked by the declaration's linkage
// name with those kinds, type arguments and the slot.
//
// A generic interface's default may name its type parameters
// (`x: List<T> = pick()`, with `pick<T>` solved from the slot's type), so
// whichever file lowers it does so in a frame binding them
// (ifaceDefaultFrameAt), at the type arguments the checker instantiated
// the call at.

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
// slot, and the kinds of the parameters before it and of the slot, then the
// type arguments of the frame it is built in by name, spelled portably.
// recv is -1, or the receiver after the slot that an accessor a call
// dispatches to also takes (keyedSlot), whose kinds then run through it.
type irIfaceSlotDefault struct {
	im    *ast.InterfaceMethod
	slot  int
	recv  int
	kinds string
}

// irIfaceSlotReq is one queued accessor, its kinds and frame in the
// declaring gen's.
type irIfaceSlotReq struct {
	key    irIfaceSlotDefault
	sym    *ir.Symbol
	name   string
	params []kind
	frame  map[string]kind
	types  map[string]analysis.Type
	// self is the checker's type of the implementor the accessor fills
	// the slot for (fnSig.defaultSelfType), nil where the asker has none.
	self  analysis.Type
	built bool
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

// ifaceDefaultGeneric reports whether the interface or the function
// declaring parameter default o has type parameters, which its default is
// lowered in a frame binding.
func ifaceDefaultGeneric(o ifaceParamDefault) bool {
	return len(o.iface.TypeParams) > 0 || len(o.im.TypeParams) > 0
}

// ifaceDefaultFrameAt binds the type parameters of the interface and the
// function declaring parameter default o at the type arguments the checker
// instantiated call t at (calleeTypeArgs), in this gen's kinds and as the
// checker's types. ok is false when the record lacks one.
func (g *gen) ifaceDefaultFrameAt(t *ast.Call, o ifaceParamDefault) (map[string]kind, map[string]analysis.Type, bool) {
	var names []string
	for _, tp := range o.iface.TypeParams {
		names = append(names, tp.Name)
	}
	for _, tp := range o.im.TypeParams {
		names = append(names, tp.Name)
	}
	return g.callTypeArgFrame(t, names)
}

// callTypeArgFrame binds names, type parameters of call t's callee, at the
// type arguments the checker instantiated t at (calleeTypeArgs), in this
// gen's kinds and as the checker's types. ok is false when the record lacks
// one.
func (g *gen) callTypeArgFrame(t *ast.Call, names []string) (map[string]kind, map[string]analysis.Type, bool) {
	ks, ts, ok := g.calleeTypeArgs(t.Func, names)
	if !ok {
		return nil, nil, false
	}
	frame := make(map[string]kind, len(names))
	var types map[string]analysis.Type
	for i, n := range names {
		frame[n] = ks[i]
		if ts[i] != nil {
			if types == nil {
				types = make(map[string]analysis.Type, len(names))
			}
			types[n] = ts[i]
		}
	}
	return frame, types, true
}

// defaultInFrame lowers parameter default p of a function at kind want in
// frame, the callee's type parameters at the call's type arguments, under
// this gen's names. types is frame as the checker's types, or nil.
func (bl *irScalarBuilder) defaultInFrame(p ast.Param, frame map[string]kind, types map[string]analysis.Type, want kind) (ir.Temp, kind, bool) {
	if len(frame) > 0 {
		bl.g.pushIfaceSubst(substParamDefault, p.Default, frame, types)
		defer bl.g.popIfaceSubst()
	}
	src, k, _, ok := bl.lowerWant(p.Default, want)
	return src, k, ok
}

// ifaceSlotDefault fills slot i of a call from the parameter default o, an
// interface function's in another file, with args holding the slots before
// it at the kinds params give and frame binding the type parameters
// (ifaceDefaultFrameAt): a call of the declaring gen's accessor, built for
// the implementor of checker type self (fnSig.defaultSelfType), or nil. The
// declaring file lowers its own default, so it names what that file names,
// a private function of it included.
func (bl *irScalarBuilder) ifaceSlotDefault(o ifaceParamDefault, p ast.Param, params []kind, frame map[string]kind, types map[string]analysis.Type, self analysis.Type, args []ir.Temp, i int) (ir.Temp, kind, bool) {
	g := bl.g
	owner := g.unitGen(o.unit)
	if owner == nil {
		return ir.NoTemp, kindInvalid, false
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
	var theirFrame map[string]kind
	if len(frame) > 0 {
		theirFrame = make(map[string]kind, len(frame))
		for name, k := range frame {
			ik, ok := owner.importKind(k)
			if !ok {
				return ir.NoTemp, kindInvalid, false
			}
			theirFrame[name] = ik
		}
	}
	sym := owner.irIfaceSlotSym(o, theirs, theirFrame, types, self, -1)
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

// irIfaceSlotSym is the accessor symbol for slot o of an interface function
// this gen's file declares, over params, the kinds of the parameters up to
// and including the slot, in frame, queued for flushIfaceSlotDefaults the
// first time any file asks. g is the DECLARING gen. A recv past the slot
// makes it the accessor that also takes that receiver (keyedSlot), with
// params running through it; -1 for none. types is frame as the checker's
// types (the program's, so one file's serve another's), or nil, and self
// the checker's type of the implementor the slot is filled for, which its
// `self` stands for in the default (fnSig.defaultSelfType), or nil. The
// accessor is keyed by kinds, so the first asker's types build it.
func (g *gen) irIfaceSlotSym(o ifaceParamDefault, params []kind, frame map[string]kind, types map[string]analysis.Type, self analysis.Type, recv int) *ir.Symbol {
	key := irIfaceSlotDefault{im: o.im, slot: o.slot, recv: recv, kinds: portableArgsKey(params) + portableFrameKey(frame)}
	if r := g.ifaceSlotDefaults[key]; r != nil {
		return r.sym
	}
	name := o.im.Name + " default " + strconv.Itoa(o.slot+1)
	r := &irIfaceSlotReq{key: key, sym: g.irCalleeSym(key, name), name: name, params: params, frame: frame, types: types, self: self}
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
			params, slot := r.params, r.key.slot
			if r.key.recv > slot {
				fd, params = keyedSlot(fd, params, slot, r.key.recv)
				slot++
			}
			sig := &fnSig{params: params, decl: fd, lowerable: true, defaultFrame: r.frame, defaultTypes: r.types, defaultSelfType: r.self}
			if len(r.frame) > 0 {
				g.pushIfaceSubst(substIfaceDefault, r.key.im, r.frame, r.types)
			}
			g.irSlotAccessorRetain(fd, sig, r.sym, r.name, slot)
			if len(r.frame) > 0 {
				g.popIfaceSubst()
			}
		}
	}
}

// portableFrameKey spells a type-parameter frame portably, its names
// sorted, or "" for none.
func portableFrameKey(frame map[string]kind) string {
	if len(frame) == 0 {
		return ""
	}
	names := make([]string, 0, len(frame))
	for n := range frame {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(";" + n + "=")
		writePortableKind(&b, frame[n])
	}
	return b.String()
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
