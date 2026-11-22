package irbuild

import (
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Abstract bodies: what a generic function does whatever its type
// arguments are, for the effect walk alone.
//
// A generic function runs only as the instances programs make of it
// (irMonoTemplate), so what it does at a type no call gives it is in no
// lowered body. Its abstract body is one more instance of its template: the
// instance at its own type parameters (abstractInstance). The frame binds
// each type parameter to the checker's type parameter itself, and to a kind
// that holds it as its bounds' interface: an existential over its one bound,
// the conjunction of several, marker interfaces among them, or for a type
// parameter with no bound, an existential over Debug, which every type
// implements (abstractHeldKind). So a value of the type parameter is
// lowered as a value of that interface is, whatever the signature does with
// it (`x: T`, `decode: (Row) -> Result<T, sql.Error>`, `Maybe<T>`), and a
// function with several type parameters holds each.
//
// A type parameter the frames bind to a checker type parameter is open
// (openParam): every other frame binds a concrete type, so in an abstract
// lowering the type parameters left open are exactly the abstract bodies'.
// A per-instance record over an open type parameter reads as the checker
// answers it at the type parameter's bounds, or at Debug for one with no
// bound (abstractRecordType), so `a == b` over `List<T>` is List's own
// impl at `List<Debug>`, and a call
// with one receiver is a dispatch on it and a rendering the erased one, and
// a call the checker answers nothing for there is a call through the bound
// (boundcall.go). An instance an abstract body asks for keeps the open type
// parameters in its checker types (checkerTypeArg): a call to another
// generic function at `T`, at `Maybe<T>`, or an impl the record resolves at
// `Maybe<T>`, builds that callee's body with `T` still open, where its own
// calls through the bound are calls through the bound too. Such an instance
// is interned apart from the program's instances (openInstKey), so a
// recursive or mutually recursive call between abstract bodies re-finds the
// pending instance, and no instance a program runs is one of them.
//
// The effect walk reads a dispatch and a call through the bound as the
// union of the implementations the program retains (vm.EffectWalk.ActCalls,
// ActCall.Some), so the abstract body's answer is an upper bound of every
// instance's, whatever types its callers give it.
//
// Nothing runs an abstract body. funcDecl queues each generic function a
// program lowers no body for (abstractQueue): a monomorphization template, a
// bounded function dict.go's seam claims, and a bound-free one whose erased
// body (generic.go) declined; implDecl queues the impl blocks below. Result.Abstract lowers the queue on first use,
// after the lowering is otherwise done, with every gen's module set aside:
// what an abstract body builds, the body itself, the instances and std
// instances it asks for and the helpers they make, goes into modules of its
// own, marked abstract (ir.Module.SetAbstract: each module a gen opens while
// the census's abstract is set), which Result.Abstract answers and which
// neither Result.IR nor Result.IRModules holds. So no image, object, std
// bundle or machine that runs a program holds one, the encoder and the
// linker refuse one, and a program that loads and runs never lowers one. A
// body that declines there records no decline a program's census, hooks or
// refusals see.
//
// Result.Abstract also answers each queued generic function whose effects
// for every type are not known (an AbstractDecline), which the effect check
// reports when a list holds the function (vmhost's effects_report.go): one
// whose abstract body declined, or reached an instance that declined, and
// one that has no template to instantiate (monoTemplateFor declines it).
//
// The functions of an impl block over a generic type have abstract bodies
// the same way: implDecl queues the type's template, whose instance at its
// own type parameters registers every block over it (registerInstanceImpls)
// and builds their functions, and an instance whose type parameters carry
// a block's own `where` bounds too builds that block's. A generic function
// of its own in any block, which each call instantiates (withheldMember),
// is built at its own type parameters under the block's frame
// (methodInstance), a block of owner functions (`impl Session<T>`'s
// `Session.new<T>`) among them. A block for an interface type (`impl
// Release for Conn`), which the lowering declines since no dispatch
// reaches it, has its functions built here with the receiver held as a
// value of the interface (abstractIfaceImpl). A function whose body
// declines is not determined; a derived one is the compiler's and acts on
// nothing, so only its own absence is known.

// abstractBody is one declaration the lowering answers for every type on
// first use (lowerAbstractBodies), one of:
//
//   - fd, a generic function, with sig its declared signature, whose
//     template (irMonoTemplate) the abstract body instantiates;
//   - tpl, a generic type the program's impl blocks are written over, whose
//     instance at its own type parameters builds each block's functions;
//   - impl, a block of a type with no type parameters left to bind that
//     withholds generic functions of its own (withheldMember), each built
//     at its own type parameters, or a block for an interface type
//     (`impl Release for Conn`), which no program lowers, each of whose
//     functions is built with its receiver held as that interface.
type abstractBody struct {
	fd   *ast.FuncDef
	sig  *fnSig
	tpl  *genericTemplate
	impl *implDef
}

// AbstractDecline is a generic function whose effects for every type it may
// be called at are not known: its abstract body did not lower, reached an
// instance that did not, or it has none.
type AbstractDecline struct {
	// Fn is the function's name, and At its declaration's position, where
	// its IR would be.
	Fn string
	At ir.Pos
	// Reason is why, for a hint.
	Reason string
}

// abstractLowering is the lowering's abstract bodies, made once.
type abstractLowering struct {
	// own reports that a gen of the program's own files, not a std
	// module's, queued a declaration (AbstractOwn).
	own      bool
	once     sync.Once
	build    func() ([]*ir.Module, []AbstractDecline)
	mods     []*ir.Module
	declines []AbstractDecline
}

// Abstract is each generic function's abstract body, lowered on first use
// into modules no program links, and each generic function whose effects for
// every type are not known. Only the effect walk reads them. Nil for a
// lowering of one file (LowerFile) and a lowering relinked through objects,
// which build none.
func (r *Result) Abstract() ([]*ir.Module, []AbstractDecline) {
	if r == nil || r.abstract == nil {
		return nil, nil
	}
	a := r.abstract
	a.once.Do(func() { a.mods, a.declines = a.build() })
	return a.mods, a.declines
}

// newAbstractLowering is the abstract lowering of gens' queued
// declarations, or nil when no gen queued one.
func newAbstractLowering(gens []*gen, dc *declineCensus, insts *stdInstances) *abstractLowering {
	var a *abstractLowering
	for _, g := range gens {
		if len(g.abstractQueue) == 0 {
			continue
		}
		if a == nil {
			a = &abstractLowering{build: func() ([]*ir.Module, []AbstractDecline) {
				return lowerAbstractBodies(gens, dc, insts)
			}}
		}
		a.own = a.own || g.stdModule == "" && !g.programStd
	}
	return a
}

// AbstractOwn reports whether the program's own files declare a
// declaration whose effects for every type only its abstract body answers
// (Abstract), which a lowering builds only whole: a relinked program
// (vmhost's filerecord.go) builds none.
func (r *Result) AbstractOwn() bool {
	return r != nil && r.abstract != nil && r.abstract.own
}

// queueAbstract queues fd, declared with sig, for lowerAbstractBodies.
func (g *gen) queueAbstract(fd *ast.FuncDef, sig *fnSig) {
	g.queueAbstractBody(abstractBody{fd: fd, sig: sig})
}

// queueAbstractBody queues a, once.
func (g *gen) queueAbstractBody(a abstractBody) {
	for _, q := range g.abstractQueue {
		if q.fd == a.fd && q.tpl == a.tpl && q.impl == a.impl {
			return
		}
	}
	g.abstractQueue = append(g.abstractQueue, a)
}

// lowerAbstractBodies lowers each queued abstract body of gens, with every
// gen's module set aside, and answers the modules they built and each queued
// declaration whose effects for every type are not known. The census is
// suspended throughout, so no hook, watch or refusal sees what declines
// here.
func lowerAbstractBodies(gens []*gen, dc *declineCensus, insts *stdInstances) ([]*ir.Module, []AbstractDecline) {
	committed := len(insts.modules())
	real := make([]*ir.Module, len(gens))
	for i, g := range gens {
		real[i], g.irMod = g.irMod, nil
	}
	resume := dc.suspend()
	unmute := irMuteObservers()
	dc.abstract, dc.open = true, map[*analysis.TypeParam_]kind{}
	defer func() {
		dc.abstract, dc.open, dc.declinedFns = false, nil, nil
		unmute()
		resume()
		for i, g := range gens {
			g.irMod = real[i]
		}
	}()
	var declines []AbstractDecline
	for _, g := range gens {
		errs, masked := len(g.errs), len(g.masked)
		for _, a := range g.abstractQueue {
			before, beforeFns := dc.declined, len(dc.declinedFns)
			why, ok := g.abstractEntry(a)
			decls := a.decls()
			if ok {
				// The bodies and the instances they asked for, of this file
				// and the others: one that declines leaves the answer short.
				// When every body that declined is one of a's declarations,
				// only those are short, and of a type's, only those with no
				// body at all: a block with a `where` of its own declines at
				// an instance that does not meet it and is built at one that
				// does. Any other decline is an instance or a body one of
				// them asked for, and leaves them all short.
				irFlushLateInstances(gens)
				if dc.declined > before {
					why, ok = dc.lastDeclined, false
					own := declinedAmong(dc.declinedFns[beforeFns:], decls)
					if dc.declined-before == len(dc.declinedFns)-beforeFns && own != nil {
						if a.tpl != nil {
							own = g.abstractMissing(own)
						}
						decls, ok = own, len(own) == 0
					}
				}
			}
			if ok {
				continue
			}
			for _, fd := range decls {
				if analysis.IsSynthesizedLine(fd.Line) {
					// A derived body is the compiler's, whatever its type:
					// it renders, compares or hashes the parts and acts on
					// nothing.
					continue
				}
				declines = append(declines, AbstractDecline{Fn: a.qualified(fd), At: g.irNodePos(fd),
					Reason: "its body, with its type parameters held as their bounds, does not lower: " + why})
			}
		}
		// A refusal is for a program's own lowering, which is done.
		g.errs, g.masked = g.errs[:errs], g.masked[:masked]
	}
	mods := insts.modules()[committed:]
	for _, g := range gens {
		if g.irMod != nil {
			irLintFinished(g.irMod)
			mods = append(mods, g.irMod)
		}
	}
	return mods, declines
}

// abstractEntry queues a's abstract bodies, and answers the builder's
// reason when there are none.
func (g *gen) abstractEntry(a abstractBody) (string, bool) {
	switch {
	case a.fd != nil:
		return g.abstractInstance(a.fd, a.sig)
	case a.tpl != nil:
		return g.abstractTypeInstance(a.tpl)
	case !a.impl.lowerable:
		return g.abstractIfaceImpl(a.impl)
	}
	return g.abstractMethods(a.impl)
}

// abstractIfaceImpl builds the functions of d, a block for an interface
// type the lowering declined (`impl Release for Conn`), with the receiver
// held as a value of that interface, as an abstract body holds a type
// parameter as its bound. No dispatch registers them: nothing runs them.
func (g *gen) abstractIfaceImpl(d *implDef) (string, bool) {
	recv := g.typeOf(d.decl.Receiver)
	if recv.tag != tagIface {
		return d.why + ": " + d.whyDetail, false
	}
	held := g.registerImplAt(d.decl, recv)
	if !held.lowerable {
		return held.why + ": " + held.whyDetail, false
	}
	defer g.enterTypeScope(held.typeScope)()
	for _, it := range held.order {
		g.implFunc(held, it)
	}
	return "", true
}

// decls is each function declaration a answers for: the function, each
// function of the type's impl blocks, or each generic function the block
// withholds.
func (a abstractBody) decls() []*ast.FuncDef {
	switch {
	case a.fd != nil:
		return []*ast.FuncDef{a.fd}
	case a.tpl != nil:
		var out []*ast.FuncDef
		for _, it := range a.tpl.impls {
			out = append(out, implFuncDecls(it.decl)...)
		}
		return out
	}
	if !a.impl.lowerable {
		return implFuncDecls(a.impl.decl)
	}
	var out []*ast.FuncDef
	for _, fd := range implFuncDecls(a.impl.decl) {
		if _, withheld := a.impl.withheldMember(fd.Name); withheld {
			out = append(out, fd)
		}
	}
	return out
}

// qualified is fd's name as a diagnostic names it: an impl function's with
// its receiver's.
func (a abstractBody) qualified(fd *ast.FuncDef) string {
	switch {
	case a.tpl != nil:
		return a.tpl.nomi + "." + fd.Name
	case a.impl != nil && a.impl.decl != nil:
		return analysis.TypeExprBaseName(a.impl.decl.Receiver) + "." + fd.Name
	}
	return fd.Name
}

// abstractMissing is each of decls this gen's abstract module holds no
// body at.
func (g *gen) abstractMissing(decls []*ast.FuncDef) []*ast.FuncDef {
	held := map[[2]int]bool{}
	if g.irMod != nil {
		for _, f := range g.irMod.Funcs() {
			held[[2]int{f.Pos().Line(), f.Pos().Col()}] = true
		}
	}
	var out []*ast.FuncDef
	for _, fd := range decls {
		if at := g.irNodePos(fd); !held[[2]int{at.Line(), at.Col()}] {
			out = append(out, fd)
		}
	}
	return out
}

// declinedAmong is each of decls among declined, the declarations whose
// bodies declined, once; nil when one of declined is not among decls, or
// none declined.
func declinedAmong(declined, decls []*ast.FuncDef) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, fd := range declined {
		if !slices.Contains(decls, fd) {
			return nil
		}
		if !slices.Contains(out, fd) {
			out = append(out, fd)
		}
	}
	return out
}

