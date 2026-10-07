package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Existentials that cross a FILE boundary, and the census of boxing sites.
//
// # The representation
//
// The existential representation itself is applied by impl.go: an
// interface-typed position is `rt.Dyn`, erasure happens at the site the checker
// accepted the widening, and dispatch goes through `rt.Method` keyed on the
// ADDRESS of the erased type's `rt.TypeID`
// (testdata/impl_field_existential.nomi). Across a file boundary three things
// must hold:
//
//  1. `typeOf` resolves an interface name in `g.ifaces`, which holds this
//     file's declarations, and falls back to a MIRROR of a sibling file's
//     declaration. Without the mirror, a field annotated with a sibling file's
//     interface would have no kind and be reported `non-scalar field type`.
//  2. A dispatch TABLE lives with the interface declaration. A dispatch site
//     in another package names the owner's table, never a second one: two
//     tables for one interface are two disjoint sets of implementations and
//     whichever the site happened to probe would answer.
//  3. Erasure needs the CONCRETE type's runtime identity, and two files each
//     minting one for the same type would be two identities for one type.
//
// # Identity: one variable, named from wherever
//
// (3) is settled the way (2) is: the identity variable belongs to the package
// that DECLARES the type, and every other package names it qualified. It
// cannot be minted on demand. A mirror in module 2 discovers it needs module
// 1's identity while module 2's body is being built, and modules are built in
// order, so module 1 may already have been rendered, and a demand arriving
// after the fact would either dangle or force a second render pass over every
// package.
//
// So the identity is minted EAGERLY, once per lowered named type, in the
// package that declares it. A Nomi type HAS a runtime identity whether or not
// this particular program erases it; the variable's existence does not depend
// on build order. The cost is one `rt.TypeID{Nomi: "mod.Name"}` per declared
// type in its own package.
//
// # Whether an impl exists is a PROGRAM question
//
// `erase` refuses to build a box no `init` binds. This file's impls are the
// wrong scope once files exchange existentials: `EffectsApp{logger:
// ProdLogger}` in one file boxes a type declared in a second whose `impl
// Logger` is written in a third, and all three are the same program. implIndex
// answers it program-wide, keyed on the two DECLARATION NODES involved rather
// than on names, as foreign.go explains.
//
// # The census
//
// A silent fall-through to boxing shows up only as a performance regression,
// never as a wrong answer. Every differential test in this package compares
// OBSERVABLE behaviour, so none of them can see it: a program that boxes where
// it could have called directly prints exactly the same bytes.
//
// So the builder counts. Every erasure records the interface and the concrete
// type boxed; every interface-qualified call records whether it resolved to a
// direct call or to a table probe. A fixture pins the numbers, so erasing a
// statically-known receiver and dispatching it through the table moves them.
// It is a DIAGNOSTIC and changes nothing about what is built.

// erasureSite is one place a concrete value was boxed into an existential.
type erasureSite struct {
	iface    string
	concrete string
}

// dispatchCensus is what the builder observed about existentials while lowering
// one program: what it boxed, and which way each interface-qualified call went.
//
// Direct and table are counted separately rather than as one total because the
// RATIO is the diagnostic. A boxing regression does not change how many
// dispatches happen; it changes which kind they are.
type dispatchCensus struct {
	boxes []erasureSite
}

// --- cross-file interfaces ---------------------------------------------------

// foreignIface resolves an interface name this file does not declare to a
// MIRROR of the sibling file's declaration, reporting whether the name named
// one at all.
//
// The mirror is registered in g.ifaces under the name it was reached by, so
// typeOf, implCall, registerImpl and the dispatch machinery all reach it
// through the one map an interface name is ever looked up in. Nothing
// downstream distinguishes a mirror from a local declaration except the two
// places that must: a dispatch table is the owner's variable, and an interface
// DEFAULT body cannot be lowered here.
//
// Every refusal below is the exact analogue of foreignType's, and for the same
// reason in each case — a refused mirror rather than a miss, so the report says
// "this interface exists and closes a type cycle" instead of "no such interface
// here".
func (g *gen) foreignIface(name string) (*ifaceDef, bool) {
	o := g.foreignIfaceOwner(name)
	if o == nil {
		return nil, false
	}
	d := g.mirrorIface(o)
	g.ifaces[name] = d
	return d, true
}

