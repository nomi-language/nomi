package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// bareImplCall lowers a bare call, inside an impl block, to a function the
// block itself supplies, as the type-qualified call it means. The checker
// resolves the bare name in the block's scope and records at the callee the
// same reference it records at the qualified spelling's method name: the
// declaration, its owner (Symbol.OwningType), and the signature and type
// arguments it instantiated the call at. So the bare call takes the route
// the qualified one takes, over the call as written, and every fact that
// route reads about the call (its instantiation, its defaults, its bare
// operands) is the checker's record at this call:
//
//   - a function declared in the enclosing block (`has(left, key)` in
//     `impl Tree<K>`) is `Tree.has(left, key)`, typeQualCall with the owner
//     the checker recorded;
//   - a function of the block's interface that the block does not write,
//     which the checker resolves to the interface's own function (`shout(v)`
//     in `Formatted`'s default `banner`, lowered for an implementor), is the
//     interface-qualified call `Formatted.shout(v)` at the block's receiver.
//
// handled is false for any other callee.
func (bl *irScalarBuilder) bareImplCall(t *ast.Call, callee *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	d := bl.g.implBlock
	if d == nil || d.decl == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	sym := resolveSymbol(bl.g.srcFA().References[analysis.Pos{Line: callee.Line, Col: callee.Col}])
	if sym == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	var owner string
	switch {
	case sym.OwningType != "" && blockDeclares(d.decl, sym.Node):
		owner = sym.OwningType
	case sym.Kind == analysis.SymbolInterfaceMethod && d.iface != nil && d.iface.decl != nil &&
		sym.OwningInterface == d.iface.decl.Name && d.iface.methods[callee.Name] != nil:
		owner = d.iface.nomi
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	// The qualifier has no position: the checker's record at the callee is
	// the function's, and a qualifier read there would be taken for its
	// owner.
	ti := &ast.TypeIdent{Name: owner}
	lower := func() (ir.Temp, kind, bool, bool) { return bl.typeQualCall(t, ti, callee.Name) }
	var v ir.Temp
	var k kind
	var mobile, ok bool
	if bl.recording > 0 {
		// Inside a VM-only assertion subject the call's rows are recorded
		// after it, as a qualified call's are.
		v, k, mobile, ok = bl.recordedQualCall(t, lower)
	} else {
		v, k, mobile, ok = lower()
	}
	return v, k, mobile, ok, true
}

// blockDeclares reports whether decl is one of impl block ib's functions.
func blockDeclares(ib *ast.ImplBlock, decl ast.Node) bool {
	if decl == nil {
		return false
	}
	for _, item := range ib.Items {
		if item == decl {
			return true
		}
	}
	return false
}
