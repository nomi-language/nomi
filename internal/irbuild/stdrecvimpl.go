package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// An impl of a program's own interface for a std generic container:
// `impl Show for List<T> where T: Display`, `impl Zero for Maybe<T>`,
// `impl Keyed for Map<String, V>`. The orphan rule admits it because the
// interface is the module's own (§13), and the checker answers a call
// through it at each instance.
//
// A std container has no template in this program to mint its instances,
// so nothing registers the block when an instance appears, as
// registerInstanceImpls does for a user generic type. Its instances are
// interned on demand, some of them process-wide (sharedcomp.go). So the
// block is registered per instance when a call needs it: a call at a
// container kind (stdRecvImplsAt) registers each of this gen's blocks of the
// interface or function the checker resolved the call to whose receiver
// matches that kind, under the frame that binds the block's parameters to
// the kind's arguments, and queues its body on genericImplQueue with that
// frame, as a generic interface instantiation's method is queued.
//
// A block over a concrete instance, `impl Show for List<Int>`, names no
// parameter and is an ordinary impl (receiverKind admits its receiver).
//
// A call in another file than the block reaches it through
// stdRecvCandidates, which asks every file of the program at the kind. A
// builtin scalar is the other receiver no file declares, so an
// `impl Show for Int` in another file is found the same way; the routes for
// a declared receiver key the impl by the receiver's declaration.

// stdRecvTemplate is one impl block whose receiver is a std generic
// container over at least one of the block's own type parameters.
type stdRecvTemplate struct {
	decl *ast.ImplBlock
	// iface is the interface's name as the header writes it.
	iface string
	head  string
	// params is the parameter each receiver argument binds, by position, ""
	// where the argument is a concrete type (args holds it, resolved when an
	// instance is matched).
	params []string
	args   []ast.TypeExpr
}

// stdRecvKey is one block registered for one container kind.
type stdRecvKey struct {
	decl *ast.ImplBlock
	recv kind
}

// stdRecvParts is the std generic container k instantiates and its type
// arguments, in the receiver's order.
func stdRecvParts(k kind) (string, []kind, bool) {
	switch {
	case k.tag == tagList && k.comp != nil:
		return "List", k.comp.parts[:1], true
	case k.tag == tagMap && k.comp != nil:
		return "Map", k.comp.parts[:2], true
	case k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil:
		return k.def.preludeOf.spec.nomi, k.def.preludeArgs, true
	}
	if e, ok := vectorElem(k); ok {
		return "Vector", []kind{e}, true
	}
	if e, ok := setElem(k); ok {
		return "Set", []kind{e}, true
	}
	if e, ok := rangeElem(k); ok {
		return "Range", []kind{e}, true
	}
	return "", nil, false
}

// stdRecvTemplateOf reports whether ib is an impl of an interface over a std
// generic container that names one of the block's parameters, and answers
// its template.
func (g *gen) stdRecvTemplateOf(ib *ast.ImplBlock) (*stdRecvTemplate, bool) {
	gt, generic := ib.Receiver.(*ast.GenericType)
	if !generic || len(gt.Params) == 0 || ib.Interface == nil {
		return nil, false
	}
	if g.blockLocalTemplate(gt.Name) != nil || g.genericTemplates[gt.Name] != nil || !g.programIface(ib) {
		return nil, false
	}
	params := analysis.ReceiverTypeParamNames(ib.Receiver, g.declaresTypeName)
	if len(params) != len(gt.Params) {
		return nil, false
	}
	// The receiver at Int for every parameter: a std container answers
	// stdRecvParts with the receiver's own head.
	probe := map[string]kind{}
	named := false
	for _, p := range params {
		if p != "" {
			probe[p] = kindInt
			named = true
		}
	}
	if !named {
		return nil, false
	}
	g.pushIfaceSubst(probe)
	k := g.typeOf(ib.Receiver)
	g.popIfaceSubst()
	head, _, ok := stdRecvParts(k)
	if !ok || head != gt.Name {
		return nil, false
	}
	iface, _ := ifaceHeaderName(ib.Interface)
	return &stdRecvTemplate{decl: ib, iface: iface, head: head, params: params, args: gt.Params}, true
}