// mirrorIface is this package's def for another file's interface DECLARATION,
// built once per declaration node.
//
// Split out of foreignIface so the by-NAME and by-ORIGIN questions reach one
// def per declaration. Those are two different questions — a name is what the
// program wrote and an origin is what the checker solved — and they must not
// build two mirrors of one declaration, because `existential(d)` takes its
// identity from this pointer: two defs for one interface is two dispatch
// tables, which is what foreign.go's own header refuses a per-package identity
// for. Registration in
// g.ifaces stays in the by-name caller, because only a name has a name to be
// registered under.
func (g *gen) mirrorIface(o *typeOwner) *ifaceDef {
	if d, ok := g.foreignIfaces[o.decl]; ok {
		return d
	}
	d := g.blockedIface(o, "", "")
	switch {
	case !g.reg.ensureTypes(o.unit):
		// The owner's interface shells are declared by the same demand-driven
		// pass that builds its type table, so a build already in progress is
		// the same cycle, reported under the same key.
		d = g.blockedIface(o, "sibling file type cycle", o.fileKey+"."+o.nomi)
	default:
		src := g.reg.gens[o.unit].ifaces[o.nomi]
		switch {
		case src == nil:
			// The declaring file registered no shell, which happens only for
			// a duplicate declaration its own file already refuses.
			d = g.blockedIface(o, "duplicate interface declaration", o.fileKey+"."+o.nomi)
		case !src.lowerable:
			d = g.blockedIface(o, src.why, o.fileKey+"."+o.nomi)
		default:
			d = g.importIface(src, o)
		}
	}
	g.foreignIfaces[o.decl] = d
	return d
}

// solvedIface resolves an interface the ANALYZER solved — a type the program
// never spelled — to this package's def for it, by `(Origin, Name)`.
//
// # The same three tables typeOf asks
//
// `typeOf` asks THREE tables for an interface name: this file's own `g.ifaces`,
// a sibling file's declaration through `foreignIface`, and an anchored stdlib
// interface through `stdIfaceNamed`. `project` asks the same question about a
// type the checker solved rather than one the programmer wrote, so it must ask
// all three too: `projectStdIface` answers the third and this answers the other
// two. Otherwise a USER interface would be reachable by ANNOTATION and
// unreachable by INFERENCE, and projectRefusal's fall-through would name
// `unrepresentable inferred type | Speaker` for an interface `typeOf` lowers
// to `rt.Dyn` in the same gen. The cross-check in projectreason_test.go holds
// the two in agreement.
//
// # Identity is (Origin, Name), not the name
//
// Resolved by ORIGIN rather than through this file's imports, for the reason
// siblingOwner gives: the positions that reach here never spell the name, so
// there is no import binding to consult, and two sibling files may each
// declare a `Renderer`. A name-keyed lookup answers one declaration for both,
// and the miss is SILENT — a dispatch table is keyed on the interface's
// declaration, so the wrong one probes a disjoint set of impls.
//
// A LOCAL answer must be a local DECLARATION: `g.ifaces` also holds mirrors,
// registered under the name they were reached by, and one of those belongs to
// another origin however this file spells it.
//
// The `d.foreign != ""` guard is UNREACHABLE BY CONSTRUCTION: `ifaceNamed`
// asks `g.ifaces` before `foreignIface`, so a file that declares `Renderer`
// never gets a mirror registered under that name, and only a file that
// declares it owns this origin. It is a fail-safe: without it a mirror would
// answer for a declaration it is not, and every impl lookup keys on the
// declaration.
func (g *gen) solvedIface(origin, name string) (*ifaceDef, bool) {
	if origin == "" || g.fa == nil || g.fa.Origin == "" {
		return nil, false
	}
	if origin == g.fa.Origin {
		d, found := g.ifaces[name]
		if !found || d.foreign != "" {
			return nil, false
		}
		return d, true
	}
	o := g.siblingIfaceOwner(origin, name)
	if o == nil {
		return nil, false
	}
	return g.mirrorIface(o), true
}