// implFuncDecls is each function ib writes.
func implFuncDecls(ib *ast.ImplBlock) []*ast.FuncDef {
	if ib == nil {
		return nil
	}
	var out []*ast.FuncDef
	for _, item := range ib.Items {
		if fd, ok := item.(*ast.FuncDef); ok {
			out = append(out, fd)
		}
	}
	return out
}

// abstractTypeInstance queues the abstract bodies of the functions the impl
// blocks over tpl write: the instance of tpl at its own type parameters,
// each held as its bounds, whose blocks registerInstanceImpls registers and
// flushGenericImpls builds, and each generic function a block withholds at
// its own type parameters too (abstractMethods).
func (g *gen) abstractTypeInstance(tpl *genericTemplate) (string, bool) {
	params := g.declTypeParamDefs(tpl.decl)
	if len(params) != len(tpl.params) {
		return "the checker recorded no type parameters for `" + tpl.nomi + "`", false
	}
	if why, ok := g.abstractTypeInstanceAt(tpl, params, nil); !ok {
		return why, false
	}
	// A block with a `where` of its own serves only the instances that meet
	// it (instanceMeetsWhere), so its functions are built at type
	// parameters that carry its bounds too.
	for _, it := range tpl.impls {
		if len(it.decl.WhereClauses) > 0 {
			if why, ok := g.abstractTypeInstanceAt(tpl, params, it.decl); !ok {
				return why, false
			}
		}
	}
	return "", true
}

