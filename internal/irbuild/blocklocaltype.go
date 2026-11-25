package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A `struct`, `enum`, `type` or `typealias` declared inside a body.
//
// # Two facts decide the design
//
// **A block-local declaration is declared for the whole module, before any
// body is lowered.** collectBlockTypeDecls walks every block under the
// module's nodes and builds a shell for each declaration, and the layout
// passes treat those shells as they treat module-level ones
// (gen.declaredTypes). A block-local type renders by its shape, and an
// opaque one gets the name-only `impl Debug` a module-level opaque type gets
// (registerBlockLocalDebug). It may be generic.
//
// That is sound because the front end has already restricted what may be
// asked. A use of a block-local name from outside its block is an analyzer
// error ("undefined type"), so the builder is only ever asked about names the
// checker approved at the position it approved them. A value of the type may
// still leave the block (`w = { struct Loc {…}  Loc{n: 1} }`); outside it the
// checker's type for the value is the declaration's own, and project finds
// the def by that type and not by name (blockLocalOfType,
// blockLocalTemplateOf).
//
// **Identity is the declaration node.** Two bodies may each declare `Point`
// with a different field set, so a block-local def is found by its
// declaration (gen.defsByDecl) or by the checker's type for it
// (blockLocalOfType), never by its name, and `gen.types` holds none. A
// block's overlay (gen.typeScopes) is pushed and popped with the block it
// was written in.
//
// # A type ALIAS is transparent and gets no def
//
// `typealias Count Int` introduces no type: `Count` IS `Int`, in both
// directions, with no conversion. So the builder holds nothing for an
// alias: an annotation that spells it has the target's type in the
// checker's record. A `type UserId Int` — a distinct — is the opposite and
// takes an ordinary def: it converts each way, which is exactly what
// separates the two in the fixture.

// blockTypeDecls is the type declarations written directly in one block.
type blockTypeDecls struct {
	// defOrder is the struct, enum and distinct declarations' defs, in
	// source order: a struct field may name a sibling declared beside it,
	// and an order that came from a map would make the output a function of
	// the hash seed.
	defOrder []*typeDef
	// templateByDecl is the generic struct and enum declarations' templates,
	// by declaration. Each also has a def in defOrder, as a module-level
	// generic has one in gen.types, so a bare mention reports `generic
	// type`.
	templateByDecl map[ast.Node]*genericTemplate
	// outer is the nearest enclosing block's declarations, or nil. A
	// declaration here may name one there (`struct Inner { p: P<Int> }`
	// under the block declaring `P`), so wherever this scope is made
	// visible, its outer scopes are too (enterTypeScope).
	outer *blockTypeDecls
}

// orderedDefs is this block's declarations in source order.
func (s *blockTypeDecls) orderedDefs() []*typeDef { return s.defOrder }

// collectBlockTypeDecls builds the shells for every type declaration nested in
// a body, and returns them in declaration order.
//
// Order is source order over the module's nodes and then over each block's
// statements, so the output is a function of the source rather than of map
// iteration.
func (g *gen) collectBlockTypeDecls(nodes []ast.Node) []*typeDef {
	var order []*typeDef
	for _, n := range nodes {
		order = g.collectBlockTypesIn(n, &order)
	}
	return order
}

// collectBlockTypesIn declares the types of every block under n, wherever
// the block stands: a body, a binding's value, a call's argument, a list
// element, an operand. The front end admits a declaration in each of them.
// The walk is pre-order, so an outer block's declarations come before an
// inner one's. The accumulator is threaded rather than returned so no
// branch's defs can be dropped.
func (g *gen) collectBlockTypesIn(n ast.Node, order *[]*typeDef) []*typeDef {
	var walk func(n ast.Node, outer *blockTypeDecls)
	walk = func(n ast.Node, outer *blockTypeDecls) {
		if b, ok := n.(*ast.Block); ok {
			g.declareBlockTypes(b, order)
			if scope := g.blockTypes[b]; scope != nil {
				if scope != outer && scope.outer == nil {
					scope.outer = outer
				}
				outer = scope
			}
		}
		ast.Children(n, func(c ast.Node) { walk(c, outer) })
	}
	walk(n, nil)
	return *order
}

