package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// irImplSource presents a source-written inherited body to the common function
// builder. The implItem, not this view or its shared body, owns callee identity.
func irImplSource(it *implItem) *ast.FuncDef {
	if it.decl != nil {
		return it.decl
	}
	if !it.inherited || it.body == nil {
		return nil
	}
	return &ast.FuncDef{Name: it.name, Params: it.params0, Body: it.body, Line: it.line}
}

// irImplLower offers declared and inherited source bodies to the same builder
// and Go reader. Unsupported bodies and tail drivers keep native lowering.
func (g *gen) irImplLower(d *implDef, it *implItem, plan *tailPlan, params []kind) (kind, bool) {
	fd := irImplSource(it)
	if fd == nil {
		return kindInvalid, false
	}
	name := d.recv.nomi() + "." + it.name
	sig := irFuncSig{result: it.result, decl: fd, name: name, origin: irFromImpl}
	sh := g.irFuncShellWithCallee(fd, sig, params, g.irCalleeSym(it, name))
	if ef, isHost := hostExtOf(fd); isHost {
		return g.irImplHostLower(d, ef, sh, it.result)
	}
	return g.irScalarLower(fd, sig, plan, sh, it.result)
}

// hostTemplateImplPlan resolves `Box.origin()` against a generic receiver's
// Go-bound impl function when no instance of `Box` is selected: its operands
// name no `Box<T>`, so nothing solves T. The Go function is the same at every
// instance, so the call crosses to it directly. A signature that names the
// template's type parameters does not resolve here and declines.
func (bl *irScalarBuilder) hostTemplateImplPlan(t *ast.Call, args irQualArgs, tpl *genericTemplate, method string) *irQualPlan {
	g := bl.g
	if g.stdModule != "" {
		return nil
	}
	blocks := append([]*ast.ImplBlock(nil), tpl.hostImpls...)
	for _, it := range tpl.impls {
		blocks = append(blocks, it.decl)
	}
	for _, ib := range blocks {
		for _, item := range ib.Items {
			ef, isExt := item.(*ast.ExternFunc)
			if !isExt || ef.Name != method || !hostBoundImplFn(ef) {
				continue
			}
			params := make([]kind, len(ef.Params))
			for i, p := range ef.Params {
				if p.TypeAnnotation == nil || p.Default != nil {
					return nil
				}
				// kindInvalid: reports — a parameter naming T, or a type outside the domain, declines the plan.
				if params[i] = g.typeOf(p.TypeAnnotation); params[i] == kindInvalid {
					return nil
				}
			}
			result := kindUnit
			if ef.ReturnTypeExpr != nil {
				// kindInvalid: reports — as a parameter's does.
				if result = g.typeOf(ef.ReturnTypeExpr); result == kindInvalid {
					return nil
				}
			}
			if !irHostFnHasVMBody(params, result) || !bl.qualSignature(t, args, params, result) {
				return nil
			}
			key := hostImplKey(ib, ef.Name)
			g.irHostKeys = append(g.irHostKeys, key)
			return &irQualPlan{token: irHostBinding{ef}, name: key, result: result, host: true}
		}
	}
	return nil
}

// implItemIsHost reports whether an impl item's declaration is the shell of a
// Go-bound impl function.
func implItemIsHost(fd *ast.FuncDef) bool {
	ef, isHost := hostExtOf(fd)
	return isHost && hostBoundImplFn(ef)
}

// irImplHostLower builds a Go-bound impl function's VM body: one crossing
// under the key the FFI wrapper registers it by (hostImplKey). Every instance
// of a generic block crosses to the one Go function.
func (g *gen) irImplHostLower(d *implDef, ef *ast.ExternFunc, sh *irFuncShell, result kind) (kind, bool) {
	decline := func(reason string) (kind, bool) {
		g.irDeclineOpen(ef.Name)
		irDeclineNote(reason)
		return kindInvalid, false
	}
	switch {
	case g.stdModule != "":
		return decline("a Go-bound impl function in the stdlib")
	case sh == nil:
		return decline("no function shell")
	case !irHostFnHasVMBody(nil, result):
		return decline("a `host fn` returning a function, which a Go func cannot become")
	}
	if why := g.irHostCrossingBody(ef, sh, result, hostImplKey(d.decl, ef.Name)); why != "" {
		return decline(why)
	}
	return result, true
}
