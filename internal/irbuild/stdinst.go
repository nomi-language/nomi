package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A GENERIC STDLIB DECLARATION, INSTANTIATED PER PROGRAM FOR THE VM.
//
// `impl FromJson for List<T> where T: FromJson` and `fn map<T, U>(gen:
// Generator<T>, f: (T) -> U)` are rules for making functions. The stdlib is
// lowered once per process and cached (stdlibLowering), and which
// instantiations exist is a property of the program, so no instance can live
// in the cache.
//
// # Where an instance lives
//
// Each instance is built by its own std gen, made from the RETAINED module
// view (stdlibIndex.views) exactly as the cached module's bodies were: the
// same AST, the same analysis, the same candidates and sibling scope. The gen
// carries the instance's substitution frame, so `T` in the body's annotations
// and in the checker's solved types (project's TypeParam_ arm) is the
// instance's argument. Its graph goes into that gen's own ir.Module, and the
// program's instance modules are linked beside the cached stdlib modules
// (Result.IRModules) and never written into the cache. A program's instances
// cannot reach another program, and the cache never holds a graph that names
// a per-program symbol.
//
// Symbols link by pointer, so the instance's symbol is interned in its own
// gen's table before its body is built; a recursive or mutually recursive
// call inside the closure names that pointer, and the shell the body is built
// in interns the same token and gets the same pointer back.
//
// # All or nothing per closure
//
// Building one body can discover more instances (`List.from_json` at `Int`
// reaches `decode_list` at `Int`). A top-level discovery runs its worklist to
// exhaustion and commits every member or none, so no committed graph calls a
// symbol that no linked module defines. A member reached while it is still
// pending answers its own symbol, which is what makes recursion terminate.
//
// # What declines
//
// The declaration refuses nothing; a call at an instantiation this builder
// cannot represent declines at the call, where the type arguments are known.
// A type argument or a substituted signature outside the retained value
// domain declines, as does a body the builder declines at that instance.

// stdInstCap bounds a chain of distinct nested instantiations, for
// monoInstCap's reason: a body that instantiates itself at a strictly larger
// argument would otherwise never terminate.
const stdInstCap = 8

// stdInst is one instantiation of one generic stdlib declaration.
type stdInst struct {
	f    *stdFunc
	fd   *ast.FuncDef
	tps  []string
	args []kind
	// holes are the type parameters the BUILDER filled: the checker left them
	// unsolved at the call, and the builder read each as Unit
	// (stdInstCallAt). No value of a hole is built or observed, so a bound
	// dispatch on one is unreachable (irHoleBoundCall). A parameter the
	// checker solved is never a hole, whatever it solved it to.
	holes map[string]bool
	// gen builds this instance's body; the signature below was resolved
	// through it, so the two cannot disagree about an annotation.
	gen  *gen
	view *stdFunc
	sym  *ir.Symbol
	// depth is the length of the instantiation chain that reached this
	// instance, for stdInstCap.
	depth int
}

// stdInstances is the per-program table of stdlib instances, shared by every
// gen of one GenerateIR call: the user units and the instance gens.
type stdInstances struct {
	std     *stdlibIndex
	byKey   map[string]*stdInst
	order   []string
	pending map[string]*stdInst
	// pendingOrder is the closure being attempted, in discovery order.
	pendingOrder []string
	failed       map[string]string
	attempting   bool
	// building is the instance whose body is being built, so an instance it
	// discovers measures its chain from there.
	building *stdInst
	// caller is the gen whose call asks for an instance. An instance gen
	// interns its structural kinds, prelude, std generic struct and std
	// generic host instances in the caller's tables, so `Maybe<Point>` or
	// `Vector<Point>` over the caller's Point is one kind on both sides of the
	// call.
	caller *gen
}

func newStdInstances(std *stdlibIndex) *stdInstances {
	return &stdInstances{
		std:     std,
		byKey:   map[string]*stdInst{},
		pending: map[string]*stdInst{},
		failed:  map[string]string{},
	}
}

// modules is the committed instances' graphs, in commit order.
func (s *stdInstances) modules() []*ir.Module {
	if s == nil {
		return nil
	}
	var out []*ir.Module
	for _, key := range s.order {
		if m := s.byKey[key].gen.irMod; m != nil {
			out = append(out, m)
		}
	}
	return out
}

