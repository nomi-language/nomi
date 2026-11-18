package irbuild

import (
	"maps"
	"slices"
	"strings"

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
// (gen.declaredTypes). A block-local type gets the universal `impl Debug` a
// module-level one gets (registerBlockLocalDebug), and it may be generic.
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
// **Identity is the declaration node, and the name-keyed table cannot express
// it.** `gen.types` is a `map[string]*typeDef`, "the only place a type NAME
// is looked up". Two bodies may each declare `Point` with a different field
// set, and one map keyed on `"Point"` can hold one of them.
//
// So a block-local declaration goes into a scoped overlay, pushed and popped
// with the block it was written in, and gen.namedType, the single lookup
// seam, consults the innermost overlay first. Scoping comes out right because
// it is the analyzer's own scoping, replayed and not reimplemented.
//
// # A block-local type does not shadow a module-level one
//
// With `struct Point { label: String }` at file scope, a block-local
// `struct Point { x: Int, y: Int }` and then `Point{x: 3, y: 4}` reports
// "struct 'Point' has no field 'x'" — the literal resolved against the module
// declaration. So the overlay can never SHADOW `gen.types`; it can only add a
// name the module scope does not have. It is still consulted first, because that
// is the order the analyzer resolves in and coding the weaker rule would make
// the builder's answer depend on a front-end limitation rather than on scope.
//
// # A type ALIAS is transparent and gets no def
//
// `typealias Count Int` introduces no type: `Count` IS `Int`, in both
// directions, with no conversion. So an alias is recorded as a name-to-KIND
// binding rather than a `*typeDef`, and `blockLocalAlias` is consulted where a
// type name is projected. A `type UserId Int` — a distinct — is the opposite and
// takes an ordinary def: it converts each way, which is exactly what separates
// the two in the fixture.

// blockTypeDecls is the type declarations written directly in one block.
type blockTypeDecls struct {
	// defs are the struct/enum/distinct declarations, by the name they
	// introduce. Two declarations of one name in ONE block is a front-end
	// error, so this map cannot lose a declaration to a collision.
	defs map[string]*typeDef
	// defOrder is the same defs in source order. Resolution walks this rather
	// than the map: a struct field may name a sibling declared beside it, and
	// map iteration order would make the output a function of the hash seed.
	// Two builds in ONE process cannot catch that, because a randomized order
	// agrees with itself there.
	defOrder []*typeDef
	// aliases are the `typealias` declarations, by name, and aliasOrder the
	// same in source order. Separate from defs because an alias introduces no
	// type — see the file comment.
	aliases    map[string]kind
	aliasOrder []*ast.TypeAlias
	// templates are the generic struct and enum declarations, by name. Each
	// also has a shell in defs, as a module-level generic has one in
	// gen.types, so a bare mention of the name reports `generic type`.
	templates map[string]*genericTemplate
	// imports are the `import` statements at the top of the block, and
	// imported the def of each type they bind, by the name it is bound
	// under (bindBlockImports): the declaration the checker resolved the
	// import to, so the name resolves to it for the rest of the block, as it
	// does for the checker (blockImportTypes). Apart from defs because the
	// block does not declare them: an imported std type is not a user type.
	imports  []*ast.ImportStmt
	imported map[string]*typeDef
	// importedIfaces is the def of each monomorphic interface the imports
	// bind, by the name it is bound under (bindBlockImports): a sibling
	// file's mirror, a std interface's shared def, or a std interface's
	// program def. The module scope need not bind the name at all, as in a
	// std module's `//!` prompt that opens with `import std::display.Display`.
	importedIfaces map[string]*ifaceDef
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
		case *ast.TypeAlias:
			if scope == nil {
				scope = newBlockTypeDecls()
			}
			scope.aliasOrder = append(scope.aliasOrder, t)
			continue
		case *ast.ImportStmt:
			if scope == nil {
				scope = newBlockTypeDecls()
			}
			scope.imports = append(scope.imports, t)
			continue
		default:
			continue
		}
		if scope == nil {
			scope = newBlockTypeDecls()
		}
		if _, dup := scope.defs[name]; dup {
			// Two declarations of one name in one block is a front-end error.
			// Refuse rather than let the second shell win silently, exactly as
			// buildTypes does for the module scope.
			scope.defs[name].refusals = append(scope.defs[name].refusals,
				refusal{construct: "duplicate type declaration", detail: name})
			continue
		}
		d := &typeDef{nomi: name, decl: stmt, line: stmt.LineNum(), isEnum: isEnum, isDistinct: isDistinct, // Optimistic for the reason buildTypes states: resolution has to
			// resolve a field naming a type declared later in the same block,
			// and settleLowerable retracts this for whatever turns out refused.
			lowerable: true}
		scope.defs[name] = d
		scope.defOrder = append(scope.defOrder, d)
		*order = append(*order, d)
		if tps := typeDeclTypeParams(stmt); len(tps) > 0 {
			// Its impls are the universal Debug the front end gives a
			// module-level generic: an impl or a derive cannot name a
			// block-local type.
			synth := analysis.SynthesizeUniversalDebug([]ast.Node{stmt})
			g.noteSynthTypeExprs(synth, stmt, name)
			tpl := g.newGenericTemplate(stmt, name, tps, synth)
			tpl.scope = scope
			scope.templates[name] = tpl
		}
	}
	if scope == nil {
		return
	}
	g.blockTypes[b] = scope
	g.blockTypeOrder = append(g.blockTypeOrder, b)
}