// abstractTypeInstanceAt is abstractTypeInstance at params, tpl's type
// parameters, each carrying the bounds ib's `where` clauses give it as
// well when ib is set.
func (g *gen) abstractTypeInstanceAt(tpl *genericTemplate, params []*analysis.TypeParam_, ib *ast.ImplBlock) (string, bool) {
	args := make([]kind, len(params))
	types := make([]analysis.Type, len(params))
	for i, p := range params {
		open := *p
		if ib != nil {
			open.Bounds = append(slices.Clip(open.Bounds), g.whereBounds(ib.WhereClauses, p.Name_)...)
		}
		// kindInvalid: reports — the reason names the type parameter.
		if args[i] = g.abstractHeldKind(&open); args[i] == kindInvalid {
			return "`" + p.Name_ + "` has no kind held as its bounds", false
		}
		types[i] = &open
		g.dc.open[&open] = args[i]
	}
	k, ok := g.genericInstance(tpl, args, types)
	if !ok || k.def == nil {
		return "`" + tpl.nomi + "` has no instance at its type parameters", false
	}
	for i := 0; i < len(g.genericImplQueue); i++ {
		if e := g.genericImplQueue[i]; e.recv == k.def {
			if why, ok := g.abstractMethods(e.impl); !ok {
				return why, false
			}
		}
	}
	return "", true
}

