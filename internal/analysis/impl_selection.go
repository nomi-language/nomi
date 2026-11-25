package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A generic interface implemented at several type arguments.
//
// `impl Contains<String> for String`, `impl Contains<Regex> for String` and
// `impl Contains<Codepoint> for String` are three impls of one interface for
// one receiver. The impls of an interface for a receiver either all name
// distinct type arguments, or there is exactly one, which may be generic in
// them (checkImplInstantiations). A call picks one statically, from the
// types of its arguments: every call form, `String.contains(s, x)`,
// `Contains.contains(s, x)`, a pipe stage, an operator and a bound a generic
// call solves, asks selectImpl, and so does a `?` that converts its error
// (applyTryFrom). Two impls are one instantiation when their type arguments
// are identical (sameTypeArgs), as coherence decides it, so `From<Circle>`
// and `From<Shape>` for a Shape that embeds Circle are two candidates. An
// argument that fits several takes the most specific (mostSpecificImpl): a
// Circle takes From<Circle>. A type argument no parameter names is chosen
// by the type the call's result is expected to have, as any generic call's
// result-only type parameter is. When neither chooses one impl the call is an
// error that names the candidates. The chosen impl's function is recorded on
// the call's method reference (Symbol.DispatchImpl), and the IR builder calls
// it directly.

// implCandidate is one impl of a generic interface for a concrete receiver:
// its recorded template and its interface type arguments at the receiver's
// own type arguments.
type implCandidate struct {
	info *ImplTypeArgs
	args []Type
}

// implCandidates is every impl of the interface iface (declared by origin)
// that concrete has, one per distinct list of type arguments.
func (c *checker) implCandidates(concrete Type, iface, origin string) []implCandidate {
	if c == nil {
		return nil
	}
	return c.fa.implCandidates(concrete, iface, origin)
}

func (fa *FileAnalysis) implCandidates(concrete Type, iface, origin string) []implCandidate {
	if dt := subtypeOf(concrete); dt != nil && dt.Inner != nil {
		// A subtype has its parent's impls, save one that makes a
		// value of its type (subtype_impls.go).
		if fa.subtypeLacksImplNamed(dt, iface, origin) {
			return nil
		}
		return fa.implCandidates(DistinctInner(dt), iface, origin)
	}
	name := concreteTypeName(concrete)
	if name == "" || fa == nil {
		return nil
	}
	cargs := concreteTypeArgs(concrete)
	var out []implCandidate
	for _, info := range distinctImpls(lookupImplTypeArgSets(name, iface, fa.implTypeArgsContext())) {
		if !sameNominalIdentity(origin, iface, info.Origin, iface) {
			continue
		}
		if !implReceiverMatches(info, concrete, fa.implsContext(), fa.implTypeArgsContext()) {
			continue
		}
		args := make([]Type, len(info.Args))
		for i, a := range info.Args {
			args[i] = substituteTypeParamDefs(info.TypeParamDefs, cargs, a)
		}
		dup := false
		for _, o := range out {
			if sameTypeArgs(o.args, args) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, implCandidate{info: info, args: args})
		}
	}
	return out
}

// implDeclKey identifies the impl block a template was recorded from by its
// source, so the two parses of one std file (the shared analysis and a
// build's own) are one impl. Two blocks of different files at one position
// with one header for one receiver are a duplicate impl either way.
func implDeclKey(info *ImplTypeArgs) string {
	return implBlockKey(info.Block)
}

// distinctImpls is infos with each impl block once.
func distinctImpls(infos []*ImplTypeArgs) []*ImplTypeArgs {
	seen := map[string]bool{}
	out := infos[:0:0]
	for _, info := range infos {
		if info == nil {
			continue
		}
		if key := implDeclKey(info); key != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, info)
	}
	return out
}

// candidateList names each candidate by its type arguments, as the error
// text does: `Regex`, or `Days, Day` for two, and renders also, the other
// types the message names. All are rendered together (distinctTypeTexts),
// so two that print alike are qualified by their files: `a.Error` and
// `b.Error`.
func candidateList(cands []implCandidate, also ...Type) (string, []string) {
	var all []Type
	for _, cand := range cands {
		all = append(all, cand.args...)
	}
	texts := distinctTypeTexts("", append(all, also...), nil)
	labels := make([]string, len(cands))
	at := 0
	for i, cand := range cands {
		labels[i] = "`" + strings.Join(texts[at:at+len(cand.args)], ", ") + "`"
		at += len(cand.args)
	}
	return quotedAnd(labels), texts[at:]
}

