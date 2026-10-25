package irbuild

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Types that refer to each other across files.
//
// One file's type table is built on demand (typeRegistry.ensureTypes): when
// file a's `struct A { bs: List<B> }` resolves `B`, b's table is built first
// and a takes a mirror of B with B's finished layout. That order cannot serve
// `struct B { parent: Maybe<A> }` in b, because b's table would then need a's,
// which is half built.
//
// One file already handles the same shape: buildTypes registers every shell,
// then resolves every field against the shells, then boxes, lays out slots and
// settles lowerability over the finished graph. A TYPE GROUP runs those phases
// over several files at once. The group is a strongly connected component of
// the graph whose edge a→b says a type, interface or alias declaration in a
// names a type or interface b declares. A file outside every such cycle builds
// on demand in a group of its own, through the same phases.
//
// A file that is finished may be asked, while a group resolves its fields, for
// a generic instance over one of the group's declarations (`struct Parent { p:
// Pair<Int, Child> }` with Pair in a finished file). Its mirror of Child is
// pending in the group, as a member's would be, and the instance waits for the
// group's layout too (deferInstLayout), as does every instance a member builds
// while fields are resolved: an instance laid out then would read a
// declaration with no fields as zero-sized.
//
// Inside a group, a mirror of another member's type is made while fields are
// still being resolved, so it starts as an identity with no layout (a pending
// mirror). Once every member's fields are resolved, each pending mirror's kinds
// are imported; that may make more pending mirrors, which are imported in turn.
// Boxing and slot assignment then run over a graph in which every mirror has
// its components, and each pending mirror takes its owner's boxing and slots.
// Lowerability is settled over the whole group until no mirror or declaration
// changes, so a refusal in one file reaches every container in the others.
// Interface signatures are resolved after that, member by member, and a mirror
// of another member's interface made before then gets its methods once every
// member's are resolved.
//
// The edges are a superset of what resolution demands: they count every type
// name in a declaration, including one a type parameter shadows. A superset
// only groups a file that did not need it, and the phases build such a file's
// types as its own buildTypes would. A demand the edges miss would be refused
// as `sibling file type cycle`, as every cycle was before groups existed. Names
// are resolved through the checker's reference at each name, then through the
// file's module scope, so a block-local type naming a type an import at the
// top of its block binds is an edge too (typeRefOwnerAt).

// typeGroupState is how far a type group's build has gone.
type typeGroupState int

const (
	// groupIdle: not started.
	groupIdle typeGroupState = iota
	// groupShells: registering shells. Nothing in the group is readable.
	groupShells
	// groupResolving: every member's shells and generic templates are
	// registered. A member asking for another member's type gets a pending
	// mirror.
	groupResolving
	// groupLayingOut: boxing, slots and lowerability. No mirror of a member
	// may be made, since it could take no layout.
	groupLayingOut
	// groupLaidOut: every member's type layout and lowerability is final, so
	// a mirror of a member type is built whole, as for any finished table.
	groupLaidOut
	// groupIfacesResolved: every member's interface signatures are resolved
	// too.
	groupIfacesResolved
)

// typeGroup is the files whose type declarations refer to each other in a
// cycle, built together.
type typeGroup struct {
	units   []int
	state   typeGroupState
	pending []pendingMirror
	// insts are the generic instances whose layout waits for the group's
	// (deferInstLayout), with the gen that owns each; registered counts
	// those whose impls are registered.
	insts      []deferredInst
	registered int
	// pendingIfaces are interface mirrors made before their owners'
	// signatures were resolved. A type's layout never reads an interface's
	// methods, only whether it is lowerable, which its shell already says.
	pendingIfaces []pendingIfaceMirror
}

// deferredInst is a generic instance waiting for a group's layout.
type deferredInst struct {
	g *gen
	d *typeDef
}

// pendingIfaceMirror is an interface mirror waiting for its methods.
type pendingIfaceMirror struct {
	g   *gen
	d   *ifaceDef
	src *ifaceDef
}

// pendingMirror is a mirror made while its owner's fields were unresolved,
// waiting for its kinds and layout.
type pendingMirror struct {
	g   *gen
	d   *typeDef
	src *typeDef
	o   *typeOwner
	// imported says importMirrorKinds imported every kind. A mirror that
	// failed is refused already and takes no layout.
	imported bool
}

