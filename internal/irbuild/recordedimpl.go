package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// An impl function the checker recorded (an operator's
// analysis.OperatorUse.Impl.Method; a call's analysis.Callee.Decl) is
// reached from the declaration the record names: a program's `fn` through
// the gens' implItemsByDecl, a std function through its identity
// (analysis.StdFunc) among the std index's functions of its owner. Neither
// lookup asks which impl a receiver's kind or an owner's name selects.

// recordedImplItem is the item of the impl function decl at receiver kind
// recv, and the gen that registered it: this gen's own, then, inside a std
// instance, the program's gens it is lowered for, then the other gens of
// the program. A generic receiver's block is registered once per instance,
// so recv picks among decl's items the one registered at it: in another
// gen, at that gen's own def of the instance (instanceIn), at the kind
// that gen interns for a receiver with no declaration (importKind), and
// for any other receiver, at that gen's mirror of its declaration
// (sameOperReceiver).
// Nil when no gen registered decl at recv.
func (g *gen) recordedImplItem(decl ast.Node, block *ast.ImplBlock, recv kind) (*gen, *implItem) {
	if isNilNode(decl) {
		return nil, nil
	}
	named := recv.tag == tagNamed && recv.def != nil && recv.def.decl != nil
	for _, og := range g.recordGens() {
		if og == nil {
			continue
		}
		if len(og.implItemsByDecl[og.ownDecl(decl)]) == 0 {
			continue
		}
		at, inst := g.instanceIn(og, recv)
		if !inst && isGenericInstance(recv) {
			continue
		}
		if og != g && !inst && recv.tag != tagNamed {
			// A container, a tuple or a scalar: the kind as og interns
			// it, as stdRecvCandidates asks each gen.
			if there, ok := og.importKind(recv); ok {
				at = there
			}
		}
		if it := og.registeredItem(decl, block, func(r kind) bool {
			return r == at || og != g && !inst && sameOperReceiver(r, recv, named)
		}); it != nil {
			return og, it
		}
	}
	return nil, nil
}

// registeredItem is this gen's item of decl (ownDecl), of block when it is
// not nil, whose receiver at answers.
func (g *gen) registeredItem(decl ast.Node, block *ast.ImplBlock, at func(kind) bool) *implItem {
	for _, it := range g.implItemsByDecl[g.ownDecl(decl)] {
		if it.impl == nil || block != nil && (it.impl.decl == nil || !sameImplBlock(it.impl.decl, block)) {
			continue
		}
		if at(it.impl.recv) {
			return it
		}
	}
	return nil
}

// recordGens is the gens a recorded function's item is looked for in, in
// order: this gen, then, inside a std instance, the program's gens it is
// lowered for, then the program's other gens.
func (g *gen) recordGens() []*gen {
	out := []*gen{g}
	var others []*gen
	if g.stdModule != "" && g.stdInsts != nil {
		others = append(others, g.stdInsts.program...)
	}
	if g.reg != nil {
		others = append(others, g.reg.gens...)
	}
	for _, og := range others {
		if og != nil && og != g {
			out = append(out, og)
		}
	}
	return out
}

// ownDecl is decl, a declaration a record names, as this gen's module
// declares it: for a std module checked from edited source, the edited
// twin of the shared analysis's declaration a module checked against the
// shared analysis names (Module.twins, one std function in two parses:
// sameDecl), else decl itself.
func (g *gen) ownDecl(decl ast.Node) ast.Node {
	if twin, ok := g.twins[decl]; ok {
		return twin
	}
	return decl
}

// ownFunc is ownDecl for a function.
func (g *gen) ownFunc(fd *ast.FuncDef) *ast.FuncDef {
	if twin, ok := g.ownDecl(fd).(*ast.FuncDef); ok {
		return twin
	}
	return fd
}

// isGenericInstance reports whether k is an instance of a generic type a
// file declares: a gen's own instance or its mirror of another's.
func isGenericInstance(k kind) bool {
	return k.tag == tagNamed && k.def != nil && (k.def.genericOf != nil || k.def.instOrigin != nil)
}

