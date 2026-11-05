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
}

// implTablesFor pairs an impl table with the project-wide interface-origin
// index. The origins index is project-wide because its key space — (receiver
// base name, interface base name) — is the same one `Impls` uses, and a
// per-file `Impls` is a BROADCAST union of every reachable file's impls
// anyway, so a per-file origins map would carry identical contents under a
// second name.
func implTablesFor(impls map[string]map[string]bool, idx *ProjectImplIndex) ImplTables {
	t := ImplTables{Impls: impls}
	if idx != nil {
		t.IfaceOrigins = idx.IfaceOriginByPair
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
// same spelling to a different declaration.
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
				origin := declaringOriginOfInterface(filesByKey[idx.ImplFiles[fn]], ifaceName)
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
// entry declares bare. With one candidate, or a concrete type of no known
// declaration, it answers candidates.
func ownReceiverImpls(idx *ProjectImplIndex, candidates []*ast.FuncDef, concrete Type) []*ast.FuncDef {
	origin, ok := nominalOrigin(resolveTypeVar(concrete))
	if !ok || origin == OriginUnresolved || len(candidates) < 2 {
		return candidates
	}
	var own, bare []*ast.FuncDef
	for _, fn := range candidates {
		o, _, qualified := strings.Cut(idx.QualifiedReceiver[fn], receiverOriginSep)
		switch {
		case qualified && o == origin:
			own = append(own, fn)
		case !qualified:
			bare = append(bare, fn)
		}
	}
	if len(own) > 0 {
		return own
	}
	if len(bare) > 0 {
		return bare
	}
	return candidates
}