// quotedAnd joins already quoted items: `a`, `a and b`, `a, b and c`.
func quotedAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// candidateSig is the method's signature at one candidate: the interface's
// type parameters replaced by its arguments and `self` by self.
func candidateSig(iface *InterfaceType, m *MethodSig, args []Type, self Type) *FuncType {
	subs := map[*TypeParam_]Type{}
	for i, def := range iface.TypeParamDefs {
		if i < len(args) {
			subs[def] = args[i]
		}
	}
	if iface.SelfParam != nil && self != nil {
		subs[iface.SelfParam] = self
	}
	params := make([]Type, len(m.Params))
	for i, p := range m.Params {
		params[i] = Substitute(p, subs)
	}
	ret := m.Return
	if ret == nil {
		ret = TypeUnit
	}
	return &FuncType{Params: params, Return: Substitute(ret, subs), DefaultCount: m.DefaultCount, WhereBounds: m.WhereBounds}
}

// solveImplParams is sig, a chosen impl's method signature, with the
// impl's own type parameters that its receiver does not fix solved from
// the arguments: `impl<C> Contains<Regex<C>> for String` called with a
// `Regex<Date>` is `(String, Regex<Date>) -> Bool`, not the impl's own C.
// The caller's own parameters are answers and stay. A parameter no
// argument fixes stays as it is.
func (c *checker) solveImplParams(sig *FuncType, argTys []Type) *FuncType {
	if !c.containsForeignTypeParam(sig) {
		return sig
	}
	subs := map[*TypeParam_]Type{}
	for i, at := range argTys {
		if at == nil || i >= len(sig.Params) {
			continue
		}
		saved := saveTypeVars(sig.Params[i], at)
		_ = c.unifyInto(sig.Params[i], at, subs)
		saved.restore()
	}
	for tp, t := range subs {
		if c.fnTypeParams[tp.Name_] == tp || c.containsForeignTypeParam(t) {
			delete(subs, tp)
		}
	}
	if len(subs) == 0 {
		return sig
	}
	if solved, ok := Substitute(sig, subs).(*FuncType); ok {
		return solved
	}
	return sig
}

// fitsInto reports whether a value of type have may flow into want, without
// solving any inference variable either side holds.
func (c *checker) fitsInto(want, have Type) bool {
	if want == nil || have == nil {
		return true
	}
	saved := saveTypeVars(want, have)
	err := c.unifyInto(want, have, map[*TypeParam_]Type{})
	saved.restore()
	return err == nil
}

// typeVarSnapshot holds what each inference variable reachable from some
// types was solved to, so a trial unification can be undone.
type typeVarSnapshot map[*TypeVar]Type

func saveTypeVars(ts ...Type) typeVarSnapshot {
	s := typeVarSnapshot{}
	for _, t := range ts {
		s.walk(t)
	}
	return s
}

func (s typeVarSnapshot) restore() {
	for tv, was := range s {
		tv.Resolved = was
	}
}

func (s typeVarSnapshot) walk(t Type) {
	walkType(t, func(p Type) bool {
		if tv, ok := p.(*TypeVar); ok {
			if _, seen := s[tv]; seen {
				return false
			}
			s[tv] = tv.Resolved
		}
		return true
	})
}

// implChoice is what selectImpl answers.
type implChoice struct {
	chosen *implCandidate
	// fits is every candidate the arguments admit, before the expected type
	// was consulted: more than one with no chosen is the ambiguity, none the
	// mismatch.
	fits []implCandidate
	// byResult says the arguments admitted several and the expected type
	// chose among them, or would have had to.
	byResult bool
}

