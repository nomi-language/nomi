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
// Any other top-level std function (`io.read_file`, `json.shape_error_root`)
// is named the same way: a function of the referenced signature whose body is
// the call stdFilePlan plans over its parameters. A selective import names
// both kinds bare (`print` after `import std/io.print`, `next_line` after
// `import std/io.{read_line as next_line}`), and funcRef sends them here.

// fileFuncRef lowers `owner.name` where owner is a file qualifier. handled is
// false when owner names no file this builder resolves, so the caller's field
// read answers instead.
func (bl *irScalarBuilder) fileFuncRef(t *ast.FieldAccess, owner *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if t.Field == nil || irQualIsLocal(bl, owner.Name) {
		return ir.NoTemp, kindInvalid, false, false, false
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
	if std, isStd := stdFileQualifier(g.fa, owner); isStd {
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
	if key, output := stdBareOutputKey(bl.g.fa, t); output {
		v, k, mobile, ok := bl.outputRef(t, key)
		return v, k, mobile, ok, true
	}
	if module, name, isStd := stdBareFileFunc(bl.g.fa, t); isStd {
		return bl.stdFileFuncRef(t, module, name)
	}
	return ir.NoTemp, kindInvalid, false, false, false
}

// stdFileFuncRef is std file module's top-level function name as a function
// value, referenced at ref. handled is false when module declares no
// function of that name.
func (bl *irScalarBuilder) stdFileFuncRef(ref ast.Node, module, name string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, true }
	g := bl.g
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
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a function value over " + name + " that does not lint: " + err.Error())
		return no()
	}
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(g, params, result)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}

// checkedRefValueKinds is the parameter and result kinds of the function type
// the checker gave the reference at ref (checkedRefFuncType).
func (g *gen) checkedRefValueKinds(ref ast.Node) ([]kind, kind, bool) {
	ft := checkedRefFuncType(g.fa, ref)
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
	var params []kind
	var result kind
	var sym *ir.Symbol
	if f.generic {
		var ok bool
		params, result, sym, ok = g.siblingGenericRef(ref, to, f)
		if !ok {
			return no()
		}
	} else {
		if !f.lowerable() || f.defaults {
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
	n := ir.NewRefFunc(g.irNodePos(ref), bl.f.NewTemp(), sym)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}
