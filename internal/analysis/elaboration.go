package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// ElabViolation is one place where a checked file does not hand the IR
// builder a fact it needs, and the builder derives the fact itself or
// declines (VerifyElaboration).
type ElabViolation struct {
	Node ast.Node
	Line int
	Col  int
	// Construct is "call", "expr" or "implicit", with "derived " before it
	// for a node on a derive-synthesis line.
	Construct string
	// Category is a short fixed phrase, such as "call: generic callee, type
	// argument open"; the census groups by it.
	Category string
	// Text names the callee or the expression and what is wrong with it.
	Text string
}

func (v ElabViolation) String() string {
	return fmt.Sprintf("%d:%d: %s: %s", v.Line, v.Col, v.Category, v.Text)
}

// VerifyElaboration checks that the checker finished elaborating the file
// fa analyzed, whose top-level declarations are nodes, and returns every
// place it did not. It reads only what the checker recorded (ExprTypes,
// ExpectedTypes, References and each reference's CallType and
// DispatchImpl, Operators, CallDefaults, HoleDisplays, Coercions,
// IterViews, Conversions, the `?` markers) and changes nothing.
// vmhost's TestElaborationGate fails on its call and expression categories,
// in derived bodies too (the analysis package doc, "Elaboration").
//
// The invariant, for a file the checker accepted:
//
//   - Every call has a recorded callee: References holds a symbol at the
//     callee's name (the field of `T.f`, past the dot of `.V(x)`), or the
//     callee is a value expression with a closed function type. When the
//     callee's declared type mentions type parameters other than the
//     enclosing declaration's own, the callee is generic, and the call's
//     reference carries its instantiated signature (CallType). The type
//     arguments are read off that signature by matching it against the
//     declaration (elabMatch); each must be found, and closed. An
//     interface method is generic over its self: the call's reference
//     names the impl it reaches (DispatchImpl) or carries the signature at
//     the receiver (CallType), unless the receiver is a type parameter or
//     an interface value, which the running program dispatches.
//   - Every value expression (the positions UnresolvedExprs counts) has a
//     recorded type, and the type is closed. A call's named callee is not
//     held to this: its type is its declaration's, and the call's record
//     is its instance.
//   - The implicit operations the builder performs on the checker's
//     decision are recorded: an operator over a value that is not a
//     primitive has the record of what it means (Operators: an impl, a
//     comparison by shape, a dispatch on two interface values, or a type
//     parameter's bound), each `?` and `!` has its marker, and a call
//     that leaves defaulted parameters out has the record of what fills
//     them (CallDefaults; a pipe stage's subject fills a slot), each
//     interpolation hole has the record of how its value renders
//     (HoleDisplays), and an expression of a concrete type checked against
//     an interface type has the record of the interface value it becomes
//     (Coercions), unless it is the receiver of a call of an interface
//     function. A source checked against an `Iter<T>` position, which the
//     callee receives as a view over the source, has the record of the
//     view (IterViews). An expression whose type converts to the expected
//     one below its top has the record of the conversion, each part that
//     enters an interface at a closed type (Conversions). An impl block of
//     a generic interface whose
//     header writes no type arguments has the record of the ones it
//     implements (ImplTypeArgSets, ImplBlockTypeArgs). A call of an
//     interface function the checker selected no impl for, with a
//     recorded signature, has the record of the interface instance it
//     goes through (Symbol.CallInterface).
//
// Closed means: no unsolved inference variable anywhere in the type, at
// any depth, and no type parameter other than the enclosing declaration's
// own (the parameters of the `fn`, impl method, interface default or
// nested `fn` whose body holds the node, and of any declaration around
// it), compared by identity as the checker compares them
// (containsForeignTypeParam). An enclosing declaration's own parameter is
// rigid: monomorphization substitutes it, so it is an answer, not a hole.
//
// The language permits an unsolved variable where no value of its type is
// ever made (undetermined.go: `[]`'s element, `Ok(1)`'s error type), and
// the checker accepts such a program and closes the variable for lowering
// (closed_vars.go, TypeVar.Closed). A closed variable is its closed type
// here. One left without a closed type is open, reported under its own
// categories ("nested unsolved variable"), apart from a variable that is
// the value's whole type or a function's parameter or result, which has no
// representation at all (UnresolvedExprs's rule).
//
// A type parameter that is not the enclosing declaration's by identity but
// has the name of one is reported apart too ("foreign type parameter named
// like an own one"): it is either a callee's parameter leaking into the
// caller, or the checker made a second pointer for the caller's own, and
// either way the builder resolves it by spelling.
//
// Nodes on derive-synthesis lines (derived impls, and the functions a
// `phrase` or `noun` expands into) are held to the same invariant, since the
// checker records them as it records written code, and are reported with
// Construct "derived ..." and the declaration around them, having no source
// position.
func VerifyElaboration(fa *FileAnalysis, nodes []ast.Node) []ElabViolation {
	vs, _ := VerifyElaborationCounting(fa, nodes)
	return vs
}