// selectImpl picks the candidate a call of m takes. argTys holds each
// parameter's argument type, nil for a parameter with no argument or one
// whose type depends on the parameter's (a `.Variant`, a placeholder).
// expected is the type the call's result flows into, or nil.
func (c *checker) selectImpl(cands []implCandidate, iface *InterfaceType, m *MethodSig, self Type, argTys []Type, expected Type) implChoice {
	var fits []implCandidate
	for _, cand := range cands {
		sig := candidateSig(iface, m, cand.args, self)
		ok := true
		for i, at := range argTys {
			if at == nil || i >= len(sig.Params) {
				continue
			}
			if !c.fitsInto(sig.Params[i], at) {
				ok = false
				break
			}
		}
		if ok {
			fits = append(fits, cand)
		}
	}
	out := implChoice{fits: fits}
	if len(fits) == 1 {
		out.chosen = &fits[0]
		return out
	}
	if len(fits) < 2 {
		return out
	}
	if best := c.mostSpecificImpl(fits, iface, m, self, argTys); best != nil {
		out.chosen = best
		return out
	}
	out.byResult = true
	if expected == nil || containsTypeVar(expected) {
		return out
	}
	var byResult []implCandidate
	for _, cand := range fits {
		sig := candidateSig(iface, m, cand.args, self)
		if c.fitsInto(expected, sig.Return) {
			byResult = append(byResult, cand)
		}
	}
	if len(byResult) == 1 {
		out.chosen = &byResult[0]
	}
	return out
}

// mostSpecificImpl is the one of fits, the candidates every argument fits,
// whose parameters at the written arguments each fit into every other
// candidate's and not the other way round: with `impl From<Circle>` and
// `impl From<Shape>` for a Shape that embeds Circle, a Circle argument
// fits both, and From<Circle> is the more specific. Nil when no one
// candidate is.
func (c *checker) mostSpecificImpl(fits []implCandidate, iface *InterfaceType, m *MethodSig, self Type, argTys []Type) *implCandidate {
	sigs := make([]*FuncType, len(fits))
	for i, cand := range fits {
		sigs[i] = candidateSig(iface, m, cand.args, self)
	}
	// asSpecific reports whether each of a's parameters at a written
	// argument fits into b's.
	asSpecific := func(a, b *FuncType) bool {
		for i, at := range argTys {
			if at == nil || i >= len(a.Params) || i >= len(b.Params) {
				continue
			}
			if !c.fitsInto(b.Params[i], a.Params[i]) {
				return false
			}
		}
		return true
	}
	var best *implCandidate
	for i := range fits {
		most := true
		for j := range fits {
			if i != j && (!asSpecific(sigs[i], sigs[j]) || asSpecific(sigs[j], sigs[i])) {
				most = false
				break
			}
		}
		if most {
			if best != nil {
				return nil
			}
			best = &fits[i]
		}
	}
	return best
}

// distinguishedByParams reports whether two of cands take different
// parameter types, so an argument could choose between them.
func distinguishedByParams(cands []implCandidate, iface *InterfaceType, m *MethodSig, self Type) bool {
	if len(cands) < 2 {
		return false
	}
	first := candidateSig(iface, m, cands[0].args, self)
	for _, cand := range cands[1:] {
		sig := candidateSig(iface, m, cand.args, self)
		if !sameTypeArgs(first.Params, sig.Params) {
			return true
		}
	}
	return false
}

// ambiguousImplMessage is the error at a call whose arguments and expected
// type do not choose one of several impls. callee names the interface
// function called (implCalleeLabel).
func ambiguousImplMessage(callee string, recv Type, cands []implCandidate, byParams bool) string {
	list, _ := candidateList(cands)
	head := fmt.Sprintf("%s for `%s` has impls at %s", callee, recv, list)
	if byParams {
		return head + "; the argument's type does not choose one: annotate it"
	}
	return head + "; only the result's type tells them apart, and nothing here expects one: annotate the result"
}

// noImplMessage is the error at a call none of several impls takes.
func noImplMessage(callee string, recv Type, cands []implCandidate, argTys []Type, recvAt int) string {
	var args []Type
	for i, at := range argTys {
		if at == nil || i == recvAt {
			continue
		}
		args = append(args, at)
	}
	list, argTexts := candidateList(cands, args...)
	got := make([]string, len(argTexts))
	for i, text := range argTexts {
		got[i] = "`" + text + "`"
	}
	head := fmt.Sprintf("%s for `%s` has impls at %s", callee, recv, list)
	if len(got) == 0 {
		return head + ", and none takes these arguments"
	}
	return head + ", and none takes " + quotedAnd(got)
}

