package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An impl block of a generic owner (`impl Box<T> { ... }`, `impl Unit for
// Wrap<T> { ... }`) puts the owner's type parameters in scope, and a
// function of the block may name one in its body when every call of the
// function determines it. A call of an interface impl's function names its
// implementor (the argument passed as `self`, a type argument, a type
// parameter's instance), which determines every parameter of the block, so
// each of the block's functions may name all of them whatever its
// signature writes: `fn symbol() -> String` in `impl Unit for Wrap<T>`
// calls `T.symbol()`. A call of an inherent function names no receiver
// type, so a function of an inherent block is generic over a parameter of
// the block only when its signature names it. One whose signature names
// none (`fn seven() -> Int`, `fn twice<U>(x: U) -> (U, U)`, or `fn
// find<T>(...)`, whose own `T` shadows the block's) is compiled once,
// without that parameter. Its body therefore cannot name it: no call
// determines it.
//
// checkFunc resolves the body's type names against ownerTypeParams, so a
// use of an undetermined parameter is an unresolved name. This file gives
// that error the reason, at the use.

// ownerTypeParams is the type parameters fn's body may name: those its
// signature ft and its own declaration name (declaredTypeParamsByName),
// and, for a function of an interface impl block, every parameter of the
// block its own do not shadow.
func (c *checker) ownerTypeParams(fn *ast.FuncDef, ft *FuncType) map[string]*TypeParam_ {
	tps := declaredTypeParamsByName(ft, fn.TypeParams)
	ib := c.implBlock
	if !namesEveryBlockParam(ib, fn) {
		return tps
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, ib)
	for name, tp := range blockTP {
		if _, own := tps[name]; own {
			continue
		}
		if tps == nil {
			tps = map[string]*TypeParam_{}
		}
		tps[name] = tp
	}
	return tps
}

// namesEveryBlockParam reports whether fn, a function of impl block ib, is
// generic over every type parameter of the block whatever its signature
// writes: a function of an interface impl block is, and one of an inherent
// block is generic over those its signature names.
func namesEveryBlockParam(ib *ast.ImplBlock, fn *ast.FuncDef) bool {
	return ib != nil && ib.Interface != nil && implBlockOwns(ib, fn)
}

