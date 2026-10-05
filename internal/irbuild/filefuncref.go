package irbuild

import (
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
	}
	return ir.NoTemp, kindInvalid, false, false, false
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

// outputRef is `io.print`, `io.write` or `io.inspect` as a function value: a function of
// one parameter whose body is the output call over it.
func (bl *irScalarBuilder) outputRef(t *ast.FieldAccess, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	params, result, ok := g.checkedRefKinds(t, 1)
	if !ok || result != kindUnit {
		return no()
	}
	at := g.irNodePos(t)
	f := ir.NewFunc(at, key)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	const name = "value"
	child.bound[name] = g.irAddParam(f, ir.NewSymbol(name), params[0])
	child.boundK[name] = params[0]
	arg := &ast.Ident{Name: name, Line: t.Field.Line, Col: t.Field.Col}
	call := &ast.Call{Func: t, Args: []ast.Node{arg}, Line: t.Field.Line, Col: t.Field.Col}
	dst, k, _, ok := child.hostOutputKey(call, key)
	if !ok || k != kindUnit || len(child.captures) != 0 {
		return no()
	}
	child.b.SetTerm(ir.NewReturn(at, dst))
	if err := ir.Lint(f); err != nil {
		irDeclineNote("an output function value that does not lint: " + err.Error())
		return no()
	}
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(g, params, kindUnit)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}
