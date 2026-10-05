package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A `struct`, `enum`, `type` or `typealias` declared INSIDE a body.
//
// # Two facts decide the design
//
// **The Go type is LIFTED to package level, not emitted as a Go local type.**
// Go does permit `type T struct{…}` inside a function body, so "emit it where it
// was written" looks like the faithful choice. It is not available here for two
// independent reasons:
//
//   - **Go methods must be declared at package level.** Every declared Nomi type
//     gets an `impl Debug` from the analyzer's universal-Debug synthesis, and an
//     `impl` lowers to Go functions plus a dispatch registration
//     (impl.go). A type declared inside one function body cannot be named by any
//     of them.
//   - **A function-local Go type cannot be GENERIC.** That fixes the ceiling of
//     the local-type encoding at "monomorphic and method-free", which is not
//     what a Nomi declaration is.
//
// Lifting is sound because the front end has already restricted what may be
// asked. A use of a block-local name from OUTSIDE its block is an analyzer error
// ("undefined type"), so hoisting the Go declaration widens a scope nothing can
// observe: the builder is only ever asked about names the checker approved at
// the position it approved them.
//
// **Identity is the DECLARATION NODE, and the name-keyed table is the one thing
// that could not express it.** typeRegistry.byDecl already keys every user type
// on the `ast.Node` that declared it and hands out program-unique Go names
// (foreign.go), so the SPELLING side is already unique. `gen.types` is not: it
// is a `map[string]*typeDef`, "the only place a type NAME is looked up". Two
// bodies may each declare `Point` with a different field set, and one map keyed
// on `"Point"` can hold one of them.
//
// So a block-local declaration goes into a SCOPED overlay instead, pushed and
// popped with the block it was written in, and gen.namedType — which foreign.go
// established as the single lookup seam — consults the innermost overlay first.
// That is nestedfn.go's choice for the same question one level down: scoping
// comes out right because it is the analyzer's own scoping, replayed rather than
// reimplemented.
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
// binding rather than a `*typeDef`, and `aliasKind` is consulted where a type
// name is projected. A `type UserId Int` — a distinct — is the opposite and
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

// collectBlockTypesIn walks one node's bodies. The accumulator is threaded
// rather than returned so the recursion cannot drop a branch's defs.
func (g *gen) collectBlockTypesIn(n ast.Node, order *[]*typeDef) []*typeDef {
	switch t := n.(type) {
	case *ast.FuncDef:
		g.collectBlockTypesIn(t.Body, order)
	case *ast.TestDecl:
		g.collectBlockTypesIn(t.Boot, order)
		g.collectBlockTypesIn(t.Setup, order)
		g.collectBlockTypesIn(t.Body, order)
	case *ast.ImplBlock:
		for _, item := range t.Items {
			g.collectBlockTypesIn(item, order)
		}
	case *ast.Block:
		g.declareBlockTypes(t, order)
		for _, stmt := range t.Stmts {
			g.collectBlockTypesIn(stmt, order)
		}
	case *ast.Binding:
		g.collectBlockTypesIn(t.Value, order)
	case *ast.PatternBinding:
		g.collectBlockTypesIn(t.Value, order)
		for _, e := range t.ElseNodes() {
			g.collectBlockTypesIn(e, order)
		}
	case *ast.GroupedExpr:
		g.collectBlockTypesIn(t.Expr, order)
	case *ast.ExprStmt:
		g.collectBlockTypesIn(t.Expr, order)
	case *ast.If:
		g.collectBlockTypesIn(t.Then, order)
		g.collectBlockTypesIn(t.Else, order)
	case *ast.With:
		g.collectBlockTypesIn(t.Value, order)
	case *ast.Case:
		for _, br := range t.Branches {
			g.collectBlockTypesIn(br.Body, order)
		}
	case *ast.Lambda:
		g.collectBlockTypesIn(t.Body, order)
	}
	return *order
}

// declareBlockTypes registers the shells for one block's own declarations.
func (g *gen) declareBlockTypes(b *ast.Block, order *[]*typeDef) {
	if b == nil || g.blockTypes[b] != nil {
		return
	}
	if g.blockTypes == nil {
		// renderTypesOnly and the other fixture paths build a gen literal
		// rather than going through newGen, so the map may not exist.
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
	}
	if scope == nil {
		return
	}
	g.blockTypes[b] = scope
	g.blockTypeOrder = append(g.blockTypeOrder, b)
}

