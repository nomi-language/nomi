package analysis

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Interface nominal identity, on the IMPL side.
//
// `InterfaceType.Origin` gives an interface the same `(Origin, Name)` identity
// a struct, enum, or distinct type has. This file is what makes that identity
// reach the two places that were answering with a bare name: the bound check
// ("does T implement Renderer") and the collision diagnostic.
//
// # The defect, measured rather than assumed
//
// Two sibling files each declaring `pub interface Renderer { fn render }`, and
// one of them writing `impl Renderer for Point`, made the OTHER one's
// `fn draw<T>(x: T) where T: Renderer` accept a `Point`. The program was
// accepted and RAN, dispatching to a method of an interface its bound never
// named. A wrong ANSWER, not a coverage gap, and the same root cause as the
// prelude-interface soundness gap fenced by reserving the prelude's interface
// names — except that no reservation can fence this one, because both names
// are the user's own.
//
// # Why a PARALLEL map, and not a re-key
//
// The same reason `QualifiedReceiver` is parallel to `ImplBlockReceiver`:
// `Impls`,
// `ImplTypeArgs`, `TypeMethods` and `ImplManifest` are queried by BASE NAME
// from all over the checker and unifier, which frequently hold nothing else.
// Identity goes where it is needed, keyed the same way the table it qualifies
// is keyed, and nothing else changes shape.
//
// # Why an unknown origin ACCEPTS
//
// `OriginUnresolved` is the bottom of the identity lattice — a build that could
// not establish which declaration was meant has nothing to confuse it with. So
// a missing entry on either side answers "implemented", which makes every path
// that does not populate this map (single-file `BuildFileWithStdlib`, the LSP's
// raw-document analysis, a hand-rolled test index) behave exactly as it did
// before the map existed. The check can only ever REJECT when both sides are
// known and disagree.

// ImplTables is one scope's answer to "which T implements which Iface",
// together with the identity of the interface each impl was written against.
//
// A struct rather than two parallel slice parameters because the unifier
// threads this through nineteen recursive call sites; a second slice would
// have to be kept in step at every one of them, and nothing would notice if it
// were not.
type ImplTables struct {
	// Impls is T → Iface base name → implemented.
	Impls map[string]map[string]bool
	// IfaceOrigins is T → Iface base name → the declaring files' build keys
	// of the interfaces the impls of that spelling named: two files may each
	// declare a `Box` and an `Announcer` and an impl of one for the other.
	// Nil, or a missing entry, means the identity was not established — see
	// the file comment.
	IfaceOrigins map[string]map[string][]string
	// Receivers is the project's impl index, whose ImplsByIdentity says
	// which declaration of a receiver's name an impl is for: a program's
	// `type Name String` does not take std/xml's `impl Display for Name`
	// (missing_impl_identity.go). Nil leaves the bare-name answer.
	Receivers *ProjectImplIndex
	// Unindexed is the origins of the program's files Receivers does not
	// fold, whose receivers it cannot judge (Program.unindexedOrigins).
	Unindexed map[string]bool
}

// implTablesFor pairs an impl table with the project-wide interface-origin
// index. The origins index is project-wide because its key space — (receiver
// base name, interface base name) — is the same one `Impls` uses, and a
// per-file `Impls` is a BROADCAST union of every reachable file's impls
// anyway, so a per-file origins map would carry identical contents under a
// second name.
func implTablesFor(impls map[string]map[string]bool, idx *ProjectImplIndex, unindexed map[string]bool) ImplTables {
	t := ImplTables{Impls: impls}
	if idx != nil {
		t.IfaceOrigins = idx.IfaceOriginByPair
		t.Receivers = idx
		t.Unindexed = unindexed
	}
	return t
}

// PopulateImplIfaceOrigins fills idx.ImplIfaceOrigin and idx.IfaceOriginByPair.
//
// Must run AFTER BuildTypes (Sweep C-types), for `PopulateQualifiedReceivers`'
// reason and by the same mechanism: the origin is read off the interface
// type's `Origin`, and a type symbol carries no `Type` pointer until then.
// Running it earlier yields an empty index rather than a wrong one, which the
// file comment's lattice rule then reads as "accept everything".
//
// The origin is resolved through the impl's HOME file, not through whichever
// FileAnalysis happens to be iterated: the per-file impl maps are broadcast, so
// every FA holds every file's impls and only `ImplFiles` says where an impl was
// written. `impl Renderer for Point` in svg.nomi resolves `Renderer` in
// svg.nomi's scope — which is the whole point, since ascii.nomi resolves the
// same spelling to a different declaration. A header that qualifies its
// interface (`impl calendar.DateParts for Q`) resolves it in the module's
// scope, since svg.nomi may declare a `DateParts` of its own.
func PopulateImplIfaceOrigins(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	if idx.ImplIfaceOrigin == nil {
		idx.ImplIfaceOrigin = make(map[*ast.FuncDef]string)
	}
	if idx.IfaceOriginByPair == nil {
		idx.IfaceOriginByPair = make(map[string]map[string][]string)
	}
	for ifaceName, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				if fn == nil {
					continue
				}
				noteStdlibWork(idx.ImplFiles[fn])
				home := filesByKey[idx.ImplFiles[fn]]
				origin := declaringOriginOfImplInterface(home, fn, ifaceName)
				if origin == OriginUnresolved {
					continue
				}
				idx.ImplIfaceOrigin[fn] = origin
				recv := idx.ReceiverOf(fn)
				if recv == "" {
					continue
				}
				if idx.IfaceOriginByPair[recv] == nil {
					idx.IfaceOriginByPair[recv] = make(map[string][]string)
				}
				if !slices.Contains(idx.IfaceOriginByPair[recv][ifaceName], origin) {
					idx.IfaceOriginByPair[recv][ifaceName] = append(idx.IfaceOriginByPair[recv][ifaceName], origin)
				}
			}
		}
	}
}