// multiImplCallee is the generic interface function a call's callee names
// when its receiver may implement that interface at several type arguments:
// `Contains.contains` (interface-qualified) or `String.contains`
// (type-qualified). qualifier is the receiver type a type-qualified callee
// names, nil when the argument must say.
func (c *checker) multiImplCallee(fn ast.Node) (iface *InterfaceType, m *MethodSig, qualifier string, ok bool) {
	fa, isField := fn.(*ast.FieldAccess)
	if !isField || fa.Field == nil {
		return nil, nil, "", false
	}
	method := fa.Field.Name
	pick := func(it *InterfaceType) (*InterfaceType, *MethodSig, bool) {
		if it == nil || len(it.TypeParamDefs) == 0 {
			return nil, nil, false
		}
		if _, isOperator := operatorInterfaceByName(it.Name); isOperator {
			// The operator path selects these (lookupOperatorImplTypeArgs).
			return nil, nil, false
		}
		for i := range it.Methods {
			if it.Methods[i].Name == method {
				return it, &it.Methods[i], true
			}
		}
		return nil, nil, false
	}
	if it, isIface := interfaceObjectType(c, fa.Object, nil); isIface {
		if decl, isDecl := c.reg.Lookup(it.Name).(*InterfaceType); isDecl && decl.Name == it.Name && sameNominalIdentity(decl.Origin, decl.Name, it.Origin, it.Name) {
			it = decl
		}
		iface, m, ok = pick(it)
		return iface, m, "", ok
	}
	recvName := c.typeQualifierName(fa.Object)
	if recvName == "" {
		return nil, nil, "", false
	}
	var found *InterfaceType
	var foundSig *MethodSig
	for _, id := range c.fa.interfacesImplementedBy(recvName, c.receiverOriginForReceiver(fa.Object, recvName)) {
		it := c.implementedInterface(recvName, id)
		if pi, pm, picked := pick(it); picked {
			if found != nil {
				// Two interfaces declare the method: the type-qualified path
				// reports that.
				return nil, nil, "", false
			}
			found, foundSig = pi, pm
		}
	}
	if found == nil {
		return nil, nil, "", false
	}
	return found, foundSig, recvName, true
}

func unnamedArg(arg ast.Node) ast.Node {
	if na, ok := arg.(*ast.NamedArg); ok {
		return na.Value
	}
	return arg
}

// selectsByResult reports whether a call names a generic interface's
// function whose result mentions one of the interface's type parameters,
// so that its expected type may be what chooses its impl.
func (c *checker) selectsByResult(n *ast.Call) bool {
	iface, m, _, ok := c.multiImplCallee(n.Func)
	if !ok || m.Return == nil {
		return false
	}
	mentions := false
	for _, def := range iface.TypeParamDefs {
		if typeMentionsParam(m.Return, def) {
			mentions = true
		}
	}
	return mentions
}

// typeMentionsParam reports whether p occurs in t: replacing it changes t.
func typeMentionsParam(t Type, p *TypeParam_) bool {
	if !ContainsTypeParam(t) {
		return false
	}
	// Unit where p stood: equal to t only when p stood nowhere.
	return !TypesEqual(Substitute(t, map[*TypeParam_]Type{p: TypeUnit}), t)
}

// isArgUndetermined reports whether an argument's type is decided by the
// parameter it fills, so it cannot choose among impls by itself.
func isArgUndetermined(arg ast.Node) bool {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	switch arg.(type) {
	case *ast.Placeholder, *ast.Capture, *ast.DotVariant:
		return true
	}
	return false
}

// pretypeArg is an argument's type checked with nothing expected of it, its
// diagnostics dropped: the real check, against the chosen impl's parameter,
// reports them.
func (c *checker) pretypeArg(arg ast.Node) Type {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	ty := c.checkNode(arg)
	c.errors = c.errors[:mark]
	c.undetermined = c.undetermined[:undeterminedMark]
	if isUnresolvedVar(ty) {
		return nil
	}
	return ty
}

// implCallSelection is a call's choice among several impls, for the call
// checker to type the call by and record.
type implCallSelection struct {
	iface  *InterfaceType
	method string
	recv   Type
	chosen *implCandidate
	// ft is the callee's type at the chosen impl: the call is checked
	// against it as against any function.
	ft *FuncType
	// impl is the chosen impl's own signature, the receiver in its self
	// position: the type of the function the call names.
	impl *FuncType
	// returnSelf marks an interface-qualified call of a function whose only
	// self is its result (`Parse.parse(3)`), whose receiver the expected
	// type gave.
	returnSelf bool
}