// siblingIfaceOwner is siblingOwner for an INTERFACE another user file
// declares: the declaration of `name` in the module whose analyzer ORIGIN is
// `origin`, resolved without consulting this file's imports.
//
// `!o.isIface` refuses rather than falls through, which is the mirror of the
// `o.isIface` guard siblingOwner keeps: a type lookup must never come back
// holding an interface, and an interface lookup must never come back holding a
// type. Either direction would hand `existential()` or `named()` a def of the
// wrong shape, and neither constructor can tell.
func (g *gen) siblingIfaceOwner(origin, name string) *typeOwner {
	if g.reg == nil || origin == "" {
		return nil
	}
	for unit, src := range g.reg.gens {
		if src == nil || src.fa == nil || src.fa.Origin != origin {
			continue
		}
		d := src.ifaces[name]
		if d == nil || d.decl == nil {
			return nil
		}
		o := g.reg.byDecl[d.decl]
		if o == nil || !o.isIface || o.unit != unit {
			return nil
		}
		return o
	}
	return nil
}

// importIfaceKind re-derives an EXISTENTIAL kind relative to this package: the
// owner's `tagIface` kind, rebuilt over this package's mirror of the same
// interface DECLARATION.
//
// A boxed value crossing a file boundary has ONE identity: `hasTID`'s mirror
// path answers from the OWNER's table, and `mintTypeIDs` records eagerly in the
// declaring file so a demand arriving after that file was walked can still be
// served.
//
// What this needs is the interface's own def: `k.iface` is interned in the
// OWNER's gen, and rendering it from here would name a dispatch table this
// package cannot see. `mirrorIface` is what `foreignIface` builds for the same
// declaration reached by NAME, so this is the third route onto one def rather
// than a new mechanism, which keeps `existential(d)` a single identity, since a
// kind's iface identity IS the pointer.
//
// # The composition hazard
//
// A `hasTID` regression here is SILENT. A TypeID is what `rt.Method` keys on,
// so a second identity for one Nomi type does not fail to compile: the probe
// misses, `Display` renders the anonymous form, `==` degrades to structural
// comparison and answers False, and ordering traps. That is why
// testdata/cotenant_existential/ boxes a THIRD co-tenant's type and then READS
// the table twice with two implementors whose answers share no character —
// building the value proves nothing here.
//
// # The crossing-back arm
//
// The fixture reaches it, because `core.App.tag` is typed by wire's OWN
// `Tagger`. Without it the fall-through gives the same answer today:
// `mirrorIface` on a self-owner resolves through
// `g.reg.gens[o.unit].ifaces[o.nomi]`, which IS the local def, and every
// downstream consumer keys on `decl` (implKeyFor). That equivalence is a
// coincidence of three unrelated lookups and not a property anything promises.
// A consumer keying on the `*ifaceDef` POINTER, which `existential(d)` makes
// the kind's identity, would see a self-mirror as two identities for one
// interface, with the silent symptom described above.
func (g *gen) importIfaceKind(src *ifaceDef) (kind, bool) {
	if g.reg == nil || src == nil || src.decl == nil {
		g.importWhy = "existential over a sibling file type"
		return kindInvalid, false
	}
	// The DECLARATION node, not the def: `src` may itself be a mirror the owner
	// built of a third file's interface, and a mirror carries the original
	// declaration. Resolving through it is what makes the answer the same
	// whichever gen the walk arrived from.
	o := g.reg.byDecl[src.decl]
	if o == nil || !o.isIface {
		g.importWhy = "non-local type"
		return kindInvalid, false
	}
	if o.unit == g.fileUnit {
		// The owner's component names an interface declared in THIS file — the
		// reference crosses back. Its own def is the identity and the mirror
		// must not be a second one, exactly as importNamed's crossing-back arm
		// requires for a type.
		local := g.ifaces[src.nomi]
		if local == nil || local.decl != src.decl {
			g.importWhy = "non-local type"
			return kindInvalid, false
		}
		if !local.lowerable {
			g.importWhy = local.why
			return kindInvalid, false
		}
		return existential(local), true
	}
	d := g.mirrorIface(o)
	if !d.lowerable {
		// A mirror the declaring file or the import graph refuses. It records
		// its OWN reason, so the report names that refusal rather than a
		// representation gap that is not there — the contract foreignIface's
		// own header states.
		g.importWhy = d.why
		return kindInvalid, false
	}
	return existential(d), true
}

// blockedIface is a mirror that names the other file's interface and refuses
// every use of it, under the reason it was refused for.
func (g *gen) blockedIface(o *typeOwner, why, detail string) *ifaceDef {
	id, _ := o.decl.(*ast.InterfaceDef)
	return &ifaceDef{
		nomi:      o.nomi,
		decl:      id,
		methods:   map[string]*ifaceMethod{},
		foreign:   o.fileKey,
		pkg:       o.pkg,
		unit:      o.unit,
		why:       why,
		whyDetail: detail,
		lowerable: why == "",
	}
}

