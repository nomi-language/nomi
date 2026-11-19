package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Existentials that cross a file boundary.
//
// # What must hold
//
// An interface-typed position holds the concrete value itself, boxed where
// its run-time type does not name its impl (irboxed.go), and a dispatch
// selects the impl recorded under the method's symbol for that type
// (testdata/impl_field_existential.nomi). Across a file boundary three things
// must hold:
//
//  1. `typeOf` resolves an interface name in `g.ifaces`, which holds this
//     file's declarations, and falls back to a mirror of a sibling file's
//     declaration. Without the mirror, a field annotated with a sibling file's
//     interface would have no kind and be reported `non-scalar field type`.
//  2. A method's dispatch symbol belongs to the unit that declares the
//     interface. A dispatch site in another file names the owner's symbol
//     (irIfaceMethodSym), never a second one: two symbols for one method are
//     two disjoint sets of recorded impls.
//  3. A type has one runtime identity, the declaring file's. A mirror answers
//     hasTID from the owner's table.
//
// # Identity is recorded eagerly
//
// The declaring file records an identity for every lowered named type it
// declares, before anything is lowered (mintTypeIDs). It cannot be recorded
// on demand: a mirror in module 2 asks for module 1's identity while module
// 2's body is being built, modules are built in order, and module 1 may
// already have been walked.
//
// # Whether an impl exists is a program question
//
// A value becomes an interface value only where the program lowers an impl of
// the interface for it (bindsImpl). This file's impls are the wrong scope once
// files exchange existentials: `EffectsApp{logger: ProdLogger}` in one file
// boxes a type declared in a second whose `impl Logger` is written in a
// third, and all three are the same program. implIndex answers it
// program-wide, keyed on the two declaration nodes involved and not on
// names, as foreign.go explains.

// --- cross-file interfaces ---------------------------------------------------

// foreignIface resolves an interface name this file does not declare to a
// MIRROR of the sibling file's declaration, reporting whether the name named
// one at all.
//
// The mirror is registered in g.ifaces under the name it was reached by, so
// typeOf, registerImpl and the dispatch plans all reach it through the one
// map an interface name is ever looked up in. Nothing downstream
// distinguishes a mirror from a local declaration except the two places that
// must: a dispatch names the owner's method symbol, and an interface default
// body is not lowered here.
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

// mirrorIface is this gen's def for another file's interface declaration,
// built once per declaration node.
//
// Split out of foreignIface so the by-name and by-origin questions reach one
// def per declaration. Those are two different questions (a name is what the
// program wrote and an origin is what the checker solved) and they must not
// build two mirrors of one declaration, because `existential(d)` takes its
// identity from this pointer: two defs for one interface would be two unequal
// existential kinds in one gen. Registration in g.ifaces stays in the by-name
// caller, because only a name has a name to be registered under.
func (g *gen) mirrorIface(o *typeOwner) *ifaceDef {
	if d, ok := g.foreignIfaces[o.decl]; ok {
		return d
	}
	d := g.blockedIface(o, "", "")
	deferred := g.reg.defersIfaceMirror(g.fileUnit, o.unit)
	switch {
	case !deferred && !g.reg.ensureIfaces(o.unit):
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
			// A generic interface's template stays usable through a
			// concrete qualifier here as in its own file.
			d.useSiteOnly = src.useSiteOnly
		case deferred:
			// A member of this file's type group, whose interface signatures
			// are not resolved yet. See typecycle.go.
			return g.pendingIfaceMirror(src, o)
		default:
			d = g.importIface(src, o)
		}
	}
	g.foreignIfaces[o.decl] = d
	return d
}