// selectCallImpl selects the impl a call of a generic interface's function
// takes when its receiver implements the interface at several type
// arguments. handled is false when the call is no such call, and the caller
// types it as before. A selection that fails has been reported, and sel is
// nil.
//
// piped is the value a pipe stage passes as its first argument, and
// pipedTy its type; nil for a direct call.
func (c *checker) selectCallImpl(n *ast.Call, expected Type, piped ast.Node, pipedTy Type) (sel *implCallSelection, handled bool) {
	iface, m, qualifier, ok := c.multiImplCallee(n.Func)
	if !ok {
		return nil, false
	}
	recvAt := -1
	for i, p := range m.Params {
		if isSelfPositionParam(p, iface) {
			recvAt = i
			break
		}
	}
	// Each parameter's argument, as the call writes it.
	argNodes := make([]ast.Node, len(m.Params))
	shift := 0
	if piped != nil {
		argNodes[0] = piped
		shift = 1
	}
	// The call's own arguments go where every call's do (placeArgs).
	for i, slot := range placeArgs(n.Args, shift, m.ParamNames, m.Params) {
		if slot >= 0 && slot < len(argNodes) {
			argNodes[slot] = n.Args[i]
		}
	}
	// A partial application (`Holds.holds(b, &1)` where a `(String) ->
	// Bool` is expected): each placeholder takes the expected function's
	// parameter of its number, and the call's result is that function's.
	var partial *FuncType
	if piped == nil && callHasPlaceholder(n.Args) {
		if ft, isFn := resolveTypeVar(expected).(*FuncType); isFn {
			partial = ft
		}
		expected = nil
		if partial != nil {
			expected = partial.Return
		}
	}
	argTys := make([]Type, len(m.Params))
	for i, arg := range argNodes {
		if ph, isPh := unnamedArg(arg).(*ast.Placeholder); isPh && partial != nil && ph.N >= 1 && ph.N <= len(partial.Params) {
			if pt := resolveTypeVar(partial.Params[ph.N-1]); !isUnresolvedVar(pt) && !containsTypeVar(pt) {
				argTys[i] = pt
			}
			continue
		}
		switch {
		case arg == nil || isArgUndetermined(arg):
		case arg == piped:
			if !isUnresolvedVar(pipedTy) {
				argTys[i] = pipedTy
			}
		default:
			argTys[i] = c.pretypeArg(arg)
		}
	}
	var recv Type
	if recvAt >= 0 {
		if at := resolveTypeVar(argTys[recvAt]); at != nil && !containsTypeVar(at) {
			if _, erased := at.(*InterfaceType); !erased {
				recv = at
			}
		}
	} else if qualifier == "" && isSelfPositionParam(m.Return, iface) && expected != nil {
		// `Parse.parse(3)` where a `Num` is expected: a function with no
		// receiver whose result is self is the expected type's.
		if et := resolveTypeVar(expected); !containsTypeVar(et) && !ContainsTypeParam(et) {
			if _, erased := et.(*InterfaceType); !erased {
				recv = et
			}
		}
	}
	if recv == nil && qualifier != "" {
		if t, ok := c.reg.Lookup(qualifier).(Type); ok {
			if !ContainsTypeParam(t) {
				recv = t
			}
		}
	}
	if recv == nil || ContainsTypeParam(recv) || concreteTypeName(recv) == "" {
		return nil, false
	}
	if qualifier != "" && concreteTypeName(recv) != qualifier {
		return nil, false
	}
	cands := c.implCandidates(recv, iface.Name, iface.Origin)
	if len(cands) < 2 {
		return nil, false
	}
	if expected != nil && c.containsForeignTypeParam(expected) {
		// A generic callee's own parameter (`T` of the `Some` this call is
		// the argument of) expects nothing yet.
		expected = nil
	}
	choice := c.selectImpl(cands, iface, m, recv, argTys, expected)
	if choice.chosen == nil {
		var msg string
		if len(choice.fits) == 0 {
			msg = noImplMessage(c.implCalleeLabel(n, iface.Name, m.Name), recv, cands, argTys, recvAt)
		} else {
			msg = ambiguousImplMessage(c.implCalleeLabel(n, iface.Name, m.Name), recv, choice.fits, distinguishedByParams(choice.fits, iface, m, recv))
		}
		c.report(errAt(n, msg))
		// The arguments' own diagnostics, which the selection dropped. An
		// argument whose type its parameter decides has none to give.
		for _, arg := range n.Args {
			if !isArgUndetermined(arg) {
				c.checkNode(arg)
			}
		}
		return nil, true
	}
	sig := candidateSig(iface, m, choice.chosen.args, recv)
	return &implCallSelection{
		iface:  iface,
		method: m.Name,
		recv:   recv,
		chosen: choice.chosen,
		ft:     sig,
		impl:   c.solveImplParams(sig, argTys),
		// Read below as the receiver an interface-qualified call's result
		// names.
		returnSelf: recvAt < 0 && qualifier == "",
	}, true
}