// whereBounds is the interfaces where's clauses bound the type parameter
// name to, as the checker resolved each bound (its type for the written
// bound).
func (g *gen) whereBounds(where []ast.WhereConstraint, name string) []*analysis.InterfaceType {
	var out []*analysis.InterfaceType
	for _, wc := range where {
		if wc.Name != name {
			continue
		}
		for _, b := range wc.Bounds {
			switch t := analysis.ResolveTypeVar(g.recordedTypeOrNil(b)).(type) {
			case *analysis.InterfaceType:
				out = append(out, t)
			case *analysis.ConjunctionType:
				out = append(out, t.Members...)
			}
		}
	}
	return out
}

// recordedTypeOrNil is the checker's type for te, or nil.
func (g *gen) recordedTypeOrNil(te ast.TypeExpr) analysis.Type {
	t, _ := g.recordedTypeExpr(te)
	return t
}

// abstractMethods queues the abstract body of each generic function block
// d withholds for a call to instantiate (withheldMember): its instance at
// its own type parameters, each held as its bounds, under d's frame.
func (g *gen) abstractMethods(d *implDef) (string, bool) {
	for _, fd := range implFuncDecls(d.decl) {
		w, withheld := d.withheldMember(fd.Name)
		if !withheld {
			continue
		}
		params := g.declTypeParams(fd)
		own := make([]kind, len(w.tps))
		types := make([]analysis.Type, len(w.tps))
		for i, tp := range w.tps {
			p := params[tp.Name]
			if p == nil {
				return "the checker recorded no type for `" + tp.Name + "` of `" + fd.Name + "`", false
			}
			// kindInvalid: reports — the reason names the type parameter.
			if own[i] = g.abstractHeldKind(p); own[i] == kindInvalid {
				return "`" + tp.Name + "` has no kind held as its bounds", false
			}
			types[i] = p
			g.dc.open[p] = own[i]
		}
		if g.methodInstance(d, fd.Name, w, own, types) == nil {
			return "`" + fd.Name + "` has no instance at its type parameters", false
		}
	}
	return "", true
}