// solvedIface resolves an interface the analyzer solved, a type the program
// never spelled, to this gen's def for it, by `(Origin, Name)`.
//
// # The tables typeOf asks
//
// `typeOf` asks three tables for an interface name: this file's own `g.ifaces`,
// a sibling file's declaration through `foreignIface`, and an anchored stdlib
// interface through `stdIfaceNamed`. `project` asks the same question about a
// type the checker solved and not one the programmer wrote, and this answers
// the first two for it. Otherwise a user interface would be reachable by
// annotation and unreachable by inference, for an interface `typeOf`
// resolves in the same gen. The cross-check in projectreason_test.go holds
// the two in agreement.
//
// # Identity is (Origin, Name), not the name
//
// Resolved by origin and not through this file's imports, for the reason
// siblingOwner gives: the positions that reach here never spell the name, so
// there is no import binding to consult, and two sibling files may each
// declare a `Renderer`. A name-keyed lookup answers one declaration for both,
// and the miss is silent: impl lookups key on the interface's declaration, so
// the wrong one finds a disjoint set of impls.
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
		// The declaration the name has in the declaring file's scope, as
		// siblingOwner finds it: that gen's own table holds it only once
		// the gen has built its interfaces.
		sym := resolvedTypeSymbol(src.fa, name)
		if sym == nil || sym.Node == nil {
			return nil
		}
		o := g.reg.byDecl[sym.Node]
		if o == nil || !o.isIface || o.unit != unit {
			return nil
		}
		return o
	}
	return nil
}

// importIfaceKind re-derives an existential kind in this gen: the owner's
// `tagIface` kind, rebuilt over this gen's mirror of the same interface
// declaration.
//
// A boxed value crossing a file boundary has one identity: `hasTID`'s mirror
// path answers from the owner's table, and `mintTypeIDs` records eagerly in the
// declaring file so a demand arriving after that file was walked can still be
// served.
//
// What this needs is the interface's own def: `k.iface` is interned in the
// owner's gen, so the kind over it equals no existential this gen builds.
// `mirrorIface` is what `foreignIface` builds for the same declaration reached
// by name, so this is the third route onto one def and not a new mechanism,
// which keeps `existential(d)` a single identity, since a kind's iface
// identity is the pointer.
//
// # The composition hazard
//
// A `hasTID` regression here is silent, and building the value proves
// nothing. testdata/cotenant_existential/ therefore boxes a third co-tenant's
// type and then dispatches twice, with two implementors whose answers share
// no character.
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
		// importKind runs with a registry, and a user interface's kind
		// carries its def and declaration.
		panic("irbuild: importIfaceKind: an interface kind with no declaration, or no registry to mirror it through")
	}
	if src.stdOrigin != "" {
		// A std interface held in another file: this file's def of the
		// same declaration (stdprogiface.go).
		if d := g.stdProgIfaceAt(src.stdOrigin, src.decl.Name); d == nil || d.decl != src.decl {
			g.importWhy = "std interface signature"
			return kindInvalid, false
		} else if src.instOf == nil {
			if !d.lowerable {
				g.importWhy = d.why
				return kindInvalid, false
			}
			return existential(d), true
		}
	}
	if src.instOf != nil {
		// `Gets<String>` held in another file: its instance here.
		return g.importIfaceInstKind(src)
	}
	// The DECLARATION node, not the def: `src` may itself be a mirror the owner
	// built of a third file's interface, and a mirror carries the original
	// declaration. Resolving through it is what makes the answer the same
	// whichever gen the walk arrived from.
	o := g.reg.byDecl[src.decl]
	if o == nil || !o.isIface {
		// Every user interface declaration is registered as an interface
		// owner.
		panic("irbuild: importIfaceKind: the registry holds no interface owner for " + src.nomi)
	}
	if o.unit == g.fileUnit {
		// The owner's component names an interface declared in THIS file — the
		// reference crosses back. Its own def is the identity and the mirror
		// must not be a second one, exactly as importNamed's crossing-back arm
		// requires for a type.
		local := g.ifaces[src.nomi]
		if local == nil || local.decl != src.decl {
			// A file's names are unique, so its def for the name is that
			// declaration's.
			panic("irbuild: importIfaceKind: the declaring file's own " + src.nomi + " is not the declaration its mirror carries")
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
		unit:      o.unit,
		why:       why,
		whyDetail: detail,
		lowerable: why == "",
	}
}