// programIface reports whether ib implements an interface a file of the
// program declares. Only such an impl may name a std container as its
// receiver: the orphan rule refuses a std interface's (§13), and the
// builder's std dispatch never reads one.
func (g *gen) programIface(ib *ast.ImplBlock) bool {
	if ib.Interface == nil {
		return false
	}
	name, named := ifaceHeaderName(ib.Interface)
	if !named {
		return false
	}
	if g.ifaceTemplate(name) != nil {
		return true
	}
	_, found := g.ifaceNamed(name)
	return found
}

// registerStdRecvTemplate records ib as one of this gen's blocks over a std
// container and reports whether it is one. The def is the template: it
// lowers nothing, and each instance's def is registered by stdRecvImplsAt.
func (g *gen) registerStdRecvTemplate(d *implDef, ib *ast.ImplBlock) bool {
	t, ok := g.stdRecvTemplateOf(ib)
	if !ok {
		return false
	}
	d.lowerable, d.why, d.whyDetail = false, "generic impl block", typeText(ib.Receiver)
	if len(ib.Generics) > 0 {
		// A binder naming something the receiver does not use has no
		// argument to be solved from.
		own := map[string]bool{}
		for _, p := range t.params {
			own[p] = true
		}
		for _, tp := range ib.Generics {
			if !own[tp.Name] {
				return true
			}
		}
	}
	for _, wc := range ib.WhereClauses {
		if !t.binds(wc.Name) {
			return true
		}
	}
	d.why = "generic type"
	d.stdRecvTemplate = t
	g.stdRecvTemplates = append(g.stdRecvTemplates, t)
	return true
}

// binds reports whether name is one of the parameters t's receiver binds.
func (t *stdRecvTemplate) binds(name string) bool {
	for _, p := range t.params {
		if p != "" && p == name {
			return true
		}
	}
	return false
}

// match is the frame binding t's parameters to args, the arguments of a
// container kind with t's head, or false when a concrete argument of the
// receiver differs or one parameter would bind two kinds.
func (t *stdRecvTemplate) match(g *gen, args []kind) (map[string]kind, bool) {
	if len(args) != len(t.params) {
		return nil, false
	}
	frame := map[string]kind{}
	for i, p := range t.params {
		if p == "" {
			continue
		}
		if prior, seen := frame[p]; seen && prior != args[i] {
			return nil, false
		}
		frame[p] = args[i]
	}
	for i, p := range t.params {
		if p != "" {
			continue
		}
		g.pushIfaceSubst(frame)
		want := g.typeOf(t.args[i])
		g.popIfaceSubst()
		if want != args[i] {
			return nil, false
		}
	}
	return frame, true
}

// stdRecvImplsAt answers this gen's blocks over k's container registered at
// k, registering at k, once, each block that implements iface or holds
// item.
//
// A block is registered only for the interface or function a call names,
// because the checker has established that impl at k for that call and no
// other: a block whose `where` clause k's arguments do not meet would
// register a body that does not lower.
func (g *gen) stdRecvImplsAt(k kind, iface string, item *ast.FuncDef) []*implDef {
	if len(g.stdRecvTemplates) == 0 {
		return nil
	}
	head, args, ok := stdRecvParts(k)
	if !ok {
		return nil
	}
	var out []*implDef
	for _, t := range g.stdRecvTemplates {
		if t.head != head {
			continue
		}
		key := stdRecvKey{decl: t.decl, recv: k}
		if d, done := g.stdRecvDone[key]; done {
			if d != nil {
				out = append(out, d)
			}
			continue
		}
		if (iface == "" || t.iface != iface) && (item == nil || !implBlockHolds(t.decl, item)) {
			continue
		}
		if g.stdRecvDone == nil {
			g.stdRecvDone = map[stdRecvKey]*implDef{}
		}
		frame, ok := t.match(g, args)
		if !ok {
			g.stdRecvDone[key] = nil
			continue
		}
		// Recorded before the registration, which resolves the block's
		// signatures and may ask for k again.
		g.stdRecvDone[key] = nil
		g.pushIfaceSubst(frame)
		d := g.registerImplAt(t.decl, k)
		g.popIfaceSubst()
		if !d.lowerable {
			continue
		}
		g.stdRecvDone[key] = d
		g.genericImplQueue = append(g.genericImplQueue, &genericImplInst{impl: d, subst: frame})
		out = append(out, d)
	}
	return out
}