// declTypeParamDefs is the checker's type parameters of decl, a generic
// type's declaration, in order: those its type (its definition's) declares.
func (g *gen) declTypeParamDefs(decl ast.Node) []*analysis.TypeParam_ {
	return typeParamDefsOf(g.srcFA().DeclaredType(decl))
}

// abstractInstance queues fd's abstract body: the instance of its template
// at its own type parameters, each held as its bounds (abstractHeldKind),
// and answers the builder's reason when there is none.
func (g *gen) abstractInstance(fd *ast.FuncDef, sig *fnSig) (string, bool) {
	tpl := g.irMonoTemplate(sig)
	if tpl == nil {
		return "the compiler has no template to build it from", false
	}
	params := g.declTypeParams(fd)
	args := make([]kind, len(tpl.params))
	types := make([]analysis.Type, len(tpl.params))
	for i, name := range tpl.params {
		p := params[name]
		if p == nil {
			return "the checker recorded no type for `" + name + "`", false
		}
		// kindInvalid: reports — the reason names the type parameter.
		if args[i] = g.abstractHeldKind(p); args[i] == kindInvalid {
			return "`" + name + "` has no kind held as its bounds", false
		}
		types[i] = p
		g.dc.open[p] = args[i]
	}
	if _, why, detail := g.resolveMonoInstance(tpl, args, types, fd.Name); why != "" {
		return why + ": " + detail, false
	}
	return "", true
}

