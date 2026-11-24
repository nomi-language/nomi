package analysis

// A `?` converts the error it propagates through std's `From` (spec §11
// *Propagating with `?`*, rule 3). A `?` on a `Result<T, E>` whose boundary
// declares `Result<_, F>` with an E that does not inhabit F applies F's
// `impl From<E>` on the Err path: the boundary returns `Err(F.from(e))`.
//
// The impl is F's that the call `F.from(e)` takes (selectImpl): the one at
// E, or, when E converts into another impl's argument (a Circle into the
// Shape that embeds it), the most specific that takes E. It is one
// conversion, never a chain through a third type's `From`, and no identity
// impl, since equal types never reach here. Only a DECLARED boundary converts; an inferred one (a lambda with no
// declared result, a `concurrent` block) has no F to look up, and
// checkTryBoundary never reaches here for it.
//
// The conversion is recorded on the `?`'s hover symbol: TryOpInfo.Convert
// names the two types, and the impl's `from` is the symbol's DispatchImpl,
// as a call's method reference carries the impl it dispatches to. So the
// IR builder calls it on the Err path, the language server's hover and inlay
// hint name it and go-to-definition reaches it, and the facts that track a
// call's impl (an impl file's import counted as used, the bodies an edit
// to the impl rechecks, a kept program's body summary) track this one too.

// fromInterfaceOrigin is the file that declares `From`. `From` is a prelude
// name no program may redeclare.
const fromInterfaceOrigin = "std/convert"

// TryConversion is the `From` impl a `?` applies to the error it propagates.
type TryConversion struct {
	// From is the propagated error type, E.
	From Type
	// To is the boundary's declared error type, F.
	To Type
}

// Label is the interface instantiation the conversion applies, as an inlay
// hint and hover name it: `From<sql.Error>`.
func (tc *TryConversion) Label() string {
	if tc == nil || tc.From == nil {
		return ""
	}
	return "From<" + tc.From.String() + ">"
}

// applyTryFrom records the conversion a `?` at line:col applies, when the
// boundary's error type want has a `From` impl that takes the propagated
// error type got, and reports whether it found one. The impl is the one
// `want.from(e)` calls (selectImpl): at got's own type, or, where got
// converts into another candidate's (a Circle into the Shape that embeds
// it), the most specific one that takes it.
func (c *checker) applyTryFrom(want, got Type, line, col int) bool {
	if containsTypeVar(want) || containsTypeVar(got) {
		return false
	}
	iface := lookupInterfaceType(c.fa, c.reg, "From")
	if iface == nil || len(iface.Methods) != 1 {
		return false
	}
	// A `?` writes no type arguments, so an impl generic in its own
	// (`impl<C> From<Box<C>> for F`) is not one it applies.
	var cands []implCandidate
	for _, cand := range c.implCandidates(want, "From", fromInterfaceOrigin) {
		if !c.containsForeignTypeParam(&TupleType{Elems: cand.args}) {
			cands = append(cands, cand)
		}
	}
	choice := c.selectImpl(cands, iface, &iface.Methods[0], want, []Type{got}, nil)
	if choice.chosen == nil {
		return false
	}
	impl := implBlockMethod(choice.chosen.info.Block, "from")
	if impl == nil {
		return false
	}
	c.recordBoundConformance(want, "From", OriginUnresolved, c.site(line, col, RecordingKindCallSite))
	if sym := c.fa.References[Pos{Line: line, Col: col}]; sym != nil && sym.Kind == SymbolTryOp && sym.TryOp != nil {
		info := *sym.TryOp
		info.Convert = &TryConversion{From: got, To: want}
		proxy := *sym
		proxy.TryOp = &info
		proxy.DispatchImpl = impl
		proxy.DispatchReceiver = want
		c.fa.References[Pos{Line: line, Col: col}] = &proxy
	}
	return true
}

// fromImplAdmissible reports whether a program could write `impl From<got>
// for want`: one of the two is a type std does not declare, so the orphan
// rule may admit it (spec §12 *Foreign Types*; `E` is an input of `From`).
func fromImplAdmissible(want, got Type) bool {
	for _, t := range []Type{want, got} {
		if origin, nominal := nominalOrigin(resolveTypeVar(t)); nominal && origin != OriginUnresolved && !isStdOrigin(origin) {
			return true
		}
	}
	return false
}