// recordedOwnerItem is the item of decl, of block when it is not nil, when
// the call t passes no operand of its receiver: an owner function (a
// block's function that takes no receiver, held once in its owner's def),
// a function of a block on a non-generic type, or, for a block on a
// generic type, the instance the checker instantiated the call at, which
// the gen that declares the type builds (templateCallInstance). Nil when
// no gen registered decl.
func (g *gen) recordedOwnerItem(t *ast.Call, decl ast.Node, block *ast.ImplBlock) (*gen, *implItem) {
	for _, og := range g.recordGens() {
		var filed []*implItem
		for _, it := range og.implItemsByDecl[og.ownDecl(decl)] {
			if it.impl != nil && (block == nil || it.impl.decl != nil && sameImplBlock(it.impl.decl, block)) {
				filed = append(filed, it)
			}
		}
		if len(filed) == 0 {
			continue
		}
		it := filed[0]
		if def := it.impl.recv.def; it.impl.owner == "" && def != nil && def.genericOf != nil {
			k, ok := g.templateCallInstance(og, t, def.genericOf, it.name)
			if !ok {
				return nil, nil
			}
			if og != g {
				return og, og.registeredItem(decl, block, func(r kind) bool { return r == k })
			}
			return g.recordedImplItem(decl, block, k)
		}
		if len(filed) != 1 {
			return nil, nil
		}
		return og, it
	}
	return nil, nil
}

// recordedCallPlan is the plan of the qualified call t from the function
// its record names (analysis.Callee.Decl), found by that declaration
// rather than by a spelling: a program's function, or a std module's
// lowered with this gen (recordedImplPlan), else a std function, the one
// of its identity (stdFuncByID). key names the call (`Owner.method`). Nil
// for a call the record leaves to dispatch where it runs
// (CalleeInterface), one with no record, and one whose function no gen
// registered where the call reaches it; the routes by name plan those.
func (bl *irScalarBuilder) recordedCallPlan(t *ast.Call, args irQualArgs, key string) *irQualPlan {
	if p := bl.recordedImplPlan(t, args, key); p != nil {
		return p
	}
	c, ok := bl.recordedCallee(t, args)
	if !ok {
		return nil
	}
	if f := bl.g.stdFuncByID(c.Std); f != nil {
		if _, inherited := c.Decl.(*ast.InterfaceMethod); inherited {
			return nil
		}
		if bl.g.stdModule != "" && f.pkg == bl.g.pkg && bl.g.leavesDefaults(t, len(f.params)) {
			// A std module's call of its own function omitting a default.
			return bl.stdSiblingShortPlan(t, args, f)
		}
		if p := bl.stdFuncPlan(t, args, f); p != nil {
			return p
		}
		return bl.shadowedIfacePlan(t, args, f)
	}
	return nil
}

// recordedCallee is t's record when it names the function t reaches, and
// args were lowered: inside an instance, the record at the instance's
// types (calleeAt), so a call on a type parameter names its impl there.
func (bl *irScalarBuilder) recordedCallee(t *ast.Call, args irQualArgs) (analysis.Callee, bool) {
	if !args.ok {
		return analysis.Callee{}, false
	}
	c := bl.g.calleeAt(t)
	switch c.Kind {
	case analysis.CalleeFunction, analysis.CalleeHost, analysis.CalleeImpl:
		return c, true
	}
	return analysis.Callee{}, false
}

// recordedImplPlan is recordedCallPlan for a program's function, or a std
// module's lowered with this gen: the item a gen registered for the
// declaration it lowers (recordedImplItem), its `fn` or Go-bound `host fn`,
// or for a default the record's block (Callee.Impl) inherits, the
// interface's function; at the receiver an operand of the call is, else,
// with no operand of its receiver, the owner function or the instance the
// checker instantiated the call at (recordedOwnerItem). A block over a std
// container is registered at the container kind an operand is first
// (stdRecvRecordedAt).
func (bl *irScalarBuilder) recordedImplPlan(t *ast.Call, args irQualArgs, key string) *irQualPlan {
	c, ok := bl.recordedCallee(t, args)
	if !ok || isNilNode(c.Decl) {
		return nil
	}
	decl := c.Decl
	var block *ast.ImplBlock
	if _, inherited := decl.(*ast.InterfaceMethod); inherited {
		// A default the block inherits: that block's item.
		b, ok := c.Impl.(*ast.ImplBlock)
		if !ok {
			return nil
		}
		block = b
	}
	recvs := args.kinds
	if bl.instCall == t && bl.instKind.def != nil {
		// `T.from_json(json)` at `T = Field<String>`, respelled
		// `Field.from_json(json)`: the instance travels beside the
		// call (stdKindQualCall).
		recvs = append(slices.Clip(recvs), bl.instKind)
	}
	if ib, ok := c.Impl.(*ast.ImplBlock); ok {
		for i, k := range recvs {
			// The checker's type of the operand, or of the instance
			// beside the call.
			kt := bl.instType
			if i < len(args.kinds) && i < len(t.Args) {
				kt = bl.g.operandCheckerType(t.Args[i])
			}
			bl.g.stdRecvRecordedAt(ib, k, kt)
		}
	}
	for _, k := range recvs {
		if og, it := bl.g.recordedImplItem(decl, block, k); it != nil {
			if p := bl.recordedItemPlan(t, args, og, it, key); p != nil {
				return p
			}
		}
	}
	if og, it := bl.g.recordedOwnerItem(t, decl, block); it != nil {
		if p := bl.recordedItemPlan(t, args, og, it, key); p != nil {
			return p
		}
	}
	if p := bl.recordedWithheldPlan(t, args, decl, block, key); p != nil {
		return p
	}
	if ef, ok := decl.(*ast.ExternFunc); ok {
		if p := bl.hostTemplateImplPlan(t, args, ef); p != nil {
			return p
		}
	}
	return nil
}