// declTypeParams is, for each of fd's own type parameters by name, the
// type parameter its abstract body leaves open: the checker's type
// parameter as its type for fd (the FuncType its definition holds) names
// it, carrying the bounds that type's `where` clauses resolved for it
// (FuncType.WhereBounds). The checker keeps a function's bounds beside its
// type parameters rather than on them, and a record read at a type holding
// an open type parameter (`Maybe<T>` meeting `impl FromSql for Maybe<T>
// where T: FromSql`) is answered by the bounds the type parameter carries.
// Each is a type parameter of its own, which no record names, so an open
// one is told by its identity (declineCensus.open) and never by a name
// another frame binds.
func (g *gen) declTypeParams(fd *ast.FuncDef) map[string]*analysis.TypeParam_ {
	names := make([]string, len(fd.TypeParams))
	for i, tp := range fd.TypeParams {
		names[i] = tp.Name
	}
	return g.declTypeParamsNamed(fd, names)
}

// declTypeParamsNamed is declTypeParams for the type parameters names fd's
// type names, its own or its impl block's.
func (g *gen) declTypeParamsNamed(fd *ast.FuncDef, names []string) map[string]*analysis.TypeParam_ {
	fa := g.srcFA()
	if fa == nil {
		return nil
	}
	own := make(map[string]bool, len(names))
	for _, name := range names {
		own[name] = true
	}
	out := map[string]*analysis.TypeParam_{}
	defer func() {
		// One the function's type does not name, or a function whose
		// definition holds none (an impl's): as the checker typed its
		// parameters' and result's annotations, with the bounds its `where`
		// clauses give it.
		var written []ast.TypeExpr
		for _, p := range fd.Params {
			written = append(written, p.TypeAnnotation)
		}
		for _, te := range append(written, fd.ReturnTypeExpr) {
			for name, p := range g.typeExprParams(te) {
				if own[name] && out[name] == nil {
					p.Bounds = append(slices.Clip(p.Bounds), g.whereBounds(fd.WhereClauses, name)...)
					out[name] = p
				}
			}
		}
	}()
	sym := fa.Definitions[analysis.Pos{Line: fd.Line, Col: fd.Col}]
	if sym == nil || sym.Node != fd {
		return out
	}
	ft, _ := sym.Type.(*analysis.FuncType)
	if ft == nil {
		return out
	}
	for _, t := range append(slices.Clip(ft.Params), ft.Return) {
		analysis.ReplaceTypeParams(t, func(p *analysis.TypeParam_) (analysis.Type, bool) {
			if own[p.Name_] && out[p.Name_] == nil {
				bounded := *p
				for _, wb := range ft.WhereBounds {
					if wb.Param == p {
						bounded.Bounds = append(slices.Clip(bounded.Bounds), wb.Bounds...)
					}
				}
				out[p.Name_] = &bounded
			}
			return nil, false
		})
	}
	return out
}

// typeExprParams is each type parameter te, a written type, names, by
// name, as the checker's types for te and the types inside it name it, a
// copy of each to leave open (declTypeParams).
func (g *gen) typeExprParams(te ast.TypeExpr) map[string]*analysis.TypeParam_ {
	out := map[string]*analysis.TypeParam_{}
	var read func(ast.TypeExpr)
	read = func(te ast.TypeExpr) {
		if te == nil {
			return
		}
		if t, ok := g.recordedTypeExpr(te); ok {
			analysis.ReplaceTypeParams(t, func(p *analysis.TypeParam_) (analysis.Type, bool) {
				if out[p.Name_] == nil {
					open := *p
					out[p.Name_] = &open
				}
				return nil, false
			})
		}
		if gt, ok := te.(*ast.GenericType); ok {
			for _, a := range gt.Params {
				read(a)
			}
		}
	}
	read(te)
	return out
}

// abstractBoundType is the checker's type of the interface an open type
// parameter p is held as: its one bound, the conjunction of its bounds, or
// for one with no bound std's Debug, which every type implements. False
// only when the stdlib's analysis is not loaded.
func abstractBoundType(p *analysis.TypeParam_) (analysis.Type, bool) {
	switch len(p.Bounds) {
	case 0:
		return stdDebugType()
	case 1:
		return p.Bounds[0], true
	}
	return analysis.NewConjunction(p.Bounds), true
}