// stdInstKey is the declaration pointer plus each argument's identity.
func stdInstKey(fd *ast.FuncDef, tps []string, args []kind, holes map[string]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%p", fd)
	for i, a := range args {
		if i < len(tps) && holes[tps[i]] {
			b.WriteString("\x00hole")
		}
		b.WriteString("\x00")
		b.WriteString(a.key())
	}
	return b.String()
}

// instantiate answers the instance of f at args, building its closure on a
// cold table. A nested call reached while a closure is attempted joins it.
func (s *stdInstances) instantiate(f *stdFunc, tps []string, args []kind, holes map[string]bool, caller *gen) (*stdInst, string) {
	key := stdInstKey(f.decl, tps, args, holes)
	prevCaller := s.caller
	s.caller = caller
	defer func() { s.caller = prevCaller }()
	if inst := s.byKey[key]; inst != nil {
		return inst, ""
	}
	if inst := s.pending[key]; inst != nil {
		return inst, ""
	}
	if why, bad := s.failed[key]; bad {
		return nil, why
	}
	if s.attempting {
		return s.queue(f, tps, args, holes, key)
	}
	return s.attemptClosure(f, tps, args, holes, key)
}

func (s *stdInstances) attemptClosure(f *stdFunc, tps []string, args []kind, holes map[string]bool, key string) (*stdInst, string) {
	s.attempting = true
	// The closure is built in the middle of the caller's own attempt, whose
	// decline bookkeeping is process state: it is set aside and restored, and
	// the test observers do not see instance bodies.
	fn, seen, why0, bodyWhy, line, col := irDeclineFn, irDeclineSeen, irDeclineWhy, irDeclineBodyWhy, irDeclineLine, irDeclineCol
	path, cur := irDeclinePath, irDeclineCur
	unmute := irMuteObservers()
	defer func() {
		unmute()
		irDeclineFn, irDeclineSeen, irDeclineWhy, irDeclineBodyWhy, irDeclineLine, irDeclineCol = fn, seen, why0, bodyWhy, line, col
		irDeclinePath, irDeclineCur = path, cur
		s.attempting = false
		s.pending = map[string]*stdInst{}
		s.pendingOrder = nil
		s.building = nil
	}()
	top, why := s.queue(f, tps, args, holes, key)
	if top == nil {
		s.failed[key] = why
		return nil, why
	}
	// A worklist: building one body queues more, so len() is re-read.
	for i := 0; i < len(s.pendingOrder); i++ {
		inst := s.pending[s.pendingOrder[i]]
		if why := s.build(inst); why != "" {
			s.failed[key] = why
			return nil, why
		}
	}
	for _, k := range s.pendingOrder {
		s.byKey[k] = s.pending[k]
		s.order = append(s.order, k)
	}
	return top, ""
}

// queue resolves one instance's signature and adds it to the closure. The
// signature is resolved here rather than when the body is built, because a
// recursive caller of a pending instance needs its parameter kinds first.
func (s *stdInstances) queue(f *stdFunc, tps []string, args []kind, holes map[string]bool, key string) (*stdInst, string) {
	depth := 1
	if s.building != nil {
		depth = s.building.depth + 1
	}
	if depth > stdInstCap {
		return nil, fmt.Sprintf("a stdlib instantiation chain deeper than %d: %s", stdInstCap, f.key)
	}
	v := s.std.views[f.module]
	if v == nil {
		return nil, "a generic stdlib function with no retained module view: " + f.key
	}
	offer := make(map[*stdCandidate]bool, len(v.settled)+len(v.withCycles))
	for c := range v.settled {
		offer[c] = true
	}
	for c := range v.withCycles {
		offer[c] = true
	}
	g := newStdGen(v.module, v.path, v.pkg, v.nodes, v.fa, v.cands, offer, v.onces, s.std)
	g.stdInsts = s
	if c := s.caller; c != nil {
		if c.preludeInsts == nil {
			c.preludeInsts = map[string][]*typeDef{}
		}
		if c.genStructInsts == nil {
			c.genStructInsts = map[string][]*typeDef{}
		}
		if c.genHostInsts == nil {
			c.genHostInsts = map[string][]*typeDef{}
		}
		if c.comps == nil {
			c.comps = map[string][]*compKind{}
		}
		g.preludeInsts, g.genStructInsts, g.genHostInsts, g.comps = c.preludeInsts, c.genStructInsts, c.genHostInsts, c.comps
	}
	frame := make(map[string]kind, len(tps))
	for i, tp := range tps {
		frame[tp] = args[i]
	}
	g.genericSubst = append(g.genericSubst, frame)
	inst := &stdInst{f: f, fd: f.decl, tps: tps, args: args, holes: holes, gen: g, depth: depth}
	g.stdInstCur = inst
	view := *f
	view.canon = nil
	view.irBody = nil
	view.irArity = nil
	view.irSlot = nil
	view.why = ""
	if len(tps) == 0 {
		// A monomorphic body keeps the signature the index resolved for it,
		// destructuring parameters included.
		view.params = append([]kind(nil), f.params...)
	} else {
		view.params = make([]kind, len(inst.fd.Params))
		for i, p := range inst.fd.Params {
			if p.TypeAnnotation == nil {
				return nil, "a generic stdlib parameter with no annotation: " + f.key
			}
			view.params[i] = g.typeOf(p.TypeAnnotation)
		}
	}
	for _, k := range view.params {
		if !irCallableValueKind(k) {
			return nil, "a generic stdlib instance's parameter outside the domain: " + f.key + " at " + kindsNomi(args)
		}
	}
	view.result = f.result
	if len(tps) != 0 {
		view.result = kindUnit
		if inst.fd.ReturnTypeExpr != nil {
			view.result = g.typeOf(inst.fd.ReturnTypeExpr)
		}
	}
	if view.result != kindUnit && !irCallableValueKind(view.result) {
		return nil, "a generic stdlib instance's result outside the domain: " + f.key + " at " + kindsNomi(args)
	}
	view.arityMin = len(view.params)
	view.body = true
	view.pkg = v.pkg
	g.stdInstName = f.key + "<" + kindsNomi(args) + ">"
	inst.view = &view
	inst.sym = g.irCalleeSym(inst.fd, f.key+"<"+kindsNomi(args)+">")
	s.pending[key] = inst
	s.pendingOrder = append(s.pendingOrder, key)
	return inst, ""
}

