package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// FILLING THE PARAMETERS A CALL LEAVES OUT.
//
// The checker records, for each call that leaves a defaulted parameter
// out, the declaration whose defaults fill it and which slots
// (analysis.FileAnalysis.CallDefaults). fillCallDefaults is the one place
// a call's omitted operands are filled, from that record: every call
// route places the written operands at their slots and hands it the rest.
//
// A default is Nomi source of the file that declared it, so where it is
// lowered depends on that file:
//
//   - This file's (a std module's own call included): lowered at the call,
//     in the callee's scope, after the written operands (callDefaults).
//   - An interface function's: lowered at the call under the declaring
//     file's names, or by the declaring file's accessor
//     (ifaceslotdefault.go), whichever file the call is in.
//   - Another file's: a call of the declaring file's accessor for each
//     omitted slot, over the slots before it (siblingdefault.go), the
//     written operands held first so they run before any default. Defaults
//     that are closed literals, which read the same in any file, and those
//     of a callee whose file builds no accessors, are lowered at the call
//     when every name in them means here what it means there
//     (defaultNamesAgree), reading the checker's answers about their nodes
//     from the declaring file's analysis (lowerNamesFrom).
//
// A call into a std module from outside it is not filled here: the module
// builds its own arity wrappers and slot accessors (stdslotfill.go).

// defaultHome is where a call's callee is declared, for fillCallDefaults:
// the declaring file's gen, nil for this one; the accessor that gen builds
// for a slot, nil when it builds none; the instance of a generic `fn`
// whose defaults are lowered here, nil for any other callee; and the
// function an interface call reached.
type defaultHome struct {
	owner    *gen
	accessor func(slot int) *ir.Symbol
	inst     *monoInst
	// params is the parameter list of the function the builder reached,
	// when the checker could not name it: a call of an interface function
	// on a type parameter (DefaultedCall.Decl an *ast.InterfaceMethod)
	// takes, at each instance, the defaults of the impl function it
	// reaches, and the interface's where that function writes none.
	params []ast.Param
}

// callDefaultsOf is the checker's record of what fills the parameters t
// leaves out, nil when it leaves none out. A call the builder makes for a
// function value (callFuncValueOf) carries the record of the value it
// stands for. The builder lowers copies of a
// written call (a pipe stage with its subject spliced in, a call
// requalified through its module's alias), which keep the written call's
// position, so a copy finds the record by it.
func (g *gen) callDefaultsOf(t *ast.Call) *analysis.DefaultedCall {
	fa := g.srcFA()
	if fa == nil || t == nil {
		return nil
	}
	if rec := g.synthDefaults[t]; rec != nil {
		return rec
	}
	if rec := fa.CallDefaults[t]; rec != nil {
		return rec
	}
	if len(fa.CallDefaults) == 0 {
		return nil
	}
	if g.callDefaultsAt == nil || g.callDefaultsFA != fa {
		g.callDefaultsFA = fa
		g.callDefaultsAt = make(map[analysis.Pos]*analysis.DefaultedCall, len(fa.CallDefaults))
		for c, rec := range fa.CallDefaults {
			g.callDefaultsAt[analysis.Pos{Line: c.Line, Col: c.Col}] = rec
		}
	}
	return g.callDefaultsAt[analysis.Pos{Line: t.Line, Col: t.Col}]
}

// fillCallDefaults fills each slot of a call t's operand vector that the
// call leaves out, as the checker recorded it. kinds holds every
// parameter's kind and temps every written operand at its slot, ir.NoTemp
// at each omitted one; mobile marks a written operand that may stay
// unforced. home is where the callee is declared. ok is false when a slot
// is omitted that the record does not name, or a default does not lower.
func (bl *irScalarBuilder) fillCallDefaults(t *ast.Call, kinds []kind, temps []ir.Temp, mobile []bool, home defaultHome) bool {
	var omitted []int
	for i, tmp := range temps {
		if tmp == ir.NoTemp {
			omitted = append(omitted, i)
		}
	}
	if len(omitted) == 0 {
		return true
	}
	g := bl.g
	rec := g.callDefaultsOf(t)
	if rec == nil || len(rec.Params) != len(temps) || len(kinds) != len(temps) {
		g.dc.note("a call leaving out a parameter the checker recorded no default for")
		return false
	}
	for _, s := range omitted {
		if !slices.Contains(rec.Slots, s) {
			g.dc.note("a call leaving out a parameter the checker recorded no default for")
			return false
		}
	}
	params := defaultParams(rec, home, omitted)
	sig := &fnSig{decl: &ast.FuncDef{Params: params}, params: kinds, instance: home.inst}
	owner := home.owner
	if owner == nil || owner == g {
		return bl.callDefaults(sig, temps)
	}
	if _, iface := g.ifaceParamDefault(params[omitted[0]].Default); iface {
		// An interface's defaults, which an impl inherits: each is filled
		// under its declaring file's names (ifaceslotdefault.go).
		return bl.callDefaults(sig, temps)
	}
	if byAccessor(params, home, omitted) {
		return bl.fillFromAccessors(t, kinds, temps, mobile, home.accessor)
	}
	if !g.defaultsLowerHere(rec, home) {
		g.dc.note("a default naming what only its declaring file can, with no accessor for it")
		return false
	}
	// The defaults are the declaring file's nodes, so what the checker
	// recorded of them (a bare `None`'s type) is in its analysis: their
	// names mean the same here, but their types are read there.
	if unit := slices.Index(g.reg.gens, owner); unit >= 0 {
		defer g.lowerNamesFrom(unit)()
	}
	return bl.callDefaults(sig, temps)
}