// stdRecvDefsAt is every impl this gen has for the container kind k: the
// blocks over a concrete instance and each block over the container
// registered at k.
func (g *gen) stdRecvDefsAt(k kind, iface string, item *ast.FuncDef) []*implDef {
	out := g.stdRecvImplsAt(k, iface, item)
	for _, d := range g.implOrder {
		if d.recv == k && d.lowerable && d.stdRecvTemplate == nil {
			out = append(out, d)
		}
	}
	return out
}

// stdRecvCand is one impl a call at a receiver no file declares may take,
// with the gen that registered it.
type stdRecvCand struct {
	g *gen
	d *implDef
}

// stdRecvCandidates is every impl, in any file, that supplies method for k,
// a container or scalar kind, and is one the call's checked callee names:
// of the impl block holding item when the checker resolved the call to a
// function of a block, of iface when it resolved it to that interface's
// function. A generic interface implemented at several type arguments has
// one candidate per impl; stdRecvPick chooses among them.
func (g *gen) stdRecvCandidates(k kind, method, iface string, item *ast.FuncDef) []stdRecvCand {
	if _, ok := undeclaredRecvOwner(k); !ok {
		return nil
	}
	gens := []*gen{g}
	if g.reg != nil {
		gens = append(gens, g.reg.gens...)
	}
	seen := map[*gen]bool{}
	var out []stdRecvCand
	for _, gg := range gens {
		if gg == nil || seen[gg] || gg.stdModule != "" {
			continue
		}
		seen[gg] = true
		kk := k
		if gg != g {
			var ok bool
			if kk, ok = gg.importKind(k); !ok {
				continue
			}
		}
		for _, d := range gg.stdRecvDefsAt(kk, iface, item) {
			if d.items[method] == nil || (iface != "" && d.ifaceName != iface) ||
				(item != nil && !implBlockHolds(d.decl, item)) {
				continue
			}
			out = append(out, stdRecvCand{g: gg, d: d})
		}
	}
	return out
}

// stdRecvPick is the call of the one candidate whose function takes the
// call's operands, and among several, whose result is the checked one.
func (bl *irScalarBuilder) stdRecvPick(t *ast.Call, args irQualArgs, cands []stdRecvCand, method string) *irQualPlan {
	var fits []*irQualPlan
	for _, c := range cands {
		if p := bl.stdRecvItemPlan(t, args, c.g, c.d, c.d.ifaceName+"."+method, method); p != nil {
			fits = append(fits, p)
		}
	}
	if len(fits) > 1 {
		want := bl.g.solvedCallReturn(t)
		var byResult []*irQualPlan
		for _, p := range fits {
			// kindInvalid: lookup — no solved result leaves the choice open, and the call declines.
			if want != kindInvalid && p.result == want {
				byResult = append(byResult, p)
			}
		}
		fits = byResult
	}
	if len(fits) != 1 {
		return nil
	}
	return fits[0]
}

// implBlockHolds reports whether fd is one of ib's functions.
func implBlockHolds(ib *ast.ImplBlock, fd *ast.FuncDef) bool {
	for _, it := range ib.Items {
		if it == ast.Node(fd) {
			return true
		}
	}
	return false
}