// build lowers one instance's body through its own gen, answering why it
// declined, or "".
func (s *stdInstances) build(inst *stdInst) string {
	g := inst.gen
	prev := s.building
	s.building = inst
	defer func() { s.building = prev }()
	g.emitStdFunc(inst.view, inst.fd)
	if g.irMod != nil && g.irMod.FuncFor(inst.sym) != nil {
		return ""
	}
	why := g.stdUnlowered[g.stdInstName]
	if _, reason, found := strings.Cut(why, "|"); found {
		why = reason
	}
	if why == "" && len(g.errs) > 0 {
		why = g.errs[0].Construct
	}
	if why == "" {
		why = "no reason recorded"
	}
	return "stdlib instance " + inst.f.key + "<" + kindsNomi(inst.args) + ">: " + why
}

// --- templates ---------------------------------------------------------------

// stdGenericTemplateUsable reports whether a candidate is a generic template
// with a body this table may instantiate.
func stdGenericTemplateUsable(f *stdFunc) bool {
	return f != nil && f.why == "stdlib generic function" && f.decl != nil && f.decl.Body != nil &&
		len(f.decl.Decorators) == 0
}

// stdModuleGenericTemplate is the one generic template the module declares
// under `recv.name` ("" for a free function), or nil. An overload set
// declines: a generic template has no parameter kinds to select on.
func stdModuleGenericTemplate(v *stdModuleView, recv, name string) *stdFunc {
	if v == nil {
		return nil
	}
	var found *stdFunc
	for _, c := range v.cands {
		if c.f == nil || c.f.recv != recv || c.f.name != name {
			continue
		}
		if found != nil {
			return nil
		}
		found = c.f
	}
	if !stdGenericTemplateUsable(found) {
		return nil
	}
	return found
}

// stdUnretainedMonoBody reports a monomorphic Nomi-bodied stdlib function
// whose cached body was not retained because it calls a generic sibling in
// its own module (`Generator.bool` calls `map`), which a program builds for
// itself. A body the cache declined for any other reason keeps that reason.
func (s *stdInstances) stdUnretainedMonoBody(f *stdFunc) bool {
	if f == nil || f.why != "" || f.rtCall != "" || f.decl == nil || f.decl.Body == nil ||
		len(f.decl.Decorators) > 0 || len(f.decl.TypeParams) > 0 {
		return false
	}
	c := f
	if f.canon != nil {
		c = f.canon
	}
	return c.irBody == nil && s.callsGenericSibling(f)
}