// ElabCounts is how much VerifyElaboration looked at: the denominators of a
// census. Nodes on derive-synthesis lines are counted apart.
type ElabCounts struct {
	// GenericCalls counts the calls of a generic function, method or
	// variant and of an interface method, which is generic over its self.
	Exprs, Calls, GenericCalls                      int
	DerivedExprs, DerivedCalls, DerivedGenericCalls int
}

// VerifyElaborationCounting is VerifyElaboration that also counts the
// value expressions, calls and generic calls it checked.
func VerifyElaborationCounting(fa *FileAnalysis, nodes []ast.Node) ([]ElabViolation, ElabCounts) {
	v := &elabVerifier{fa: fa, scopes: map[ast.Node]map[*TypeParam_]bool{}, names: map[ast.Node]map[string]bool{}, callees: map[ast.Node]bool{}, labels: map[ast.Node]string{}}
	v.receivers = coercionReceivers(fa, nodes)
	for _, top := range nodes {
		v.assignScopes(top, nil, nil, "")
	}
	w := &unresolvedWalk{
		fa: fa, skip: map[ast.Node]bool{}, notValue: map[ast.Node]bool{},
		stage: map[ast.Node]bool{}, where: map[ast.Node]string{},
		synthesized: true,
	}
	w.each = v.visit
	v.stage = w.stage
	for _, top := range nodes {
		ast.Inspect(top, w.visit)
	}
	for _, top := range nodes {
		if ib, ok := top.(*ast.ImplBlock); ok {
			v.checkImplBlockArgs(ib)
		}
	}
	return v.out, v.counts
}

// checkImplBlockArgs reports an impl block of a generic interface whose
// header writes no type arguments when the file has no record of the ones
// it implements (ImplBlockTypeArgs).
func (v *elabVerifier) checkImplBlockArgs(ib *ast.ImplBlock) {
	if ib.Interface == nil || headerWritesTypeArgs(ib.Interface) {
		return
	}
	iface := lookupInterfaceType(v.fa, nil, TypeExprBaseName(ib.Interface))
	if iface == nil || len(iface.TypeParamDefs) == 0 || ImplBlockTypeArgs(v.fa, ib) != nil {
		return
	}
	v.report(ib, "implicit", ElabImplArgsUnrecorded, "impl "+ib.Interface.TypeString())
}

// ElabImplArgsUnrecorded is the category of an impl block of a generic
// interface, its header writing no type arguments, with no record of the
// ones it implements (FileAnalysis.ImplTypeArgSets).
const ElabImplArgsUnrecorded = "implicit: impl block's interface type arguments not recorded"

type elabVerifier struct {
	fa *FileAnalysis
	// scopes is the enclosing declarations' own type parameters at each
	// node, by identity, and names the same by name.
	scopes map[ast.Node]map[*TypeParam_]bool
	names  map[ast.Node]map[string]bool
	// callees are the named callees of the calls visited: a callee's own
	// type is its declaration's, and the call's record is its instance.
	callees map[ast.Node]bool
	// labels names the declaration around each node ("impl Debug for
	// Point fn inspect"), which a report on a synthesized line, having no
	// source position, carries.
	labels map[ast.Node]string
	// stage marks the pipe stages the walk found: a stage's subject fills
	// the call's first slot.
	stage map[ast.Node]bool
	// receivers are the arguments calls of interface functions pass as
	// their receivers (coercionReceivers): checked against the interface,
	// and no coercion.
	receivers map[ast.Node]bool
	out       []ElabViolation
	counts    ElabCounts
}

