package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A function another FILE declares, named as a value: `span.double`,
// `span.ident`, `ident` after `import span.{ident}`, and `io.print`.
//
// A sibling file's `fn` is its declaring unit's symbol, as a call to it links
// (irSiblingCalleeSym); a generic one is the instance the checker instantiated
// the reference at, built by the declaring file's gen (siblinggeneric.go).
//
// `io.print` and `io.inspect` have no body to name: a call renders its
// operand by the operand's kind (hostOutputKey). So the value is a one-
// parameter function whose body is that call over its parameter, at the kind
// the checker instantiated the reference at.
//
// Any other top-level std function (`io.read_line`, `json.shape_error_root`)
// is named the same way: a function of the referenced signature whose body is
// the call stdFilePlan plans over its parameters. A selective import names
// both kinds bare (`print` after `import std::io.print`, `next_line` after
// `import std::io.{read_line as next_line}`), and funcRef sends them here.

// fileFuncRef lowers `owner.name` where owner is a file qualifier. handled is
// false when owner names no file this builder resolves, so the caller's field
// read answers instead.
func (bl *irScalarBuilder) fileFuncRef(t *ast.FieldAccess, owner *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if t.Field == nil || irQualIsLocal(bl, owner.Name) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if g.defaultFilledRef(t) {
		v, k, mobile, ok := bl.callFuncValue(t, owner.Name+"."+t.Field.Name)
		return v, k, mobile, ok, true
	}
	if g.files != nil {
		if to, isSibling := g.files.lookupQualifier(g.fa, owner); isSibling {
			f := g.files.units[to].funcs[t.Field.Name]
			if f == nil {
				return ir.NoTemp, kindInvalid, false, false, false
			}
			v, k, mobile, ok := bl.siblingFuncRef(t, to, f, false)
			return v, k, mobile, ok, true
		}
	}
	std, isStd := stdFileQualifier(g.fa, owner)
	if !isStd {
		// `hex.encode` in std/hex's own tests (stdOwnFileQualifier).
		std, isStd = bl.stdOwnFileQualifier(owner.Name)
	}
	if isStd {
		key := std + "." + t.Field.Name
		if isOutputKey(key) {
			v, k, mobile, ok := bl.outputRef(t, key)
			return v, k, mobile, ok, true
		}
		if v, k, mobile, ok, handled := bl.stdFileFuncRef(t, std, t.Field.Name); handled {
			return v, k, mobile, ok, true
		}
	}
	return ir.NoTemp, kindInvalid, false, false, false
}

// stdBareFuncRef lowers a bare name a selective import bound to a top-level
// std function, named as a value. handled is false for any other name.
func (bl *irScalarBuilder) stdBareFuncRef(t *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	if key, output := stdBareOutputKey(bl.g.srcFA(), t); output {
		v, k, mobile, ok := bl.outputRef(t, key)
		return v, k, mobile, ok, true
	}
	if module, name, isStd := stdBareFileFunc(bl.g.srcFA(), t); isStd {
		return bl.stdFileFuncRef(t, module, name)
	}
	return ir.NoTemp, kindInvalid, false, false, false
}