// importIface builds this package's mirror of another package's ifaceDef.
//
// The METHOD SHAPE is copied verbatim — names, self positions, the receiver
// slot, which methods are dispatchable — because those are properties of the
// declaration the owner read, and this package is naming that same
// declaration. Only the KINDS are re-derived, through importKind, so every
// parameter and result renders relative to this package. That is the same
// split importDef makes for a type mirror, for the same reason: a kind
// interned in the owner's table renders as a Go name that is correct there and
// undefined here.
//
// A method whose signature cannot cross the boundary loses its TABLE rather
// than its interface: the interface stays usable as a type and as a direct
// qualifier, and the refusal lands at a dispatch site, which is a position
// somebody wrote. Refusing the whole interface would make a file unlowerable
// for a method it may never call.
func (g *gen) importIface(src *ifaceDef, o *typeOwner) *ifaceDef {
	d := &ifaceDef{
		nomi:      src.nomi,
		decl:      src.decl,
		methods:   make(map[string]*ifaceMethod, len(src.order)),
		fields:    make(map[string]*ifaceField, len(src.fieldOrder)),
		foreign:   o.fileKey,
		pkg:       o.pkg,
		unit:      o.unit,
		lowerable: true,
	}
	// Registered before the methods are imported: an interface function may
	// name a type whose own mirror reaches back here.
	g.foreignIfaces[o.decl] = d
	for _, sm := range src.order {
		m := &ifaceMethod{
			name: sm.name,
			decl: sm.decl,
			// ONE field, so a mirror cannot copy part of the self-position model
			// and silently disagree with the owner's (a kind comparison never
			// looks at it). selfpos.go's selfShape cannot be half-copied.
			shape:  sm.shape,
			why:    sm.why,
			params: make([]kind, len(sm.params)),
		}
		ok := true
		for i, k := range sm.params {
			if sm.shape.selfTyped(i) {
				// A self position has no kind until an implementing type
				// supplies one, so there is nothing to re-derive.
				m.params[i] = k
				continue
			}
			ik, fine := g.importKind(k)
			if !fine {
				ok = false
				break
			}
			m.params[i] = ik
		}
		if ok {
			if ik, fine := g.importKind(sm.result); fine {
				m.result = ik
			} else {
				ok = false
			}
		}
		switch {
		case !ok:
			m.table, m.why = false, "non-scalar sibling file interface signature"
		case !sm.table:
			m.table = false
		default:
			// One interface, one table: the OWNER's variable, qualified only
			// when the owner is a different Go package. Qualifying
			// unconditionally reads `nomimod0.NomiX_…` from inside nomimod0
			// itself once a component shares a package, which Go rejects as a
			// self-import — and if it ever resolved it would be a wrong
			// ANSWER rather than a compile error, because a table name is only
			// ever compared and bound. See component.go.
			m.table = sm.table
		}
		d.methods[m.name] = m
		d.order = append(d.order, m)
	}
	// A `field` requirement mirrors on exactly the footing a method does: the
	// NAME is the owner's, the KIND is re-derived here, and the table is the
	// owner's variable qualified. A requirement whose type cannot cross the
	// boundary loses its table and nothing else, so the interface stays usable
	// and the refusal lands at a read site.
	for _, sf := range src.fieldOrder {
		f := &ifaceField{name: sf.name}
		ik, fine := g.importKind(sf.k)
		switch {
		case !fine || !sf.table:
			f.k = kindInvalid
		default:
			f.k, f.table = ik, sf.table
		}
		d.fields[f.name] = f
		d.fieldOrder = append(d.fieldOrder, f)
	}
	return d
}

// ifaceNamed resolves an interface NAME to this package's def for it: its own
// declaration, or a mirror of a sibling file's.
//
// The one place an interface name is looked up, since g.ifaces is not the
// whole answer, on exactly the footing namedType stands on for types. Local
// wins, for the same reason.
func (g *gen) ifaceNamed(name string) (*ifaceDef, bool) {
	if d, local := g.ifaces[name]; local {
		return d, true
	}
	return g.foreignIface(name)
}

// --- a sibling file's interface DEFAULT --------------------------------------