// importIface builds this gen's mirror of another gen's ifaceDef.
//
// The method shape is copied: names, self positions, the receiver slot, which
// methods are dispatchable. Those are properties of the declaration the owner
// read, and this gen is naming that same declaration. Only the kinds are
// re-derived, through importKind, so every parameter and result is interned
// in this gen. importDef makes the same split for a type mirror, for the same
// reason: a kind interned in the owner's table equals no kind this gen
// builds.
//
// A method whose signature cannot cross the boundary has `table` cleared and
// the interface stays: it is still usable as a type and as a direct
// qualifier, and the refusal lands at a dispatch site, which is a position
// somebody wrote. Refusing the whole interface would make a file unlowerable
// for a method it may never call.
func (g *gen) importIface(src *ifaceDef, o *typeOwner) *ifaceDef {
	d := g.ifaceMirrorShell(src, o)
	g.importIfaceMethods(d, src)
	return d
}

// ifaceMirrorShell is an interface mirror with no methods yet, registered in
// the intern map.
//
// Registered before the methods are imported: an interface function may name a
// type whose own mirror reaches back here.
func (g *gen) ifaceMirrorShell(src *ifaceDef, o *typeOwner) *ifaceDef {
	d := &ifaceDef{
		nomi:      src.nomi,
		decl:      src.decl,
		methods:   make(map[string]*ifaceMethod, len(src.order)),
		foreign:   o.fileKey,
		unit:      o.unit,
		lowerable: true,
	}
	g.foreignIfaces[o.decl] = d
	return d
}

// importIfaceMethods imports the owner's resolved method shapes into a mirror.
func (g *gen) importIfaceMethods(d, src *ifaceDef) {
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
			if sm.shape.selfTyped(i) || sm.mentionsSelfAt(i) {
				// A self position has no kind until an implementing type
				// supplies one, so there is nothing to re-derive; nor has a
				// type holding self (`List<self>`), which a default inherited
				// here reads per implementor (inheritedDefaultSig).
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
		if ok && sm.decl != nil && typeExprHasSelf(sm.decl.ReturnTypeExpr) {
			m.result = sm.result
		} else if ok {
			if ik, fine := g.importKind(sm.result); fine {
				m.result = ik
			} else {
				ok = false
			}
		}
		switch {
		case !ok && sm.why == gapGenericIfaceFunc:
			// A position naming the function's own type parameter has no
			// kind until a call instantiates it (methodinst.go), which a
			// generic default is withheld for in this file as in its own.
			m.table = false
		case !ok:
			m.table, m.why = false, "non-scalar sibling file interface signature"
		case !sm.table:
			m.table = false
		default:
			// The owner's answer: the method dispatches here exactly when
			// it does in its own file.
			m.table = sm.table
		}
		d.methods[m.name] = m
		d.order = append(d.order, m)
	}
}

// ifaceNamed resolves an interface name to this gen's def for it: its own
// declaration, or a mirror of a sibling file's.
//
// The one place an interface name is looked up, since g.ifaces is not the
// whole answer, on exactly the footing namedType stands on for types. Local
// wins, for the same reason.
func (g *gen) ifaceNamed(name string) (*ifaceDef, bool) {
	if d, imported := g.blockImportedIface(name); imported {
		// A sibling file's interface a block's import binds
		// (bindBlockImports). A std interface the import binds is
		// stdIfaceNamed's or stdProgIfaceNamed's to answer.
		if d.stdOrigin == "" && !d.stdShared {
			return d, true
		}
		return nil, false
	}
	if sym, inBlock := g.blockModuleSymbol(name); inBlock {
		// `store.Store` through a qualifier a visible block's import binds:
		// that file's interface, as blockImportedIface's bare name is.
		if d := g.importedIfaceDef(sym); d != nil && d.stdOrigin == "" && !d.stdShared {
			return d, true
		}
		return nil, false
	}
	if d, local := g.ifaces[name]; local {
		return d, true
	}
	return g.foreignIface(name)
}