// groupFor is the type group unit belongs to, or nil for a file on no cycle.
func (r *typeRegistry) groupFor(unit int) *typeGroup {
	if !r.groupsFound {
		r.groupsFound = true
		r.groupOf = findTypeGroups(r.gens, r.owner)
	}
	if r.groupOf == nil || unit < 0 || unit >= len(r.groupOf) {
		return nil
	}
	return r.groupOf[unit]
}

// ensureLayout is ensureTypes for a caller that reads the owner's finished
// layout: fields, boxing, slots and lowerability.
func (r *typeRegistry) ensureLayout(unit int) bool {
	if !r.ensureTypes(unit) {
		return false
	}
	g := r.gens[unit]
	return g.typesDone || g.building == nil || g.building.state >= groupLaidOut
}

// ensureIfaces is ensureTypes for a caller that reads the owner's resolved
// interface signatures.
func (r *typeRegistry) ensureIfaces(unit int) bool {
	if !r.ensureTypes(unit) {
		return false
	}
	g := r.gens[unit]
	return g.typesDone || g.building == nil || g.building.state >= groupIfacesResolved
}

// deferringGroup is the group in which a mirror unit `from` asks for of a type
// unit `to` declares is made pending, or nil: `to` is resolving its fields,
// and `from` is building in the same group or is finished. A finished file
// asked for such a type is building a generic instance over it for `to`'s
// fields (`struct Parent { p: Pair<Int, Child> }` with Pair in that file).
func (r *typeRegistry) deferringGroup(from, to int) *typeGroup {
	grp := r.gens[to].building
	if grp == nil || grp.state != groupResolving {
		return nil
	}
	if f := r.gens[from]; f.building == grp || f.typesDone {
		return grp
	}
	return nil
}

// defersIfaceMirror is defersMirror for an interface, whose signatures are
// resolved after every member's types are laid out.
func (r *typeRegistry) defersIfaceMirror(from, to int) bool {
	grp := r.groupFor(to)
	return grp != nil && grp.state >= groupResolving && grp.state <= groupLaidOut && r.groupFor(from) == grp
}

// pendingIfaceMirror makes this gen's mirror of src, an interface another
// member of its type group declares, with its methods to be imported once
// every member's signatures are resolved.
func (g *gen) pendingIfaceMirror(src *ifaceDef, o *typeOwner) *ifaceDef {
	d := g.ifaceMirrorShell(src, o)
	grp := g.reg.groupFor(o.unit)
	grp.pendingIfaces = append(grp.pendingIfaces, pendingIfaceMirror{g: g, d: d, src: src})
	return d
}

// pendingMirror makes this gen's mirror of src, the declaration o names in
// another file whose group grp is resolving fields, as an identity whose
// kinds and layout grp.layOut fills in.
func (g *gen) pendingMirror(grp *typeGroup, src *typeDef, o *typeOwner) *typeDef {
	d := g.mirrorShell(src, o)
	d.layoutGroup = grp
	grp.pending = append(grp.pending, pendingMirror{g: g, d: d, src: src, o: o})
	return d
}

// buildGroup builds every member's type table, one phase over every member
// before the next. The phases are declareTypes', with buildTypes' split out.
func (r *typeRegistry) buildGroup(grp *typeGroup) {
	gens := make([]*gen, 0, len(grp.units))
	for _, u := range grp.units {
		gens = append(gens, r.gens[u])
	}
	for _, g := range gens {
		g.building = grp
	}
	grp.state = groupShells
	for _, g := range gens {
		g.collectHostPkgs()
		g.declareIfaces(g.nodes)
		g.declareTypeShells(g.nodes)
	}
	grp.state = groupResolving
	for _, g := range gens {
		g.resolveTypeShells(g.nodes)
	}
	grp.layOut(gens)
	for _, g := range gens {
		g.resolveIfaces()
	}
	// By index, as above: importing one interface's methods can make another
	// pending interface mirror.
	for i := 0; i < len(grp.pendingIfaces); i++ {
		p := grp.pendingIfaces[i]
		p.g.importIfaceMethods(p.d, p.src)
	}
	grp.state = groupIfacesResolved
	for _, g := range gens {
		g.mintTypeIDs()
		g.typesDone = true
	}
}