// declareBlockTypes registers the shells for one block's own declarations.
func (g *gen) declareBlockTypes(b *ast.Block, order *[]*typeDef) {
	if b == nil || g.blockTypes[b] != nil {
		return
	}
	if g.blockTypes == nil {
		// A fixture path may build a gen literal and not go through newGen,
		// so the map may not exist.
		g.blockTypes = map[*ast.Block]*blockTypeDecls{}
	}
	var scope *blockTypeDecls
	for _, stmt := range b.Stmts {
		var name string
		isEnum, isDistinct := false, false
		switch t := stmt.(type) {
		case *ast.StructDef:
			name = t.Name
		case *ast.EnumDef:
			name, isEnum = t.Name, true
		case *ast.TypeDef:
			name, isDistinct = t.Name, true
		default:
			continue
		}
		if scope == nil {
			scope = newBlockTypeDecls()
		}
		d := &typeDef{nomi: name, decl: stmt, line: stmt.LineNum(), isEnum: isEnum, isDistinct: isDistinct, // Optimistic for the reason buildTypes states: resolution has to
			// resolve a field naming a type declared later in the same block,
			// and settleLowerable retracts this for whatever turns out refused.
			lowerable: true}
		g.fileDef(stmt, d)
		scope.defOrder = append(scope.defOrder, d)
		*order = append(*order, d)
		if tps := typeDeclTypeParams(stmt); len(tps) > 0 {
			// Its impls are the name-only Debug the front end gives an
			// opaque one, if it is opaque: an impl or a derive cannot name
			// a block-local type.
			tpl := g.newGenericTemplate(stmt, name, tps, g.blockLocalDebug(stmt))
			tpl.scope = scope
			scope.templateByDecl[stmt] = tpl
		}
	}
	if scope == nil {
		return
	}
	g.blockTypes[b] = scope
	g.blockTypeOrder = append(g.blockTypeOrder, b)
}

func newBlockTypeDecls() *blockTypeDecls {
	return &blockTypeDecls{templateByDecl: map[ast.Node]*genericTemplate{}}
}

// declareCaseBlockTypes declares and resolves the types written inside test
// case bodies the module's own walk did not reach: a stdlib module's `//!`
// prompts, which are parsed apart from its declarations
// (`//! type TraceId String`). Only the blocks it adds are resolved, slotted
// and settled, the way buildTypes treats the module's own.
func (g *gen) declareCaseBlockTypes(bodies []*ast.Block) {
	defer changingTypes()()
	first := len(g.blockTypeOrder)
	var order []*typeDef
	for _, b := range bodies {
		g.collectBlockTypesIn(b, &order)
	}
	added := g.blockTypeOrder[first:]
	if len(added) == 0 {
		return
	}
	for _, b := range added {
		g.resolveBlockScope(g.blockTypes[b])
	}
	for _, d := range order {
		for i := range d.fields {
			for _, c := range appendInlineDefs(nil, d.fields[i].k) {
				if c == d || c.reaches(d) {
					d.fields[i].boxed = true
					break
				}
			}
		}
	}
	for _, d := range order {
		d.assignSlots()
	}
	g.blockLocalOrder = append(g.blockLocalOrder, order...)
	g.settleLowerable(order)
	// buildImpls has already run for the module, so these blocks' Debug
	// impls are registered and lowered here.
	g.emitSynthImpls(g.registerBlockLocalDebug(added))
}

// registerBlockLocalDebug gives every opaque block-local struct, enum and
// distinct the name-only universal Debug a module-level one gets; any other
// block-local type renders by its shape (nestedDebugImpls' byShape).
//
// An `impl` is a module-level declaration that cannot name a block-local
// type, so no module-level impl reaches this builder for one. The checker
// treats the type as Debug, as it treats every type, so `Debug.inspect`,
// `dbg` and `io.inspect` over it are legal programs, and the front end
// writes and checks its universal Debug in the declaring block
// (blockLocalDebug). The impl is registered with that block's overlay
// active (implDef.typeScope), where its receiver's name resolves. It
// answers the impls it registered, in order.
func (g *gen) registerBlockLocalDebug(blocks []*ast.Block) []*implDef {
	var out []*implDef
	for _, b := range blocks {
		scope := g.blockTypes[b]
		for _, d := range scope.orderedDefs() {
			if len(typeDeclTypeParams(d.decl)) > 0 || g.implAt(stdIfaceImplKey(stdDebug), named(d)) != nil {
				// A generic declaration's synthesized impl is generic, which
				// registerImpl refuses at module level too.
				continue
			}
			for _, n := range g.blockLocalDebug(d.decl) {
				ib, ok := n.(*ast.ImplBlock)
				if !ok {
					continue
				}
				impl := &implDef{decl: ib, synth: isSynthesized(ib), items: map[string]*implItem{}, lowerable: true, typeScope: scope}
				g.implOrder = append(g.implOrder, impl)
				out = append(out, impl)
				leave := g.enterTypeScope(scope)
				g.resolveImplDef(impl, ib, named(d))
				leave()
			}
		}
	}
	return out
}

// blockLocalDebug is the universal Debug the front end writes for decl, an
// opaque block-local declaration, as it writes one for a module-level one:
// the impl the checker checked in decl's block
// (analysis.FileBodies.BlockDebug), or none.
func (g *gen) blockLocalDebug(decl ast.Node) []ast.Node {
	if impl := g.srcFA().BlockDebug[decl]; impl != nil {
		return []ast.Node{impl}
	}
	return nil
}

// typeDeclTypeParams is a struct's, enum's or distinct's declared type
// parameters.
func typeDeclTypeParams(n ast.Node) []ast.TypeParam {
	switch t := n.(type) {
	case *ast.StructDef:
		return t.TypeParams
	case *ast.EnumDef:
		return t.TypeParams
	case *ast.TypeDef:
		return t.TypeParams
	}
	return nil
}