// callsGenericSibling reports whether f's body names, as a bare or
// type-qualified callee, a generic template of its own module.
func (s *stdInstances) callsGenericSibling(f *stdFunc) bool {
	v := s.std.views[f.module]
	if v == nil {
		return false
	}
	found := false
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if found || isNilNode(n) {
			return
		}
		if c, isCall := n.(*ast.Call); isCall {
			switch callee := c.Func.(type) {
			case *ast.Ident:
				if stdModuleGenericTemplate(v, f.recv, callee.Name) != nil ||
					stdModuleGenericTemplate(v, "", callee.Name) != nil {
					found = true
					return
				}
			case *ast.FieldAccess:
				if ti, isType := callee.Object.(*ast.TypeIdent); isType && callee.Field != nil &&
					stdModuleGenericTemplate(v, ti.Name, callee.Field.Name) != nil {
					found = true
					return
				}
			}
		}
		for _, child := range childNodes(n) {
			walk(child)
		}
	}
	walk(f.decl.Body)
	return found
}

// stdGenericTemplate is the generic stdlib declaration `owner.method` names,
// or nil. A user unit reads the whole index; a std unit reads its own
// module's candidates, since a generic declaration never settles into its
// sibling scope.
func (g *gen) stdGenericTemplate(owner, method string) *stdFunc {
	if g.stdInsts == nil {
		return nil
	}
	if g.stdModule != "" {
		if f := stdModuleGenericTemplate(g.stdInsts.std.views[g.stdModule], owner, method); f != nil {
			return f
		}
	}
	if g.std == nil {
		return nil
	}
	fs := g.std.byType[owner+"."+method]
	if len(fs) != 1 || !(stdGenericTemplateUsable(fs[0]) || g.stdInsts.stdUnretainedMonoBody(fs[0])) {
		return nil
	}
	return fs[0]
}

// stdModuleTypeTemplate is stdGenericTemplate for a call that names the
// stdlib module (`random.Generator.bool()`): the one `owner.method` that
// module declares, when its body is instantiated per program.
func (g *gen) stdModuleTypeTemplate(module, owner, method string) *stdFunc {
	if g.stdInsts == nil || g.std == nil {
		return nil
	}
	var found *stdFunc
	for _, f := range g.std.byType[owner+"."+method] {
		if f.module != module {
			continue
		}
		if found != nil {
			return nil
		}
		found = f
	}
	if found == nil || !(stdGenericTemplateUsable(found) || g.stdInsts.stdUnretainedMonoBody(found)) {
		return nil
	}
	return found
}

// stdImplBlockOf is the impl block one stdlib declaration is a member of.
func stdImplBlockOf(nodes []ast.Node, fd *ast.FuncDef) *ast.ImplBlock {
	for _, n := range nodes {
		ib, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		for _, item := range ib.Items {
			if item == ast.Node(fd) {
				return ib
			}
		}
	}
	return nil
}

// stdTemplateParams is the type parameters a template's body may name: its
// own, then its impl block's receiver parameters, in order and deduplicated.
// It answers the impl block too, for a receiver-driven solve.
func (s *stdInstances) stdTemplateParams(f *stdFunc) ([]string, *ast.ImplBlock) {
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, tp := range f.decl.TypeParams {
		add(tp.Name)
	}
	v := s.std.views[f.module]
	if v == nil {
		return names, nil
	}
	ib := stdImplBlockOf(v.nodes, f.decl)
	if ib == nil {
		return names, nil
	}
	declared := stdAnchorsOf(v.fa).declared
	for _, n := range analysis.ReceiverTypeParamNames(ib.Receiver, declared) {
		add(n)
	}
	for _, gp := range ib.Generics {
		add(gp.Name)
	}
	return names, ib
}

// solve binds a template's type parameters by unifying its declared
// annotations against concrete kinds: each parameter's, the result's, and
// for a receiver-selected impl member the receiver's against self. An
// unsolved parameter or one outside the value domain answers false.
func (g *gen) stdInstSolve(f *stdFunc, tps []string, ib *ast.ImplBlock, params []kind, result, self kind) ([]kind, bool) {
	return g.stdInstSolveHoled(f, tps, ib, params, result, self, false)
}

// stdInstSolveHoled is stdInstSolve, admitting a Unit argument when unitOK:
// the reading of a type argument the checker left unsolved.
func (g *gen) stdInstSolveHoled(f *stdFunc, tps []string, ib *ast.ImplBlock, params []kind, result, self kind, unitOK bool) ([]kind, bool) {
	solved, ok := g.stdInstUnify(f, tps, ib, params, result, self)
	if !ok {
		return nil, false
	}
	out := make([]kind, len(tps))
	for i, n := range tps {
		k, ok := solved[n]
		if !ok || (!irCallableValueKind(k) && !(unitOK && k == kindUnit)) {
			return nil, false
		}
		out[i] = k
	}
	return out, true
}