func newBlockTypeDecls() *blockTypeDecls {
	return &blockTypeDecls{
		defs:    map[string]*typeDef{},
		aliases: map[string]kind{},
	}
}

// declareCaseBlockTypes declares and resolves the types written inside test
// case bodies the module's own walk did not reach: a stdlib module's `//!`
// prompts, which are parsed apart from its declarations
// (`//! type TraceId String`). Only the blocks it adds are resolved, slotted
// and settled, the way buildTypes treats the module's own.
func (g *gen) declareCaseBlockTypes(bodies []*ast.Block) {
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
			for _, n := range analysis.SynthesizeUniversalDebug([]ast.Node{d.decl}) {
				ib, ok := n.(*ast.ImplBlock)
				if !ok {
					continue
				}
				impl := &implDef{decl: ib, synth: true, items: map[string]*implItem{}, lowerable: true, typeScope: scope}
				g.implOrder = append(g.implOrder, impl)
				out = append(out, impl)
				g.pushTypeScope(scope)
				g.resolveImplDef(impl, ib, named(d))
				g.popTypeScope()
			}
		}
	}
	return out
}

// typeDeclTypeParams is a struct's or enum's declared type parameters.
func typeDeclTypeParams(n ast.Node) []ast.TypeParam {
	switch t := n.(type) {
	case *ast.StructDef:
		return t.TypeParams
	case *ast.EnumDef:
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
	g.pushTypeScope(scope)
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
	g.popTypeScope()
}

// aliasTarget is the kind a `typealias` names, or kindInvalid.
//
// An alias is transparent, so this is the ONE fact it contributes and there is
// no def to build. A target the builder has no representation for makes the
// alias unusable; the refusal is reported at the declaration by
// blockAliasDecl, so nothing is reported here.
func (g *gen) aliasTarget(t *ast.TypeAlias) kind {
	if t == nil || t.TargetTypeExpr == nil || len(t.Bounds) > 0 {
		// A BOUND alias (`typealias Num Add and Multiply`) names a conjunction
		// of interfaces rather than a type, so there is no kind to bind and the
		// declaration is refused at its own position by blockAliasDecl.
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
func (g *gen) blockLocalNamed(name string) (*typeDef, bool) {
	for i := len(g.typeScopes) - 1; i >= 0; i-- {
		if d, found := g.typeScopes[i].defs[name]; found {
			return d, true
		}
	}
	return nil, false
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

// declaredAs reports whether the type NAME `owner` resolves to d in the scope
// being emitted.
//
// The question every `Owner.member` site asks of a qualifier. A plain
// `g.types[owner] == d` is right for a module-level declaration and always
// false for a block-local one, because that table cannot hold two bodies'
// `Point` and so holds neither. Routed through the overlay so the qualifier is
// resolved the way the name is.
//
// # AN INSTANCE'S QUALIFIER NAMES ITS TEMPLATE, NOT ITSELF
//
// `g.types[owner] == d` is also always false for a MONOMORPHIZED instance, and
// for a sharper reason: `Holder.Of{value}` over a `Holder<Int>` value has a
// qualifier naming the TEMPLATE, while `d` is the instance — a fresh def per
// argument tuple, deliberately not in `g.types` at all (generictype.go's
// "the def in `g.types` is left exactly as it was"). So the identity question
// for an instance is "does this name resolve to my template", and it is asked
// through the same resolver a construction site uses, which answers for the
// module-qualified spelling as well as the bare one.
func (g *gen) declaredAs(owner string, d *typeDef) bool {
	if bl, isBlockLocal := g.blockLocalNamed(owner); isBlockLocal {
		// A block-local declaration WINS its name for the extent of its block,
		// so a module-level type of the same name is not what `owner` meant
		// here even if one exists.
		return bl == d
	}
	if g.types[owner] == d {
		return true
	}
	if d != nil && d.genericOf != nil {
		tpl, _, isTemplate := g.genericTemplateNamed(owner)
		return isTemplate && tpl == d.genericOf
	}
	// Another file's type, named by a spelling this file has not resolved
	// yet: `ticket.Status.Open` in a function whose subject is typed `Status`
	// registered only the selective import's `Status`. The table is filled as
	// spellings are resolved, so a miss here says nothing about identity; the
	// resolver answers by declaration node, and one declaration has one mirror.
	if r, found := g.namedType(owner); found && d != nil {
		return r == d
	}
	return false
}
