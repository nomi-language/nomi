package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `Comparable.compare` resolved from a KIND when the impl is declared in a
// SIBLING FILE — the ordering counterpart of siblingiface.go's call-site route.
//
// # Three consumers of one lookup
//
// `implCall`'s interface routes read `g.implsByIface`, the impl blocks of the
// file BEING LOWERED, so a sibling's block is not there; siblingiface.go serves
// the CALL SITE. `compareResult` (stdenum.go) has the same lookup with the same
// scope, reached from a KIND, and three separate consumers read it:
//
//	consumer                       reached from        refused as
//	sortComparator via iterSort    iterext.go          Iter.sort over an unorderable element
//	sortComparator via iterSortBy  iterext.go          Iter.sort_by over an unorderable key
//	compareCall                    native.go binary()  comparison outside Int and Float
//
// Without this route, a `P` with a sibling `impl Comparable` would refuse
// `Iter.sort([b, a])`, `a < b` and `Iter.sort_by([b, a], |p| p)` under those
// three names, while `Comparable.compare(a, b)` written out would lower through
// siblingIfaceCall. The two sort keys both go through `sortComparator`, so they
// cannot discriminate each other; `compareCall` is a third, independent caller
// reached from `binary()` and touching nothing in iterext.go.
//
// # Why this is not `bindsImpl`, which already answers program-wide
//
// existential.go's `implIndex` answers "does this PROGRAM lower an impl of d for
// k" — a bool, keyed on declaration nodes. That is the right question for a BOX,
// which needs only to know a table will be bound. An ordering needs a CALLABLE,
// and `implIndex` deliberately carries none: it holds `map[implKey]bool`.
//
// `g.files.implMembers` is the index that carries one, built by
// `resolveImplMembers` from every gen's `implOrder` and keyed on (receiver
// declaration, method name) with the interface recorded beside it. It is
// siblingIfaceCall's index, used here for a second question about the same
// entries — which is the point: two routes reading one index cannot disagree
// about which file provides an impl.
//
// # Why this cannot use callSibling, and what it reuses instead
//
// `callSibling` is the shared cross-file lowering and it takes an `*ast.Call`: it
// walks the argument NODES, coerces each, and records the assertion operands. An
// ordering has no call node — `sortComparator` builds a comparator over the
// synthetic operands `a` and `b`, and `compareCall` has a `*ast.Binary`. So what
// is reused is the part that is about the BOUNDARY rather than about the call:
// `importKind` for every kind the declaring package named, `closesGoCycle` for
// the Go import edge, and `useFilePkg` plus the unit's package for the qualified
// Go name. The argument walk is exactly the part that does not apply.
//
// # Where the demand is raised decides whether it answers
//
// On a `P` declared in a sibling with its `impl Comparable` in that same
// sibling, `nomi run` gives:
//
//	Iter.sort([b, a])            [1, 2]   answers
//	a < b                        True     answers
//	Comparable.compare(a, b)     Less     answers
//	Iter.sort_by([b, a], |p| p)  [1, 2]   answers
//	List.compare([a], [b])       FAULT: Comparable.compare: no implementation for type 'shapes.P'
//
// The discriminator is not where the type is declared: it is WHERE THE
// `Comparable.compare` DEMAND IS RAISED. Every answering row raises it at a
// CONCRETE call site, which is what records the conformance in the analyzer's
// manifest. The faulting row raises it inside std's OWN generic body (`impl
// Comparable for List<T>` calling `Comparable.compare(ha, hb)` at the bare `T`),
// where there is no concrete site to record.
//
// So orderingOf's Int/Float restriction for a LIST element stays, and this file
// must not reach it. It does not, by construction: `orderingOf` tests
// `k != kindInt && k != kindFloat` BEFORE calling `compareResult`, so a nested
// `List<P>` still refuses at the element and still agrees with the fault.
//
// # What selects, and why every clause is load-bearing
//
// (receiver DECLARATION, method name, INTERFACE), and the third is the one a
// reader will be tempted to drop. `implMemberKey` is only (declaration, method),
// so an INHERENT `impl Widget { pub fn compare(a, b): Ordering }` lands under the
// same key as `impl Comparable for Widget`'s member and both return `Ordering` —
// a lookup ignoring `iface` picks by index order and emits WELL-TYPED GO WITH THE
// WRONG BODY, which nothing downstream can catch. testdata/sortscope/ carries
// exactly that rival, ordering the opposite way, and its `inherent` row against
// its `sort widget` rows is the only thing that separates them.
//
// The obvious negative case — a type with NO `impl Comparable` — is NOT the
// discriminator, because it cannot reach here: `where T: Comparable` on std's own
// `sort` is discharged by the checker, which rejects the program with "Thing does
// not implement Comparable". TestSortScope_TheNoImplCaseBelongsToTheFrontEnd pins
// that boundary so the builder's refusal is not silently load-bearing.

