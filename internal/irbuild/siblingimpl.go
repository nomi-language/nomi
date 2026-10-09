package irbuild

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// A TYPE-qualified call whose callee lives in a SIBLING FILE's impl block:
// `Status.active()` in one file, `impl Status { pub fn active(): Status }` in
// another.
//
// # Why the local route cannot find it
//
// implCall's typeQualifiedCall scans `g.implOrder`, which is the impl blocks
// of the file BEING LOWERED. A sibling's block is not in it, so without this
// index the call finds no candidate, falls through to variantCall, and
// lookupVariant reports whichever of its two arms the receiver's shape reaches:
//
//   - an ENUM receiver has no variant by that name, so `Status.active`;
//   - a STRUCT or DISTINCT receiver is not an enum at all, so
//     `User.inspect: User is a struct`.
//
// Both messages would be about the reporting site, not the call.
//
// # Resolution is the analyzer's, and the identity is the declaration NODE
//
// The qualifier names a TYPE, so the sibling is found through the type rather
// than through a file API object: foreignDecl asks the analyzer what `Status`
// resolves to and the registry which unit declares that node. So an alias, a
// re-export facade and a selective import all resolve through the front end
// that already decided them — siblings.go's rule, applied one level in.
//
// The index is keyed on (declaration node, method name) and never on a name
// pair. Two files may each declare a `Status` with an `active`, the checker
// keeps them apart nominally, and a name-keyed index would let one file's impl
// answer for the other's.
//
// # Why an index rather than a walk at the call site
//
// The orphan rule admits `impl I for T` in T's module or in I's, and a Nomi
// MODULE is several FILES: `pub struct Point` in a.nomi and `impl Debug for
// Point` in b.nomi is legal. So "which impl serves Point.inspect" is a
// PROGRAM-wide question, and asking it by scanning one gen would answer "none"
// for a type that has one. foreignNoEquatableImpl answers `==` the same way,
// and buildImplIndex boxing.
//
// It is built once, after every gen's declareFuncs and before any body is
// lowered, which is the one window where every impl table is complete and no
// body has been built. irbuild.go's three-wave comment states why that window
// exists.
//
// # What it does NOT do
//
// `==` on a mirror still refuses. testdata/sibsig_impl pins that, and it is a
// different question: this route serves a call the programmer WROTE
// type-qualified, while `==` would have to choose the owner's `impl Equatable`
// over the structural comparator on the strength of an index — a silent change
// of answer rather than a call finding its callee. The two are independent and
// only the second needs a decision.

// implMemberKey identifies one impl function by the declaration of the type it
// is for, plus its own name.
//
// The NODE and not the type's name, for the reason fileSite's comment gives
// about aliases: the identity of a Nomi type is its declaration, and two
// spellings of one declaration must reach one entry while two declarations
// sharing a spelling must not.
type implMemberKey struct {
	recv   ast.Node
	method string
}

// implMemberSite is one indexed impl function: which unit declares it, what a
// call in another file may do with it, and whether anybody WROTE it.
//
// `synth` is load-bearing rather than informational. The front end injects a
// universal `impl Debug for T` for every declared type, so a type declared in
// one file and given an explicit `impl Debug` in another has TWO providers of
// `T.inspect` and they are not rivals — the written one is what callers
// observe, which is what `12-derives-and-standard-interfaces/cross_file_debug`
// pins and what the rival check below must not break.
//
// `iface` is the interface spelling the block implements, empty for an inherent
// `impl Type { … }`. Recorded because a call may name EITHER, and the two
// questions have different answers over the same index: a TYPE-qualified call
// takes every block for the receiver and refuses when two files rival each
// other, while an INTERFACE-qualified call must take exactly the named
// interface's — `impl Loud for Quiet` and `impl Soft for Quiet` both provide
// `say` for one receiver, legally, and only the qualifier says which.
//
// There is deliberately NO receiver-position field. An interface-qualified call
// finds its implementation by matching the WHOLE argument list against the
// site's declared parameters — foreignIfaceCall's discipline, one file boundary
// out — so `fn tag(label: String, target: self)` needs no special case: the
// String position simply fails to be a named type and the Point position
// succeeds. A stored position would also have to be DERIVED here, and the only
// derivation available to an index that cannot see a stdlib interface's
// declaration is "the first parameter whose kind is the receiver's" — which is
// a guess for `fn merge(a: self, b: self)` and a second definition of a
// question selfShape already answers.
//
// `block` is the impl block this function was declared in, and it is the one
// field a reader may key on by NODE rather than by (type, method). A
// type-qualified call has only a name pair to resolve with, so it needs the
// rival rule below; a site whose handler the FRONT END already chose — a typed
// literal, whose tag reference carries `DispatchImpl` — has the declaration
// itself, and matching on it is strictly stronger than any rule about rivals.
// See literal.go's siblingLiteralImpl, the only reader.
type implMemberSite struct {
	unit  int
	fn    *fileFunc
	synth bool
	iface string
	block *ast.ImplBlock
	// item and symName are the declaring unit's identity for the function's
	// retained body, which irImplLower interns as Symbol(item, symName).
	item    *implItem
	symName string
	// withheld is the declaring unit's block when the function is one it
	// withheld for a call to instantiate (implDef.withheldMember): item is
	// nil, and the declaring gen builds the instance a call asks for.
	withheld *implDef
}

