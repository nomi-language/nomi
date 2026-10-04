package irbuild

// Call identities: a call site's tail mark and the symbol a callee is
// interned under.

import (
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
// (`List.head`, `Map.get`), so two families' same-named methods are two
// tokens. The hazard, named because it is the only way this goes wrong: a
// family added with an UNQUALIFIED key would share a token with another
// family's method of the same name.
func (g *gen) irCalleeSym(token any, name string) *ir.Symbol {
	return g.irTypes().Symbol(token, name)
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