// siblingCompareItem is a sibling file's `Comparable.compare` for one receiver:
// the unit that declares it and the function to call.
type siblingCompareItem struct {
	unit int
	fn   *fileFunc
}

// siblingCompareAt resolves `Comparable.compare` for receiver kind k in a
// SIBLING file, reporting whether exactly one file provides it.
//
// Declines — never refuses — so a receiver with no sibling impl keeps whatever
// refusal its consumer already reported. That is implCall's rule for every arm it
// calls, and here it matters more than usual: three consumers with three
// different key names read this, and a refusal minted here would replace all
// three with one name that says less.
//
// The SIGNATURE is verified against the call site's assumption, in the
// caller's own kinds: two parameters of the receiver's kind and a result that is
// the shared `Ordering` def by pointer. `f.params` belongs to the DECLARING
// package, so each is translated through `importKind` first — foreign.go's rule,
// and the reason is that a mirror is a different pointer from the owner's def, so
// a raw `f.params[i] != k` is true for two spellings of one type.
func (g *gen) siblingCompareAt(k, ord kind, at ast.Node) (*siblingCompareItem, bool) {
	if g.files == nil || g.fileUnit < 0 {
		return nil, false
	}
	if k.tag != tagNamed || k.def == nil || k.def.decl == nil {
		return nil, false
	}
	// AN ALIASED TYPE IMPORT IS OUTSIDE THE SUBSET, and siblingIfaceCall's
	// header carries the whole derivation: a dispatch key that is the value's
	// runtime type NAME puts the alias there, so the golden answer for
	// `Shout.shout(Doodad{n: 3})` is a TRAP, where this index —
	// keyed on the declaration node, with no name in the path — resolves
	// happily. Answering where the golden file records a trap is a DIFF, not
	// an improvement. Same fence, same reason.
	if g.locallyAliased(k.def) {
		return nil, false
	}
	sites := writtenImplMembers(g.files.implMembers[implMemberKey{recv: k.def.decl, method: "compare"}])
	var chosen *implMemberSite
	for i := range sites {
		s := &sites[i]
		if s.iface != "Comparable" || s.unit == g.fileUnit {
			continue
		}
		if chosen != nil && (chosen.unit != s.unit || chosen.fn != s.fn) {
			// Two files provide `Comparable.compare` for one receiver. That is a
			// coherence error the analyzer rejects, so this is unreachable from
			// any accepted program — and it DECLINES rather than choosing,
			// because a silent choice between two orderings is the one wrong
			// answer this file exists to prevent. siblingIfaceCall refuses at
			// the same point; declining is right here because the consumer's own
			// refusal already names the operand.
			return nil, false
		}
		chosen = s
	}
	if chosen == nil {
		return nil, false
	}
	if !chosen.fn.lowerable() {
		// The implementation exists and this builder will not call it. Declining
		// keeps the consumer's own refusal, which names the OPERAND — strictly
		// more informative than `chosen.fn.why`, which would name the sibling
		// declaration for a site the reader reached through `Iter.sort`.
		return nil, false
	}
	if len(chosen.fn.params) != 2 {
		return nil, false
	}
	for _, p := range chosen.fn.params {
		ip, ok := g.importKind(p)
		if !ok || ip != k {
			return nil, false
		}
	}
	res, ok := g.importKind(chosen.fn.result)
	if !ok || res != ord {
		return nil, false
	}
	return &siblingCompareItem{unit: chosen.unit, fn: chosen.fn}, true
}