// declaringOriginOfImplInterface is the Origin of the interface the impl
// block holding fn names, resolved in fa, the file that writes the block:
// through the module its header qualifies the interface with (`impl
// calendar.DateParts for Q` beside fa's own `interface DateParts` names
// std/calendar's), else by name in fa's scope.
func declaringOriginOfImplInterface(fa *FileAnalysis, fn *ast.FuncDef, name string) string {
	if fa == nil {
		return OriginUnresolved
	}
	if module := fa.ImplBlockInterfaceModule[fn]; module != "" {
		if it := interfaceThrough(fa, module, name); it != nil {
			return it.Origin
		}
		return OriginUnresolved
	}
	return declaringOriginOfInterface(fa, name)
}

// declaringOriginOfInterface finds the Origin of the interface `name` denotes
// in this file's scope, following the import chain to the declaration.
//
// declaringOriginOfType's sibling, and deliberately a separate function rather
// than an arm added to it: that one answers for a VALUE-shaped type and falls
// back to the file's own origin for a local declaration whose type carries
// none (a built-in primitive singleton). An interface has no such shape, so a name
// that does not resolve to an *InterfaceType is not an interface at all and
// must answer "unknown" rather than borrow this file's origin.
func declaringOriginOfInterface(fa *FileAnalysis, name string) string {
	if fa == nil || fa.ModuleScope == nil || name == "" {
		return OriginUnresolved
	}
	sym := fa.ModuleScope.Lookup(name)
	if sym == nil {
		return OriginUnresolved
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if it, ok := sym.Type.(*InterfaceType); ok {
		return it.Origin
	}
	return OriginUnresolved
}

// namesInterface reports whether an impl of the interface spelled
// ifaceName for the receiver spelled concreteName names the interface
// declared at ifaceOrigin: one of the pair's recorded origins is that
// declaration's, or none is recorded.
func (t ImplTables) namesInterface(concreteName, ifaceName, ifaceOrigin string) bool {
	origins := t.IfaceOrigins[concreteName][ifaceName]
	if len(origins) == 0 {
		return sameNominalIdentity(ifaceOrigin, ifaceName, OriginUnresolved, ifaceName)
	}
	for _, o := range origins {
		if sameNominalIdentity(ifaceOrigin, ifaceName, o, ifaceName) {
			return true
		}
	}
	return false
}

// ownReceiverImpls narrows candidates, impl functions whose receiver is
// spelled as concrete's type is, to those whose receiver is concrete's own
// declaration: two files may each declare a type of one name with an impl
// of one interface, and only its own file's impl is concrete's.
// QualifiedReceiver names a receiver by its declaring file, and one the
// entry declares bare, so a bare one is concrete's only when concrete is
// the entry's (entryOrigin, Program.entryOrigin). This holds for a single
// candidate too: an entry that derives Equatable for its Wallet gives
// another file's Wallet no impl, and that file's `==` stays structural, as
// it is when the file is checked against interfaces and the entry's impl
// is not in sight. An impl with no qualified receiver recorded, and a
// concrete type of no known declaration, match by name.
func ownReceiverImpls(idx *ProjectImplIndex, candidates []*ast.FuncDef, concrete Type, entryOrigin string) []*ast.FuncDef {
	origin, ok := nominalOrigin(resolveTypeVar(concrete))
	if !ok || origin == OriginUnresolved {
		return candidates
	}
	entry := origin == OriginEntry || origin == entryOrigin
	var own []*ast.FuncDef
	for _, fn := range candidates {
		q, recorded := idx.QualifiedReceiver[fn]
		o, _, qualified := strings.Cut(q, receiverOriginSep)
		switch {
		case !recorded || q == "":
			own = append(own, fn)
		case qualified && o == origin:
			own = append(own, fn)
		case !qualified && entry:
			own = append(own, fn)
		}
	}
	return own
}
