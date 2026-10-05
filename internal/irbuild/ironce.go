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
	return irRetainedValueKind(k) || k.tag == tagFunc && irCallableValueKind(k)
}

func (bl *irScalarBuilder) onceValue(t *ast.Ident) (ir.Temp, kind, bool, bool) {
	d := bl.g.onces[t.Name]
	if _, bound := bl.g.lookup(t.Name); bound {
		return ir.NoTemp, kindInvalid, false, false
	}
	for parent := bl.parent; parent != nil; parent = parent.parent {
		if _, bound := parent.boundK[t.Name]; bound {
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	if d == nil {
		g := bl.g
		if g.files != nil && g.reg != nil {
			if site, ok := g.files.lookupBare(g.fa, t); ok && site.once != nil && site.unit < len(g.reg.gens) && g.reg.gens[site.unit] != nil {
				return bl.siblingOnceValue(t, site.unit, g.reg.gens[site.unit].oncesByDecl[site.once])
			}
		}
		return ir.NoTemp, kindInvalid, false, false
	}
	if !d.lowerable() || !irOnceKind(d.k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewRefOnce(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irTypes().Symbol(d, d.nomi))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: d.k})
	return n.Dst(), d.k, false, true
}

func (bl *irScalarBuilder) qualifiedOnceValue(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	g := bl.g
	owner, ok := t.Object.(*ast.Ident)
	if !ok || t.Field == nil || g.files == nil || g.reg == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	if _, bound := g.lookup(owner.Name); bound {
		return ir.NoTemp, kindInvalid, false, false
	}
	for scope := bl; scope != nil; scope = scope.parent {
		if _, bound := scope.boundK[owner.Name]; bound {
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	to, ok := g.files.lookupQualifier(g.fa, owner)
	if !ok || to >= len(g.reg.gens) || g.reg.gens[to] == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.siblingOnceValue(t, to, g.reg.gens[to].onces[t.Field.Name])
}

func (bl *irScalarBuilder) siblingOnceValue(at ast.Node, to int, d *onceDef) (ir.Temp, kind, bool, bool) {
	if d == nil || !d.lowerable() || !irOnceKind(d.k) {
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

func (g *gen) irOnceLower(d *onceDef) (kind, bool) {
	return g.irOnceCellLower(d.decl, d.k, g.irTypes().Symbol(d, d.nomi), d.nomi)
}

// irOnceCellLower builds a `once` initializer as the IR lazy cell sym and
// emits its Go body, read from that graph, into dst. User and stdlib bindings
// share it; name is the binding as Nomi spells it.
func (g *gen) irOnceCellLower(decl *ast.OnceBinding, k kind, sym *ir.Symbol, name string) (got kind, ok bool) {
	g.irDeclineOpen("once " + name)
	defer func() {
		if !ok {
			irDeclineClose()
		}
	}()
	if !irOnceKind(k) {
		irDeclineNote("a once of a kind outside the domain: " + k.nomi())
		return kindInvalid, false
	}
	body := decl.Value
	var lead []ast.Node
	if block, ok := body.(*ast.Block); ok {
		lead, body = g.irScalarBlock(block, "")
	}
	if body == nil {
		irDeclineNote(irDeclineBodyWhy)
		return kindInvalid, false
	}
	at := g.irNodePos(decl)
	f := ir.NewFunc(at, "once "+name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	sh.result = f.NewTemp()
	sh.slot = ir.NewSlot(at, sh.result, g.irTypeOf(k))
	sh.entry.Append(sh.slot)
	sh.prologue = 1
	bl := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, returnKind: k, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	bl.openWithScope(true)
	if !bl.leading(lead) {
		return kindInvalid, false
	}
	// The tail faces the binding's kind, published in g.wantOf; a
	// constructor such as `Channel.buffered(2)` reads its instantiation from
	// there.
	if g.wantOf == nil {
		g.wantOf = map[ast.Node]kind{}
	}
	prevWant, held := g.wantOf[body]
	g.wantOf[body] = k
	val, got, ok := bl.tail(body, irFuncSig{result: k})
	if held {
		g.wantOf[body] = prevWant
	} else {
		delete(g.wantOf, body)
	}
	if !ok {
		return kindInvalid, false
	}
	if val != ir.NoTemp {
		bl.resultCopy(body, val)
		bl.side(sh.result, irScalarSide{k: got})
		bl.b.SetTerm(ir.NewReturn(g.irNodePos(body), sh.result))
	}
	p := &irScalarPlan{fn: f, result: got}
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a `once` initializer graph that does not lint: " + err.Error())
		return kindInvalid, false
	}
	g.irModule().DeclareLazyCell(sym, g.irTypeOf(k), f)
	return p.result, true
}

// irWithdrawUnforceableReads takes back every retained body that reads a
// `once` whose initializer no module retained, so the VM never meets a RefOnce
// it cannot force. A read is admitted when the builder reaches it, and in user
// code that may be before the declaration's own cell is attempted: a function
// declared above its once, or a sibling unit emitted later. Only the finished
// program knows which cells exist, so the answer is taken here. Withdrawal
// removes a body from the VM's modules only; its Go was already emitted. A
// withdrawn cell's readers are withdrawn in turn. The stdlib
// modules are only consulted: their reads already require a retained cell
// (stdOnceValue's irCell).
func irWithdrawUnforceableReads(user, libs []*ir.Module) {
	mods := append(append([]*ir.Module(nil), user...), libs...)
	for changed := true; changed; {
		changed = false
		for _, mod := range user {
			for _, c := range append([]*ir.Cell(nil), mod.Cells()...) {
				if body := c.Initializer(); body != nil && !irForcesOnly(body, mods) {
					mod.RemoveCell(c)
					changed = true
				}
			}
		}
	}
	for _, mod := range user {
		for _, f := range append([]*ir.Func(nil), mod.Funcs()...) {
			if !irForcesOnly(f, mods) {
				mod.RemoveFunc(f)
			}
		}
	}
}

// irForcesOnly reports whether every once f reads, in its own blocks and in
// the bodies of the function values it builds, has a cell with a retained
// initializer in one of mods. The modules are asked directly rather than
// through a map keyed on the symbol, so a withdrawn cell stops answering the
// moment RemoveCell takes it out.
func irForcesOnly(f *ir.Func, mods []*ir.Module) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch in := in.(type) {
			case *ir.Ref:
				if in.Forces() && !irCellForceable(mods, in.Sym()) {
					return false
				}
			case *ir.FuncValue:
				if !irForcesOnly(in.Body(), mods) {
					return false
				}
			}
		}
	}
	return true
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

// ownerOnceValue reads an owner-level `once` (`Policy.default`), declared in
// an inherent impl block of this file or of another. The checker records the
// binding's declaration at the member position, whatever spelling named the
// owner (`Policy`, an imported `Policy`, `other.Policy`), so the reference is
// resolved from that record rather than from the owner's text. A stdlib
// binding (`Int.max_value`) is in no user file and is left to stdOnceValue.
func (bl *irScalarBuilder) ownerOnceValue(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	if t.Field == nil || g.fa == nil {
		return no()
	}
	sym := g.fa.References[analysis.Pos{Line: t.Field.Line, Col: t.Field.Col}]
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != analysis.SymbolOnce || sym.OwningType == "" {
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
		return bl.siblingOnceValue(t, site.unit, g.reg.gens[site.unit].oncesByDecl[ob])
	}
	if !d.lowerable() || !irOnceKind(d.k) {
		return no()
	}
	n := ir.NewRefOnce(g.irNodePos(t), bl.f.NewTemp(), g.irTypes().Symbol(d, d.nomi))
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: d.k})
	return n.Dst(), d.k, false, true
}