// recordedWithheldPlan is the call t of decl, a function its block
// withheld for each call to instantiate (implDef.withheldMember: one with
// type parameters of its own, or an expansive one), in whichever gen
// registered the block that declares it (blockWrites), or for an
// interface's default, the block that inherits it (block, the record's
// Callee.Impl): at the instance of a generic receiver an operand is
// (instanceIn), among the owner functions, among the blocks on non-generic
// receivers, else at the instance the checker instantiated the call at
// (templateCallInstance). This gen's block builds the instance itself
// (genericMethodItem); another gen's builds it for the call
// (siblingMethodPlan), unless decl is an inherent function that gen keeps
// private.
func (bl *irScalarBuilder) recordedWithheldPlan(t *ast.Call, args irQualArgs, decl ast.Node, block *ast.ImplBlock, key string) *irQualPlan {
	g := bl.g
	var name string
	var holds func(og *gen, ib *ast.ImplBlock) bool
	private := false
	switch fn := decl.(type) {
	case *ast.FuncDef:
		name, private = fn.Name, !fn.Public
		holds = func(og *gen, ib *ast.ImplBlock) bool { return blockWrites(ib, og.ownFunc(fn)) }
	case *ast.InterfaceMethod:
		if block == nil {
			return nil
		}
		name = fn.Name
		holds = func(_ *gen, ib *ast.ImplBlock) bool { return ib != nil && sameImplBlock(ib, block) }
	default:
		return nil
	}
	plan := func(og *gen, d *implDef) *irQualPlan {
		if !d.lowerable || !holds(og, d.decl) {
			return nil
		}
		if _, withheld := d.withheldMember(name); !withheld {
			return nil
		}
		if og != g {
			if d.ifaceName == "" && private {
				return nil
			}
			return bl.siblingMethodPlan(t, args, og, d, name)
		}
		if it := bl.genericMethodItem(t, d, name); it != nil {
			return bl.implItemPlan(t, args, it, key)
		}
		return nil
	}
	for _, og := range g.recordGens() {
		for _, k := range args.kinds {
			at, ok := g.instanceIn(og, k)
			if !ok {
				continue
			}
			for _, d := range og.implsOf(at) {
				if d.recv == at {
					if p := plan(og, d); p != nil {
						return p
					}
				}
			}
		}
		for _, defs := range og.ownerImpls {
			for _, d := range defs {
				if p := plan(og, d); p != nil {
					return p
				}
			}
		}
		for _, d := range og.implOrder {
			if p := plan(og, d); p != nil {
				return p
			}
		}
		if tpl := og.blockTemplate(func(ib *ast.ImplBlock) bool { return holds(og, ib) }); tpl != nil {
			// Last, since it builds the instance it names.
			if k, ok := g.templateCallInstance(og, t, tpl, name); ok {
				for _, d := range og.implsOf(k) {
					if d.recv == k {
						if p := plan(og, d); p != nil {
							return p
						}
					}
				}
			}
		}
	}
	return nil
}

// blockTemplate is the generic template of this gen one of whose impl
// blocks holds answers for, or nil.
func (g *gen) blockTemplate(holds func(*ast.ImplBlock) bool) *genericTemplate {
	for _, tpl := range g.genericTemplates {
		for _, it := range tpl.impls {
			if holds(it.decl) {
				return tpl
			}
		}
	}
	return nil
}