// resolveImplMembers indexes every impl function in the program by the type it
// is declared for, so a call site in any file can find it.
//
// Every gen, including the caller's own: the rival check below is only correct
// program-wide, and a call site excludes its own unit itself (see
// qualSiblingImplPlan), which runs after the local impl route.
//
// A NON-lowerable impl block is indexed too, and deliberately. A call site that
// cannot find it reports "no such member", which is a different gap from "the
// implementation exists and the builder will not call it" — the distinction
// blockedMirror exists for, which says whether the call or the declaration is
// what the builder cannot serve.
func (x *fileIndex) resolveImplMembers(gens []*gen) {
	x.implMembers = map[implMemberKey][]implMemberSite{}
	for unit, g := range gens {
		if g == nil {
			continue
		}
		for _, d := range g.implOrder {
			// A receiver the builder could not resolve to a declared type has
			// no identity to key on: `impl for an unlowered type` and the
			// generic-header refusals return from registerImpl before recv is
			// set. Such a call keeps its existing refusal.
			if d.recv.tag != tagNamed || d.recv.def == nil || d.recv.def.decl == nil {
				continue
			}
			for _, it := range d.order {
				k := implMemberKey{recv: d.recv.def.decl, method: it.name}
				x.implMembers[k] = append(x.implMembers[k],
					implMemberSite{unit: unit, fn: implMemberFunc(d, it),
						synth: d.synth, iface: d.ifaceName, block: d.decl,
						item: it, symName: d.recv.nomi() + "." + it.name})
			}
			// A member the block withheld for a call to instantiate has no
			// item yet; the declaring gen builds one per call
			// (siblingMethodInstance). Sorted, so sites keep one order.
			withheld := make([]string, 0, len(d.gaps))
			for method := range d.gaps {
				withheld = append(withheld, method)
			}
			sort.Strings(withheld)
			for _, method := range withheld {
				w, ok := d.withheldMember(method)
				if !ok {
					continue
				}
				f := &fileFunc{name: method, params0: w.params, generic: true, why: "generic impl function"}
				if d.ifaceName == "" && w.decl != nil && !w.decl.Public {
					f.why = "private sibling file impl function"
				}
				k := implMemberKey{recv: d.recv.def.decl, method: method}
				x.implMembers[k] = append(x.implMembers[k],
					implMemberSite{unit: unit, fn: f, synth: d.synth, iface: d.ifaceName,
						block: d.decl, withheld: d})
			}
		}
	}
}

// writtenImplMembers is the sites somebody WROTE, or every site when nobody
// did: a synthesized impl is a fallback, not a candidate beside a written one.
//
// One function so the two readers cannot drift. They ask different questions of
// the same rule — "is my local answer rivalled?" and "which sibling do I
// call?" — and a rule spelled out at each of them would let the two drift.
func writtenImplMembers(sites []implMemberSite) []implMemberSite {
	written := make([]implMemberSite, 0, len(sites))
	for _, s := range sites {
		if !s.synth {
			written = append(written, s)
		}
	}
	if len(written) == 0 {
		return sites
	}
	return written
}

// implMemberFunc projects one impl function onto what a call in ANOTHER FILE
// may do with it.
//
// A *fileFunc rather than a new shape, because the cross-file call path
// already reads one: qualSiblingImplPlan translates the declaring package's
// kinds into the caller's and qualifies the Go name. An impl function's
// receiver is an ordinary parameter in whatever position the signature put it
// (implFunc), so nothing is added for it.
func implMemberFunc(d *implDef, it *implItem) *fileFunc {
	f := &fileFunc{name: it.name, params: it.params, result: it.result, params0: it.params0}
	for _, p := range it.params0 {
		if p.Default != nil {
			f.defaults = true
		}
	}
	switch {
	case !d.lowerable:
		// The DECLARING file rejects the block at its own position
		// (implDecl), so naming a construct here would count one gap twice
		// under a name nobody can lower — fileFunc.echo's rule. A SYNTHESIZED
		// block is the exception: nobody wrote it, implDecl deliberately
		// reports nothing for it, so the call site is the only position there
		// is and it reports.
		f.why, f.echo = d.why, !d.synth
		if f.why == "" {
			f.why, f.echo = "call to an unlowered impl", false
		}
	case d.ifaceName == "" && !it.public:
		// An INHERENT impl function without `pub` is file-private, and the
		// analyzer says so: "type 'Widget' has no member 'hidden'". So no such
		// call can reach the builder and this arm is a fence rather than a
		// route — see implItem.public for the measurement, and
		// fileFuncShell for the same fence over a free function. An INTERFACE
		// impl function carries no `pub` and is deliberately not checked.
		f.why = "private sibling file impl function"
	}
	return f
}