// portableDefault reports whether a sibling file's interface default can be
// monomorphized HERE without changing what its body means.
//
// An interface default is not called across the boundary, it is COPIED: impl.go
// lowers the body once per implementing type, with `self` bound to that type.
// So the body's names resolve in the IMPLEMENTING file's scope, while the
// programmer wrote them against the DECLARING file's. When the two files
// resolve a name differently that is a silent wrong answer rather than a Go
// compile error — the same hazard foreign.go refuses `sibling file field
// default` for.
//
// Refusing every cross-file default would be too blunt: in
// `15-app-and-defer/effects/log.nomi`, `Logger.info`'s whole body is
// `Logger.log(value, "INFO", message)` — one module-level name, `Logger`,
// which both files resolve to the same declaration.
//
// So the question asked is the precise one: does every name the body mentions
// resolve to the SAME declaration from both files? Names are collected
// reflectively and the set is deliberately a SUPERSET — it includes field
// names, parameter names and locals, none of which are module-level — because
// a name that resolves in neither scope agrees trivially, and a name that
// resolves in only one is exactly the divergence being looked for. Over-
// collecting can only over-refuse.
func (g *gen) portableDefault(d *ifaceDef, m *ifaceMethod) bool {
	if m == nil || m.decl == nil || d == nil || g.reg == nil {
		return false
	}
	if d.unit < 0 || d.unit >= len(g.reg.gens) {
		return false
	}
	owner := g.reg.gens[d.unit]
	if owner == nil || owner.fa == nil || g.fa == nil {
		return false
	}
	names := map[string]bool{}
	walkNames(m.decl.Body, names, pickAnyName)
	for i := range m.decl.Params {
		walkNames(m.decl.Params[i].TypeAnnotation, names, pickAnyName)
		// The DEFAULT expression too, since impl.go monomorphizes a default
		// with defaulted parameters. It is Nomi written in the declaring file
		// exactly as the body is, and it is copied by exactly the same route,
		// so leaving it out of this walk would let `suffix: String = tag`
		// resolve `tag` here while the programmer wrote it there.
		walkNames(m.decl.Params[i].Default, names, pickAnyName)
	}
	for name := range names {
		there, here := resolvedTypeSymbol(owner.fa, name), resolvedTypeSymbol(g.fa, name)
		if valueOnce(there) && (here == nil || here.Kind == analysis.SymbolOnce) {
			// The declaring file's own `once`: the lowered default reads
			// that file's cell, whatever this file binds the name to
			// (onceValue under namesFrom). A function-valued one could be
			// called, which reads this file's binding, so it stays refused.
			continue
		}
		switch {
		case there == nil && here == nil:
			// A parameter, a local, a field label, or a name neither file has.
			// Nothing for the two scopes to disagree about.
		case there == nil || here == nil:
			return false
		case there.Node != here.Node:
			return false
		}
	}
	return true
}

// valueOnce reports a symbol for a module-level `once` whose type is not a
// function's.
func valueOnce(sym *analysis.Symbol) bool {
	if sym == nil || sym.Kind != analysis.SymbolOnce {
		return false
	}
	if _, ok := sym.Node.(*ast.OnceBinding); !ok {
		return false
	}
	_, fn := sym.Type.(*analysis.FuncType)
	return !fn
}

// pickAnyName contributes every name a node mentions that a MODULE SCOPE could
// resolve — an identifier, a type, a file qualifier.
//
// A MEMBER name is deliberately excluded. `Logger.log(value, "INFO", message)`
// mentions `Logger` at module scope and `log` only relative to it, and a
// member is resolved against its object rather than against the file. Reading
// it as a module-level name is wrong: in `15-app-and-defer/effects` the
// implementing file is `log.nomi`, whose own file API object is bound under
// the spelling `log`, so the member `log` would make the two files disagree
// and refuse a default that is portable. Over-collection is harmless for the
// reference graph, where it can only over-refuse an import cycle; here it
// would refuse a correct program.
func pickAnyName(n ast.Node, out map[string]bool) bool {
	switch t := n.(type) {
	case *ast.FieldAccess:
		// The object only. Returning true stops the generic descent, which
		// would otherwise reach Field.
		walkNames(t.Object, out, pickAnyName)
		return true
	case *ast.Ident:
		out[t.Name] = true
	case *ast.SimpleType:
		out[t.Name] = true
	case *ast.GenericType:
		out[t.Name] = true
	case *ast.QualifiedType:
		out[t.Module] = true
	case *ast.TypeIdent:
		out[t.Name] = true
	}
	return false
}