// stdInstUnify is the type parameters of f each of params, result and self
// solves, by name; a parameter nothing solves is absent.
func (g *gen) stdInstUnify(f *stdFunc, tps []string, ib *ast.ImplBlock, params []kind, result, self kind) (map[string]kind, bool) {
	set := make(map[string]bool, len(tps))
	for _, n := range tps {
		set[n] = true
	}
	solved := map[string]kind{}
	// kindInvalid: sentinel — no receiver kind selects this member.
	if self != kindInvalid && ib != nil {
		g.unifyTypeParams(ib.Receiver, self, set, solved)
	}
	if params != nil {
		if len(params) != len(f.decl.Params) {
			return nil, false
		}
		for i, p := range f.decl.Params {
			// kindInvalid: lookup — a parameter the checker gave no kind binds nothing; an unsolved type parameter declines below.
			if p.TypeAnnotation != nil && params[i] != kindInvalid {
				g.unifyTypeParams(p.TypeAnnotation, params[i], set, solved)
			}
		}
	}
	// kindInvalid: lookup — as above, for the result.
	if result != kindInvalid && f.decl.ReturnTypeExpr != nil {
		g.unifyTypeParams(f.decl.ReturnTypeExpr, result, set, solved)
	}
	return solved, true
}

// --- call sites --------------------------------------------------------------

// stdInstCallAt instantiates template f for the call t, solving its type
// parameters from the checker's instantiated signature at t (and from self,
// for a receiver-selected impl member), and lowers the call. handled is false
// when f is nil.
func (bl *irScalarBuilder) stdInstCallAt(t *ast.Call, f *stdFunc, self kind) (ir.Temp, kind, bool, bool, bool) {
	if f == nil || bl.g.stdInsts == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	no := func(why string) (ir.Temp, kind, bool, bool, bool) {
		if why != "" {
			irDeclineNote(why)
		}
		return ir.NoTemp, kindInvalid, false, false, true
	}
	s := bl.g.stdInsts
	tps, ib := s.stdTemplateParams(f)
	var params []kind
	result := kindInvalid
	if ft := bl.g.checkedCallSignature(t); ft != nil {
		params = make([]kind, len(ft.Params))
		for i, p := range ft.Params {
			params[i] = bl.g.project(p)
		}
		result = bl.g.project(ft.Return)
	}
	var args []kind
	var holes map[string]bool
	if stdGenericTemplateUsable(f) {
		if len(tps) == 0 {
			return no("a generic stdlib call with no type parameters to solve: " + f.key)
		}
		var ok bool
		// kindInvalid: sentinel — no receiver kind selects this member.
		if cur := bl.g.stdInstCur; (params == nil || irKindsUnprojected(params)) && self == kindInvalid && cur != nil && cur.fd == f.decl {
			// A self-call the checker recorded no instantiated signature
			// for (a tail call inside the template's own body), or recorded
			// one over the template's own type parameters, which do not
			// project outside the instance's frame: the same type
			// parameters, so the instance's own arguments.
			args, ok, holes = cur.args, true, cur.holes
		} else {
			args, ok = bl.g.stdInstSolve(f, tps, ib, params, result, self)
		}
		if ft := bl.g.checkedCallSignature(t); !ok && ft != nil {
			// A type argument nothing in the program constrains
			// (`Result.map(Result.Ok(3), f)` leaves E open) is read as Unit,
			// as checkedPreludeArgsUnitHoles reads a constructor's: no value
			// of that type is built or observed.
			holed := make([]kind, len(ft.Params))
			for i, p := range ft.Params {
				holed[i] = bl.g.project(unitHoles(p))
			}
			args, ok = bl.g.stdInstSolveHoled(f, tps, ib, holed, bl.g.project(unitHoles(ft.Return)), self, true)
		}
		if ft := bl.g.checkedCallSignature(t); !ok && ft != nil && irOnlyEmptyListsUnsolved(t, ft) {
			// `List.next_item([])`: the open type argument is an element of
			// an empty list and nothing else, so no value of it is built or
			// observed. Read as Int, the representation listCallPlan gives
			// `List.head([])`, where Unit has no retained list.
			holed := make([]kind, len(ft.Params))
			for i, p := range ft.Params {
				holed[i] = bl.g.project(irFillHoles(p, analysis.TypeInt))
			}
			args, ok = bl.g.stdInstSolveHoled(f, tps, ib, holed, bl.g.project(irFillHoles(ft.Return, analysis.TypeInt)), self, true)
		}
		if !ok {
			return no("a generic stdlib call whose type arguments are unsolved or outside the domain: " + f.key)
		}
		if ft := bl.g.checkedCallSignature(t); ft != nil && holes == nil {
			holes = irCheckerHoles(f.decl, tps, args, ft)
		}
	} else {
		// A monomorphic body the cache could not retain because it reaches
		// a generic sibling (`Generator.bool` calls `map`): built per program
		// like an instance with no arguments.
		tps = nil
	}
	inst, why := s.instantiate(f, tps, args, holes, bl.g)
	if inst == nil {
		return no(why)
	}
	v, k, mobile, ok := bl.stdInstCall(t, inst)
	return v, k, mobile, ok, true
}

