package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Default type arguments (spec §4 *Default type arguments*). A type
// annotation that leaves out arguments a declaration gives defaults to,
// `route: Route` for `Route<P = Unit>`, means the declaration at its
// defaults. The analyzer resolves the annotation so; this builder resolves
// annotations itself (typeOf), so it writes the left-out arguments in
// first: `Route` is read as `Route<Unit>`. Which declaration the name
// means is the analyzer's answer, the reference it recorded at the name.
//
// A default names what it names where the declaration wrote it, as the
// checker resolves it: `Box<T = Config>` declared beside an import of
// Config means that Config in a file that imports only Box, and in one
// that declares a Config of its own. So a program declaration's default
// is resolved by the gen of the file that declares it, at module scope,
// and its kind is carried into this gen (importKind). The default's node
// stands in the written arguments, and typeOf answers it from
// defaultArgKinds rather than reading its names here.

// withDefaultTypeArgs answers te with the arguments it leaves out written
// as their defaults, or te itself.
func (g *gen) withDefaultTypeArgs(te ast.TypeExpr) ast.TypeExpr {
	var name string
	var args []ast.TypeExpr
	var line, col int
	switch t := te.(type) {
	case *ast.SimpleType:
		name, line, col = t.Name, t.Line, t.Col
	case *ast.GenericType:
		name, args, line, col = t.Name, t.Params, t.Line, t.Col
	default:
		return te
	}
	decl, tps := g.declaredTypeParams(line, col, name)
	if len(args) >= len(tps) || tps[len(args)].Default == nil {
		return te
	}
	full := append([]ast.TypeExpr(nil), args...)
	for _, tp := range tps[len(args):] {
		if tp.Default == nil {
			return te
		}
		g.resolveDefaultArg(decl, tp.Default)
		full = append(full, tp.Default)
	}
	return &ast.GenericType{Name: name, Params: full, Line: line, Col: col}
}

// resolveDefaultArg records in defaultArgKinds what def, a default type
// argument of decl, means in the file that declares decl. A kind is kept
// for the gen; a refusal is asked again at the next use, since the
// declaring file's types may not have been built at this one. A
// declaration no program file declares at module level (a block-local
// one) keeps its default's syntax, which this gen resolves where the
// declaration's scope is still open.
func (g *gen) resolveDefaultArg(decl ast.Node, def ast.TypeExpr) {
	// kindInvalid: lookup — a cached refusal is asked again; a miss falls through to typeOf, which reports.
	if k, done := g.defaultArgKinds[def]; (done && k != kindInvalid) || g.reg == nil {
		return
	}
	o := g.reg.byDecl[decl]
	if o == nil || o.unit < 0 || o.unit >= len(g.reg.gens) || g.reg.gens[o.unit] == nil {
		return
	}
	if o.unit != g.fileUnit && !g.reg.ensureTypes(o.unit) {
		return
	}
	k := g.reg.gens[o.unit].moduleTypeOf(def)
	// kindInvalid: propagates — a default that did not resolve stays kindless for the caller's typeOf to judge.
	if o.unit != g.fileUnit && k != kindInvalid {
		var ok bool
		if k, ok = g.importKind(k); !ok {
			k = kindInvalid
		}
	}
	if g.defaultArgKinds == nil {
		g.defaultArgKinds = make(map[ast.TypeExpr]kind)
	}
	g.defaultArgKinds[def] = k
}

// moduleTypeOf is typeOf at this file's module scope: no type parameter,
// block-local type, interface default's `self`, std declaration or
// sibling's names that a body being lowered here has open is consulted.
//
// defaultArgKinds is set aside too, so te, a default this gen also uses
// bare, is resolved here rather than answered by the record of that use.
func (g *gen) moduleTypeOf(te ast.TypeExpr) kind {
	subst, scopes, dict := g.genericSubst, g.typeScopes, g.dict
	self, stdSrc, namesFrom := g.defaultSelf, g.stdSrc, g.namesFrom
	defaults := g.defaultArgKinds
	g.genericSubst, g.typeScopes, g.dict = nil, nil, nil
	g.defaultSelf, g.stdSrc, g.namesFrom = nil, nil, 0
	g.defaultArgKinds = nil
	defer func() {
		g.genericSubst, g.typeScopes, g.dict = subst, scopes, dict
		g.defaultSelf, g.stdSrc, g.namesFrom = self, stdSrc, namesFrom
		g.defaultArgKinds = defaults
	}()
	return g.typeOf(te)
}

// declaredTypeParams is the declaration the type name at line:col refers
// to and its type parameters, or nil.
func (g *gen) declaredTypeParams(line, col int, name string) (ast.Node, []ast.TypeParam) {
	fa := g.srcFA()
	if fa == nil {
		return nil, nil
	}
	sym := fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil || sym.Name != name {
		return nil, nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch d := sym.Node.(type) {
	case *ast.StructDef:
		return d, d.TypeParams
	case *ast.EnumDef:
		return d, d.TypeParams
	case *ast.TypeDef:
		return d, d.TypeParams
	case *ast.InterfaceDef:
		return d, d.TypeParams
	case *ast.ExternType:
		return d, d.TypeParams
	}
	return nil, nil
}
