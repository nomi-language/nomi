package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

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

// irImplLower lowers an impl function's body, declared or inherited, through
// the function builder a file-level function uses (irScalarLower). A default
// another file declares gets a call to it (irImplForwardDefault).
func (g *gen) irImplLower(d *implDef, it *implItem, params []kind) (kind, bool) {
	fd := irImplSource(it)
	if fd == nil {
		return kindInvalid, false
	}
	if it.inherited && d.iface != nil && d.iface.foreign != "" {
		// Another file's default is lowered there, in its own scope; this
		// block's function calls it (ifacedefault.go).
		return g.irImplForwardDefault(d, it, fd, params)
	}
	sym := g.irImplSym(it)
	sig := irFuncSig{result: it.result, decl: fd, name: sym.Name(), origin: irFromImpl}
	sh := g.irFuncShellWithCallee(fd, sig, params, sym)
	if it.inherited {
		prev := g.defaultSelf
		g.defaultSelf = &irDefaultSelf{iface: d.iface, recv: d.recv, typ: d.recvType}
		defer func() { g.defaultSelf = prev }()
	}
	return g.irScalarLower(fd, sig, sh, it.result)
}

// irDefaultSelf is the implementor an interface default is being lowered
// for. See gen.defaultSelf.
type irDefaultSelf struct {
	iface *ifaceDef
	recv  kind
	// typ is the implementor's checker type, nil where the block's record
	// has none: what the default's `self` type parameter (the interface's
	// analysis.InterfaceType.SelfParam) stands for (genericSubstType).
	typ analysis.Type
}

// projectDefaultSelf answers project for a checked type that is the `self` of
// the default being lowered (analysis.InterfaceType.DefaultSelf, of the
// default's own interface): the implementor's kind. Any other interface type,
// including the same interface named by annotation, is not answered here.
func (g *gen) projectDefaultSelf(ty *analysis.InterfaceType) (kind, bool) {
	ds := g.defaultSelf
	if ds == nil || !ty.DefaultSelf || ds.iface == nil {
		return kindInvalid, false
	}
	d, found := g.solvedIface(ty.Origin, ty.Name)
	if !found || d == nil || d.decl != ds.iface.decl {
		return kindInvalid, false
	}
	return ds.recv, true
}