// irKindsUnprojected reports a projected parameter list with a position that
// did not project (kindInvalid).
func irKindsUnprojected(ks []kind) bool {
	for _, k := range ks {
		// kindInvalid: sentinel — the projection's failure.
		if k == kindInvalid {
			return true
		}
	}
	return false
}

// stdInstCall lowers the call t to a committed or pending instance, with its
// arguments typed by the instance's parameters as a direct call's are.
func (bl *irScalarBuilder) stdInstCall(t *ast.Call, inst *stdInst) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	params := inst.view.params
	if len(t.Args) != len(params) {
		return no()
	}
	temps := make([]ir.Temp, len(params))
	if bl.stdInstPreCall == t {
		// Operands already lowered, in order and forced, by irQualLowerArgs.
		pre := bl.stdInstPreArgs
		if !pre.ok || len(pre.temps) != len(params) {
			return no()
		}
		for i, a := range t.Args {
			src, k, ok := bl.coerceEmpty(a, pre.temps[i], pre.kinds[i], params[i])
			if !ok || k != params[i] {
				return no()
			}
			temps[i] = src
		}
		c := ir.NewCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), inst.sym, temps...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: inst.view.result, deferrable: true})
		return c.Dst(), inst.view.result, false, true
	}
	for i, a := range t.Args {
		if _, named := a.(*ast.NamedArg); named {
			return no()
		}
		src, k, _, ok := bl.lowerTypedOperand(a, params[i])
		if !ok {
			return no()
		}
		src, k, ok = bl.coerceEmpty(a, src, k, params[i])
		if !ok || k != params[i] {
			return no()
		}
		if i == len(t.Args)-1 && bl.recording > 0 && bl.qualRecord == t && !irHeldValue(bl.f, src, bl.sides) {
			// Named twice, in its row and in the call: held once, as
			// irQualLowerArgs holds a qualified call's final operand.
			cp := ir.NewCopy(bl.g.irNodePos(a), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyHold})
			src = cp.Dst()
		}
		temps[i] = src
	}
	if bl.qualRecord == t {
		// Inside an assertion subject, recordedQualCall records these
		// operands' rows after the call.
		kinds := append([]kind(nil), params...)
		mobile := make([]bool, len(temps))
		bl.qualRecordArgs, bl.qualRecordSeen = irQualArgs{temps: temps, kinds: kinds, mobile: mobile, ok: true}, true
	}
	c := ir.NewCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), inst.sym, temps...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: inst.view.result, deferrable: true})
	return c.Dst(), inst.view.result, false, true
}