// byAccessor reports whether the omitted slots of a call of another file's
// function are filled by the declaring file's accessors: when it builds
// them, unless every default is a closed literal (irClosedLiteral).
func byAccessor(params []ast.Param, home defaultHome, omitted []int) bool {
	if home.accessor == nil {
		return false
	}
	for _, s := range omitted {
		if !irClosedLiteral(params[s].Default) {
			return true
		}
	}
	return false
}

// defaultParams is the parameter list whose defaults fill omitted: the
// record's, or, for an interface function the checker left to dispatch,
// the reached function's when it writes a default at each omitted slot.
func defaultParams(rec *analysis.DefaultedCall, home defaultHome, omitted []int) []ast.Param {
	if _, iface := rec.Decl.(*ast.InterfaceMethod); !iface || len(home.params) != len(rec.Params) {
		return rec.Params
	}
	for _, s := range omitted {
		if home.params[s].Default == nil {
			return rec.Params
		}
	}
	return home.params
}

// defaultsLowerHere reports whether every default the record names means
// in this file what it means in the declaring one (defaultNamesAgree), so
// it may be lowered at the call.
func (g *gen) defaultsLowerHere(rec *analysis.DefaultedCall, home defaultHome) bool {
	owner := home.owner
	if owner == nil || owner == g {
		return true
	}
	if owner.fa == nil || g.fa == nil {
		return false
	}
	for _, s := range rec.Slots {
		if !defaultNamesAgree(rec.Params[s].Default, owner, g) {
			return false
		}
	}
	return true
}

// fillFromAccessors fills each omitted slot of temps with a call to the
// declaring file's accessor for it (irFileSlotRetain), in ascending order,
// after every written operand is held, so the written arguments run first
// and each default runs once.
func (bl *irScalarBuilder) fillFromAccessors(at ast.Node, params []kind, temps []ir.Temp, mobile []bool, accessor func(int) *ir.Symbol) bool {
	pos := bl.g.irNodePos(at)
	for i, t := range temps {
		if t == ir.NoTemp || (i < len(mobile) && mobile[i]) || irHeldValue(bl.f, t, bl.sides) {
			continue
		}
		cp := ir.NewCopy(pos, bl.f.NewTemp(), t)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[i], copy: irCopyForce})
		temps[i] = cp.Dst()
	}
	for slot := range temps {
		if temps[slot] != ir.NoTemp {
			continue
		}
		sym := accessor(slot)
		if sym == nil || !irCallOperandKind(params[slot]) {
			return false
		}
		c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, temps[:slot]...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: params[slot]})
		cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[slot], copy: irCopyForce})
		temps[slot] = cp.Dst()
	}
	return true
}

// filledOperands is a short call's operands against params, its callee's
// parameter kinds, and decl, its parameter list: each written operand at
// the slot a direct call gives it (slottedOperands), every omitted one
// filled (fillCallDefaults). A call that omits nothing answers its own
// operands.
func (bl *irScalarBuilder) filledOperands(t *ast.Call, args irQualArgs, params []kind, decl []ast.Param, home defaultHome) (irQualArgs, bool) {
	temps, _, mobile, ok := slottedOperands(t, args, params, decl)
	home.params = decl
	if !ok || !bl.fillCallDefaults(t, params, temps, mobile, home) {
		return args, false
	}
	return irQualArgs{temps: temps, kinds: slices.Clone(params), mobile: make([]bool, len(temps)), ok: true}, true
}

// fileGen is the gen of the program file at path, nil for this gen's own
// file, a path no file of the program has, and "" (the stdlib).
func (g *gen) fileGen(path string) *gen {
	if path == "" || g.reg == nil || (g.fa != nil && g.fa.FilePath == path) {
		return nil
	}
	for _, o := range g.reg.gens {
		if o != nil && o.fa != nil && o.stdModule == "" && !o.programStd && o.fa.FilePath == path {
			return o
		}
	}
	return nil
}