// assignScopes records the type parameters in scope at n and below it.
func (v *elabVerifier) assignScopes(n ast.Node, scope map[*TypeParam_]bool, names map[string]bool, label string) {
	if n == nil {
		return
	}
	switch d := n.(type) {
	case *ast.ImplBlock:
		label = "impl "
		if d.Interface != nil {
			label += d.Interface.TypeString() + " for "
		}
		if d.Receiver != nil {
			label += d.Receiver.TypeString()
		}
	case *ast.FuncDef:
		label = strings.TrimSpace(label + " fn " + d.Name)
	case *ast.AttachedTest:
		// A `//!` test is a test of its own, checked outside the
		// declaration it is written on: none of the declaration's type
		// parameters is in scope there.
		scope, names = nil, nil
	}
	v.labels[n] = label
	if own := v.declParams(n); len(own) > 0 {
		next := map[*TypeParam_]bool{}
		nextNames := map[string]bool{}
		for p := range scope {
			next[p] = true
		}
		for name := range names {
			nextNames[name] = true
		}
		for _, p := range own {
			next[p] = true
			nextNames[p.Name_] = true
		}
		scope, names = next, nextNames
	}
	v.scopes[n] = scope
	v.names[n] = names
	ast.Children(n, func(c ast.Node) { v.assignScopes(c, scope, names, label) })
}

