package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irOnceKind is the kinds a `once` cell holds: every retained value, and a
// function value over the callable domain (`once add_op: (Int) -> Int =
// add`). A function value lives in a cell's ref register like any other
// boxed value, so reading it is a RefOnce and calling it is a closure call on
// that temporary.
func irOnceKind(k kind) bool {
	return irRetainedValueKind(k) || k.tag == tagFunc && irCallableValueKind(k) || irHeldKind(k) || k == kindUnit
}

// onceValue reads the `once` the bare name t names, by the binding the
// checker resolved it to (declaredOnceValue): this file's, or another
// file's a selective import or a parameter default of that file's filled
// here (lowerNamesFrom) names. A block `import` shadows the file's `once` of
// the name.
func (bl *irScalarBuilder) onceValue(t *ast.Ident) (ir.Temp, kind, bool, bool) {
	if bl.g.blockImported(t) {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.declaredOnceValue(t, bl.g.identRef(t))
}

// qualifiedOnceValue reads the `once` a qualified name names: a file-level
// binding of another file (`config.port`) or an owner-level one
// (`Policy.default`) declared in an inherent impl block of this file or of
// another. The checker records the binding's declaration at the member
// position, whatever spelling named the file or the owner (`Policy`, an
// imported `Policy`, `other.Policy`), so the reference is resolved from that
// record rather than from the qualifier's text. A stdlib binding
// (`io.stdout`) is in no user file and is left to stdFileOnceValue and
// stdOnceValue.
func (bl *irScalarBuilder) qualifiedOnceValue(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	if t.Field == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.declaredOnceValue(t, bl.g.identRef(t.Field))
}

// declaredOnceValue reads the `once` of a user file that ref, the checker's
// reference at a read, resolves to: this file's cell (oncesByDecl), or the
// declaring file's (fileIndex.byDecl). ok is false when ref names no such
// binding.
func (bl *irScalarBuilder) declaredOnceValue(at ast.Node, ref *analysis.Symbol) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	sym := resolveSymbol(ref)
	if sym == nil || sym.Kind != analysis.SymbolOnce {
		return no()
	}
	ob, ok := sym.Node.(*ast.OnceBinding)
	if !ok {
		return no()
	}
	d := g.oncesByDecl[ob]
	if d == nil {
		if g.files == nil || g.reg == nil {
			return no()
		}
		site, ok := g.files.byDecl[ob]
		if !ok || site.once == nil || site.unit >= len(g.reg.gens) || g.reg.gens[site.unit] == nil {
			return no()
		}
		return bl.siblingOnceValue(at, site.unit, g.reg.gens[site.unit].oncesByDecl[ob])
	}
	if !d.lowerable() {
		g.dc.declineOnceRead(d)
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(at), bl.f.NewTemp(), g.irTypes().Symbol(d, d.nomi))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: d.k})
	return n.Dst(), d.k, false, true
}

// lowerNamesFrom lowers what follows under the names of the file at unit,
// where an inherited default was written, until the answer is called.
func (g *gen) lowerNamesFrom(unit int) func() {
	prev := g.namesFrom
	g.namesFrom = unit + 1
	return func() { g.namesFrom = prev }
}

// namesFromGen is the gen of the file whose names are being lowered under
// lowerNamesFrom, or nil.
func (g *gen) namesFromGen() *gen {
	if g.namesFrom == 0 || g.reg == nil || g.namesFrom-1 >= len(g.reg.gens) {
		return nil
	}
	return g.reg.gens[g.namesFrom-1]
}

// srcFA is the analysis of the file whose source is being lowered: the
// declaring std file's while a std interface's signatures are resolved here
// (withStdSrc), the declaring file's while a sibling's interface default or
// parameter default is lowered here (lowerNamesFrom), else this file's. Every lookup keyed by a
// node of that source or by its position (References, Definitions,
// ExprTypes, ExpectedTypes) reads it: this file's maps hold nothing for the
// sibling's nodes, and at a position they hold whatever this file wrote
// there.
func (g *gen) srcFA() *analysis.FileAnalysis {
	if g.stdSrc != nil {
		return g.stdSrc
	}
	if from := g.namesFromGen(); from != nil && from.fa != nil {
		return from.fa
	}
	return g.fa
}

