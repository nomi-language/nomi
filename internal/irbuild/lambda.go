package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Lambdas and closures.
//
// A Nomi lambda lowers to an `ir.FuncValue` over a body that is an `ir.Func`
// of its own. Two properties of Nomi's closures make that representation
// correct.
//
// # 1. Capture
//
// A closure captures the VALUE each free name holds when the closure is made
// (spec §7 *Closures*). A later rebinding of the name, in the same scope or a nested
// one, is a new binding (spec §6 *Bindings*) that the closure never sees. In the IR a
// capture is an operand of the `ir.FuncValue` that builds the closure, read
// once when it runs, and a rebinding is a fresh `ir.Bind`; nothing more is
// needed. testdata/closures.nomi and testdata/nested_fn.nomi observe both
// kinds of rebinding.
//
// A nested `fn` that names itself cannot capture its own name, which is bound
// only once the closure exists. It refers to itself through its own closure:
// the closure's first capture is the closure itself (ir.NewRecursiveFuncValue,
// irnestedfn.go).
//
// # 2. `return` is lambda-scoped
//
// A `return` inside a Nomi lambda returns from the lambda, not from the
// enclosing `fn`. The lambda's body is its own `ir.Func`, so a return in it
// ends that function's activation at any nesting depth. The work is on the
// type side: a lambda declares no return type, so the builder takes the
// result the checker's records give it (lambdaResultKind) and lowers every
// `return` in the body at that result (returnKind) instead of the enclosing
// function's declared one.
//
// Getting this wrong produces a plausible answer: lowering the lambda body
// into the enclosing function would make `return "positive"` return from
// `scoped`, which is a well-typed program that prints the wrong thing.

// funcKind is the kind of a Nomi function type.
//
// Structural, so its identity is its interned *compKind and not its tag: two
// function types are the same type exactly when their parameter kinds and
// result kind are identical. See composite.go.
func (g *gen) funcKind(params []kind, result kind) kind {
	return funcKindIn(g, params, result)
}

// funcKindIn is funcKind for a caller that may have NO gen — the stdlib
// signature boundary — following listKindIn's and mapKindIn's shape.
//
// tupleKind, listKindIn and mapKindIn all route their only gen access through
// internComp, which handles nil by interning process-wide whenever every
// component is package-neutral, and this does the same. A
// `stdGenStructSpecs` field whose kind is a FUNCTION (`std/random.Generator<T>`'s
// `run`) reaches it with no gen.
func funcKindIn(g *gen, params []kind, result kind) kind {
	// kindInvalid: propagates — a kind CONSTRUCTOR; the lambda`s own decline is counted below.
	if result == kindInvalid {
		return kindInvalid
	}
	nomiParams := make([]string, 0, len(params))
	for _, p := range params {
		// kindInvalid: propagates — a kind CONSTRUCTOR; the parameter`s decline is reported in lambdaParams.
		if p == kindInvalid {
			return kindInvalid
		}
		nomiParams = append(nomiParams, p.nomi())
	}
	nomi := "(" + strings.Join(nomiParams, ", ") + ") -> " + result.nomi()
	parts := make([]kind, 0, len(params)+1)
	parts = append(parts, params...)
	parts = append(parts, result)
	return kind{tag: tagFunc, comp: internComp(g, nomi, parts...)}
}

// funcParams and funcResult split an interned function kind back apart. The
// result is the last part; see funcKind.
func funcParams(k kind) []kind { return k.comp.parts[:len(k.comp.parts)-1] }
func funcResult(k kind) kind   { return k.comp.parts[len(k.comp.parts)-1] }

// --- destructuring parameters -----------------------------------------------
//
// A destructuring parameter is one value and several names. The value arrives
// in one parameter and the names its pattern introduces are bound from it
// before the body by bindPattern, the pattern test every position uses: the
// checker rejects a refutable pattern with a "use a `case` in the body"
// diagnostic, so the test's failure edge is a NoMatch no value reaches.

// patternParamKind is the kind of the value a destructuring parameter receives.
//
// The annotation wins when there is one. When there is not, the self-typing
// rule applies: a pattern whose head NAMES a type is its own annotation
// (`Duration(x)`, `Point{x:, y:}`, `E.V(x)`). A head-less pattern — a tuple, an
// anonymous struct, a dotted variant, a bare identifier — has nothing to read a
// type off, and Nomi requires an annotation there, so its absence here means
// the annotation named a type outside the subset.
func (g *gen) patternParamKind(p ast.Param, at ast.Node) (kind, bool) {
	if p.TypeAnnotation != nil {
		k := g.typeOf(p.TypeAnnotation)
		// kindInvalid: reports — rejectTypeAnnotation names the type's own gap.
		if k == kindInvalid {
			g.rejectTypeAnnotation(p.TypeAnnotation, patternText(p.Destructure), at)
			g.probePattern(p.Destructure)
			return kindInvalid, false
		}
		return k, true
	}
	// The pattern matches the whole parameter, whose type the checker
	// recorded for it: the one its head names (`E.V(x)` names the enum
	// through one of its variants, `Duration(x)` and `Point{x:, y:}` the
	// type), or for a lambda's pattern with no head the one its position
	// expects, as for an unannotated plain parameter.
	k, recorded := g.patternKind(p.Destructure)
	if !recorded {
		g.reject("parameter without a declared type", patternText(p.Destructure), at)
		g.probePattern(p.Destructure)
		return kindInvalid, false
	}
	// kindInvalid: reports — the parameter's type has no representation.
	if k == kindInvalid {
		g.reject("destructuring parameter", patternText(p.Destructure)+", whose type is not a lowered type", at)
		g.probePattern(p.Destructure)
		return kindInvalid, false
	}
	return k, true
}

// patternText is a pattern's spelling for a refusal's detail, which the AST
// does not carry: a pattern node has no TypeString and reconstructing one would
// be a second pretty-printer. The node kind plus the position the refusal
// already carries is enough to find it.
func patternText(pat ast.Node) string {
	if isNilNode(pat) {
		return "<pattern>"
	}
	return constructName(pat)
}

// --- capture ----------------------------------------------------------------
