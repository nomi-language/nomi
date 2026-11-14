package irbuild

import (
	"fmt"
	"sort"
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
	// program is the program's own units, which a std instance body at a
	// program's interface reads the implementers from.
	program []*gen
	// caller is the gen whose call asks for an instance. An instance gen
	// interns its structural kinds, prelude, std generic struct and std
	// generic host instances in the caller's tables, so `Maybe<Point>` or
	// `Vector<Point>` over the caller's Point is one kind on both sides of the
	// call.
	caller *gen
	// dc is the census of the lowering the table belongs to.
	dc *declineCensus
}

// newStdInstances is a lowering's instance table, whose instance gens note
// their declines in dc, the lowering's census.
func newStdInstances(std *stdlibIndex, dc *declineCensus) *stdInstances {
	return &stdInstances{
		std:     std,
		dc:      dc,
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

// needs is, for each of modules' instances in its order, the listed std
// modules (programStdModules) that declare a type in its arguments: what a
// program must reach for the instance to name only what it links
// (programstdlink.go's restrict).
func (s *stdInstances) needs() [][]string {
	if s == nil {
		return nil
	}
	var out [][]string
	for _, key := range s.order {
		inst := s.byKey[key]
		if inst.gen.irMod == nil {
			continue
		}
		out = append(out, listedModulesNamed(portableArgsKey(inst.args)))
	}
	return out
}

// listedModulesNamed is each listed std module a portable spelling
// (writePortableKind) names a declaration of, sorted: a std declaration is
// spelled by its linkage path, `@std/<module>.<path>`.
func listedModulesNamed(spelling string) []string {
	var out []string
	for name := range programStdModules {
		if strings.Contains(spelling, "@std/"+name+".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// stdInstKey is the declaration pointer plus each argument's identity.
func stdInstKey(fd *ast.FuncDef, args []kind) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%p", fd)
	for _, a := range args {
		b.WriteString("\x00")
		b.WriteString(a.key())
	}
	return b.String()
}

// instantiate answers the instance of f at args, building its closure on a
// cold table. A nested call reached while a closure is attempted joins it.
func (s *stdInstances) instantiate(f *stdFunc, tps []string, args []kind, caller *gen) (*stdInst, string) {
	key := stdInstKey(f.decl, args)
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
		return s.queue(f, tps, args, key)
	}
	return s.attemptClosure(f, tps, args, key)
}

func (s *stdInstances) attemptClosure(f *stdFunc, tps []string, args []kind, key string) (*stdInst, string) {
	s.attempting = true
	// The closure is built in the middle of the caller's own attempt, whose
	// decline record is set aside and restored, and neither the decline
	// hooks nor the test observers see instance bodies.
	resume := s.dc.suspend()
	unmute := irMuteObservers()
	defer func() {
		unmute()
		resume()
		s.attempting = false
		s.pending = map[string]*stdInst{}
		s.pendingOrder = nil
		s.building = nil
	}()
	top, why := s.queue(f, tps, args, key)
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
func (s *stdInstances) queue(f *stdFunc, tps []string, args []kind, key string) (*stdInst, string) {
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
	stdBodies(f.module)
	offer := make(map[*stdCandidate]bool, len(v.settled)+len(v.withCycles))
	for c := range v.settled {
		offer[c] = true
	}
	for c := range v.withCycles {
		offer[c] = true
	}
	g := newStdGen(v.module, v.path, v.pkg, v.nodes, v.fa, v.cands, offer, v.onces, s.std)
	g.stdInsts, g.dc = s, s.dc
	if c := s.caller; c != nil {
		g.progLinks = c.programLinks()
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
		switch {
		case c.stdProgCaller != nil:
			g.stdProgCaller = c.stdProgCaller
		case c.stdProgBuilds():
			g.stdProgCaller = c
		}
	}
	frame := make(map[string]kind, len(tps))
	for i, tp := range tps {
		frame[tp] = args[i]
	}
	g.genericSubst = append(g.genericSubst, frame)
	if f.why == stdIfaceDefaultWhy && (len(args) != 1 || !g.bindStdIfaceDefaultSelf(f, args[0])) {
		// A default's every `self` is the receiver it is instantiated at.
		return nil, "a stdlib interface default whose interface its module does not declare: " + f.key
	}
	inst := &stdInst{f: f, fd: f.decl, tps: tps, args: args, gen: g, depth: depth}
	g.stdInstCur = inst
	view := *f
	view.canon = nil
	view.irBody = nil
	view.irArity = nil
	view.irSlot = nil
	view.why = ""
	if len(tps) == 0 && f.why != stdProgSigWhy {
		// A monomorphic body keeps the signature the index resolved for it,
		// destructuring parameters included.
		view.params = append([]kind(nil), f.params...)
	} else {
		// A generic signature names the instance's arguments, and an Iter
		// or a std interface held as a value has no kind outside a program
		// (stdProgSigWhy).
		view.params = make([]kind, len(inst.fd.Params))
		for i, p := range inst.fd.Params {
			if p.TypeAnnotation == nil {
				return nil, "a generic stdlib parameter with no annotation: " + f.key
			}
			view.params[i] = g.typeOf(p.TypeAnnotation)
		}
	}
	for _, k := range view.params {
		if !irCallOperandKind(k) {
			return nil, "a generic stdlib instance's parameter outside the domain: " + f.key + " at " + kindsNomi(args)
		}
	}
	view.result = f.result
	if len(tps) != 0 || f.why == stdProgSigWhy {
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
		if len(inst.tps) == 0 && inst.f.ifaceDefault == nil && irDefaultsNotAllLiteral(inst.fd) {
			// A monomorphic body built per program whose defaults are not
			// all literals: its arity wrappers and default accessors, built
			// here in its own module's scope, where those defaults resolve
			// (stdUnretainedDefaultedCall).
			inst.view.irBody = g.irMod.FuncFor(inst.sym)
			g.emitStdDefaultWrappers(inst.view, inst.fd)
			g.emitStdSlotDefaults(inst.view, inst.fd)
		}
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
// its own module (`Generator.bool` calls `map`) or a generic template of
// another std module (Regex's Debug calls `String.contains`), which a
// program builds for itself, or calls a monomorphic std function that is
// not retained for that reason (io's `Replayed` failure reaches
// `guttered?`, which calls `String.starts_with`), or calls a function of a
// std module lowered after its own (otherModuleCallee: Regex's Debug calls
// `String.to_typed_literal`). A body the cache declined for any other
// reason keeps that reason.
func (s *stdInstances) stdUnretainedMonoBody(f *stdFunc) bool {
	return s.unretainedMono(f, map[*stdFunc]bool{})
}

// unretainedMono is stdUnretainedMonoBody with the functions whose answer is
// being worked out, which answer false so a recursive pair terminates.
func (s *stdInstances) unretainedMono(f *stdFunc, visiting map[*stdFunc]bool) bool {
	if f == nil || (f.why != "" && f.why != stdProgSigWhy) || f.rtCall != "" || f.decl == nil || f.decl.Body == nil ||
		len(f.decl.Decorators) > 0 || len(f.decl.TypeParams) > 0 {
		return false
	}
	c := f
	if f.canon != nil {
		c = f.canon
	}
	if c.irBody != nil || visiting[f] {
		return false
	}
	if f.why == stdProgSigWhy {
		return true
	}
	visiting[f] = true
	defer delete(visiting, f)
	return s.callsGenericSibling(f, visiting)
}

// stdProgSigWhy is the why of a monomorphic std function a parameter or
// the result of which has a kind only inside a program (stdProgSigType):
// an `Iter<T>` (`Reader.lines`, `Writer.write_all`), or a std interface
// held as a value with no rt table (`String.split(s, separator:
// Matcher)`, stdprogiface.go). Neither has a shared lowering, so the cache
// retains no such body and a program builds it as an instance with no
// arguments (stdUnretainedMonoBody), where the Iter is the builder's own
// sequence and the interface is the program's def of it.
const stdProgSigWhy = "stdlib function whose signature has a kind only in a program"

// stdProgSigType reports a parameter or result type of a Nomi-bodied std
// function that has a kind only inside a program (stdProgSigWhy).
func stdProgSigType(t ast.TypeExpr, anchors stdAnchors) bool {
	return isIterType(t) || stdIfaceValueType(t, anchors)
}

// isIterType reports a type written `Iter<T>`.
func isIterType(t ast.TypeExpr) bool {
	g, ok := t.(*ast.GenericType)
	return ok && g.Name == "Iter"
}

// stdIfaceValueType reports a type written as the bare name of a std
// interface a program holds as a value through its own def
// (stdProgIfaceOf): not generic, not one with an rt table, std's `Struct`
// or `Iter`, nor one of a std module lowered with the program, whose
// interfaces are the program's own.
func stdIfaceValueType(t ast.TypeExpr, anchors stdAnchors) bool {
	st, ok := t.(*ast.SimpleType)
	if !ok || anchors.fa == nil {
		return false
	}
	sym := resolvedTypeSymbol(anchors.fa, st.Name)
	if sym == nil {
		return false
	}
	decl, isDecl := sym.Node.(*ast.InterfaceDef)
	it, isIface := sym.Type.(*analysis.InterfaceType)
	if !isDecl || !isIface || !strings.HasPrefix(it.Origin, "std/") || isProgramStd(strings.TrimPrefix(it.Origin, "std/")) {
		return false
	}
	if decl.Name == structIfaceName || decl.Name == "Iter" || len(decl.TypeParams) > 0 || len(decl.WhereClauses) > 0 {
		return false
	}
	for _, s := range stdIfaceSpecs {
		if s.nomi == decl.Name && s.module == it.Origin {
			return false
		}
	}
	return true
}

// callsGenericSibling reports whether f's body names, as a bare or
// type-qualified callee, a generic template of its own module, or as a
// type-qualified callee one of another std module, or names either way a
// monomorphic std function a program builds for itself (unretainedMono).
func (s *stdInstances) callsGenericSibling(f *stdFunc, visiting map[*stdFunc]bool) bool {
	v := s.std.views[f.module]
	if v == nil {
		return false
	}
	stdBodies(f.module)
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
					stdModuleGenericTemplate(v, "", callee.Name) != nil ||
					s.unretainedMono(stdModuleMonoFunc(v, f.recv, callee.Name), visiting) ||
					s.unretainedMono(stdModuleMonoFunc(v, "", callee.Name), visiting) {
					found = true
					return
				}
				if module, name, isStd := stdBareFileFunc(v.fa, callee); isStd &&
					s.otherModuleCallee(f.module, s.std.byFile[module+"."+name], visiting) {
					found = true
					return
				}
			case *ast.FieldAccess:
				if ti, isType := callee.Object.(*ast.TypeIdent); isType && callee.Field != nil &&
					(stdModuleGenericTemplate(v, ti.Name, callee.Field.Name) != nil ||
						s.unretainedMono(stdModuleMonoFunc(v, ti.Name, callee.Field.Name), visiting) ||
						s.otherModuleTemplate(f.module, ti.Name, callee.Field.Name, visiting) ||
						s.otherModuleOverload(f.module, s.std.byType[ti.Name+"."+callee.Field.Name], visiting) ||
						s.namesStdIfaceDefault(v.fa, callee)) {
					found = true
					return
				}
				if id, isIdent := callee.Object.(*ast.Ident); isIdent && callee.Field != nil {
					if module, isStd := stdFileQualifier(v.fa, id); isStd &&
						s.otherModuleCallee(f.module, s.std.byFile[module+"."+callee.Field.Name], visiting) {
						found = true
						return
					}
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

// otherModuleCallee reports whether callee, a function a body of std
// module module calls, is declared in another std module and is one a
// program builds the body for: a monomorphic function a program builds for
// itself (unretainedMono), or any function a program can call that is
// declared in a module lowered after module. The shared lowering lowers
// modules in dependency order, and a module's bodies resolve only against
// the modules before it (buildStdlibIndexBy), so a body that calls into a
// later module of its import cycle (std/regex's Debug calling
// `String.to_typed_literal`, where std/strings imports std/regex) is not
// retained there. A program builds it, against every module.
func (s *stdInstances) otherModuleCallee(module string, callee *stdFunc, visiting map[*stdFunc]bool) bool {
	if callee == nil || callee.module == module {
		return false
	}
	if s.unretainedMono(callee, visiting) {
		return true
	}
	if s.std.earlierMods[module][callee.module] || callee.why != "" {
		return false
	}
	c := callee
	if callee.canon != nil {
		c = callee.canon
	}
	return callee.rtCall != "" || c.irBody != nil
}

// otherModuleOverload is otherModuleCallee for a type-qualified spelling,
// which may name an overload set: whether any member is such a callee.
func (s *stdInstances) otherModuleOverload(module string, fs []*stdFunc, visiting map[*stdFunc]bool) bool {
	for _, f := range fs {
		if s.otherModuleCallee(module, f, visiting) {
			return true
		}
	}
	return false
}

// stdModuleMonoFunc is the one monomorphic Nomi-bodied function the module
// declares under `recv.name` ("" for a free function), or nil.
func stdModuleMonoFunc(v *stdModuleView, recv, name string) *stdFunc {
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
	if found == nil || found.decl == nil || len(found.decl.TypeParams) > 0 {
		return nil
	}
	return found
}

// otherModuleTemplate reports whether `owner.method` names one generic
// template another std module declares (`String.contains` named in
// std/regex), or one monomorphic function of it that a program builds for
// itself (unretainedMono), as a program builds a sibling's.
func (s *stdInstances) otherModuleTemplate(module, owner, method string, visiting map[*stdFunc]bool) bool {
	fs := s.std.byType[owner+"."+method]
	return len(fs) == 1 && fs[0].module != module &&
		(stdGenericTemplateUsable(fs[0]) || s.unretainedMono(fs[0], visiting))
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

// stdRecvTypeArgs is the arguments of tps, the type parameters of a member
// of impl block ib, at the receiver kind self that selected the block
// (`impl Display for List<T>` at `List<Int>`): each is read off self where
// ib's receiver names it. A parameter the receiver does not name, or an
// argument outside the value domain (Unit admitted when unitOK), answers
// false. This reads which instance of the block a dispatch reaches; a
// call's own type arguments come from the checker (calleeTypeArgs).
func (g *gen) stdRecvTypeArgs(tps []string, ib *ast.ImplBlock, self kind, unitOK bool) ([]kind, bool) {
	if ib == nil {
		return nil, false
	}
	set := make(map[string]bool, len(tps))
	for _, n := range tps {
		set[n] = true
	}
	solved := map[string]kind{}
	g.unifyTypeParams(ib.Receiver, self, set, solved)
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

// --- call sites --------------------------------------------------------------

// stdInstCallAt instantiates template f for the call t at the type
// arguments the checker instantiated t at (stdInstTypeArgs), and lowers the
// call. handled is false when f is nil.
func (bl *irScalarBuilder) stdInstCallAt(t *ast.Call, f *stdFunc, self kind) (ir.Temp, kind, bool, bool, bool) {
	if f == nil || bl.g.stdInsts == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	no := func(why string) (ir.Temp, kind, bool, bool, bool) {
		if why != "" {
			bl.g.dc.note(why)
		}
		return ir.NoTemp, kindInvalid, false, false, true
	}
	s := bl.g.stdInsts
	tps, ib := s.stdTemplateParams(f)
	var args []kind
	if stdGenericTemplateUsable(f) {
		if len(tps) == 0 {
			return no("a generic stdlib call with no type parameters to solve: " + f.key)
		}
		var ok bool
		args, ok = bl.stdInstTypeArgs(t, f, tps, ib, self)
		if !ok {
			return no("a generic stdlib call whose type arguments are unrecorded or outside the domain: " + f.key)
		}
	} else {
		// A monomorphic body the cache could not retain because it reaches
		// a generic sibling (`Generator.bool` calls `map`): built per program
		// like an instance with no arguments.
		tps = nil
	}
	inst, why := s.instantiate(f, tps, args, bl.g)
	if inst == nil {
		return no(why)
	}
	v, k, mobile, ok := bl.stdInstCall(t, inst)
	return v, k, mobile, ok, true
}

// stdInstTypeArgs is the arguments of tps, template f's type parameters, at
// the call t: the checker's (calleeTypeArgs). For a member of an impl
// block the receiver kind self selected, the call's record is the interface
// method's, so the block's receiver parameters are read off self
// (stdRecvTypeArgs). A type argument inference left open is the type the
// checker closed it as, which may be Unit (`Result.map(Result.Ok(3), f)`'s
// E).
func (bl *irScalarBuilder) stdInstTypeArgs(t *ast.Call, f *stdFunc, tps []string, ib *ast.ImplBlock, self kind) ([]kind, bool) {
	g := bl.g
	recv := map[string]kind{}
	// kindInvalid: sentinel — no receiver kind selects this member.
	if self != kindInvalid && ib != nil {
		// The parameters the block's receiver names, which a derived
		// member's own declaration may list too.
		set := make(map[string]bool, len(tps))
		for _, n := range tps {
			set[n] = true
		}
		mentioned := map[string]bool{}
		markMentionedParams(ib.Receiver, set, mentioned)
		var named []string
		for _, n := range tps {
			if mentioned[n] {
				named = append(named, n)
			}
		}
		ks, ok := g.stdRecvTypeArgs(named, ib, self, true)
		if !ok {
			return nil, false
		}
		for i, n := range named {
			recv[n] = ks[i]
		}
	}
	var rest []string
	for _, n := range tps {
		if _, ok := recv[n]; !ok {
			rest = append(rest, n)
		}
	}
	called, ok := g.calleeTypeArgs(t.Func, rest)
	if !ok {
		return nil, false
	}
	out := make([]kind, len(tps))
	for i, n := range tps {
		if k, ok := recv[n]; ok {
			out[i] = k
			continue
		}
		out[i], called = called[0], called[1:]
	}
	return out, true
}

// irClosedDefaults is args, the positional operands of a call to fd, with
// each omitted trailing parameter's default after them, or args unchanged
// when an omitted one has no default that is a literal.
func irClosedDefaults(fd *ast.FuncDef, args []ast.Node) []ast.Node {
	if fd == nil || len(args) >= len(fd.Params) {
		return args
	}
	for _, a := range args {
		if _, named := a.(*ast.NamedArg); named {
			return args
		}
	}
	out := append([]ast.Node(nil), args...)
	for _, p := range fd.Params[len(args):] {
		if p.Default == nil || !irClosedLiteral(p.Default) {
			return args
		}
		out = append(out, p.Default)
	}
	return out
}

// irDefaultsNotAllLiteral reports a parameter of fd whose default is not a
// literal (irClosedLiteral), which a call that leaves it out cannot fill in
// the caller's file.
func irDefaultsNotAllLiteral(fd *ast.FuncDef) bool {
	for _, p := range fd.Params {
		if p.Default != nil && !irClosedLiteral(p.Default) {
			return true
		}
	}
	return false
}

// stdInstCall lowers the call t to a committed or pending instance, with its
// arguments typed by the instance's parameters as a direct call's are.
func (bl *irScalarBuilder) stdInstCall(t *ast.Call, inst *stdInst) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	params := make([]kind, len(inst.view.params))
	for i, p := range inst.view.params {
		// One instance serves the program, built at the first caller's
		// defs; this file passes a std interface value at its own.
		params[i] = bl.g.stdProgHere(p)
	}
	args := t.Args
	if len(args) < len(params) && bl.stdInstPreCall != t {
		// `Reader.chunks(r)`: each omitted trailing default a literal
		// (irClosedLiteral), which lowers the same in any file.
		args = irClosedDefaults(inst.fd, args)
	}
	if len(args) != len(params) {
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
			src, _, ok := bl.coerceEmpty(a, pre.temps[i], pre.kinds[i], params[i])
			if !ok {
				return no()
			}
			temps[i] = src
		}
		c := ir.NewCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), inst.sym, temps...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: inst.view.result, deferrable: true})
		return c.Dst(), inst.view.result, false, true
	}
	for i, a := range args {
		if _, named := a.(*ast.NamedArg); named {
			return no()
		}
		src, k, _, ok := bl.lowerTypedOperand(a, params[i])
		if !ok {
			return no()
		}
		src, k, ok = bl.coerceEmpty(a, src, k, params[i])
		if !ok {
			return no()
		}
		if i == len(args)-1 && bl.recording > 0 && bl.qualRecord == t && !irHeldValue(bl.f, src, bl.sides) {
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
		// Inside an assertion subject, recordedQualCall records the
		// written operands' rows after the call; a filled default has none.
		n := len(t.Args)
		kinds := append([]kind(nil), params[:n]...)
		mobile := make([]bool, n)
		bl.qualRecordArgs, bl.qualRecordSeen = irQualArgs{temps: temps[:n], kinds: kinds, mobile: mobile, ok: true}, true
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

// stdGenericFileCall lowers `module.fn(args)` where fn is a generic top-level
// function of a stdlib file (`io.capture(input, run)`), instantiated at the
// call's types like a generic std member. handled is false when the
// qualifier is not a stdlib file or the file declares no such template.
func (bl *irScalarBuilder) stdGenericFileCall(t *ast.Call, ownerID *ast.Ident, method string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	if bl.g.stdInsts == nil {
		return no()
	}
	if bl.g.files != nil {
		if _, isSibling := bl.g.files.lookupQualifier(bl.g.fa, ownerID); isSibling {
			return no()
		}
	}
	module, isStd := stdFileQualifier(bl.g.fa, ownerID)
	if !isStd {
		module, isStd = bl.stdOwnFileQualifier(ownerID.Name)
	}
	if !isStd {
		return no()
	}
	f := stdModuleInstFunc(bl.g.stdInsts.std.views[module], "", method)
	if f == nil {
		return no()
	}
	return bl.stdInstCallAt(t, f, kindInvalid)
}

// stdBareGenericFileFunc is the generic top-level stdlib function a bare name
// was selectively imported as (`import std::io.capture`), or nil.
func (bl *irScalarBuilder) stdBareGenericFileFunc(id *ast.Ident) *stdFunc {
	g := bl.g
	if g.stdInsts == nil {
		return nil
	}
	module, name, ok := stdBareFileFunc(g.srcFA(), id)
	if !ok {
		return nil
	}
	return stdModuleInstFunc(g.stdInsts.std.views[module], "", name)
}

// stdModuleInstFunc is the function `recv.name` of a std module that a
// program instantiates for itself: a generic template, or a monomorphic
// function whose signature names an Iter (`io.lines()`) or a std interface
// held as a value (`String.split`), stdProgSigWhy.
func stdModuleInstFunc(v *stdModuleView, recv, name string) *stdFunc {
	if f := stdModuleGenericTemplate(v, recv, name); f != nil {
		return f
	}
	if f := stdModuleMonoFunc(v, recv, name); f != nil && f.why == stdProgSigWhy {
		return f
	}
	return nil
}

// stdBareFileFunc names the std file and the declared name of the top-level
// stdlib function a bare name was selectively imported as, aliased or not:
// `read_line` after `import std::io.read_line` is ("io", "read_line"), and
// `next_line` after `import std::io.{read_line as next_line}` is too. A member
// of a type (`import std::calendar.Date.parse`) is not a file's function.
func stdBareFileFunc(fa *analysis.FileAnalysis, id *ast.Ident) (string, string, bool) {
	if fa == nil {
		return "", "", false
	}
	sym := resolvedBareSymbolAt(fa, id)
	if sym == nil || sym.OwningType != "" || sym.Name == "" {
		return "", "", false
	}
	for module, scope := range fa.Program().StdlibModuleScopes {
		if isProgramStd(module) {
			continue
		}
		if scope != nil && resolveSymbol(scope.Lookup(sym.Name)) == sym {
			return module, sym.Name, true
		}
	}
	return "", "", false
}

// stdGenericSiblingCall lowers a bare call inside a stdlib body to a generic
// sibling: `map(...)` inside `impl Generator<T>`, `decode_list(...)`. A bare
// call to a `host fn` of the receiver's inherent block (`concat(lhs, rhs)`
// inside `impl Add<Vector<T>, Vector<T>> for Vector<T>`) is the owner-qualified
// call it names, `Vector.concat(lhs, rhs)`, and lowers as that call does.
// So is a bare call inside `impl Iter<T>` to an operation iterCall lowers
// itself (irIterOwnArity): `chunk_by(...)` is `Iter.chunk_by(...)`. Its std
// body (`chunk_by_each`, `flat_map_each`) reaches a module host fn that only
// iterCall lowers, so instantiating the body would decline.
func (bl *irScalarBuilder) stdGenericSiblingCall(t *ast.Call, name string) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if g.stdModule == "" || g.stdInsts == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if g.stdModule == "io" && name == "run_captured" {
		v, k, mobile, ok := bl.runCaptured(t, "io.run_captured", kindString, kindString, kindString)
		return v, k, mobile, ok, true
	}
	if g.stdModule == "fs/host" && name == "run_on" {
		v, k, mobile, ok := bl.runOnFiles(t, "fs/host.run_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "process/host" && name == "run_on" {
		v, k, mobile, ok := bl.runOnFiles(t, "process/host.run_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "http/host" && name == "run_on" {
		v, k, mobile, ok := bl.runOnFiles(t, "http/host.run_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "http/host" && name == "request_on" {
		v, k, mobile, ok := bl.requestOn(t, "http/host.request_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "http/host" && name == "answer_on" {
		v, k, mobile, ok := bl.answerOn(t, "http/host.answer_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "http/host" && name == "recorded_on" {
		v, k, mobile, ok := bl.recordedOn(t, "http/host.recorded_on")
		return v, k, mobile, ok, true
	}
	if g.stdModule == "io" && name == "run_replayed" {
		v, k, mobile, ok := bl.runCaptured(t, "io.run_replayed", kindString, kindString, kindString, kindInt)
		return v, k, mobile, ok, true
	}
	v := g.stdInsts.std.views[g.stdModule]
	var f *stdFunc
	if g.implSelf != "" {
		f = stdModuleInstFunc(v, g.implSelf, name)
	}
	if f == nil {
		f = stdModuleInstFunc(v, "", name)
	}
	_, iterOwn := irIterOwnArity(name)
	iterOwn = iterOwn && g.implSelf == "Iter" && f != nil && f.recv == "Iter"
	if callee, bare := t.Func.(*ast.Ident); bare && g.implSelf != "" && (iterOwn || f == nil && stdModuleInherentHost(v, g.implSelf, name)) {
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
	if v, rk, mobile, ok, handled := bl.parseFieldsCall(t, k, method); handled {
		// `P.parse_fields(texts)` at an anonymous struct (irparsefields.go).
		return v, rk, mobile, ok, true
	}
	if v, rk, mobile, ok, handled := bl.recordJsonKindCall(t, k, method); handled {
		// `T.from_json(json)` at an anonymous struct (irrecordjson.go).
		return v, rk, mobile, ok, true
	}
	if _, std := stdNamedRecv(k); std {
		// `T.from_sql(v)` at a `Date`: a program's impl for a std type,
		// which no operand may name. A call std's own impl answers has
		// no candidate here and is spelled by the owner below.
		if v, rk, mobile, ok, handled := bl.stdRecvKindCall(t, k, method); handled {
			return v, rk, mobile, ok, true
		}
	}
	if owner := irKindOwnerName(bl.g, k); owner != "" {
		call := *t
		call.TypeArgs = nil
		call.Func = &ast.FieldAccess{Object: &ast.TypeIdent{Name: owner, Line: fa.Line, Col: fa.Col},
			Field: fa.Field, Line: fa.Line, Col: fa.Col}
		if k.def != nil && k.def.genericOf != nil {
			// The template's name does not say which instance an owner
			// function's call is (`Field.from_json(json)` has no Field
			// operand), so the instance travels beside the call.
			prevCall, prevKind := bl.instCall, bl.instKind
			bl.instCall, bl.instKind = &call, k
			defer func() { bl.instCall, bl.instKind = prevCall, prevKind }()
		}
		v, rk, mobile, ok := bl.lower(&call)
		return v, rk, mobile, ok, true
	}
	if irKindDeclared(k) {
		v, rk, mobile, ok := bl.kindImplCall(t, k, method)
		return v, rk, mobile, ok, true
	}
	base := irContainerBaseName(k)
	if base == "" {
		if len(t.Args) == 0 {
			if iface := bl.boundCallIface(fa); iface != "" {
				// `T.empty_sum()` in `Iter.sum<Decimal>`: an interface
				// function with no receiver, at a std type no std module's
				// scope names. No operand selects the impl, so the
				// interface-qualified spelling below has nothing to
				// dispatch on; T is k, and k's owner selects it.
				if v, rk, mobile, ok, handled := bl.stdReceiverlessImplCall(t, iface, method, k); handled {
					return v, rk, mobile, ok, true
				}
			}
		}
		if ti, isType := fa.Object.(*ast.TypeIdent); isType {
			if iface := bl.stdBoundIfaceAt(ti.Name, k, method); iface != "" {
				// `M.split_in(sep, s)` in `String.split<Regex>`: a std host
				// type no std module's scope names, so the call is the
				// bound interface's at that receiver, which stdIfacePlan
				// selects by the receiver's kind.
				v, rk, mobile, ok := bl.lower(ifaceQualified(t, fa, iface))
				return v, rk, mobile, ok, true
			}
		}
		if iface := bl.boundCallIface(fa); iface != "" {
			// `T.to_string(x)` at a tuple or record: a type with no owner
			// to spell the call by, so it is the bound interface's
			// (`Display.to_string(x)`), which renders or dispatches by the
			// argument's kind.
			v, rk, mobile, ok := bl.lower(ifaceQualified(t, fa, iface))
			return v, rk, mobile, ok, true
		}
		return no()
	}
	if v, rk, mobile, ok, handled := bl.stdRecvKindCall(t, k, method); handled {
		// `T.zero(n)` at a `List<Int>`, `Zero.zero<Maybe<Int>>(1)`: the
		// program's impl over the container. See stdrecvimpl.go.
		return v, rk, mobile, ok, true
	}
	if method == "contains" && (base == "Set" || base == "Map" || base == "List" || base == "Vector") {
		// `C.contains(c, x)` at a Set, Map, List or Vector: the impl is the
		// intrinsic the owner-qualified call reaches (`Set.contains`), not
		// a body to instantiate.
		call := *t
		call.TypeArgs = nil
		call.Func = &ast.FieldAccess{Object: &ast.TypeIdent{Name: base, Line: fa.Line, Col: fa.Col},
			Field: fa.Field, Line: fa.Line, Col: fa.Col}
		v, rk, mobile, ok := bl.lower(&call)
		return v, rk, mobile, ok, true
	}
	f := bl.g.stdGenericTemplate(base, method)
	if f == nil {
		// No impl of the container writes method: a std interface's
		// default (`T.from_missing()` at a `List<Int>`).
		if v, rk, mobile, ok, handled := bl.stdIfaceDefaultAtKind(t, bl.irQualLowerArgs(t), k, method); handled {
			return v, rk, mobile, ok, true
		}
		return no()
	}
	return bl.stdInstCallAt(t, f, k)
}

// stdReceiverlessImplCall lowers `T.method()`, an interface function of iface
// that takes no value of T, at the std kind k the instance binds T to: the one
// function std's `impl iface for <k's owner>` declares under method. handled
// is false when std declares no such function for k.
func (bl *irScalarBuilder) stdReceiverlessImplCall(t *ast.Call, iface, method string, k kind) (ir.Temp, kind, bool, bool, bool) {
	if bl.g.std == nil || k.tag != tagNamed || k.def == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	var f *stdFunc
	for _, c := range bl.g.std.byType[k.def.nomi+"."+method] {
		if c.iface != iface {
			continue
		}
		if f != nil {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		f = c
	}
	if f == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	args := bl.irQualLowerArgs(t)
	plan := bl.stdFuncPlan(t, args, f)
	if plan == nil {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, rk, mobile, ok := bl.qualEmit(t, args, plan)
	return v, rk, mobile, ok, true
}

// stdBoundIfaceAt is the interface among the bounds the instance being built
// declares for type parameter tp that std implements at k with a function
// named method, or "" when there is not exactly one.
func (bl *irScalarBuilder) stdBoundIfaceAt(tp string, k kind, method string) string {
	cur := bl.g.stdInstCur
	if cur == nil || cur.fd == nil || bl.g.std == nil {
		return ""
	}
	found := ""
	for _, b := range typeParamBoundsOf(cur.fd.TypeParams, cur.fd.WhereClauses) {
		if b.name != tp {
			continue
		}
		for _, iface := range b.bounds {
			if len(bl.g.std.byIface[iface+"."+method][k]) == 0 {
				continue
			}
			if found != "" {
				return ""
			}
			found = iface
		}
	}
	return found
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
		if plan == nil {
			// The impl is not beside the type: `impl Display for
			// lib.Dog` in the file that calls `T.to_string`. The bound
			// interface's call finds it, as `Display.to_string(d)` does.
			if fa, qualified := t.Func.(*ast.FieldAccess); qualified {
				if iface := bl.boundCallIface(fa); iface != "" {
					plan = bl.qualImplPlan(t, args, iface, method)
				}
			}
		}
	}
	if plan == nil {
		// No impl of k writes method: a std interface's default
		// (`T.from_missing()` at a derived struct).
		if v, rk, mobile, ok, handled := bl.stdIfaceDefaultAtKind(t, args, k, method); handled {
			return v, rk, mobile, ok
		}
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.qualEmit(t, args, plan)
}

// boundCallIface is the interface the checker resolved a bounded
// `T.method` call to (`Display` for `T.to_string` under `T: Display`), or ""
// when the call is not an interface function's.
func (bl *irScalarBuilder) boundCallIface(fa *ast.FieldAccess) string {
	if bl.g.fa == nil || fa.Field == nil {
		return ""
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != analysis.SymbolInterfaceMethod {
		return ""
	}
	return sym.OwningInterface
}

// ifaceQualified is the call t spelled through iface: `Display.to_string(x)`
// for `T.to_string(x)`.
func ifaceQualified(t *ast.Call, fa *ast.FieldAccess, iface string) *ast.Call {
	call := *t
	call.TypeArgs = nil
	call.Func = &ast.FieldAccess{Object: &ast.TypeIdent{Name: iface, Line: fa.Line, Col: fa.Col},
		Field: fa.Field, Line: fa.Line, Col: fa.Col}
	return &call
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
	case "Iter":
		// The constructors, `loop` and the operations iterCall lowers.
		_, own := irIterOwnArity(method)
		return own || method == "from" || method == "iterate" || method == "repeat" || method == "loop"
	case "Range", "Map", "List", "Vector", "Set", "Channel", "Sender", "Receiver",
		"Task", "Supervisor", "Struct":
		return true
	case "Result":
		return method == "map_err" || method == "with_default"
	case "Maybe":
		return method == "to_result" || method == "with_default"
	}
	return false
}