func (bl *irScalarBuilder) siblingOnceValue(at ast.Node, to int, d *onceDef) (ir.Temp, kind, bool, bool) {
	if d == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	if !d.lowerable() {
		bl.g.dc.declineOnceRead(d)
		return ir.NoTemp, kindInvalid, false, false
	}
	k, ok := bl.g.refSiblingOnce(at, to, d)
	if !ok || !irOnceKind(k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	owner := bl.g.reg.gens[to]
	n := ir.NewRefOnce(bl.g.irNodePos(at), bl.f.NewTemp(), owner.irTypes().Symbol(d, d.nomi))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

// declineOnceRead records the open attempt's decline as a read of d, a `once`
// declared without a type the builder can represent, so a host blames the
// `once` rather than the name that reads it.
func (dc *declineCensus) declineOnceRead(d *onceDef) {
	why := d.whyNot()
	dc.noteOnce("a read of `once "+d.nomi+"`, which cannot lower: "+why, "once "+d.nomi)
}

func (g *gen) irOnceLower(d *onceDef) (kind, bool) {
	return g.irOnceCellLower(d.decl, d.k, g.irTypes().Symbol(d, d.nomi), d.nomi)
}

// irOnceCellLower builds a `once` initializer and declares it as the IR lazy
// cell sym. User and stdlib bindings share it; name is the binding as Nomi
// spells it.
func (g *gen) irOnceCellLower(decl *ast.OnceBinding, k kind, sym *ir.Symbol, name string) (got kind, ok bool) {
	g.irDeclineOpen("once " + name)
	defer func() {
		if !ok {
			g.dc.close()
		}
	}()
	// The binding's declared or checked type, projected: its callers ask
	// the declaration's lowerability first, which holds a user binding's
	// kind to irOnceKind (onceDef.whyNot), and irOnceKind is the call
	// operands.
	irHeldOperand(k, "a `once` binding")
	body := decl.Value
	var lead []ast.Node
	if block, ok := body.(*ast.Block); ok {
		defer g.enterBlockTypes(block)()
		lead, body = g.irScalarBlock(block)
	}
	if body == nil {
		panic("irbuild: a `once` with no value, which the parser does not build: " + decl.Name)
	}
	at := g.irNodePos(decl)
	f := ir.NewFunc(at, "once "+name)
	sh := &irFuncShell{fn: f, params: map[string]ir.Temp{}}
	sh.entry = f.NewBlock(at, "entry")
	sh.result = f.NewTemp()
	sh.slot = ir.NewSlot(at, sh.result, g.irTypeOf(k))
	sh.entry.Append(sh.slot)
	// The value a `noun` declaration expanded into keeps its name's position
	// and has the value it was given in the synthesized band.
	bl := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, returnKind: k,
		synthesized: decl.Expansion != nil}
	bl.openWithScope(true)
	if !bl.leading(lead) {
		return kindInvalid, false
	}
	val, got, ok := bl.tail(body, irFuncSig{result: k})
	if !ok {
		return kindInvalid, false
	}
	if val != ir.NoTemp {
		bl.resultCopy(body, val)
		bl.side(sh.result, irScalarSide{k: got})
		bl.b.SetTerm(ir.NewReturn(g.irNodePos(body), sh.result))
	}
	p := &irScalarPlan{fn: f, result: got}
	g.irRequireLint(f, "a `once` initializer graph")
	g.irModule().DeclareLazyCell(sym, g.irTypeOf(k), f)
	return p.result, true
}

// irWithdrawUnforceableReads takes back every retained body that reads a
// `once` whose initializer no module retained, so the VM never meets a RefOnce
// it cannot force. A read is admitted when the builder reaches it, and in user
// code that may be before the declaration's own cell is attempted: a function
// declared above its once, or a sibling unit lowered later. Only the finished
// program knows which cells exist, so the answer is taken here. Withdrawal
// removes a body from the VM's modules. A withdrawn cell's readers are
// withdrawn in turn. The stdlib
// modules are only consulted: their reads already require a retained cell
// (stdOnceValue's irCell).
//
// Each withdrawal is a decline of the withdrawn body (declineCensus.withdrawn), so
// a host reports it at the `once` whose initializer did not lower rather than
// as a body that declined for no reason.
func irWithdrawUnforceableReads(dc *declineCensus, user, libs []*ir.Module) {
	mods := append(append([]*ir.Module(nil), user...), libs...)
	for changed := true; changed; {
		changed = false
		for _, mod := range user {
			for _, c := range append([]*ir.Cell(nil), mod.Cells()...) {
				body := c.Initializer()
				if body == nil {
					continue
				}
				if read := irUnforceableRead(body, mods); read != nil {
					mod.RemoveCell(c)
					dc.withdrawn("once "+c.Sym().Name(), read)
					changed = true
				}
			}
		}
	}
	for _, mod := range user {
		for _, f := range append([]*ir.Func(nil), mod.Funcs()...) {
			if read := irUnforceableRead(f, mods); read != nil {
				mod.RemoveFunc(f)
				dc.withdrawn(f.Name(), read)
			}
		}
	}
}

// irUnforceableRead is the first read of a once in f, in its own blocks or in
// the bodies of the function values it builds, that has no cell with a
// retained initializer in one of mods, or nil when every read has one. The
// modules are asked directly rather than through a map keyed on the symbol, so
// a withdrawn cell stops answering the moment RemoveCell takes it out.
func irUnforceableRead(f *ir.Func, mods []*ir.Module) *ir.Ref {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch in := in.(type) {
			case *ir.Ref:
				if in.Forces() && !irCellForceable(mods, in.Sym()) {
					return in
				}
			case *ir.FuncValue:
				if read := irUnforceableRead(in.Body(), mods); read != nil {
					return read
				}
			}
		}
	}
	return nil
}

// irCellForceable reports whether some module holds sym's cell with a
// retained initializer.
func irCellForceable(mods []*ir.Module, sym *ir.Symbol) bool {
	for _, mod := range mods {
		if c := mod.Cell(sym); c != nil && c.Initializer() != nil {
			return true
		}
	}
	return false
}
