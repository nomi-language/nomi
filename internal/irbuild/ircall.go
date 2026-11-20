package irbuild

// Call identities: a call site's tail mark and the symbol a callee is
// interned under.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irCallSite is an AST node's tail-position mark as the typed value
// `ir.NewCall` takes.
//
// FALSE FOR A NON-CALL NODE IS CORRECT RATHER THAN UNKNOWN, and that is worth
// stating because a default that stood in for a missing answer would be a lie
// in the shared model. `analysis.MarkTailCalls` sets `IsTailCall` on
// `*ast.Call` and on nothing else, so an `*ast.Binary` reaching an operator
// impl call carries no mark because the front end makes none.
func irCallSite(at ast.Node) ir.CallSite {
	if c, isCall := at.(*ast.Call); isCall && c.IsTailCall {
		return ir.TailCall
	}
	return ir.OrdinaryCall
}

// irCalleeSym interns the callee the producer identifies by token.
//
// THE TOKEN IS THE PRODUCER'S OWN IDENTITY, which is `irTypeOf`'s rule at a
// second use: a POINTER where this package has one for the declaration
// (`*fnSig`, `*stdFunc`, `*implItem`, `*fileFunc`, `*ifaceMethod`), and the
// module-qualified Nomi name where this package's own table is keyed by name —
// the `rt`-builtin families for `List`, `Map`, `Set`, `Vector`, `Channel` and
// `Task`, whose entries are struct VALUES in a `map[string]…` and have no
// address.
//
// A STRING TOKEN AND A POINTER TOKEN CANNOT COLLIDE, which is what makes the
// mixture faithful rather than convenient: `Table.Symbol` keys on `any`, and
// Go compares an interface value by dynamic type first, so a `string` never
// equals a `*fnSig`. Within the string half every name is owner-qualified
// (`List.first`, `Map.get`), so two families' same-named methods are two
// tokens. The hazard, named because it is the only way this goes wrong: a
// family added with an UNQUALIFIED key would share a token with another
// family's method of the same name.
//
// A FUNCTION OF AN IMPL BLOCK IS NAMED BY ITS BLOCK, NOT BY THE CALLER: name
// is not read for a token implFuncOf answers, and the symbol carries the
// block (ir.Table.ImplSymbol), so the function's name does not depend on
// which site lowers first.
func (g *gen) irCalleeSym(token any, name string) *ir.Symbol {
	if f, ok := g.implFuncOf(token); ok {
		return g.irTypes().ImplSymbol(token, f)
	}
	return g.irTypes().Symbol(token, name)
}

// irImplSym is the symbol of the impl function token identifies, which
// implFuncOf answers for: what a site that holds an impl function's token
// interns it by, with no name of its own to offer.
func (g *gen) irImplSym(token any) *ir.Symbol {
	f, ok := g.implFuncOf(token)
	if !ok {
		panic(fmt.Sprintf("irbuild: irImplSym: a %T names no impl function", token))
	}
	return g.irTypes().ImplSymbol(token, f)
}

// implFuncOf is the impl block the function token identifies is declared
// in, and false for a token of anything else. It is the one place an impl
// function's ir.ImplFunc, and so its name, is decided: a program's impl
// function (its implItem), an interface default lowered for another file's
// implementor (ifaceDefaultInst), and a std function by its declaration:
// an instance of a generic one in its own gen (stdInst.impl), any other by
// the block that declares it (declName.impl). A std interface's dispatch
// entry for a type (stdProgThunkToken) forwards to the impl and keeps its
// own name, since one entry serves every instance of its type.
func (g *gen) implFuncOf(token any) (ir.ImplFunc, bool) {
	switch t := token.(type) {
	case *implItem:
		return t.implFunc(), true
	case *ifaceDefaultInst:
		return ir.ImplFunc{Type: kindSpelling(t.recv), Interface: t.iface.nomi, Func: t.m.name}, true
	case *ast.FuncDef:
		if inst := g.stdInstCur; inst != nil && inst.fd == t {
			if inst.impl == nil {
				return ir.ImplFunc{}, false
			}
			return *inst.impl, true
		}
		if d, ok := g.declNameOf(t); ok && d.impl != nil {
			return headerImplFunc(d.impl, t.Name), true
		}
	}
	return ir.ImplFunc{}, false
}

// implFunc is the impl block it is declared in: its receiver as Nomi writes
// it at the instance the block is registered for (the owner's name for an
// owner function, which has no receiver), and its interface.
func (it *implItem) implFunc() ir.ImplFunc {
	d := it.impl
	typ := d.owner
	if typ == "" {
		typ = kindSpelling(d.recv)
	}
	return ir.ImplFunc{Type: typ, Interface: d.ifaceName, Func: it.name}
}

// headerImplFunc is the impl block ib its function name is declared in, as
// the block's header writes the receiver and names the interface.
func headerImplFunc(ib *ast.ImplBlock, name string) ir.ImplFunc {
	f := ir.ImplFunc{Type: typeText(ib.Receiver), Func: name}
	if ib.Interface != nil {
		f.Interface, _ = ifaceHeaderName(ib.Interface)
	}
	return f
}

// irFunctionCallee shares module-function identity across calls, references and
// bodies. A concrete generic instance is a declaration, even when its AST is
// shared with another instance.
func (g *gen) irFunctionCallee(sig *fnSig) *ir.Symbol {
	if sig.instance != nil {
		return g.irCalleeSym(sig.instance, sig.decl.Name)
	}
	return g.irCalleeSym(sig.decl, sig.decl.Name)
}

// irSlotDefault identifies one std function's PER-SLOT default-fill wrapper,
// which is its own declaration.
//
// A distinct token because `*stdFunc` alone would collapse them: a std function
// with two defaulted parameters has two wrappers, `…_default_1` and
// `…_default_2`, and filing two Go declarations under one symbol is exactly the
// receipt-not-identity failure `Table.Symbol`'s comment describes. Caught by
// asking what the token would say if the function had two holes.
type irSlotDefault struct {
	f    *stdFunc
	slot int
}