// ImplFuncBlockParams is the type parameters of impl block ib, of those
// named in block, that fd, one of ib's functions, is generic over
// (ownerTypeParams): all of them for an interface impl block, and for an
// inherent block those fd's checked signature names. A call of fd
// determines no other, so the checker records no type argument for one
// (CallTypeArgs), and fd's body cannot name one. The IR builder builds an
// instance of a std generic function at these and the function's own
// (internal/irbuild's stdTemplateParams). block is answered whole when fa
// holds no signature for fd.
func ImplFuncBlockParams(fa *FileAnalysis, fd *ast.FuncDef, ib *ast.ImplBlock, block []string) []string {
	if namesEveryBlockParam(ib, fd) || fa == nil || fd == nil {
		return block
	}
	sym := fa.Definitions[Pos{Line: fd.Line, Col: fd.Col}]
	if sym == nil || sym.Node != fd {
		return block
	}
	ft, _ := sym.Type.(*FuncType)
	if ft == nil {
		return block
	}
	named := collectTypeParamsByName(ft)
	var out []string
	for _, n := range block {
		if _, ok := named[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// explainOwnerTypeParams gives each unresolved-name error that fn's body
// reported since errors[from] its reason, when the name is a type parameter
// of fn's impl block that fn's calls do not determine (ownerTypeParams).
// checkFunc defers it, so it runs with fn's type parameters still in scope
// and sees every error the body reported, whichever path reported it.
func (c *checker) explainOwnerTypeParams(fn *ast.FuncDef, from int) {
	ib := c.implBlock
	if ib == nil || IsSynthesizedLine(ib.Line) || from >= len(c.errors) || !implBlockOwns(ib, fn) {
		return
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, ib)
	owner := TypeExprBaseName(ib.Receiver)
	for i := from; i < len(c.errors); i++ {
		e := &c.errors[i]
		if e.Code != UnknownTypeCode {
			continue
		}
		name := e.Args[0]
		if _, own := c.fnTypeParams[name]; own {
			continue
		}
		if _, ofBlock := blockTP[name]; ofBlock {
			// The error now says why the name is out of reach, so it is
			// no longer an unknown type's.
			e.Message, e.Hints = ownerTypeParamText(name, owner, fn.Name)
			e.Code, e.Args = "", nil
		}
	}
}

// ownerTypeParamText is the error's message and help.
func ownerTypeParamText(name, owner, fn string) (string, []string) {
	return fmt.Sprintf("`%s` is `%s`'s type parameter, and `%s`'s signature does not determine it", name, owner, fn),
		[]string{fmt.Sprintf("use `%s` in the signature of `%s`, or declare the function's own type parameter (`fn %s<%s>(...)`)",
			name, fn, fn, name)}
}

// undeterminedOwnerQualifier reports a `T.member` qualifier naming a type
// parameter of the current function's impl block that the function's
// calls do not determine (ownerTypeParams). The name resolver puts the block's
// parameters in scope for the whole block, so without this the member would
// type permissively and reach the IR builder.
func (c *checker) undeterminedOwnerQualifier(fa *ast.FieldAccess) bool {
	ib, fn := c.implBlock, c.currentFnDecl
	if fa == nil || ib == nil || fn == nil || IsSynthesizedLine(ib.Line) || !implBlockOwns(ib, fn) {
		return false
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType {
		return false
	}
	if _, own := c.fnTypeParams[ti.Name]; own {
		return false
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, ib)
	if _, block := blockTP[ti.Name]; !block {
		return false
	}
	msg, hints := ownerTypeParamText(ti.Name, TypeExprBaseName(ib.Receiver), fn.Name)
	c.report(TypeError{Line: ti.Line, Col: ti.Col, EndLine: ti.Line, EndCol: ti.Col + len(ti.Name), Message: msg, Hints: hints})
	return true
}

// unnamedImplementorInstance reports a call of an interface's function
// through the name of a generic implementor whose impl is for a generic
// instance, when nothing in the call names that instance: `Wrap.symbol()`
// for `fn symbol() -> String` in `impl Unit2 for Wrap<T>`. The function's
// body may name `T` (ownerTypeParams), so it runs at one instance of
// `Wrap`, and a call through the type's name writes no type arguments on
// its qualifier. A signature that names `self` or the implementor (`fn
// show(w: Wrap<T>)`) takes the instance from the arguments or the result,
// as any generic call does, and an impl for one instance (`impl Sized for
// List<Int>`) is that instance; any other signature names none, nor can
// an expected type. The call through the interface names it:
// `Unit2.symbol<Wrap<Meters>>()`. An inherent function needs no instance:
// its body cannot name a parameter its signature does not.
func (c *checker) unnamedImplementorInstance(n *ast.Call, ft *FuncType) bool {
	fa, ok := n.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || ft == nil || c.throughInterface(n) != nil {
		return false
	}
	ref := c.fa.qualifierRef(fa.Object)
	if ref == nil {
		return false
	}
	decl := resolveTypeVar(resolveRef(ref).Type)
	defs, ok := typeParamDefsOf(decl)
	if !ok || len(defs) == 0 {
		return false
	}
	owner := typeQualifiedOwnerBaseName(c, fa.Object)
	providers := c.interfacesDeclaringMethod(owner, c.receiverOriginForReceiver(fa.Object, owner), fa.Field.Name)
	if len(providers) != 1 {
		return false
	}
	named := false
	for _, t := range append(slices.Clone(ft.Params), ft.Return) {
		walkType(t, func(p Type) bool {
			if tp, isParam := p.(*TypeParam_); isParam && tp.Name_ == "self" || interfaceImplName(p) == interfaceImplName(decl) {
				named = true
			}
			return !named
		})
	}
	if named {
		return false
	}
	generic := false
	for _, info := range lookupImplTypeArgSets(interfaceImplName(decl), providers[0].Name, c.implTypeArgsContext()) {
		generic = generic || info != nil && ContainsTypeParam(info.Receiver)
	}
	if !generic {
		return false
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name_
	}
	placeholders := strings.TrimSuffix(strings.Repeat("_, ", len(defs)), ", ")
	args := "(...)"
	if len(ft.Params) == 0 {
		args = "()"
	}
	c.report(errAt(n.Func, fmt.Sprintf(
		"`%s.%s` names no instance of `%s`, and its impl is for every `%s<%s>`, so this call does not say which one it runs for",
		owner, fa.Field.Name, owner, owner, strings.Join(names, ", "))).WithHint(fmt.Sprintf(
		"call it through the interface with the instance as its type argument: `%s.%s<%s<%s>>%s`, with the type for each `_`",
		providers[0].Name, fa.Field.Name, owner, placeholders, args)))
	return true
}

// implBlockOwns reports whether fn is one of ib's items rather than a function
// nested in one of their bodies.
func implBlockOwns(ib *ast.ImplBlock, fn *ast.FuncDef) bool {
	for _, item := range ib.Items {
		if item == ast.Node(fn) {
			return true
		}
	}
	return false
}

// checkImplReceiverArity reports an impl block whose receiver names a generic
// type without its type parameters: `impl Box { ... }` for `struct Box<T>`.
// The block's type parameters come from the receiver (§12), so the header
// writes them: `impl Box<T> { ... }`.
func (c *checker) checkImplReceiverArity(n *ast.ImplBlock) {
	st, bare := n.Receiver.(*ast.SimpleType)
	if !bare {
		return
	}
	t := c.reg.Lookup(st.Name)
	name, want, got, generic := declaredArity(t)
	if !generic || got != 0 || want == 0 {
		return
	}
	params := typeParamDefsForType(t)
	names := make([]string, 0, len(params))
	for _, p := range params {
		if p != nil {
			names = append(names, p.Name_)
		}
	}
	if len(names) != want {
		return
	}
	recv := fmt.Sprintf("%s<%s>", name, strings.Join(names, ", "))
	header := "impl " + recv
	if n.Interface != nil {
		header = fmt.Sprintf("impl %s for %s", n.Interface.TypeString(), recv)
	}
	c.report(TypeError{
		Line: st.Line, Col: st.Col, EndLine: st.Line, EndCol: st.Col + len(st.Name),
		Message: arityMessage(st.Name, want, 0),
		Hints:   []string{fmt.Sprintf("an impl block of a generic type names its type parameters: write `%s`", header)},
	})
}