// stdOwnFuncRef lowers a bare name in a stdlib module's body that names one
// of the module's top-level functions, a sibling of the cached lowering
// (stdlibSibling), as a value: `encode` in std/hex is the value `hex.encode`
// is. In one of the module's own tests (inTest) the sibling resolves before
// the cases gen's own table, as a call to it does; elsewhere funcRef asks
// only once its own table has no lowerable function of the name. handled is
// false for any other name.
func (bl *irScalarBuilder) stdOwnFuncRef(t *ast.Ident, inTest bool) (ir.Temp, kind, bool, bool, bool) {
	f := bl.g.stdlibSibling(t.Name)
	if f == nil || f != bl.g.stdSiblings["."+t.Name] || bl.g.stdModule == "" || inTest != bl.stdTestOwnType() {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if inTest {
		return bl.stdFileFuncRef(t, bl.g.stdModule, t.Name)
	}
	// A body of the module being lowered names the sibling's own
	// declaration, as a call to it links (stdFuncPlan); the module is not
	// yet in the index stdFileFuncRef reads. A value the checker typed
	// shorter than the declaration (defaults filled) has no body to name.
	params, result, ok := bl.g.checkedRefValueKinds(t)
	if !ok || len(params) != len(f.params) || f.decl == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	v, k, mobile, ok := bl.stdNamedFuncRef(t, f, params, result)
	return v, k, mobile, ok, true
}

// stdFileFuncRef is std file module's top-level function name as a function
// value, referenced at ref. handled is false when module declares no
// function of that name.
func (bl *irScalarBuilder) stdFileFuncRef(ref ast.Node, module, name string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, true }
	g := bl.g
	if module == "testing" && name == "check" {
		// `testing.check` is an intrinsic with no body to name, so its
		// value is a function whose body is the check of its parameter
		// (testingCheck), as an output primitive's is (outputRef).
		params, result, ok := g.checkedRefValueKinds(ref)
		if !ok || len(params) != 1 {
			return no()
		}
		v, k, mobile, ok := bl.wrapperRef(ref, "testing.check", params, result, func(child *irScalarBuilder, call *ast.Call) (ir.Temp, kind, bool) {
			dst, k, _, ok := child.testingCheck(call)
			return dst, k, ok
		})
		return v, k, mobile, ok, true
	}
	var generic *stdFunc
	if g.stdInsts != nil {
		generic = stdModuleGenericTemplate(g.stdInsts.std.views[module], "", name)
	}
	if generic == nil && (g.std == nil || g.std.byFile[module+"."+name] == nil) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	params, result, ok := g.checkedRefValueKinds(ref)
	if !ok {
		return no()
	}
	if f := g.std.byFile[module+"."+name]; generic == nil && f != nil && f.decl != nil {
		if g.namedCtlForm(f.decl) != irCtlNone {
			// An iter-sensitive helper (a std module's own, named in its
			// tests) is its body itself, which signals in the adapter's
			// form, as a program's named function is (funcRef).
			v, k, mobile, ok := bl.stdNamedFuncRef(ref, f, params, result)
			return v, k, mobile, ok, true
		}
	}
	if generic != nil {
		// `io.capture`: the instance a call at the checker's instantiation
		// of the reference builds.
		v, k, mobile, ok := bl.wrapperRef(ref, generic.key, params, result, func(child *irScalarBuilder, call *ast.Call) (ir.Temp, kind, bool) {
			dst, k, _, ok, _ := child.stdInstCallAt(call, generic, kindInvalid)
			return dst, k, ok
		})
		return v, k, mobile, ok, true
	}
	v, k, mobile, ok := bl.wrapperRef(ref, module+"."+name, params, result, func(child *irScalarBuilder, call *ast.Call) (ir.Temp, kind, bool) {
		args := child.irQualLowerArgs(call)
		plan := child.stdFilePlan(call, args, module, name)
		if plan == nil {
			return ir.NoTemp, kindInvalid, false
		}
		dst, k, _, ok := child.qualEmit(call, args, plan)
		return dst, k, ok
	})
	return v, k, mobile, ok, true
}

// stdNamedFuncRef is f, a std Nomi body, as a function value of params to
// result referenced at ref: its own declaration, in the signalling form an
// iter-sensitive one is lowered under (namedCtlForm) when ref is an
// iteration callback.
func (bl *irScalarBuilder) stdNamedFuncRef(ref ast.Node, f *stdFunc, params []kind, result kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	ctl := g.namedCtlForm(f.decl)
	if ctl != irCtlNone && bl.ctlRefs[ref] != ctl {
		g.dc.note("an iter-sensitive function passed where its break or continue cannot be caught: " + f.key)
		return no()
	}
	view := f
	if f.canon != nil {
		f = f.canon
	}
	var sym *ir.Symbol
	if f.irBody != nil {
		sym = f.irBody.Sym()
	} else {
		sym = g.irStdSiblingSym(view)
	}
	// params and result are the checker's type for the reference
	// (funcTypeKinds), held, so the function kind is callable.
	fk := funcKindIn(g, params, result)
	if sym == nil {
		return no()
	}
	n := ir.NewRefFunc(g.irNodePos(ref), bl.f.NewTemp(), sym)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: fk, ctl: ctl})
	return n.Dst(), fk, true, true
}