// recordSelectedImpl records the chosen impl's function on the call's method
// reference, as attachDispatchImpl does for a receiver with one impl.
func (c *checker) recordSelectedImpl(n *ast.Call, sel *implCallSelection) {
	fa, ok := n.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || sel == nil || sel.chosen == nil {
		return
	}
	c.recordBoundConformance(sel.recv, sel.iface.Name, sel.iface.Origin, c.callSiteOf(n, n.Line, n.Col))
	pos := Pos{Line: fa.Field.Line, Col: fa.Field.Col}
	existing := c.fa.References[pos]
	if existing == nil {
		return
	}
	proxy := *existing
	// An inherited default has no function of the block's to name; the
	// signature below still says which impl's it is, and the call's
	// callee names the block (classifyCallee).
	if impl := implBlockMethod(sel.chosen.info.Block, sel.method); impl != nil {
		proxy.DispatchImpl = impl
		proxy.DispatchReceiver = sel.recv
	} else if sel.chosen.info.Block != nil {
		if c.selectedImplBlocks == nil {
			c.selectedImplBlocks = map[Pos]*ast.ImplBlock{}
		}
		c.selectedImplBlocks[pos] = sel.chosen.info.Block
	}
	proxy.Type = sel.impl
	proxy.CallType = sel.impl
	if sel.returnSelf {
		proxy.ReturnSelf = sel.recv
	}
	c.fa.References[pos] = &proxy
}

// implBlockMethod is the function an impl block writes under name, or nil
// when the block inherits it.
func implBlockMethod(b *ast.ImplBlock, name string) ast.ImplMethodDecl {
	if b == nil {
		return nil
	}
	for _, item := range b.Items {
		switch it := item.(type) {
		case *ast.FuncDef:
			if it.Name == name {
				return it
			}
		case *ast.ExternFunc:
			if it.Name == name {
				return it
			}
		}
	}
	return nil
}

// receiversDiffer reports whether two impls are for two nominal types of one
// name that two modules declare, a program's `type Sql` and std/sql's `Sql`:
// two receivers, so neither is a second impl of the other's.
func receiversDiffer(a, b *ImplTypeArgs) bool {
	if a.Receiver == nil || b.Receiver == nil {
		return false
	}
	ao, aNominal := nominalOrigin(resolveTypeVar(a.Receiver))
	bo, bNominal := nominalOrigin(resolveTypeVar(b.Receiver))
	if !aNominal || !bNominal || ao == "" || bo == "" || ao == OriginUnresolved || bo == OriginUnresolved {
		return false
	}
	return ao != bo
}

