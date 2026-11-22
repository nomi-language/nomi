package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Parameter-shape sugar: default values, destructuring parameters, and the slot
// an argument actually fills.
//
// All three are places where one Nomi parameter is not one lowered parameter.
// They are together because they share one rule: a parameter list is resolved
// in the CALLEE's environment, and by SLOT rather than by position.
//
// # Where a default is evaluated, and why it is not the call site
//
// It reads like call-site sugar, but Nomi does not evaluate it there. For
// each parameter the call left out, the default is evaluated in the callee's
// scope: a fresh environment off the FUNCTION'S OWN CLOSURE, after parameters
// 0..i-1 have been bound into it. Three consequences, and each one is
// observable:
//
//  1. A default may name an earlier PARAMETER. `fn f(a: Int, b: Int = a + 1)`
//     works, because `a` is already bound when `b`'s default runs, and so
//     does `c: Int = b * 10` after it.
//  2. A default may NOT name a caller local, and a default that names a
//     module-level binding sees the MODULE's, even where the caller shadows
//     that name. Lowering the expression in the caller's scope would resolve it
//     the other way round.
//  3. It is evaluated once per activation that omits it, and NOT at all when
//     the argument is supplied — so a default with a side effect fires exactly
//     as often as the call omits it. testdata/defaults.nomi prints from inside
//     a default and calls the function both ways, which is what distinguishes
//     this from a lowering that hoisted the default to a constant.
//
// That scope is also an assertion-trace boundary, so nothing a default computes
// reaches an enclosing assertion's report. Same seam as a lambda body.
//
// # Where a default is lowered
//
// fillCallDefaults (irdefaults.go) is the one place a call's omitted operands
// are filled. A default this file declares is lowered at the call, in the
// callee's scope, after the written operands. Another file's is a call of
// the declaring file's accessor for the slot (siblingdefault.go).
//
// # A destructuring parameter
//
// One value, several names. The value arrives in one parameter and the
// names its pattern introduces are bound from it before the body, by the
// pattern test every position uses (irScalarBuild, bindPattern). The
// checker rejects a refutable pattern with a "use a `case` in the body"
// diagnostic, so the test's failure edge is a NoMatch no value reaches.

// paramKind is a parameter's kind, resolved without reporting a refusal:
// its annotation's, the type the checker recorded for its pattern, or for
// a parameter written with neither the type the checker gave it on its
// symbol (paramSymbolType): a lambda's from the function type its position
// expects or from the parameter at the same place in another element of
// its collection literal, any parameter's from its default. kindInvalid
// where there is none.
//
// signature() runs before any body, for every declaration in the module
// including ones no call reaches, so it must not blame a position — funcDecl
// names the reason for the parameters it lowers. The two must agree on
// WHICH parameters resolve, which is why this is the one implementation and
// patternParamKind is its reporting counterpart.
func (g *gen) paramKind(p ast.Param) kind {
	if p.TypeAnnotation != nil {
		return g.typeOf(p.TypeAnnotation)
	}
	if p.Destructure == nil {
		return g.project(paramSymbolType(g.srcFA(), p))
	}
	// The pattern matches the whole parameter, so its kind is the one the
	// checker recorded for the pattern: the type its head names (the
	// self-typing rule), or the type a lambda's position gives it. See
	// patternParamKind for the reporting version.
	k, _ := g.patternKind(p.Destructure)
	return k
}

// --- call-site argument placement ------------------------------------------

// argExpr is the expression an argument contributes. For a named argument that
// is its VALUE; the name is placement, not a value.
func argExpr(a ast.Node) ast.Node {
	if na, ok := a.(*ast.NamedArg); ok {
		return na.Value
	}
	return a
}

// sigParamNames is the callee's parameter names positionally, or nil when the
// signature has no declaration behind it.
//
// A synthesized signature records parameter kinds only.
func sigParamNames(sig *fnSig) []string {
	if sig == nil || sig.decl == nil || len(sig.decl.Params) != len(sig.params) {
		return nil
	}
	names := make([]string, len(sig.decl.Params))
	for i, p := range sig.decl.Params {
		names[i] = p.Name
	}
	return names
}