// --- the names a default mentions -------------------------------------------

// pickAnyName contributes every name a node mentions that a MODULE SCOPE could
// resolve — an identifier, a type, a file qualifier.
//
// A MEMBER name is deliberately excluded. `Logger.log(value, "INFO", message)`
// mentions `Logger` at module scope and `log` only relative to it, and a
// member is resolved against its object rather than against the file. Reading
// it as a module-level name is wrong: in `15-app-and-defer/effects` the
// implementing file is `log.nomi`, whose own file API object is bound under
// the spelling `log`, so the member `log` would make the two files disagree
// and refuse a default that is portable. Over-collection here would refuse
// a correct program.
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
	case *ast.TaggedString:
		// A typed literal's tag names its type, or the file that declares
		// it (`url.Url`): the literal's handler is found through it.
		if t.Tag != "" {
			name, _, _ := strings.Cut(t.Tag, ".")
			out[name] = true
		}
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
// no node is an integer. An instance of a generic interface adds its type
// arguments (portableArgsKey): an impl of `Gets<Int>` is no impl of
// `Gets<String>`.
type implKey struct {
	iface ast.Node
	args  string
	recv  ast.Node
	tag   tag
}

// implIndex is every lowerable impl in one program, so a boxing site in one
// file can ask whether any file lowers the implementation its box will need.
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
			if iface := implIface(d); d.lowerable && iface != nil && iface.decl != nil {
				x.has[implKeyFor(iface, d.recv)] = true
			}
		}
		for _, d := range g.implOrder {
			if iface := implIface(d); d.lowerable && iface != nil && iface.decl != nil {
				x.has[implKeyFor(iface, d.recv)] = true
			}
		}
	}
	return x
}

// implKeyFor is the key an interface and a receiver kind share across gens.
func implKeyFor(d *ifaceDef, recv kind) implKey {
	k := implKey{iface: d.decl, tag: recv.tag}
	if d.instOf != nil {
		k.args = portableArgsKey(d.instArgs)
	}
	if recv.tag == tagNamed && recv.def != nil {
		k.recv = recv.def.decl
	}
	return k
}

// bindsImpl reports whether this program lowers an impl of d for k. kt
// is the checker's type k was read from, or nil (stdProgBinds).
//
// The program and not this file: a value boxed here dispatches to an impl
// any file may have written, and asking only about the local file would
// refuse a box the program does in fact implement. Falls back to the local
// view when there is no program: the single-module fixture path lowers one
// file with no registry at all.
func (g *gen) bindsImpl(d *ifaceDef, k kind, kt analysis.Type) bool {
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
	// A stdlib impl of a stdlib interface is not an `impl` block in any
	// module, so neither index below holds it, and without this every boxing
	// site would answer that no impl of Display is lowered for Int, on a
	// program the checker accepted. See debugerased.go.
	if g.bindsStdScalarImpl(d, k) {
		return true
	}
	if d.stdOrigin != "" {
		// A std interface built from std's declaration (stdprogiface.go).
		return g.stdProgBinds(d, k, kt)
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
	impl := g.implsByIface[g.implsKey(d)][k]
	if impl != nil && impl.lowerable && (d.instOf == nil || impl.iface == d) {
		return true
	}
	for _, impl := range g.instImpls[g.implsKey(d)][k] {
		// One of k's impls of a generic interface at several arguments:
		// the one at d's.
		if impl.lowerable && impl.iface == d {
			return true
		}
	}
	if k.tag == tagNamed && k.def != nil && (k.def.genericOf != nil || k.def.instOrigin != nil) && d.decl != nil {
		// A generic instance whose impl another file registers for it
		// (siblingtemplateimpl.go), or the owner does for a mirror here.
		_, impl := g.instanceImplAnywhere(instIdentity(k.def), g.implsKey(d), "")
		return impl != nil && impl.lowerable && impl.iface != nil && impl.iface.decl == d.decl
	}
	return false
}
