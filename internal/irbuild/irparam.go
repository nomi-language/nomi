package irbuild

// A RETAINED FUNCTION'S PARAMETER IS THE STRUCTURE, AND A DESTRUCTURING
// PARAMETER'S NAMES ARE `ir.Proj` READS OFF IT.
//
// `fn sum_point(Point{x, y}): Int { x + y }` has ONE parameter, the Point, in
// its `ir.Func` — the arity the Nomi declaration and every call site agree on
// — and `x` and `y` are `ir.Bind`s of projections read off it at the head of
// the entry block. `Params()` keeps one entry per declared parameter; the
// identity of a destructuring one is the synthesized `__destr_<line>_<col>`
// name the parser gave it, which no source can spell. A nested pattern binds a
// number of names that has nothing to do with arity, which is why the names
// are not the parameter list.
//
// A DISCARDED PARAMETER IS ALSO A PARAMETER, with no projections:
// `fn f(_: Int, b: Int)` has two operands at every call site. Its identity is
// MINTED rather than interned — see `irFuncShell.paramSym`.
//
// # THE PROLOGUE IS BUILT BY `g.destructure`
//
// `g.destructure` resolves an irrefutable pattern against the parameter's
// kind, reports `pattern type mismatch` / `unknown struct field` /
// `refutable parameter pattern`, and handles `embeds` coercion, zero-sized
// materialization and boxed slots. `irPrologueOpen` installs the shell for the
// duration of `g.destructure`, and while it is installed:
//
//   - `g.irHold` answers the temporary a value is already held in, or mints
//     one of the FUNCTION's;
//   - `g.irAppend` records each node into the entry block, so the graph holds
//     them in the order the destructure built them.
//
// The parameter's own value enters as `expr{t: <the parameter's temporary>}`,
// so a projection off it names the parameter rather than a temporary nothing
// defines.

import "github.com/nomi-language/nomi/internal/ir"

// irPrologueOpen installs sh for the extent of its destructuring prologue and
// answers the closer, which takes whether every pattern lowered.
func (g *gen) irPrologueOpen(sh *irFuncShell) func(bool) {
	prevPro := g.irPro
	g.irPro = sh
	return func(ok bool) {
		g.irPro = prevPro
		sh.prologue = len(sh.entry.Instrs())
		sh.patternOK = ok
	}
}

// irAppend records one construct-and-consume instruction into the retained
// function's prologue, when one is open.
//
// ONE SITE PER NODE CLASS THE PROLOGUE CAN BUILD, and the classes are
// `ir.Proj` (irProjExpr) and `ir.Bind` / `ir.Copy` (irBindNode, irCopyNode).
// A projection the destructure reaches through some other constructor would be
// ABSENT from the graph rather than wrong, and `ir.Lint` reports it as an
// operand nothing defines.
func (g *gen) irAppend(in ir.Instr) {
	if g.irPro != nil {
		g.irPro.entry.Append(in)
	}
}

// hold answers the temporary e is already held in, or mints one of the
// function's for it.
func (sh *irFuncShell) hold(e expr) ir.Temp {
	if e.t != ir.NoTemp {
		return e.t
	}
	t := sh.fn.NewTemp()
	sh.set(t, e.k)
	return t
}

// set records the kind one of the prologue's temporaries holds.
func (sh *irFuncShell) set(t ir.Temp, k kind) {
	sh.frame.setKind(t, k)
}

// irPrologueBinds is the destructured names one prologue introduced, with the
// temporary each is held in and the kind the builder gave it.
//
// Read off the graph rather than collected while it was built: the `ir.Bind`
// nodes ARE the record, and their Symbols' names are the Nomi names. That is
// what `irScalarBuilder` seeds `bound` from, so a destructured name is
// indistinguishable to the body from a name the body itself bound — which is
// why no `RefLocal` is built for one and `RuleLocalDeclared` has nothing to
// report about it.
func (sh *irFuncShell) irPrologueBinds() []*ir.Bind {
	var out []*ir.Bind
	for _, in := range sh.entry.Instrs()[:sh.prologue] {
		if b, isBind := in.(*ir.Bind); isBind {
			out = append(out, b)
		}
	}
	return out
}