// stdRecvCallee is what the checker resolved a qualified call's function
// to: a function of an impl block, or an interface's function.
func (bl *irScalarBuilder) stdRecvCallee(t *ast.Call) (string, *ast.FuncDef) {
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil || bl.g.fa == nil {
		return "", nil
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym != nil {
		if fd, isFn := sym.DispatchImpl.(*ast.FuncDef); isFn {
			// The impl function the checker selected for the call.
			return "", fd
		}
	}
	for sym != nil {
		if fd, isFn := sym.Node.(*ast.FuncDef); isFn {
			return "", fd
		}
		if sym.Kind == analysis.SymbolInterfaceMethod {
			return sym.OwningInterface, nil
		}
		sym = sym.Resolved
	}
	return "", nil
}

// stdRecvCallPlan is the call of a program's impl function for the std
// container kind k: `List.show(xs)`, `T.zero(n)` with T a `List<Int>`,
// `Zero.zero<Maybe<Int>>(1)`. Nil when no impl of the program supplies
// method at k, so std's own function for the container is the call's.
func (bl *irScalarBuilder) stdRecvCallPlan(t *ast.Call, args irQualArgs, k kind, method string) *irQualPlan {
	if bl.g.stdModule != "" || !args.ok {
		return nil
	}
	iface, item := bl.stdRecvCallee(t)
	return bl.stdRecvPick(t, args, bl.g.stdRecvCandidates(k, method, iface, item), method)
}

// stdRecvOwnerPlan is `List.show(xs)`: a call qualified by a std container
// whose function a program impl supplies, at the instance an operand of
// the container is, else the instance the checked result is.
func (bl *irScalarBuilder) stdRecvOwnerPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	if bl.g.stdModule != "" || !args.ok {
		return nil
	}
	iface, item := bl.stdRecvCallee(t)
	if iface == "" && item == nil {
		return nil
	}
	at := func(k kind) *irQualPlan {
		if name, _ := undeclaredRecvOwner(k); name != owner {
			return nil
		}
		if _, scalar := irDispatchScalarName(k); scalar {
			// A scalar's impl in this file is the local routes'.
			for _, d := range bl.g.implOrder {
				if d.recv == k && d.items[method] != nil {
					return nil
				}
			}
		}
		return bl.stdRecvCallPlan(t, args, k, method)
	}
	for _, k := range args.kinds {
		if name, ok := undeclaredRecvOwner(k); ok && name == owner {
			return at(k)
		}
	}
	// kindInvalid: lookup — an owner naming no type tries the checked result.
	if k := bl.ownerKind(t, owner); k != kindInvalid {
		// `Date.from_sql(v)`, `Int.from_sql(v)`: a function with no operand
		// of its owner's type, of an owner that takes no type arguments, is
		// that owner's.
		if name, ok := undeclaredRecvOwner(k); ok && name == owner {
			if _, _, container := stdRecvParts(k); !container {
				return at(k)
			}
		}
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || ft.Return == nil {
		return nil
	}
	result := bl.g.project(ft.Return)
	if p := at(result); p != nil {
		return p
	}
	// `Maybe.make(1)` typed `Result<Maybe<Int>, String>`, for an impl of
	// `make(n: Int) -> Result<self, String>`: the instance is a part of the
	// checked result. Each part the owner names is tried, and the one whose
	// impl answers exactly the checked result is the call's.
	var found *irQualPlan
	var foundAt kind
	ambiguous := false
	var walk func(k kind)
	walk = func(k kind) {
		_, parts, container := stdRecvParts(k)
		if !container && irRetainedTupleKind(k) {
			parts = k.comp.parts
		}
		for _, part := range parts {
			if name, ok := undeclaredRecvOwner(part); ok && name == owner && part != foundAt {
				if p := at(part); p != nil && p.result == result {
					ambiguous = ambiguous || found != nil
					found, foundAt = p, part
				}
			}
			walk(part)
		}
	}
	walk(result)
	if ambiguous {
		return nil
	}
	return found
}

// ownerKind is the kind of the type t's qualifier names, owner: a builtin
// scalar or a declared type by this file's tables, else the type the checker
// bound the qualifier to (a std type no table here holds, `Date`).
// kindInvalid when it names neither.
func (bl *irScalarBuilder) ownerKind(t *ast.Call, owner string) kind {
	// kindInvalid: lookup — a miss asks the checker.
	if k := bl.g.qualifierKind(owner); k != kindInvalid {
		return k
	}
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified {
		return kindInvalid
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType || ti.Name != owner {
		return kindInvalid
	}
	sym := bl.g.checkedTypeRef(ti.Line, ti.Col)
	if sym == nil || sym.Type == nil {
		return kindInvalid
	}
	return bl.g.project(sym.Type)
}

// undeclaredRecvOwner is the owner a call spells for k when k is a receiver
// no file of the program declares, whose impls any file may write: a std
// generic container's head, a builtin scalar's name, or a std type's.
func undeclaredRecvOwner(k kind) (string, bool) {
	if name, scalar := irDispatchScalarName(k); scalar {
		return name, true
	}
	if name, std := stdNamedRecv(k); std {
		return name, true
	}
	head, _, ok := stdRecvParts(k)
	return head, ok
}

// stdNamedRecv is the name of k when k is a std type with no type
// parameters, `Date`, `Decimal`, `Instant` or `Json`: a process-wide def no
// file of the program declares (rtDeclared, with no declaration of the
// program's), whose impls of a program's interface the orphan rule admits in
// any file, as `impl ToSql for Date` in std/sql.
func stdNamedRecv(k kind) (string, bool) {
	if k.tag != tagNamed || k.def == nil {
		return "", false
	}
	d := k.def
	if !d.rtDeclared || d.decl != nil || d.genStructOf != nil || d.genHostOf != nil || d.preludeOf != nil ||
		d.genericOf != nil || d.foreign != "" {
		return "", false
	}
	return d.nomi, true
}

// stdRecvKindCall lowers the call t of method at the container kind k when
// a program impl supplies it; handled is false otherwise.
func (bl *irScalarBuilder) stdRecvKindCall(t *ast.Call, k kind, method string) (ir.Temp, kind, bool, bool, bool) {
	unhandled := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	if bl.g.stdModule != "" {
		return unhandled()
	}
	// Asked before the operands are lowered, which the call below does once.
	if iface, item := bl.stdRecvCallee(t); len(bl.g.stdRecvCandidates(k, method, iface, item)) == 0 {
		return unhandled()
	}
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	args := bl.irQualLowerArgs(t)
	plan := bl.stdRecvCallPlan(t, args, k, method)
	if plan == nil {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, rk, mobile, ok := bl.qualEmit(t, args, plan)
	return v, rk, mobile, ok, true
}

// stdRecvIfacePlan is the call of the impl of d's method for recv, a std
// container kind, that any file of the program writes: `Show.show([1, 2])`
// and, through the routes that spell it so, `T.show(xs)` in an instance and
// `List.show(xs)`.
func (bl *irScalarBuilder) stdRecvIfacePlan(t *ast.Call, args irQualArgs, d *ifaceDef, method string, recv kind) *irQualPlan {
	if bl.g.stdModule != "" {
		return nil
	}
	_, item := bl.stdRecvCallee(t)
	var cands []stdRecvCand
	for _, c := range bl.g.stdRecvCandidates(recv, method, d.nomi, item) {
		if d.decl == nil || (c.d.iface != nil && c.d.iface.decl == d.decl) {
			cands = append(cands, c)
		}
	}
	return bl.stdRecvPick(t, args, cands, method)
}

// stdRecvItemPlan is the call of impl's function method, impl registered in
// og, with the operands args.
func (bl *irScalarBuilder) stdRecvItemPlan(t *ast.Call, args irQualArgs, og *gen, impl *implDef, key, method string) *irQualPlan {
	it := impl.items[method]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return nil
	}
	if og == bl.g {
		return bl.implItemPlan(t, args, it, key)
	}
	// The declaring gen's kinds, named in this gen's tables.
	params := make([]kind, len(it.params))
	for i, p := range it.params {
		ip, ok := bl.g.importKind(p)
		if !ok {
			return nil
		}
		params[i] = ip
	}
	result, ok := bl.g.importKind(it.result)
	if !ok || !bl.qualSignature(t, args, params, result) {
		return nil
	}
	return &irQualPlan{token: it, name: key, result: result, sym: og.irCalleeSym(it, impl.recv.nomi()+"."+it.name)}
}
