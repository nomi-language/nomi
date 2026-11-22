package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// bareImplCall lowers a bare call of a function declared under a type or
// an interface as the qualified call it means. The checker resolves
// the bare name in the scope of the impl block or interface that writes the
// call and records at the callee the same reference it records at the
// qualified spelling's method name: the declaration, the type or interface
// it is declared under (analysis.Callee.Owner), and the signature and type
// arguments it instantiated the call at. So the bare call takes the route
// the qualified one takes, over the call as written, and every fact that
// route reads about the call (its instantiation, its defaults, its bare
// operands) is the checker's record at this call. Nothing here reads where
// the call is being lowered, so a body of the block and a parameter default
// it declares, lowered at a caller or in the declaring file's accessor
// (irdefaults.go), lower the call alike:
//
//   - a function an impl block declares (`has(left, key)` in
//     `impl Tree<K>`, `side(q)` in a default of `impl Sq`) is
//     `Tree.has(left, key)`, typeQualCall with the type it is declared
//     under;
//   - a function of an interface (`shout(v)` in `Formatted`'s default
//     `banner`, lowered for an implementor; `side(value)` in a parameter
//     default of `Shape.area`) is the interface-qualified call
//     `Formatted.shout(v)`, which reaches the receiver's function.
//
// handled is false for any other callee.
func (bl *irScalarBuilder) bareImplCall(t *ast.Call, callee *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	target := bl.g.calleeOf(t)
	sym := resolveSymbol(target.Ref)
	if sym == nil || target.Qualifier.Kind != analysis.QualNone {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch o := target.Owner; {
	case o.Kind == analysis.QualType && sym.OwningType != "":
	case o.Kind == analysis.QualInterface && sym.Kind == analysis.SymbolInterfaceMethod && sym.OwningInterface != "":
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	lower := func() (ir.Temp, kind, bool, bool) { return bl.typeQualCall(t, callee.Name) }
	var v ir.Temp
	var k kind
	var mobile, ok bool
	if bl.recording > 0 {
		// Inside an assertion subject the call's rows are recorded
		// after it, as a qualified call's are.
		v, k, mobile, ok = bl.recordedQualCall(t, lower)
	} else {
		v, k, mobile, ok = lower()
	}
	return v, k, mobile, ok, true
}