// --- whether an impl exists, program-wide ------------------------------------

// implKey identifies one (interface, receiver) pair by DECLARATION NODE.
//
// Nodes rather than names because that is what an identity is here: two files
// may each declare a `Logger`, the checker keeps them apart nominally, and a
// name-keyed index would let one file's impl satisfy the other's interface.
// The receiver of an impl for a BUILTIN has no declaration node, so its key is
// the scalar kind's own tag — distinct from every node by construction, since
// no node is an integer.
type implKey struct {
	iface ast.Node
	recv  ast.Node
	tag   tag
}

// implIndex is every lowerable impl in one program, so a boxing site in one
// file can ask whether ANY file binds the implementation its box will need.
type implIndex struct {
	has map[implKey]bool
}

// buildImplIndex indexes the program's impls, after every file's impl tables
// are built and before any body is lowered.
func buildImplIndex(gens []*gen) *implIndex {
	x := &implIndex{has: map[implKey]bool{}}
	for _, g := range gens {
		if g == nil {
			continue
		}
		// An impl registered against a generic INSTANCE, which is not in
		// implOrder. Keyed by implKeyFor on the RECEIVER'S DECLARATION NODE,
		// which every instance of one template shares — and that collapse is
		// CORRECT here rather than a hazard: `impl Greet for Wrapper<T>` applies
		// to every instantiation of `Wrapper`, so "the program lowers an impl of
		// Greet for some Wrapper" is exactly the claim a boxing site needs. See
		// genericimpl.go.
		for _, e := range g.genericImplQueue {
			d := e.impl
			if !d.lowerable || d.iface == nil || d.iface.decl == nil {
				continue
			}
			x.has[implKeyFor(d.iface, d.recv)] = true
		}
		for _, d := range g.implOrder {
			if !d.lowerable || d.iface == nil || d.iface.decl == nil {
				continue
			}
			x.has[implKeyFor(d.iface, d.recv)] = true
		}
	}
	return x
}

// implKeyFor is the key an interface and a receiver kind share across packages.
func implKeyFor(d *ifaceDef, recv kind) implKey {
	k := implKey{iface: d.decl, tag: recv.tag}
	if recv.tag == tagNamed && recv.def != nil {
		k.recv = recv.def.decl
	}
	return k
}

// bindsImpl reports whether this PROGRAM lowers an impl of d for k.
//
// The program rather than this file, which is the whole point: a value boxed
// here dispatches through a table any package may have bound into, and asking
// only about the local file would refuse a box the program does in fact
// implement. Falls back to the local view when there is no program — the
// single-module fixture path lowers one file with no registry at all.
func (g *gen) bindsImpl(d *ifaceDef, k kind) bool {
	if d == nil {
		return false
	}
	if d.universal {
		// A STRUCTURAL marker: conformance is the value's shape, and there is no
		// `impl` block anywhere to find — the analyzer REJECTS one. So both
		// indexes below are empty for it by construction and every boxing site
		// would answer no. See structiface.go.
		return structIfaceSatisfiedBy(k)
	}
	// A STDLIB impl of a STDLIB interface is not an `impl` block in any
	// module, so neither index below holds it, and without this every boxing
	// site would answer `gapNoImplForBound` ("no impl of Display is lowered for
	// Int") on a program the checker accepted. flushErasedBindings names std's
	// own function for these, so the
	// answer here and the binding there are two halves of one claim and are
	// asked through one function. See debugerased.go.
	if g.bindsStdScalarImpl(d, k) {
		return true
	}
	if g.impls != nil && d.decl != nil && g.impls.has[implKeyFor(d, k)] {
		return true
	}
	// The LOCAL table, asked whether or not there is a program index. The
	// index above is built from `g.implOrder`, and an impl registered against
	// a generic INSTANCE is deliberately not in that list (registerImplAt says
	// why), so for an instance receiver the index is a strict SUBSET of what
	// this gen lowers. Asking only the index would answer "no impl of Greet is
	// lowered for Wrapper" for a program that lowers exactly that impl.
	//
	// Additive in the safe direction: this can only turn a "no" into a "yes" for
	// an impl THIS gen registered and marked lowerable, never the reverse.
	impl := g.implsByIface[d.nomi][k]
	return impl != nil && impl.lowerable
}