// stdGenericQualCall lowers `Owner.method(args)` where Owner.method is a
// generic stdlib declaration.
func (bl *irScalarBuilder) stdGenericQualCall(t *ast.Call, owner, method string) (ir.Temp, kind, bool, bool, bool) {
	if _, local := bl.g.types[owner]; local && bl.g.stdModule == "" {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if irOwnOperationFamily(owner, method) {
		// The builder lowers these families' operations itself.
		return ir.NoTemp, kindInvalid, false, false, false
	}
	return bl.stdInstCallAt(t, bl.g.stdGenericTemplate(owner, method), kindInvalid)
}

// stdGenericSiblingCall lowers a bare call inside a stdlib body to a generic
// sibling: `map(...)` inside `impl Generator<T>`, `decode_list(...)`. A bare
// call to a `host fn` of the receiver's inherent block (`concat(lhs, rhs)`
// inside `impl Add<Vector<T>, Vector<T>> for Vector<T>`) is the owner-qualified
// call it names, `Vector.concat(lhs, rhs)`, and lowers as that call does.
func (bl *irScalarBuilder) stdGenericSiblingCall(t *ast.Call, name string) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if g.stdModule == "" || g.stdInsts == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	v := g.stdInsts.std.views[g.stdModule]
	var f *stdFunc
	if g.implSelf != "" {
		f = stdModuleGenericTemplate(v, g.implSelf, name)
	}
	if f == nil {
		f = stdModuleGenericTemplate(v, "", name)
	}
	if callee, bare := t.Func.(*ast.Ident); f == nil && bare && g.implSelf != "" && stdModuleInherentHost(v, g.implSelf, name) {
		// The field keeps the bare name's position, where the checker
		// recorded the call's signature.
		call := *t
		call.Func = &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: g.implSelf, Line: callee.Line, Col: callee.Col},
			Field:  &ast.Ident{Name: name, Line: callee.Line, Col: callee.Col},
			Line:   callee.Line,
			Col:    callee.Col,
		}
		v, k, mobile, ok := bl.lower(&call)
		return v, k, mobile, ok, true
	}
	return bl.stdInstCallAt(t, f, kindInvalid)
}

// stdModuleInherentHost reports whether the module declares exactly one
// `host fn` named name in recv's inherent block (`impl Vector<T> { host fn
// concat... }`).
func stdModuleInherentHost(v *stdModuleView, recv, name string) bool {
	if v == nil {
		return false
	}
	n := 0
	for _, c := range v.cands {
		if c.f != nil && c.fd == nil && c.f.recv == recv && c.f.iface == "" && c.f.name == name {
			n++
		}
	}
	return n == 1
}

// stdKindQualCall lowers `method` on the concrete kind a type parameter or a
// turbofish names: a scalar or local declared type's impl as the
// type-qualified call it spells, and a generic std container's impl
// (`List<Int>`, `Maybe<String>`, `Map<String, Int>`) as an instance selected
// by the receiver. handled is false when k names neither.
func (bl *irScalarBuilder) stdKindQualCall(t *ast.Call, k kind, method string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil {
		return no()
	}
	if owner := irKindOwnerName(bl.g, k); owner != "" {
		call := *t
		call.TypeArgs = nil
		call.Func = &ast.FieldAccess{Object: &ast.TypeIdent{Name: owner, Line: fa.Line, Col: fa.Col},
			Field: fa.Field, Line: fa.Line, Col: fa.Col}
		v, rk, mobile, ok := bl.lower(&call)
		return v, rk, mobile, ok, true
	}
	if irKindDeclared(k) {
		v, rk, mobile, ok := bl.kindImplCall(t, k, method)
		return v, rk, mobile, ok, true
	}
	base := irContainerBaseName(k)
	if base == "" {
		return no()
	}
	f := bl.g.stdGenericTemplate(base, method)
	if f == nil {
		return no()
	}
	return bl.stdInstCallAt(t, f, k)
}

// irKindOwnerName is the owner a type-qualified call spells for k in this
// unit: a scalar's name, or a declared type this unit names by the same
// declaration. Anything else answers "".
func irKindOwnerName(g *gen, k kind) string {
	switch k {
	case kindInt, kindFloat, kindString, kindBool:
		return k.nomi()
	}
	if k.tag == tagNamed && k.def != nil && k.def.genericOf != nil {
		// An instance of this unit's own generic template is spelled by the
		// template's name; qualImplPlan selects the instance by its receiver.
		if g.genericTemplates[k.def.nomi] == k.def.genericOf {
			return k.def.nomi
		}
		return ""
	}
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf != nil || k.def.genStructOf != nil ||
		k.def.genHostOf != nil {
		return ""
	}
	if d, found := g.namedType(k.def.nomi); found && d == k.def {
		return k.def.nomi
	}
	return ""
}

// irKindDeclared reports a kind a program file declares: a struct, enum or
// generic instance with its own declaration, which no std container base
// names.
func irKindDeclared(k kind) bool {
	return k.tag == tagNamed && k.def != nil && k.def.decl != nil && k.def.preludeOf == nil &&
		k.def.genStructOf == nil && k.def.genHostOf == nil
}