// declParams is the type parameters n declares, as the checker binds them
// while it checks n's body: a function's from its signature and its
// written `<...>` (declaredTypeParamsByName), an interface's own and its
// self parameter.
func (v *elabVerifier) declParams(n ast.Node) []*TypeParam_ {
	var out []*TypeParam_
	switch d := n.(type) {
	case *ast.FuncDef:
		sym := v.fa.Definitions[Pos{Line: d.Line, Col: d.Col}]
		if sym == nil {
			return nil
		}
		if ft, ok := sym.Type.(*FuncType); ok {
			for _, p := range collectTypeParamsByName(ft) {
				out = append(out, p)
			}
		}
	case *ast.ExternFunc:
		sym := v.fa.Definitions[Pos{Line: d.Line, Col: d.Col}]
		if sym == nil {
			return nil
		}
		if ft, ok := sym.Type.(*FuncType); ok {
			for _, p := range collectTypeParamsByName(ft) {
				out = append(out, p)
			}
		}
	case *ast.StructDef:
		if v.fa.ModuleScope != nil {
			if sym := v.fa.ModuleScope.Lookup(d.Name); sym != nil {
				if st, ok := sym.Type.(*StructType); ok {
					out = append(out, st.TypeParamDefs...)
				}
			}
		}
	case *ast.EnumDef:
		if v.fa.ModuleScope != nil {
			if sym := v.fa.ModuleScope.Lookup(d.Name); sym != nil {
				if et, ok := sym.Type.(*EnumType); ok {
					out = append(out, et.TypeParamDefs...)
				}
			}
		}
	case *ast.InterfaceDef:
		sym := v.fa.Definitions[Pos{Line: d.Line, Col: d.Col}]
		if sym == nil && v.fa.ModuleScope != nil {
			sym = v.fa.ModuleScope.Lookup(d.Name)
		}
		if sym == nil {
			return nil
		}
		if it, ok := sym.Type.(*InterfaceType); ok {
			out = append(out, it.TypeParamDefs...)
			if it.SelfParam != nil {
				out = append(out, it.SelfParam)
			}
			for _, m := range it.Methods {
				for _, p := range collectTypeParamsByName(&FuncType{Params: m.Params, Return: m.Return}) {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func (v *elabVerifier) report(n ast.Node, construct, category, text string) {
	line, col := nodePos(n)
	if IsSynthesizedLine(line) {
		construct = "derived " + construct
		text = v.labels[n] + ": " + text
	}
	v.out = append(v.out, ElabViolation{Node: n, Line: line, Col: col, Construct: construct, Category: category, Text: text})
}

// visit is called for every node the value walk reaches.
func (v *elabVerifier) visit(n ast.Node, value bool) {
	n = v.fa.AsTodo(n)
	if c, ok := n.(*ast.Call); ok {
		if _, named := calleeKey(c.Func); named {
			v.callees[c.Func] = true
		}
	}
	synth := IsSynthesizedLine(n.LineNum())
	if value && !v.callees[n] {
		if synth {
			v.counts.DerivedExprs++
		} else {
			v.counts.Exprs++
		}
		v.checkExpr(n)
	}
	if _, ok := n.(*ast.Call); ok {
		if synth {
			v.counts.DerivedCalls++
		} else {
			v.counts.Calls++
		}
	}
	switch n := n.(type) {
	case *ast.Call:
		v.checkCall(n)
	case *ast.Binary:
		v.checkOperator(n)
	case *ast.TryOp:
		v.checkTryMarker(n, n.Line, n.Col, "`?`")
	case *ast.Unwrap:
		v.checkTryMarker(n, n.Line, n.Col, "`!`")
	case *ast.StringInterp:
		// One read as a typed literal (TargetLiterals) passes its holes to
		// the literal's handler, not through Display.
		if _, typed := v.fa.TargetLiterals[n]; !typed {
			v.checkInterpolation(n.Parts)
		}
	}
}

// checkExpr holds a value expression to a recorded, closed type, and
// lists the interface boxing its position implies.
func (v *elabVerifier) checkExpr(n ast.Node) {
	t, ok := v.fa.ExprTypes[n]
	if !ok || t == nil {
		v.report(n, "expr", "expr: no type", exprLabel(n))
		return
	}
	if cat, why := v.openness(n, t); cat != "" {
		v.report(n, "expr", "expr: "+cat, exprLabel(n)+": "+why)
	}
	if exp, ok := v.fa.ExpectedTypes[n]; ok && exp != nil && !v.receivers[n] {
		if it, iface := resolveTypeVar(exp).(*InterfaceType); iface && concreteType(t) {
			what := fmt.Sprintf("%s: %s as %s", exprLabel(n), resolveTypeVar(t), it)
			switch {
			case isStdIterType(it):
				if v.fa.IterViews[n] == nil {
					v.report(n, "implicit", ElabIterViewUnrecorded, what)
				}
			case v.fa.Coercions[n] == nil:
				v.report(n, "implicit", ElabBoxingUnrecorded, what)
			}
		} else if !iface && concreteType(t) && conversionParts(exp, t, func(*InterfaceType, Type) {}) {
			what := fmt.Sprintf("%s: %s as %s", exprLabel(n), resolveTypeVar(t), resolveTypeVar(exp))
			rec := v.fa.Conversions[n]
			switch {
			case rec == nil:
				v.report(n, "implicit", ElabConversionUnrecorded, what)
			default:
				for _, b := range rec.Boxes {
					if cat, why := v.openness(n, b.Value); cat != "" {
						v.report(n, "implicit", ElabConversionUnrecorded, what+": "+cat+": "+why)
					}
				}
			}
		}
	}
}

// ElabConversionUnrecorded is the category of an expression whose type
// converts to the expected one below its top with no record of the
// conversion, or with a part entering an interface at an open type
// (FileAnalysis.Conversions).
const ElabConversionUnrecorded = "implicit: covariant conversion not recorded"

// ElabBoxingUnrecorded is the category of an expression of a concrete type
// checked against an interface with no record of the coercion
// (FileAnalysis.Coercions).
const ElabBoxingUnrecorded = "implicit: interface boxing not recorded"

// ElabIterViewUnrecorded is the category of a source checked against an
// `Iter<T>` position with no record of the view the callee receives over
// it (FileAnalysis.IterViews).
const ElabIterViewUnrecorded = "implicit: Iter view not recorded"

// openness classifies t at n: "" when closed, else a category and the
// detail.
func (v *elabVerifier) openness(n ast.Node, t Type) (string, string) {
	if unrepresentable(t) {
		return "open type (unsolved variable as the value or a function's parameter or result)", t.String()
	}
	o := v.openParts(n, t)
	switch {
	case o.foreign != "":
		return "open type (foreign type parameter)", o.foreign + " in " + t.String()
	case o.sameName != "":
		return "open type (foreign type parameter named like an own one)", o.sameName + " in " + t.String()
	case o.unsolved:
		return "open type (nested unsolved variable)", t.String()
	}
	return "", ""
}

type openParts struct {
	unsolved          bool
	foreign, sameName string
}

// openParts walks t for unsolved variables and type parameters not in
// scope at n.
func (v *elabVerifier) openParts(n ast.Node, t Type) openParts {
	var o openParts
	scope, names := v.scopes[n], v.names[n]
	seen := map[Type]bool{}
	var walk func(t Type, depth int)
	walk = func(t Type, depth int) {
		t = resolveTypeVar(t)
		if t == nil || depth > 32 || seen[t] {
			return
		}
		switch tt := t.(type) {
		case *TypeVar:
			// A variable the checker closed (closed_vars.go) is its
			// closed type.
			if tt.Closed != nil {
				walk(tt.Closed, depth+1)
			} else {
				o.unsolved = true
			}
		case *TypeParam_:
			if !scope[tt] {
				if names[tt.Name_] {
					if o.sameName == "" {
						o.sameName = tt.Name_
					}
				} else if o.foreign == "" {
					o.foreign = tt.Name_
				}
			}
		case *ListType:
			walk(tt.Elem, depth+1)
		case *MapType:
			walk(tt.Key, depth+1)
			walk(tt.Val, depth+1)
		case *TupleType:
			for _, e := range tt.Elems {
				walk(e, depth+1)
			}
		case *FuncType:
			seen[t] = true
			for _, p := range tt.Params {
				walk(p, depth+1)
			}
			walk(tt.Return, depth+1)
		case *AnonStructType:
			seen[t] = true
			for _, f := range tt.Fields {
				walk(f.Type, depth+1)
			}
		case *EnumType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *StructType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *DistinctType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *InterfaceType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *PartialType:
			walk(tt.Inner, depth+1)
		}
	}
	walk(t, 0)
	return o
}

// concreteType reports whether t is a value's own representation rather
// than an interface, a type parameter or a variable.
func concreteType(t Type) bool {
	switch resolveTypeVar(t).(type) {
	case *InterfaceType, *TypeParam_, *TypeVar, nil:
		return false
	}
	return true
}

// calleeKey is where the checker records a call's callee, as
// attachCallType keys it, and false for a callee that is a value
// expression with no name of its own.
func calleeKey(f ast.Node) (Pos, bool) {
	switch fn := f.(type) {
	case *ast.Ident:
		return Pos{Line: fn.Line, Col: fn.Col}, true
	case *ast.TypeIdent:
		return Pos{Line: fn.Line, Col: fn.Col}, true
	case *ast.FieldAccess:
		if fn.Field != nil {
			return Pos{Line: fn.Field.Line, Col: fn.Field.Col}, true
		}
	case *ast.DotVariant:
		return Pos{Line: fn.Line, Col: fn.Col + 1}, true
	}
	return Pos{}, false
}

// calleeText is the callee as written, for a report.
func elabCalleeText(f ast.Node) string {
	switch fn := f.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.TypeIdent:
		return fn.Name
	case *ast.DotVariant:
		return "." + fn.Name
	case *ast.FieldAccess:
		if fn.Field != nil {
			return elabCalleeText(fn.Object) + "." + fn.Field.Name
		}
	case nil:
		return "?"
	}
	return f.NodeType()
}

func (v *elabVerifier) checkCall(n *ast.Call) {
	name := elabCalleeText(n.Func)
	pos, named := calleeKey(n.Func)
	var sym *Symbol
	if named {
		sym = v.fa.References[pos]
	}
	if sym == nil {
		// A value callee: a lambda, a call's result, a field holding a
		// function. Its function type is the record. A name with no
		// reference is not one, except a field of a value (`r.handler(x)`).
		valueCallee := !named
		if fa, ok := n.Func.(*ast.FieldAccess); ok {
			if _, typed := v.fa.ExprTypes[fa.Object]; typed {
				valueCallee = true
			}
		}
		if ft, ok := v.fa.ExprTypes[n.Func]; ok && ft != nil && valueCallee {
			if _, isFn := resolveTypeVar(ft).(*FuncType); isFn {
				return
			}
		}
		if named {
			v.report(n, "call", "call: no recorded callee", name)
		} else {
			v.report(n, "call", "call: value callee with no function type recorded", name)
		}
		return
	}
	base := sym
	for base.Resolved != nil && base.Resolved != base {
		base = base.Resolved
	}
	switch base.Kind {
	case SymbolParam, SymbolBinding, SymbolField, SymbolOnce:
		// A value call through a name: the binding's type is the record,
		// and checkExpr holds the callee expression to it.
		return
	case SymbolAssertion, SymbolTestSetup, SymbolControlFlow, SymbolTryOp:
		// A keyword form written as a call (`testing.check(c)`): its marker
		// carries what it needs.
		return
	case SymbolInterfaceMethod:
		v.checkInterfaceCall(n, name, sym)
		return
	}
	decl := base.Type
	if decl == nil {
		decl = sym.Type
	}
	if decl == nil {
		v.report(n, "call", "call: callee has no declared type", name+" ("+symbolKindName(base.Kind)+")")
		return
	}
	scope, names := v.scopes[n], v.names[n]
	var calleeParams []*TypeParam_
	declFT, _ := decl.(*FuncType)
	if declFT != nil {
		for _, p := range collectTypeParamsByName(declFT) {
			if !scope[p] {
				calleeParams = append(calleeParams, p)
			}
		}
	} else if containsTypeParamOutside(decl, scope) {
		calleeParams = []*TypeParam_{nil}
	}
	written := len(n.Args)
	if v.stage[n] {
		// A pipe stage's subject fills a slot too.
		written++
	}
	if declFT != nil && declFT.DefaultCount > 0 && written < len(declFT.Params) && v.fa.CallDefaults[n] == nil {
		v.report(n, "implicit", ElabDefaultsUnrecorded,
			fmt.Sprintf("%s: %d of %d written", name, written, len(declFT.Params)))
	}
	if len(calleeParams) == 0 {
		return
	}
	if IsSynthesizedLine(n.Line) {
		v.counts.DerivedGenericCalls++
	} else {
		v.counts.GenericCalls++
	}
	kind := symbolKindName(base.Kind)
	if sym.CallType == nil {
		v.report(n, "call", "call: generic callee, no instantiation recorded", name+" ("+kind+"): "+decl.String())
		return
	}
	ct := sym.CallType
	if cat, why := v.openness(n, ct); cat != "" {
		cat = strings.Replace(cat, "open type", "type argument open", 1)
		v.report(n, "call", "call: generic callee, "+cat, name+" ("+kind+") at "+why)
		return
	}
	if _, ok := resolveTypeVar(ct).(*FuncType); declFT == nil || !ok {
		return
	}
	// Every type parameter of the callee has its argument in the record:
	// read off the signature, or, for one only a where bound names (`T` of
	// `where I: Iter<T>`), in the substitution the checker recorded.
	args := CallTypeArgs(sym)
	var missing []string
	for _, p := range calleeParams {
		if p == nil || names[p.Name_] {
			continue
		}
		if t, ok := args[p.Name_]; !ok {
			missing = append(missing, p.Name_)
		} else if cat, why := v.openness(n, t); cat != "" {
			cat = strings.Replace(cat, "open type", "type argument open", 1)
			v.report(n, "call", "call: generic callee, "+cat, name+" ("+kind+") at "+p.Name_+" = "+why)
			return
		}
	}
	if len(missing) > 0 {
		v.report(n, "call", "call: generic callee, type argument not in the recorded signature",
			fmt.Sprintf("%s (%s): %s unread from %s", name, kind, strings.Join(missing, ", "), ct))
	}
}

// ElabCallInterfaceUnrecorded is the category of a call of an interface
// function the checker selected no impl for, with no closed record of the
// interface instance it goes through (Symbol.CallInterface).
const ElabCallInterfaceUnrecorded = "implicit: interface instance of a dispatched call not recorded"

// ElabDefaultsUnrecorded is the category of a call that leaves a defaulted
// parameter out with no record of what fills it (FileAnalysis.CallDefaults).
const ElabDefaultsUnrecorded = "implicit: defaulted arguments filled, not recorded"

// checkInterfaceCall holds a call of an interface method to a record of
// which impl it reaches. An interface method's symbol has no type of its
// own: the call is generic over the interface's self. The checker records
// the impl it chose (DispatchImpl) or the signature at the receiver
// (CallType). With neither, a concrete receiver means the builder picks
// the impl itself; a receiver that is a type parameter or an interface
// value is dispatched when the program runs, which needs no record. The
// receiver is the first argument, or for a call with none (`T.zero()`,
// `Int.max()`) the type that qualifies it.
func (v *elabVerifier) checkInterfaceCall(n *ast.Call, name string, sym *Symbol) {
	if IsSynthesizedLine(n.Line) {
		v.counts.DerivedGenericCalls++
	} else {
		v.counts.GenericCalls++
	}
	if sym.CallType != nil {
		if cat, why := v.openness(n, sym.CallType); cat != "" {
			cat = strings.Replace(cat, "open type", "type argument open", 1)
			v.report(n, "call", "call: interface method, "+cat, name+" at "+why)
			return
		}
	}
	if sym.DispatchImpl == nil && sym.CallType != nil {
		// Dispatched where it runs: the record names the interface
		// instance the call goes through (call_interfaces.go).
		switch it := sym.CallInterface; {
		case it == nil:
			v.report(n, "implicit", ElabCallInterfaceUnrecorded, name)
		default:
			// Held to what the call's own signature is held to above.
			if cat, _ := v.openness(n, it); cat != "" {
				v.report(n, "implicit", ElabCallInterfaceUnrecorded, name+" through "+it.String())
			}
		}
	}
	if sym.DispatchImpl != nil || sym.CallType != nil {
		return
	}
	// The receiver is the argument at the method's first parameter of the
	// self type (a bare type parameter in its declaration). A method with
	// none (`check(statics)` in CheckedLiteral's default) has its receiver
	// in the type that qualifies it, or the enclosing default's own self.
	recv := interfaceSelfIndex(v.fa, sym)
	if recv < 0 || recv >= len(n.Args) {
		if sym.CallReceiver != "" && !v.names[n][sym.CallReceiver] {
			v.report(n, "call", "call: interface method on a concrete type, no impl recorded", name)
		}
		return
	}
	at, ok := v.fa.ExprTypes[argValue(n.Args[recv])]
	if !ok || at == nil || !concreteType(at) || !v.closed(n, at) {
		return
	}
	v.report(n, "call", "call: interface method on a concrete receiver, no impl recorded",
		fmt.Sprintf("%s on %s", name, resolveTypeVar(at)))
}

// elabMatch binds each type parameter in param to the part of arg at the
// same position, as matchTypeParams does, through interface and distinct
// types too (Iter<T>, Vector<T>).
func elabMatch(param, arg Type, out map[*TypeParam_]Type, depth int) {
	arg = resolveTypeVar(arg)
	if param == nil || arg == nil || depth > 32 {
		return
	}
	args := func(ps, as []Type) {
		if len(ps) == len(as) {
			for i := range ps {
				elabMatch(ps[i], as[i], out, depth+1)
			}
		}
	}
	switch p := param.(type) {
	case *TypeParam_:
		if _, seen := out[p]; !seen {
			out[p] = arg
		}
	case *ListType:
		if a, ok := arg.(*ListType); ok {
			elabMatch(p.Elem, a.Elem, out, depth+1)
		}
	case *MapType:
		if a, ok := arg.(*MapType); ok {
			elabMatch(p.Key, a.Key, out, depth+1)
			elabMatch(p.Val, a.Val, out, depth+1)
		}
	case *TupleType:
		if a, ok := arg.(*TupleType); ok {
			args(p.Elems, a.Elems)
		}
	case *FuncType:
		if a, ok := arg.(*FuncType); ok {
			args(p.Params, a.Params)
			elabMatch(p.Return, a.Return, out, depth+1)
		}
	case *EnumType:
		if a, ok := arg.(*EnumType); ok && a.Name == p.Name {
			args(p.TypeArgs, a.TypeArgs)
		}
	case *StructType:
		if a, ok := arg.(*StructType); ok && a.Name == p.Name {
			args(p.TypeArgs, a.TypeArgs)
		}
	case *DistinctType:
		if a, ok := arg.(*DistinctType); ok && a.Name == p.Name {
			args(p.TypeArgs, a.TypeArgs)
		}
	case *InterfaceType:
		if a, ok := arg.(*InterfaceType); ok && a.Name == p.Name {
			args(p.TypeArgs, a.TypeArgs)
		}
	case *AnonStructType:
		if a, ok := arg.(*AnonStructType); ok && len(a.Fields) == len(p.Fields) {
			for i := range p.Fields {
				elabMatch(p.Fields[i].Type, a.Fields[i].Type, out, depth+1)
			}
		}
	}
}

func (v *elabVerifier) closed(n ast.Node, t Type) bool {
	cat, _ := v.openness(n, t)
	return cat == ""
}

func containsTypeParamOutside(t Type, scope map[*TypeParam_]bool) bool {
	found := false
	var walk func(t Type, depth int)
	walk = func(t Type, depth int) {
		if t == nil || found || depth > 32 {
			return
		}
		switch tt := resolveTypeVar(t).(type) {
		case *TypeParam_:
			found = !scope[tt]
		case *ListType:
			walk(tt.Elem, depth+1)
		case *MapType:
			walk(tt.Key, depth+1)
			walk(tt.Val, depth+1)
		case *TupleType:
			for _, e := range tt.Elems {
				walk(e, depth+1)
			}
		case *EnumType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *StructType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		case *DistinctType:
			for _, a := range tt.TypeArgs {
				walk(a, depth+1)
			}
		}
	}
	walk(t, 0)
	return found
}

// elabOperatorOps are the operators that go through an interface when
// an operand is not a primitive.
var elabOperatorOps = map[string]bool{
	"+": true, "-": true, "*": true, "/": true,
	"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
}

// The categories of an operator over a value that is not a primitive with
// no record of what it means (FileAnalysis.Operators), by the left
// operand's type.
const (
	ElabOperatorNominal    = "implicit: operator impl not recorded (nominal type)"
	ElabOperatorStructural = "implicit: operator over a structural type, no record"
	ElabOperatorTypeParam  = "implicit: operator over a type parameter, no record"
)

func (v *elabVerifier) checkOperator(n *ast.Binary) {
	if !elabOperatorOps[n.Op] {
		return
	}
	lt, ok := v.fa.ExprTypes[n.Left]
	if !ok || lt == nil {
		return
	}
	if _, prim := resolveTypeVar(lt).(*PrimitiveType); prim {
		return
	}
	if v.fa.Operators[n] != nil {
		return
	}
	cat := ElabOperatorNominal
	switch resolveTypeVar(lt).(type) {
	case *TupleType, *AnonStructType, *ListType, *MapType, *FuncType:
		cat = ElabOperatorStructural
	case *TypeParam_:
		cat = ElabOperatorTypeParam
	}
	v.report(n, "implicit", cat, fmt.Sprintf("%s on %s", n.Op, resolveTypeVar(lt)))
}

func (v *elabVerifier) checkTryMarker(n ast.Node, line, col int, what string) {
	sym := v.fa.References[Pos{Line: line, Col: col}]
	if sym == nil || sym.TryOp == nil {
		v.report(n, "implicit", "implicit: "+what+" propagation not recorded", what)
	}
}

func (v *elabVerifier) checkInterpolation(parts []ast.StringPart) {
	for _, p := range parts {
		e, ok := p.(ast.StringExpr)
		if !ok || e.Expr == nil {
			continue
		}
		t, ok := v.fa.ExprTypes[e.Expr]
		if !ok || t == nil {
			continue
		}
		if v.fa.HoleDisplays[e.Expr] == nil {
			v.report(e.Expr, "implicit", ElabDisplayUnrecorded, resolveTypeVar(t).String())
		}
	}
}

// ElabDisplayUnrecorded is the category of an interpolation hole with no
// record of how its value renders (FileAnalysis.HoleDisplays).
const ElabDisplayUnrecorded = "implicit: interpolation's Display call not recorded"

func symbolKindName(k SymbolKind) string {
	switch k {
	case SymbolFunction:
		return "function"
	case SymbolStruct:
		return "struct"
	case SymbolEnum:
		return "enum"
	case SymbolEnumVariant:
		return "variant"
	case SymbolType:
		return "type"
	case SymbolTypeAlias:
		return "type alias"
	case SymbolInterface:
		return "interface"
	case SymbolInterfaceMethod:
		return "interface method"
	case SymbolModule:
		return "module"
	}
	return fmt.Sprintf("kind %d", k)
}