// layOut boxes, slots and settles the group's declarations once every
// member's fields are resolved, with the mirrors and generic instances that
// waited for them. A group of one file runs it too (buildTypes).
func (grp *typeGroup) layOut(gens []*gen) {
	// By index: importing one mirror's kinds can make another pending mirror,
	// or an instance over one.
	for i := 0; i < len(grp.pending); i++ {
		p := grp.pending[i]
		grp.pending[i].imported = p.g.importMirrorKinds(p.d, p.src, p.o)
	}
	grp.state = groupLayingOut
	for _, g := range gens {
		g.boxTypeFields()
	}
	for _, in := range grp.insts {
		in.d.boxFields()
	}
	// A mirror's boxing before any slot is assigned: zero-sizedness reads it.
	for _, p := range grp.pending {
		if p.imported {
			for i := range p.d.fields {
				p.d.fields[i].boxed = p.src.fields[i].boxed
			}
		}
	}
	for _, g := range gens {
		g.assignTypeSlots()
	}
	for _, in := range grp.insts {
		in.d.assignSlots()
	}
	for _, p := range grp.pending {
		if p.imported {
			p.g.copyMirrorLayout(p.d, p.src, p.o)
		}
	}
	var order []*typeDef
	for _, g := range gens {
		order = append(order, g.typeSettleOrder()...)
	}
	for _, in := range grp.insts {
		order = append(order, in.d)
	}
	// settleLowerable starts each declaration over from its own refusals, and
	// a mirror or an instance only ever turns unlowerable, so this ends.
	for changed := true; changed; {
		changed = false
		gens[0].settleLowerable(order)
		for _, p := range grp.pending {
			if p.d.lowerable && !p.src.lowerable {
				refuseMirror(p.d, p.src, p.o)
				changed = true
			}
		}
		if !changed {
			changed = grp.registerInsts()
		}
	}
	for _, p := range grp.pending {
		p.d.layoutGroup = nil
	}
	for _, in := range grp.insts {
		in.d.layoutGroup = nil
	}
	grp.state = groupLaidOut
}

// registerInsts registers the impls of every waiting instance still lowerable
// once the group is settled, as genericInstance does for one built whole, and
// reports whether one was refused, which the caller settles again so its
// containers follow.
func (grp *typeGroup) registerInsts() bool {
	refused := false
	for ; grp.registered < len(grp.insts); grp.registered++ {
		in := grp.insts[grp.registered]
		if !in.d.lowerable {
			continue
		}
		if why, detail := in.g.registerDeferredInstImpls(in.d); why != "" {
			in.d.lowerable = false
			in.d.why, in.d.whyDetail = why, detail
			refused = true
		}
	}
	return refused
}

// findTypeGroups is every type group in the program, indexed by unit: nil for a
// unit on no cycle. Nil when there is none.
//
// Only files that share an import cycle can share a type cycle, since naming
// another file's type needs an import, so only those are walked. owner is
// componentOwners' answer.
func findTypeGroups(gens []*gen, owner []int) []*typeGroup {
	n := len(gens)
	if n < 2 || len(owner) != n {
		return nil
	}
	size := map[int]int{}
	for _, o := range owner {
		size[o]++
	}
	edges := make([][]int, n)
	found := false
	for u, g := range gens {
		if g == nil || size[owner[u]] < 2 {
			continue
		}
		for _, v := range g.typeDeclEdges() {
			if owner[v] == owner[u] {
				edges[u] = append(edges[u], v)
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	var out []*typeGroup
	groupOf := make([]*typeGroup, n)
	for _, scc := range stronglyConnected(edges) {
		if len(scc) < 2 {
			continue
		}
		grp := &typeGroup{units: scc}
		for _, u := range scc {
			groupOf[u] = grp
		}
		out = append(out, grp)
	}
	if out == nil {
		return nil
	}
	return groupOf
}

// typeDeclEdges is every other unit that declares a type or interface this
// file's type, interface or alias declarations name, at any depth (a block's
// own declarations included).
func (g *gen) typeDeclEdges() []int {
	seen := map[int]bool{}
	var out []int
	add := func(o *typeOwner) {
		if o != nil && !seen[o.unit] {
			seen[o.unit] = true
			out = append(out, o.unit)
		}
	}
	names := func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch t := m.(type) {
			case *ast.SimpleType:
				add(g.typeRefOwnerAt(t.Name, t.Line, t.Col))
			case *ast.GenericType:
				add(g.typeRefOwnerAt(t.Name, t.Line, t.Col))
			case *ast.TypeIdent:
				add(g.typeRefOwnerAt(t.Name, t.Line, t.Col))
			case *ast.QualifiedType:
				var member string
				switch mt := t.Member.(type) {
				case *ast.SimpleType:
					member = mt.Name
				case *ast.GenericType:
					member = mt.Name
				}
				add(g.ownerOfSymbol(resolvedScopeSymbol(g.moduleScope(t.Module), member)))
			}
			return true
		})
	}
	for _, n := range g.nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			switch t := m.(type) {
			case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.InterfaceDef, *ast.TypeAlias:
				names(m)
				return false
			case *ast.Block:
				for _, stmt := range t.Stmts {
					if imp, ok := stmt.(*ast.ImportStmt); ok {
						g.blockImportOwners(imp, add)
					}
				}
			}
			return true
		})
	}
	return out
}