// outputRef is `io.print`, `io.write` or `io.inspect` as a function value: a
// function of one parameter whose body is the output call over it.
func (bl *irScalarBuilder) outputRef(ref ast.Node, key string) (ir.Temp, kind, bool, bool) {
	params, result, ok := bl.g.checkedRefKinds(ref, 1)
	if !ok || result != kindUnit {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.wrapperRef(ref, key, params, result, func(child *irScalarBuilder, call *ast.Call) (ir.Temp, kind, bool) {
		dst, k, _, ok := child.hostOutputKey(call, key)
		return dst, k, ok
	})
}

// wrapperRef is a function value for a callee with no body of its own to
// name, referenced at ref: a function of params whose body is what body
// lowers for a call at ref over those parameters, which must answer result.
func (bl *irScalarBuilder) wrapperRef(ref ast.Node, name string, params []kind, result kind,
	body func(child *irScalarBuilder, call *ast.Call) (ir.Temp, kind, bool)) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	at := g.irNodePos(ref)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	line, col := calleeRefPos(ref)
	call := &ast.Call{Func: ref, Line: line, Col: col}
	for i, pk := range params {
		p := "value"
		if len(params) > 1 {
			p += strconv.Itoa(i)
		}
		child.bound[p] = g.irAddParam(f, ir.NewSymbol(p), pk)
		child.boundK[p] = pk
		call.Args = append(call.Args, &ast.Ident{Name: p, Line: line, Col: col})
	}
	dst, k, ok := body(child, call)
	if !ok || k != result || len(child.captures) != 0 {
		return no()
	}
	child.b.SetTerm(ir.NewReturn(at, dst))
	bl.g.irRequireLint(f, "a function value over "+name)
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(g, params, result)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}

// checkedRefValueKinds is the parameter and result kinds of the function type
// the checker gave the reference at ref (checkedRefFuncType).
func (g *gen) checkedRefValueKinds(ref ast.Node) ([]kind, kind, bool) {
	ft := checkedRefFuncType(g.srcFA(), ref)
	if ft == nil {
		return nil, kindInvalid, false
	}
	return g.funcTypeKinds(ft, len(ft.Params))
}

// checkedRefFuncType is the function type the checker gave the reference at
// ref: the instantiated one a generic reference records, else the type of
// the reference expression. nil when it has neither.
func checkedRefFuncType(fa *analysis.FileAnalysis, ref ast.Node) *analysis.FuncType {
	if fa == nil {
		return nil
	}
	line, col := calleeRefPos(ref)
	if sym := fa.References[analysis.Pos{Line: line, Col: col}]; sym != nil {
		if ft, _ := sym.CallType.(*analysis.FuncType); ft != nil {
			return ft
		}
	}
	ft, _ := fa.ExprTypes[ref].(*analysis.FuncType)
	return ft
}

// siblingFuncRef is unit `to`'s `fn` f as a function value. host marks a
// Go-bound `host fn` reached by a bare name (fileSite.host).
func (bl *irScalarBuilder) siblingFuncRef(ref ast.Node, to int, f *fileFunc, host bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	if g.siblingPrivate(to, f) {
		return no()
	}
	var params []kind
	var result kind
	var sym *ir.Symbol
	if f.generic {
		var ok bool
		params, result, sym, ok = g.siblingGenericInstance(ref, to, f)
		if !ok {
			return no()
		}
	} else {
		if !f.lowerable() {
			return no()
		}
		var ok bool
		params, result, ok = g.siblingSignature(f)
		if !ok {
			return no()
		}
		if host && !irHostFnHasVMBody(params, result) {
			return no()
		}
		sym = g.irSiblingCalleeSym(to, f)
	}
	if sym == nil {
		return no()
	}
	fk := funcKindIn(g, params, result)
	if !irCallableValueKind(fk) {
		return no()
	}
	ctl := g.namedCtlForm(f.decl)
	if ctl != irCtlNone && bl.ctlRefs[ref] != ctl {
		bl.g.dc.note("an iter-sensitive function passed where its break or continue cannot be caught: " + f.name)
		return no()
	}
	n := ir.NewRefFunc(g.irNodePos(ref), bl.f.NewTemp(), sym)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: fk, ctl: ctl})
	return n.Dst(), fk, true, true
}
