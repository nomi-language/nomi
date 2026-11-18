package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The signature of a bound-free generic function whose type parameters are
// mentioned only bare.
//
// # What runs
//
// Every call and every function value of a generic `fn` lowers an instance of
// a template (irMonoTemplate, irfuncbody.go), one per type-argument tuple. No
// call reaches the declaration's own function.
//
// declaredSignature (native.go) asks dictSignature, then genericSignature
// here, then monoTemplateFor. genericSignature claims a declaration with no
// bound and no `where` clause whose every type-parameter mention is a bare
// `T`. Its signature holds each type parameter as a kind of its own
// (tagTypeParam) and `sig.tps` lists them.
//
// funcDecl attempts the body of a declaration claimed here, with each type
// parameter as `ir.AnyType`. The attempt declines with "an ident kind outside
// the domain: T" when the body reads a value of a type-parameter kind. When
// the body reads none, the function is retained and nothing references it.
//
// # The line it draws
//
// `T.method(x)` resolves by scanning the type parameter's bounds for one whose
// interface declares the method. With zero bounds there is nothing to scan,
// so a bound-free type parameter cannot host a type-parameter-qualified call.
// A value of a bound-free `T` is only moved: there is no interface to go
// through and no impl to look up.
//
//   - Bound-free, every mention a bare type parameter: claimed here.
//   - Anything with a bound or a `where` clause: genericSignature answers
//     `generic function`. dictSignature (dict.go) is asked before it.
//
// typeParamsNeedDictionary is that line, written once so the two halves cannot
// drift apart.
//
// # What genericSignature declines
//
//   - A type parameter mentioned anywhere but bare: `List<T>`, `Maybe<T>`,
//     `Pair<A, B>`, `(T, Int)`, `(A) -> C`.
//   - A type parameter no parameter mentions.
//   - A parameter with a default, a destructuring pattern or no annotation.
//   - A decorator, a missing body, `main`.
//
// For each, declaredSignature asks monoTemplateFor (genericmono.go), and
// funcDecl reports `generic function` at the declaration when that answers
// nil too.

// typeParamDef is one type parameter's identity.
//
// A declaration, so kind equality is Go pointer equality and two functions'
// `T` are two kinds however identically they spell, which is the rule
// native.go's `kind` comment states for named types, applied here for the same
// reason. Interning on the spelling would collapse two functions' `T`s, and a
// call inside a generic body would then be able to bind one function's `T`
// from another's.
type typeParamDef struct {
	// nomi is the source spelling, for a refusal's detail text.
	nomi string
	// fn is the declaring function's name. Diagnostics only; identity is the
	// pointer.
	fn string
}

// typeParamKind is the kind of values of a bound-free type parameter.
func typeParamKind(d *typeParamDef) kind { return kind{tag: tagTypeParam, tp: d} }

// typeParamsNeedDictionary reports whether fd's type parameters carry any
// constraint that makes its body dispatch-dependent, and so belongs to the
// dictionary seam rather than here.
//
// TRUE for any bound on any type parameter and for any `where` clause. Both
// halves are read, not just one: inline `<T: Comparable>` is a PARSE ERROR in
// Nomi (parseTypeParams rejects it by name and tells the programmer to write a
// `where` clause), so every source-written bound arrives in WhereClauses — but
// `TypeParam.Bounds` carries bounds the ANALYZER synthesized
// (`analysis/builder.go`, `analysis/derive_synthesis.go`), and irbuild reads the
// AST after analysis. Reading only WhereClauses would therefore admit a
// synthesized bound as bound-free.
//
// `Debug` is not special-cased even though the analyzer erases it as universal.
// Any bound means the seam, so there is one rule instead of a rule plus an
// exception nobody re-checks.
func typeParamsNeedDictionary(fd *ast.FuncDef) bool {
	if len(fd.WhereClauses) > 0 {
		return true
	}
	for _, tp := range fd.TypeParams {
		if len(tp.Bounds) > 0 {
			return true
		}
	}
	return false
}

// genericSignature resolves a generic `fn`'s parameter and result kinds with its
// type parameters as kinds of their own, or names why it cannot.
//
// Answers why == "" and tps == nil for a NON-generic declaration, so signature()
// can call it unconditionally and keep one place that decides.
func (g *gen) genericSignature(fd *ast.FuncDef) (params []kind, result kind, tps []*typeParamDef, why string) {
	if len(fd.TypeParams) == 0 && len(fd.WhereClauses) == 0 {
		return nil, kindInvalid, nil, ""
	}
	refuse := func() ([]kind, kind, []*typeParamDef, string) {
		return nil, kindInvalid, nil, "generic function"
	}
	if typeParamsNeedDictionary(fd) || len(fd.TypeParams) == 0 {
		return refuse()
	}
	// A generic `fn` reaching funcDecl through any of these is refused by
	// funcDecl's own clause for that reason, at its own position — so it must
	// not be admitted here and lowered past those clauses.
	if len(fd.Decorators) > 0 || fd.Body == nil || fd.Name == "main" {
		return refuse()
	}
	byName := make(map[string]*typeParamDef, len(fd.TypeParams))
	tps = make([]*typeParamDef, len(fd.TypeParams))
	for i, tp := range fd.TypeParams {
		d := &typeParamDef{nomi: tp.Name, fn: fd.Name}
		tps[i] = d
		byName[tp.Name] = d
	}
	// bare answers the kind a signature position names, resolving a LONE type
	// parameter to its own kind and everything else through typeOf. A type
	// parameter mentioned INSIDE a larger type is not resolved: typeOf has no
	// arm for one and answers kindInvalid, which is the refusal. So the depth
	// rule holds by construction rather than by a second walk that could
	// disagree with it.
	bare := func(te ast.TypeExpr) kind {
		if st, isSimple := te.(*ast.SimpleType); isSimple {
			if d, isParam := byName[st.Name]; isParam {
				return typeParamKind(d)
			}
		}
		return g.typeOf(te)
	}
	used := make(map[*typeParamDef]bool, len(tps))
	params = make([]kind, len(fd.Params))
	for i, p := range fd.Params {
		switch {
		case p.Destructure != nil, p.TypeAnnotation == nil, p.Default != nil:
			return refuse()
		}
		k := bare(p.TypeAnnotation)
		// A type parameter mentioned inside a larger type lands here, and that
		// is the depth rule: funcDecl reports `generic function` for the whole
		// declaration at its own position.
		// kindInvalid: reports — funcDecl names `generic function` at the declaration.
		if k == kindInvalid {
			return refuse()
		}
		if k.tag == tagTypeParam {
			used[k.tp] = true
		}
		params[i] = k
	}
	// Every type parameter must be inferable from a parameter. One that is not
	// cannot be produced inside the body either.
	for _, d := range tps {
		if !used[d] {
			return refuse()
		}
	}
	result = kindUnit
	if fd.ReturnTypeExpr != nil {
		result = bare(fd.ReturnTypeExpr)
		// kindInvalid: reports — funcDecl names `generic function` at the declaration.
		if result == kindInvalid {
			return refuse()
		}
	}
	return params, result, tps, ""
}