// checkImplInstantiations holds one impl block of a generic interface to the
// rule for a receiver with several impls of it: each writes its type
// arguments, and they are distinct concrete types. An impl generic in them
// stands alone; beside another it overlaps. Two impls at the same arguments
// are the duplicate detectImplCollisions reports.
func (c *checker) checkImplInstantiations(n *ast.ImplBlock) {
	if c == nil || c.fa == nil || n.Interface == nil {
		return
	}
	ifaceName := implInterfaceName(c.fa, n.Interface)
	recvName := TypeExprBaseName(n.Receiver)
	if ifaceName == "" || recvName == "" {
		return
	}
	var own *ImplTypeArgs
	for _, info := range c.fa.ImplTypeArgSets[recvName][ifaceName] {
		if info != nil && info.Block == n {
			own = info
			break
		}
	}
	if own == nil || len(own.Args) == 0 {
		return
	}
	var others []*ImplTypeArgs
	ownKey := implDeclKey(own)
	for _, info := range distinctImpls(lookupImplTypeArgSets(recvName, ifaceName, c.implTypeArgSetsContext())) {
		if info == own || info.Block == n || implDeclKey(info) == ownKey || !sameNominalIdentity(own.Origin, ifaceName, info.Origin, ifaceName) ||
			receiversDiffer(own, info) {
			continue
		}
		if sameTypeArgs(info.Args, own.Args) {
			// The same arguments: this block seen twice, or the duplicate
			// the collision check reports.
			continue
		}
		if own.Receiver != nil && info.Receiver != nil && !typesMayOverlap(own.Receiver, info.Receiver) {
			continue
		}
		seen := false
		for _, o := range others {
			if sameTypeArgs(o.Args, info.Args) {
				seen = true
				break
			}
		}
		if !seen {
			others = append(others, info)
		}
	}
	if len(others) == 0 {
		return
	}
	header := (&InterfaceType{Name: ifaceName, TypeArgs: own.Args}).String()
	if !own.Written {
		c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
			"`%s` implements `%s` more than once, so each impl writes its type arguments in its header",
			recvName, ifaceName)}.WithHint(fmt.Sprintf("write `impl %s for %s`", header, n.Receiver.TypeString())))
		return
	}
	if !ContainsTypeParam(&TupleType{Elems: own.Args}) {
		c.checkCoveredByStdImpl(n, own, others, header, recvName)
		return
	}
	var rivals []string
	for _, o := range others {
		if argsMayOverlap(own.Args, o.Args) {
			rivals = append(rivals, "`"+(&InterfaceType{Name: ifaceName, TypeArgs: o.Args}).String()+"`")
		}
	}
	if len(rivals) == 0 {
		return
	}
	c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
		"overlapping impls: `%s` for `%s` is generic in its type arguments, so it covers %s, which `%s` also implements",
		header, recvName, quotedAnd(rivals), recvName)}.WithHint(
		"a receiver implements an interface either once, generic in its type arguments, or once per distinct concrete argument list"))
}

// checkCoveredByStdImpl reports an impl at concrete type arguments that a
// std impl generic in them covers: `impl Contains<Rate> for List<Rate>`
// beside std's `impl Contains<T> for List<T>`, which the orphan rule admits
// through `Rate`. Between two of the program's own impls the generic one
// reports the overlap (checkImplInstantiations), but std is checked once,
// apart from every program, so its impl never sees a program's; the
// program's impl reports it instead.
func (c *checker) checkCoveredByStdImpl(n *ast.ImplBlock, own *ImplTypeArgs, others []*ImplTypeArgs, header, recvName string) {
	if strings.HasPrefix(own.File, "std/") {
		return
	}
	var rivals []string
	for _, o := range others {
		if strings.HasPrefix(o.File, "std/") && ContainsTypeParam(&TupleType{Elems: o.Args}) && argsMayOverlap(own.Args, o.Args) {
			rivals = append(rivals, "`"+(&InterfaceType{Name: implInterfaceName(c.fa, n.Interface), TypeArgs: o.Args}).String()+"`")
		}
	}
	if len(rivals) == 0 {
		return
	}
	c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
		"overlapping impls: std's %s for `%s` is generic in its type arguments, so it covers `%s`, which this impl implements",
		quotedAnd(rivals), recvName, header)}.WithHint(
		"a receiver implements an interface either once, generic in its type arguments, or once per distinct concrete argument list"))
}

// headerWritesTypeArgs reports whether an impl header writes the interface's
// type arguments: `Holds<Int>`, `lib.Holds<Int>`.
func headerWritesTypeArgs(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.GenericType:
		return len(t.Params) > 0
	case *ast.QualifiedType:
		return headerWritesTypeArgs(t.Member)
	}
	return false
}

// argsMayOverlap reports whether two impls' type-argument lists could
// name the same instantiation, a type parameter standing for any type.
func argsMayOverlap(a, b []Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !typesMayOverlap(a[i], b[i]) {
			return false
		}
	}
	return true
}

func typesMayOverlap(a, b Type) bool {
	a, b = resolveTypeVar(a), resolveTypeVar(b)
	if a == nil || b == nil {
		return true
	}
	if _, ok := a.(*TypeParam_); ok {
		return true
	}
	if _, ok := b.(*TypeParam_); ok {
		return true
	}
	if _, ok := a.(*TypeVar); ok {
		return true
	}
	if _, ok := b.(*TypeVar); ok {
		return true
	}
	if TypesEqual(a, b) {
		return true
	}
	as, bs, same := pairedTypeChildren(a, b)
	return same && argsMayOverlap(as, bs)
}