// resolveBlockTypeDecls fills in the components of every block-local shell,
// with that block's own overlay active so a declaration may name a sibling
// declared beside it.
func (g *gen) resolveBlockTypeDecls() {
	for _, b := range g.blockTypeOrder {
		g.resolveBlockScope(g.blockTypes[b])
	}
}

// resolveBlockScope resolves one block's declarations with its overlay active.
func (g *gen) resolveBlockScope(scope *blockTypeDecls) {
	defer g.enterTypeScope(scope)()
	for _, d := range scope.orderedDefs() {
		switch t := d.decl.(type) {
		case *ast.StructDef:
			g.resolveStruct(d, t)
		case *ast.EnumDef:
			g.resolveEnum(d, t)
		case *ast.TypeDef:
			g.resolveDistinct(d, t)
		}
	}
}

// importedTypeDef is the def of the monomorphic struct, enum or distinct type
// sym declares, found by its declaration rather than by its name, or nil.
func (g *gen) importedTypeDef(sym *analysis.Symbol) *typeDef {
	if sym == nil {
		return nil
	}
	switch sym.Kind {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType:
	default:
		return nil
	}
	if len(typeDeclTypeParams(sym.Node)) > 0 {
		return nil
	}
	if o := g.ownerOfSymbol(sym); o != nil {
		if o.isIface {
			return nil
		}
		return g.mirrorOf(o)
	}
	if k := g.project(sym.Type); k.tag == tagNamed && k.def != nil && k.def.genericOf == nil {
		return k.def
	}
	return nil
}

// typeScopeActive reports whether scope's declarations are visible now.
func (g *gen) typeScopeActive(scope *blockTypeDecls) bool {
	for _, s := range g.typeScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// enterBlockTypes makes the types block declares visible for the extent of
// its lowering, as they are to the front end, and answers the function that
// hides them again. A block that declares none, or whose scope is already
// visible, changes nothing.
func (g *gen) enterBlockTypes(block *ast.Block) func() {
	return g.enterTypeScope(g.blockTypes[block])
}

// enterTypeScope makes scope and the scopes enclosing it visible, outermost
// first, and answers the function that hides those it made visible. Any
// already visible stay as they are.
func (g *gen) enterTypeScope(scope *blockTypeDecls) func() {
	var chain []*blockTypeDecls
	for s := scope; s != nil; s = s.outer {
		if !g.typeScopeActive(s) {
			chain = append(chain, s)
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		g.pushTypeScope(chain[i])
	}
	return func() {
		for range chain {
			g.popTypeScope()
		}
	}
}

// pushTypeScope makes one block's declarations visible.
func (g *gen) pushTypeScope(scope *blockTypeDecls) {
	g.typeScopes = append(g.typeScopes, scope)
}

func (g *gen) popTypeScope() { g.typeScopes = g.typeScopes[:len(g.typeScopes)-1] }

// blockLocalOfType answers the block-local def whose declaration the
// checker typed as t, or nil: the identity of a block-local type outside
// the block that declares it, where its name resolves to nothing. A generic
// one's instances are other types; blockLocalTemplateOf finds its template.
func (g *gen) blockLocalOfType(t analysis.Type) *typeDef {
	if g.fa == nil || t == nil {
		return nil
	}
	g.indexBlockLocals()
	return g.blockLocalByType[t]
}

// blockLocalTemplateOf answers the block-local generic template whose
// declaration has the type parameters params, which every instance the
// checker solves of it shares, or nil.
func (g *gen) blockLocalTemplateOf(params []*analysis.TypeParam_) *genericTemplate {
	if g.fa == nil || len(params) == 0 {
		return nil
	}
	g.indexBlockLocals()
	return g.blockLocalTemplates[params[0]]
}

// indexBlockLocals indexes the block-local declarations of the blocks
// declared since it last ran by the checker's type for each declaration:
// a non-generic def by that type, a generic template by its first type
// parameter.
func (g *gen) indexBlockLocals() {
	if g.blockLocalIndexed >= len(g.blockTypeOrder) {
		return
	}
	if g.blockLocalByType == nil {
		g.blockLocalByType = map[analysis.Type]*typeDef{}
		g.blockLocalTemplates = map[*analysis.TypeParam_]*genericTemplate{}
	}
	for _, b := range g.blockTypeOrder[g.blockLocalIndexed:] {
		scope := g.blockTypes[b]
		for _, d := range scope.orderedDefs() {
			line, col := nodePos(d.decl)
			sym := g.fa.Definitions[analysis.Pos{Line: line, Col: col}]
			if sym == nil || sym.Type == nil {
				continue
			}
			if len(typeDeclTypeParams(d.decl)) == 0 {
				g.blockLocalByType[sym.Type] = d
				continue
			}
			var params []*analysis.TypeParam_
			switch t := sym.Type.(type) {
			case *analysis.StructType:
				params = t.TypeParamDefs
			case *analysis.EnumType:
				params = t.TypeParamDefs
			}
			if tpl := scope.templateByDecl[d.decl]; tpl != nil && len(params) != 0 {
				g.blockLocalTemplates[params[0]] = tpl
			}
		}
	}
	g.blockLocalIndexed = len(g.blockTypeOrder)
}
