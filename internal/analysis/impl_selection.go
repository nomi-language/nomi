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
// types of its arguments: every call form, `String.contains?(s, x)`,
// `Contains.contains?(s, x)`, a pipe stage, an operator and a bound a generic
// call solves, asks selectImpl. A type argument no parameter names is chosen
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
	name := concreteTypeName(concrete)
	if name == "" || c == nil || c.fa == nil {
		return nil
	}
	cargs := concreteTypeArgs(concrete)
	var out []implCandidate
	for _, info := range distinctImpls(lookupImplTypeArgSets(name, iface, c.implTypeArgSetsContext())) {
		if !sameNominalIdentity(origin, iface, info.Origin, iface) {
			continue
		}
		if !implReceiverMatches(info, concrete, c.implsContext(), c.implTypeArgsContext()) {
			continue
		}
		args := make([]Type, len(info.Args))
		for i, a := range info.Args {
			args[i] = substituteTypeParamDefs(info.TypeParamDefs, cargs, a)
		}
		dup := false
		for _, o := range out {
			if typesAllEqual(o.args, args) {
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
	b := info.Block
	if b == nil || b.Interface == nil || b.Receiver == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s:%s", b.Line, b.Col, b.Interface.TypeString(), b.Receiver.TypeString())
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

// candidateLabel renders a candidate's type arguments as the error text
// names them: `Regex`, or `Days, Day` for two.
func candidateLabel(args []Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}

func candidateList(cands []implCandidate) string {
	labels := make([]string, len(cands))
	for i, cand := range cands {
		labels[i] = "`" + candidateLabel(cand.args) + "`"
	}
	return quotedAnd(labels)
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
	switch tt := t.(type) {
	case nil:
	case *TypeVar:
		if _, seen := s[tt]; seen {
			return
		}
		s[tt] = tt.Resolved
		s.walk(tt.Resolved)
	case *ListType:
		s.walk(tt.Elem)
	case *MapType:
		s.walk(tt.Key)
		s.walk(tt.Val)
	case *FuncType:
		for _, p := range tt.Params {
			s.walk(p)
		}
		s.walk(tt.Return)
	case *TupleType:
		for _, e := range tt.Elems {
			s.walk(e)
		}
	case *AnonStructType:
		for _, f := range tt.Fields {
			s.walk(f.Type)
		}
	case *StructType:
		for _, a := range tt.TypeArgs {
			s.walk(a)
		}
	case *EnumType:
		for _, a := range tt.TypeArgs {
			s.walk(a)
		}
	case *DistinctType:
		for _, a := range tt.TypeArgs {
			s.walk(a)
		}
	case *InterfaceType:
		for _, a := range tt.TypeArgs {
			s.walk(a)
		}
	case *PartialType:
		s.walk(tt.Inner)
	}
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

// distinguishedByParams reports whether two of cands take different
// parameter types, so an argument could choose between them.
func distinguishedByParams(cands []implCandidate, iface *InterfaceType, m *MethodSig, self Type) bool {
	if len(cands) < 2 {
		return false
	}
	first := candidateSig(iface, m, cands[0].args, self)
	for _, cand := range cands[1:] {
		sig := candidateSig(iface, m, cand.args, self)
		if !typesAllEqual(first.Params, sig.Params) {
			return true
		}
	}
	return false
}

// ambiguousImplMessage is the error at a call whose arguments and expected
// type do not choose one of several impls.
func ambiguousImplMessage(iface *InterfaceType, method string, recv Type, cands []implCandidate, byParams bool) string {
	head := fmt.Sprintf("`%s.%s` for `%s` has impls at %s", iface.Name, method, recv, candidateList(cands))
	if byParams {
		return head + "; the argument's type does not choose one: annotate it"
	}
	return head + "; only the result's type tells them apart, and nothing here expects one: annotate the result"
}

// noImplMessage is the error at a call none of several impls takes.
func noImplMessage(iface *InterfaceType, method string, recv Type, cands []implCandidate, argTys []Type, recvAt int) string {
	var got []string
	for i, at := range argTys {
		if at == nil || i == recvAt {
			continue
		}
		got = append(got, "`"+at.String()+"`")
	}
	head := fmt.Sprintf("`%s.%s` for `%s` has impls at %s", iface.Name, method, recv, candidateList(cands))
	if len(got) == 0 {
		return head + ", and none takes these arguments"
	}
	return head + ", and none takes " + quotedAnd(got)
}

// multiImplCallee is the generic interface function a call's callee names
// when its receiver may implement that interface at several type arguments:
// `Contains.contains?` (interface-qualified) or `String.contains?`
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
	for _, name := range c.interfacesImplementedBy(recvName) {
		it := c.implementedInterface(recvName, name)
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

// implementedInterface is the declaration of an interface recvName
// implements: the one this file names, else std's, which `String.contains?`
// reaches in a std file that never imports `Contains`.
func (c *checker) implementedInterface(recvName, name string) *InterfaceType {
	if it, ok := c.reg.Lookup(name).(*InterfaceType); ok && it != nil {
		return it
	}
	for _, info := range lookupImplTypeArgSets(recvName, name, c.implTypeArgSetsContext()) {
		if info == nil {
			continue
		}
		mod, isStd := strings.CutPrefix(info.Origin, "std/")
		if !isStd {
			continue
		}
		if scope := c.fa.Program().StdlibModuleScopes[mod]; scope != nil {
			if sym := scope.Lookup(name); sym != nil {
				if it, ok := sym.Type.(*InterfaceType); ok {
					return it
				}
			}
		}
	}
	return nil
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
	pos := shift
	for _, arg := range n.Args {
		if na, named := arg.(*ast.NamedArg); named {
			for i, pn := range m.ParamNames {
				if pn == na.Name && i < len(argNodes) {
					argNodes[i] = arg
				}
			}
			continue
		}
		if pos < len(argNodes) {
			argNodes[pos] = arg
		}
		pos++
	}
	// A partial application (`Holds.holds?(b, &1)` where a `(String) ->
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
			msg = noImplMessage(iface, m.Name, recv, cands, argTys, recvAt)
		} else {
			msg = ambiguousImplMessage(iface, m.Name, recv, choice.fits, distinguishedByParams(choice.fits, iface, m, recv))
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
	var self Type = recv
	if qualifier == "" && recvAt >= 0 {
		// `Contains.contains?(s, x)`: the receiver flows into the
		// interface at the chosen arguments, as an interface-qualified
		// call's receiver always does, so its conformance is recorded.
		self = &InterfaceType{
			Origin:        iface.Origin,
			Name:          iface.Name,
			Methods:       iface.Methods,
			TypeParams:    iface.TypeParams,
			TypeParamDefs: iface.TypeParamDefs,
			TypeArgs:      choice.chosen.args,
			SelfParam:     iface.SelfParam,
		}
	}
	return &implCallSelection{
		iface:  iface,
		method: m.Name,
		recv:   recv,
		chosen: choice.chosen,
		ft:     candidateSig(iface, m, choice.chosen.args, self),
		impl:   candidateSig(iface, m, choice.chosen.args, recv),
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
	c.recordBoundConformance(sel.recv, sel.iface.Name, c.recPos(n.Line, n.Col), RecordingKindCallSite)
	pos := Pos{Line: fa.Field.Line, Col: fa.Field.Col}
	existing := c.fa.References[pos]
	if existing == nil {
		return
	}
	proxy := *existing
	// An inherited default has no function of the block's to name; the
	// signature below still says which impl's it is.
	if impl := implBlockMethod(sel.chosen.info.Block, sel.method); impl != nil {
		proxy.DispatchImpl = impl
		proxy.DispatchReceiver = sel.recv
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

// checkImplInstantiations holds one impl block of a generic interface to the
// rule for a receiver with several impls of it: each writes its type
// arguments, and they are distinct concrete types. An impl generic in them
// stands alone; beside another it overlaps. Two impls at the same arguments
// are the duplicate detectImplCollisions reports.
func (c *checker) checkImplInstantiations(n *ast.ImplBlock) {
	if c == nil || c.fa == nil || n.Interface == nil {
		return
	}
	ifaceName := TypeExprBaseName(n.Interface)
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
		if info == own || info.Block == n || implDeclKey(info) == ownKey || !sameNominalIdentity(own.Origin, ifaceName, info.Origin, ifaceName) {
			continue
		}
		if typesAllEqual(info.Args, own.Args) {
			// The same arguments: this block seen twice, or the duplicate
			// the collision check reports.
			continue
		}
		if own.Receiver != nil && info.Receiver != nil && !typesMayOverlap(own.Receiver, info.Receiver) {
			continue
		}
		seen := false
		for _, o := range others {
			if typesAllEqual(o.Args, info.Args) {
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
	switch at := a.(type) {
	case *ListType:
		bt, ok := b.(*ListType)
		return ok && typesMayOverlap(at.Elem, bt.Elem)
	case *MapType:
		bt, ok := b.(*MapType)
		return ok && typesMayOverlap(at.Key, bt.Key) && typesMayOverlap(at.Val, bt.Val)
	case *TupleType:
		bt, ok := b.(*TupleType)
		return ok && argsMayOverlap(at.Elems, bt.Elems)
	case *FuncType:
		bt, ok := b.(*FuncType)
		return ok && argsMayOverlap(at.Params, bt.Params) && typesMayOverlap(at.Return, bt.Return)
	case *StructType:
		bt, ok := b.(*StructType)
		return ok && at.Name == bt.Name && argsMayOverlap(at.TypeArgs, bt.TypeArgs)
	case *EnumType:
		bt, ok := b.(*EnumType)
		return ok && at.Name == bt.Name && argsMayOverlap(at.TypeArgs, bt.TypeArgs)
	case *DistinctType:
		bt, ok := b.(*DistinctType)
		return ok && at.Name == bt.Name && argsMayOverlap(at.TypeArgs, bt.TypeArgs)
	case *InterfaceType:
		bt, ok := b.(*InterfaceType)
		return ok && at.Name == bt.Name && argsMayOverlap(at.TypeArgs, bt.TypeArgs)
	}
	return false
}