// instanceIn is k, a generic instance as g names it, as og registers impls
// at it: g's own kind when og is g, else the owner's instance or og's
// mirror of it (instanceDef). False for a kind that is no generic
// instance, and for an instance og has not registered.
func (g *gen) instanceIn(og *gen, k kind) (kind, bool) {
	if !isGenericInstance(k) {
		return k, false
	}
	if og == g {
		return k, true
	}
	at := og.instanceDef(instIdentity(k.def))
	if at == nil {
		return k, false
	}
	return named(at), true
}

// recordedItemPlan is the call t of it, a function og registered: this
// gen's own item (implItemPlan), inside a std instance built for a
// program, the program's item (callerItemPlan), another gen's item of a
// generic receiver's instance (siblingInstanceItemPlan), else the site
// another file's index holds for it, an impl's function (siblingSitePlan)
// or an owner function (siblingOwnerPlanFor).
func (bl *irScalarBuilder) recordedItemPlan(t *ast.Call, args irQualArgs, og *gen, it *implItem, key string) *irQualPlan {
	g := bl.g
	if og == g {
		return bl.implItemPlan(t, args, it, key)
	}
	if g.stdModule != "" && g.stdInsts != nil && g.stdInsts.caller != nil && og.stdModule == "" {
		// `Hashable.hash(w)` in `Maybe<Written>`'s derived hash: the
		// program's impl, called from the instance.
		return bl.callerItemPlan(t, args, og, it, key)
	}
	if g.files == nil || g.fileUnit < 0 || g.reg == nil || it.impl == nil {
		return nil
	}
	if it.impl.owner != "" {
		for k, sites := range g.files.ownerMembers {
			if k.method != it.name {
				continue
			}
			for _, s := range sites {
				if s.item == it && s.unit != g.fileUnit {
					return bl.siblingOwnerPlanFor(t, args, k.recv, it.impl.owner, it.name)
				}
			}
		}
		return nil
	}
	def := it.impl.recv.def
	if def == nil || def.decl == nil {
		// An impl another file writes for a receiver no file declares
		// (`impl Multiply<Shares, Decimal> for Decimal`), whose kind is
		// one process-wide: called by the symbol that file interned.
		return bl.stdRecvItemPlan(t, args, og, it.impl, key, it.name)
	}
	if def.genericOf != nil || def.instOrigin != nil {
		return bl.siblingInstanceItemPlan(t, args, og, it.impl, it)
	}
	sites := g.files.implMembers[implMemberKey{recv: def.decl, method: it.name}]
	for i := range sites {
		if sites[i].item == it && sites[i].unit != g.fileUnit {
			return bl.siblingSitePlan(t, args, &sites[i], def.nomi, it.name)
		}
	}
	return nil
}

// itemKinds are it's parameter and result kinds as this gen names them:
// it's own for an item og is this gen, else each imported (importKind).
// ok is false when one has no kind here.
func (g *gen) itemKinds(og *gen, it *implItem) (params []kind, result kind, ok bool) {
	if og == g {
		return it.params, it.result, true
	}
	if c := g.stdProgCaller; c != nil && c != g {
		// A std instance interns a program's kinds in the tables of the
		// program gen that asked for it, so they are named as that gen
		// names them.
		return c.itemKinds(og, it)
	}
	params = make([]kind, len(it.params))
	for i, p := range it.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil, kindInvalid, false
		}
		params[i] = ip
	}
	result, ok = g.importKind(it.result)
	if !ok {
		return nil, kindInvalid, false
	}
	return params, result, true
}

// recordedOperatorImpl is the function the checker recorded for the
// operator t (analysis.OperatorUse.Impl.Method), a binary operator or a
// prefix `~~~`, whose operand implementing it (the left one of two) is of
// kind lk: a program's impl item and its gen, or a std function. recorded
// is false when the record names no impl: an operator over a primitive
// the checker leaves unrecorded, one over a type parameter (resolved at
// each instance's kind) or over interface values, and one the builder
// wrote itself.
func (bl *irScalarBuilder) recordedOperatorImpl(t ast.Node, lk kind) (og *gen, it *implItem, f *stdFunc, recorded bool) {
	use := bl.g.operatorUse(t)
	if use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil || isNilNode(use.Impl.Method) {
		return nil, nil, nil, false
	}
	if og, it := bl.g.recordedImplItem(use.Impl.Method, nil, lk); it != nil {
		return og, it, nil, true
	}
	return nil, nil, bl.g.stdFuncByID(bl.g.srcFA().StdFuncOf(use.Impl.Method)), true
}