// stdDebugType is the checker's type of std's Debug interface, as std's
// module scope binds it.
func stdDebugType() (analysis.Type, bool) {
	lib := stdAnchorLib()
	if lib == nil {
		return nil, false
	}
	for i := range stdIfaceSpecs {
		if stdIfaceSpecs[i].nomi != "Debug" {
			continue
		}
		owner := lib.Files[strings.TrimPrefix(stdIfaceSpecs[i].module, "std/")]
		if owner == nil {
			return nil, false
		}
		if sym := resolvedScopeSymbol(owner.ModuleScope, "Debug"); sym != nil && sym.Type != nil {
			return sym.Type, true
		}
	}
	return nil, false
}

// abstractHeldKind is the kind values of p, an abstract body's type
// parameter, are lowered at: its bounds' interface (abstractBoundType), the
// conjunction of those of its bounds this gen has a kind of where another
// has none (an interface over the type parameter itself, `Add<T, T>`, or
// one a std module's view does not name), or with none, an existential over
// Debug, the one interface every type implements. A use of a bound with no
// kind here reads its record and declines where it needs the kind.
func (g *gen) abstractHeldKind(p *analysis.TypeParam_) kind {
	if t, ok := abstractBoundType(p); ok {
		// kindInvalid: sentinel — a bound with no kind here falls to those that have one.
		if k := g.project(t); k != kindInvalid {
			return k
		}
		var held []kind
		for _, b := range p.Bounds {
			// kindInvalid: sentinel — as above.
			if k := g.project(b); k != kindInvalid {
				held = append(held, k)
			}
		}
		// kindInvalid: sentinel — conjKind's answer for no member is none.
		if k := g.conjKind(held); len(held) > 0 && k != kindInvalid {
			return k
		}
	}
	for i := range stdIfaceSpecs {
		if stdIfaceSpecs[i].nomi == "Debug" {
			return stdIfaceKind(i)
		}
	}
	return kindInvalid
}

// openType answers t when it is a type parameter an abstract body leaves
// open (abstractInstance).
func (dc *declineCensus) openType(t analysis.Type) (*analysis.TypeParam_, bool) {
	p, ok := analysis.ResolveTypeVar(t).(*analysis.TypeParam_)
	if !ok || dc == nil {
		return nil, false
	}
	if _, open := dc.open[p]; !open {
		return nil, false
	}
	return p, true
}

// openParam reports whether ty, the type a per-instance record is for, is a
// type parameter left open (openType), or one the frames bind to one: a
// type parameter of an abstract body, or of an instance it asked for at
// its type parameters. It answers the open one.
func (g *gen) openParam(ty analysis.Type) (*analysis.TypeParam_, bool) {
	if open, isOpen := g.dc.openType(ty); isOpen {
		return open, true
	}
	p, ok := analysis.ResolveTypeVar(ty).(*analysis.TypeParam_)
	if !ok || g.dc == nil || !g.dc.abstract {
		return nil, false
	}
	t, bound := g.genericSubstType(p.Name_)
	if !bound {
		return nil, false
	}
	return g.dc.openType(t)
}

// abstractRecordType is the checker's type p, a type parameter a
// per-instance record names, stands for in an abstract lowering: the
// instance's type argument (genericSubstType), or for an open type
// parameter (openParam), the interface it is held as (abstractBoundType). A
// record over a type that holds an open parameter (`Maybe<T>`) reads at
// that type, so the impl it resolves keeps `T` open.
func (g *gen) abstractRecordType(p *analysis.TypeParam_) (analysis.Type, bool) {
	if open, isOpen := g.openParam(p); isOpen {
		return abstractBoundType(open)
	}
	return g.genericSubstType(p.Name_)
}

// openInstKey is key, an instance's intern key, apart from every instance a
// program runs when types, its checker types, hold an open type parameter
// (openType): an abstract body's instance is interned with its own kind.
func openInstKey(dc *declineCensus, key string, types []analysis.Type) string {
	if dc == nil || len(dc.open) == 0 {
		return key
	}
	for _, t := range types {
		if t == nil {
			continue
		}
		open := false
		analysis.ReplaceTypeParams(t, func(p *analysis.TypeParam_) (analysis.Type, bool) {
			_, isOpen := dc.open[p]
			open = open || isOpen
			return nil, false
		})
		if open {
			return key + "\x00open"
		}
	}
	return key
}