func newBlockTypeDecls() *blockTypeDecls {
	return &blockTypeDecls{
		defs:           map[string]*typeDef{},
		imported:       map[string]*typeDef{},
		importedIfaces: map[string]*ifaceDef{},
		aliases:        map[string]kind{},
		templates:      map[string]*genericTemplate{},
	}
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

// registerBlockLocalDebug gives every block-local struct, enum and distinct
// the universal Debug a module-level one gets.
//
// The front end's SynthesizeUniversalDebug walks module-level declarations
// only, and an `impl` is a module-level declaration that cannot name a
// block-local type, so no impl reaches this builder for one. The checker
// still treats the type as Debug, as it treats every type, so
// `Debug.inspect`, `dbg` and `io.inspect` over it are legal programs. The
// impl is the one SynthesizeUniversalDebug would write for the same
// declaration at module level, registered with the declaring block's
// overlay active (implDef.typeScope), where its receiver's name resolves.
// It answers the impls it registered, in order.
func (g *gen) registerBlockLocalDebug(blocks []*ast.Block) []*implDef {
	var out []*implDef
	for _, b := range blocks {
		scope := g.blockTypes[b]
		for _, d := range scope.orderedDefs() {
			if len(typeDeclTypeParams(d.decl)) > 0 || g.implsByIface["Debug"][named(d)] != nil {
				// A generic declaration's synthesized impl is generic, which
				// registerImpl refuses at module level too.
				continue
			}
			synth := analysis.SynthesizeUniversalDebug([]ast.Node{d.decl})
			g.noteSynthTypeExprs(synth, d.decl, d.nomi)
			for _, n := range synth {
				ib, ok := n.(*ast.ImplBlock)
				if !ok {
					continue
				}
				// The checker never checks this impl: an impl cannot name a
				// type declared inside a body.
				g.markUnchecked(ib)
				impl := &implDef{decl: ib, synth: true, items: map[string]*implItem{}, lowerable: true, typeScope: scope}
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

// noteSynthTypeExprs keeps the types of the type expressions of synth, the
// universal Debug written for decl, a block-local declaration of name, and
// the variants its patterns' heads name (analysis.UniversalDebugTypes),
// from the checker's type for decl, for recordedTypeExpr and
// patternVariant to answer.
func (g *gen) noteSynthTypeExprs(synth []ast.Node, decl ast.Node, name string) {
	line, col := nodePos(decl)
	sym := g.fa.Definitions[analysis.Pos{Line: line, Col: col}]
	if sym == nil || sym.Type == nil {
		return
	}
	if g.synthTypeExprs == nil {
		g.synthTypeExprs = map[ast.TypeExpr]analysis.Type{}
		g.synthPatternVariants = map[ast.TypeExpr]string{}
	}
	types, variants := analysis.UniversalDebugTypes(synth, name, sym.Type)
	maps.Copy(g.synthTypeExprs, types)
	maps.Copy(g.synthPatternVariants, variants)
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
	// Imported types first: a declaration or an alias may name one.
	g.bindBlockImports(scope)
	// Aliases first: a struct field may be annotated with one.
	for _, a := range scope.aliasOrder {
		scope.aliases[a.Name] = g.aliasTarget(a)
	}
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

// bindBlockImports puts each monomorphic struct, enum, distinct type or
// interface the block's imports bind into the block's overlay under the name
// the import binds, as the def of the declaration the checker resolved it to:
// another file's type or interface by its mirror, a std type by the def its
// analyzer type projects to, a std interface by its shared def or program
// def (importedIfaceDef). A generic type or interface is left to the
// name-keyed lookups, and testImport declines an import that rebinds one.
func (g *gen) bindBlockImports(scope *blockTypeDecls) {
	if len(scope.imports) == 0 || g.fa == nil {
		return
	}
	fa := g.srcFA()
	for _, imp := range scope.imports {
		bound := fa.BlockImportScopes[imp]
		if bound == nil {
			continue
		}
		// Sorted, so the mirrors are made in the same order on every run.
		names := make([]string, 0, len(bound.Symbols))
		for name, sym := range bound.Symbols {
			if sym != nil && sym.Node == ast.Node(imp) {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		for _, name := range names {
			sym := resolveSymbol(bound.Symbols[name])
			if d := g.importedTypeDef(sym); d != nil {
				scope.imported[name] = d
			} else if d := g.importedIfaceDef(sym); d != nil {
				scope.importedIfaces[name] = d
			}
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

// importedIfaceDef is the def of the monomorphic interface sym declares,
// found by its declaration rather than by its name, or nil. The name-keyed
// lookups (ifaceNamed, stdIfaceNamed, stdProgIfaceNamed) read the module
// scope, which a block's import does not reach.
func (g *gen) importedIfaceDef(sym *analysis.Symbol) *ifaceDef {
	if sym == nil || sym.Kind != analysis.SymbolInterface {
		return nil
	}
	decl, isDecl := sym.Node.(*ast.InterfaceDef)
	if !isDecl || len(decl.TypeParams) > 0 {
		return nil
	}
	if o := g.ownerOfSymbol(sym); o != nil {
		if !o.isIface {
			return nil
		}
		return g.mirrorIface(o)
	}
	it, isIface := sym.Type.(*analysis.InterfaceType)
	if !isIface || !strings.HasPrefix(it.Origin, "std/") {
		return nil
	}
	if d := g.stdIfaceOfDecl(it.Origin, decl); d != nil {
		return d
	}
	if !g.stdProgBuilds() {
		return nil
	}
	return g.stdProgIfaceOf(sym)
}

// blockModuleSymbol is the symbol a dotted name (`fmt.WriteOptions`) names
// when a visible block's `import` binds its qualifier to another file than
// the module scope does (visibleBlockModule over moduleScopeOf), reporting
// whether one does. That binding shadows the file's for the rest of the
// block, as the checker resolves it, so a name it reports is never answered
// from the file's binding.
func (g *gen) blockModuleSymbol(name string) (*analysis.Symbol, bool) {
	dot := strings.IndexByte(name, '.')
	if dot <= 0 || len(g.typeScopes) == 0 {
		return nil, false
	}
	scope := g.visibleBlockModule(name[:dot])
	if file := moduleScopeOf(g.fa, name[:dot]); scope == nil || file == nil || file == scope {
		return nil, false
	}
	return resolvedScopeSymbol(scope, name[dot+1:]), true
}

// blockImportedIface answers an interface name from the innermost enclosing
// block whose imports bind it (bindBlockImports).
func (g *gen) blockImportedIface(name string) (*ifaceDef, bool) {
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if d, found := g.typeScopes[i].importedIfaces[name]; found {
			return d, true
		}
	}
	return nil, false
}

// aliasTarget is the kind a `typealias` names, or kindInvalid.
//
// An alias is transparent, so this is the ONE fact it contributes and there is
// no def to build. A target the builder has no representation for makes the
// alias unusable: a use of the name declines where it is written, so nothing
// is reported here.
func (g *gen) aliasTarget(t *ast.TypeAlias) kind {
	if t == nil || t.TargetTypeExpr == nil || len(t.Bounds) > 0 {
		// A BOUND alias (`typealias Num Add and Multiply`) names a conjunction
		// of interfaces rather than a type, so there is no kind to bind.
		return kindInvalid
	}
	return g.typeOf(t.TargetTypeExpr)
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

// pushTypeScope makes one block's declarations visible to namedType.
func (g *gen) pushTypeScope(scope *blockTypeDecls) {
	g.typeScopes = append(g.typeScopes, scope)
}

func (g *gen) popTypeScope() { g.typeScopes = g.typeScopes[:len(g.typeScopes)-1] }

// blockLocalNamed answers a type name from the innermost enclosing block that
// declares it.
//
// Innermost first, which is the analyzer's own resolution order. See the file
// comment for why the overlay can never actually shadow gen.types today, and
// why it is still consulted first.
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
			if tpl := scope.templates[d.nomi]; tpl != nil && len(params) != 0 {
				g.blockLocalTemplates[params[0]] = tpl
			}
		}
	}
	g.blockLocalIndexed = len(g.blockTypeOrder)
}

func (g *gen) blockLocalNamed(name string) (*typeDef, bool) {
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if d, found := g.typeScopes[i].defs[name]; found {
			return d, true
		}
	}
	return nil, false
}

// blockImportedNamed answers a type name from the innermost enclosing block
// whose imports bind it, as the def of the declaration the import names.
func (g *gen) blockImportedNamed(name string) (*typeDef, bool) {
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if d, found := g.typeScopes[i].imported[name]; found {
			return d, true
		}
	}
	return nil, false
}

// blockLocalTemplate answers a generic type name from the innermost enclosing
// block that declares it, or nil. A module-level type of the same name wins,
// as it does for the checker: `Box{v: 1}` beside a module-level `Box<T>`
// with field `v` and a block-local one with field `w` checks against the
// module's.
func (g *gen) blockLocalTemplate(name string) *genericTemplate {
	if _, module := g.types[name]; module {
		return nil
	}
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if tpl := g.typeScopes[i].templates[name]; tpl != nil {
			return tpl
		}
	}
	return nil
}

// blockLocalAlias answers a `typealias` name from the innermost enclosing block
// that declares it.
func (g *gen) blockLocalAlias(name string) (kind, bool) {
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if k, found := g.typeScopes[i].aliases[name]; found {
			return k, true
		}
	}
	return kindInvalid, false
}