// kindImplCall lowers `T.method(args)` where T is bound to a declared type
// whose name this unit does not bind: a std instance's body calling back into
// the program (`T.from_json(item)` in `List.from_json<Note>`), or a user
// generic instantiated at another file's type. The impl is found by the
// kind's declaration, as a receiver operand would find it, so no operand
// need be of the type.
func (bl *irScalarBuilder) kindImplCall(t *ast.Call, k kind, method string) (ir.Temp, kind, bool, bool) {
	if bl.qualBare == nil {
		bl.qualBare = bl.checkedBareOperands(t)
	}
	args := bl.irQualLowerArgs(t)
	var plan *irQualPlan
	if s := bl.g.stdInsts; bl.g.stdModule != "" && s != nil && s.caller != nil {
		if iface, ok := callerKindIface(s.caller, k, method); ok {
			plan = bl.callerImplPlan(t, args, iface, method, k)
		}
	} else {
		plan = bl.qualSiblingImplPlanFor(t, args, k.def, method)
	}
	if plan == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.qualEmit(t, args, plan)
}

// callerKindIface is the one interface whose impl for k, among the program's
// units, declares method: the interface a bounded `T.method` names. Two
// interfaces that both do answer false, as the type-qualified call
// `Note.method` is ambiguous.
func callerKindIface(caller *gen, k kind, method string) (string, bool) {
	gens := []*gen{caller}
	if caller.reg != nil {
		gens = append(gens, caller.reg.gens...)
	}
	found := ""
	for _, g := range gens {
		if g == nil {
			continue
		}
		for iface, impls := range g.implsByIface {
			for recv, d := range impls {
				if recv != k && (recv.def == nil || recv.def.decl != k.def.decl) {
					continue
				}
				if d.items[method] == nil {
					continue
				}
				if found != "" && found != iface {
					return "", false
				}
				found = iface
			}
		}
	}
	return found, found != ""
}

// irContainerBaseName is the std receiver name a generic container kind's
// impl blocks are written for.
func irContainerBaseName(k kind) string {
	switch {
	case k.tag == tagList:
		return "List"
	case k.tag == tagMap:
		return "Map"
	case k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil:
		return k.def.preludeOf.spec.nomi
	}
	if _, ok := vectorElem(k); ok {
		return "Vector"
	}
	if _, ok := setElem(k); ok {
		return "Set"
	}
	if _, ok := rangeElem(k); ok {
		return "Range"
	}
	return ""
}

// irOwnOperationFamily reports an owner whose qualified calls the builder
// routes through a family arm of its own (iterCall, rangeCall, the container
// plans, the prelude special cases), which a generic instance must not
// preempt.
func irOwnOperationFamily(owner, method string) bool {
	switch owner {
	case "Iter", "Range", "Map", "List", "Vector", "Set", "Channel", "Sender", "Receiver",
		"Task", "Supervisor", "Struct":
		return true
	case "Result":
		return method == "map_err" || method == "with_default"
	case "Maybe":
		return method == "to_result" || method == "with_default"
	}
	return false
}

// irOnlyEmptyListsUnsolved reports whether every operand of t whose checked
// parameter the checker left open is an empty list literal, and one is.
func irOnlyEmptyListsUnsolved(t *ast.Call, ft *analysis.FuncType) bool {
	if len(ft.Params) != len(t.Args) {
		return false
	}
	open := false
	for i, p := range ft.Params {
		if !irUnsolvedType(p) {
			continue
		}
		lit, isList := t.Args[i].(*ast.ListLit)
		if !isList || len(lit.Items) != 0 || lit.TypeName != nil {
			return false
		}
		open = true
	}
	return open
}

// irFillHoles is ty with every inference variable the checker left open
// replaced by fill, at any depth.
func irFillHoles(ty analysis.Type, fill analysis.Type) analysis.Type {
	switch t := ty.(type) {
	case *analysis.TypeVar:
		if t.Resolved == nil {
			return fill
		}
		return irFillHoles(t.Resolved, fill)
	case *analysis.ListType:
		return &analysis.ListType{Elem: irFillHoles(t.Elem, fill)}
	case *analysis.MapType:
		return &analysis.MapType{Key: irFillHoles(t.Key, fill), Val: irFillHoles(t.Val, fill)}
	case *analysis.TupleType:
		elems := make([]analysis.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = irFillHoles(e, fill)
		}
		return &analysis.TupleType{Elems: elems}
	case *analysis.EnumType:
		c := *t
		c.TypeArgs = make([]analysis.Type, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			c.TypeArgs[i] = irFillHoles(a, fill)
		}
		return &c
	case *analysis.StructType:
		c := *t
		c.TypeArgs = make([]analysis.Type, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			c.TypeArgs[i] = irFillHoles(a, fill)
		}
		return &c
	}
	return ty
}