// blockImportOwners calls add with the owner of each monomorphic struct, enum
// or distinct type imp, an import at the top of a block, binds: the types
// bindBlockImports mirrors while this file's types are built, whether or not
// the block declares a type (`//! import std/sql_testing.Fake` in a prompt of
// std/sql, whose Fake names sql's Dialect).
func (g *gen) blockImportOwners(imp *ast.ImportStmt, add func(*typeOwner)) {
	if g.fa == nil {
		return
	}
	bound := g.srcFA().BlockImportScopes[imp]
	if bound == nil {
		return
	}
	for _, sym := range bound.Symbols {
		if sym == nil || sym.Node != ast.Node(imp) {
			continue
		}
		decl := resolveSymbol(sym)
		if decl == nil {
			continue
		}
		switch decl.Kind {
		case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType:
			if o := g.ownerOfSymbol(decl); o != nil && !o.isIface {
				add(o)
			}
		}
	}
}

// typeRefOwnerAt is typeRefOwner for the type name written at line:col,
// resolved first through the checker's reference there, which also sees a name
// an import at the top of an enclosing block binds (`import b.B`, then a
// block-local `struct Local { x: B }`).
func (g *gen) typeRefOwnerAt(name string, line, col int) *typeOwner {
	if g.fa != nil {
		if o := g.ownerOfSymbol(resolveSymbol(g.fa.References[analysis.Pos{Line: line, Col: col}])); o != nil {
			return o
		}
	}
	return g.typeRefOwner(name)
}

// typeRefOwner is the other file's declaration a type name in this file
// resolves to, bare (`B`) or module-qualified in one name (`b.B`).
func (g *gen) typeRefOwner(name string) *typeOwner {
	if o := g.foreignDecl(name); o != nil {
		return o
	}
	if dot := strings.LastIndex(name, "."); dot > 0 && dot < len(name)-1 {
		return g.ownerOfSymbol(resolvedScopeSymbol(g.moduleScope(name[:dot]), name[dot+1:]))
	}
	return nil
}

// stronglyConnected is Tarjan's strongly connected components of a graph given
// as adjacency lists, each component's members in ascending order and the
// components in the order Tarjan finishes them.
func stronglyConnected(edges [][]int) [][]int {
	n := len(edges)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	var out [][]int
	next := 0
	var visit func(u int)
	visit = func(u int) {
		index[u], low[u] = next, next
		next++
		stack = append(stack, u)
		onStack[u] = true
		for _, v := range edges[u] {
			switch {
			case index[v] < 0:
				visit(v)
				low[u] = min(low[u], low[v])
			case onStack[v]:
				low[u] = min(low[u], index[v])
			}
		}
		if low[u] != index[u] {
			return
		}
		var scc []int
		for {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[v] = false
			scc = append(scc, v)
			if v == u {
				break
			}
		}
		slices.Sort(scc)
		out = append(out, scc)
	}
	for u := range n {
		if index[u] < 0 {
			visit(u)
		}
	}
	return out
}
