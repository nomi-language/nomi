package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `Comparable.compare` resolved from a KIND when the impl is declared in a
// SIBLING FILE.
//
// One consumer reads it: compareResolves (stdenum.go), reached from
// rangeOrders through sortOrders, which decides whether a range literal
// (`a..b`, `a..=b`) or `Range.step_by` over a named element can order its
// bounds. `Iter.sort` and `Iter.sort_by` choose their comparator through
// sortComparator's siblingCompareSym instead, and `<` and
// `Comparable.compare(a, b)` do not come here. A list element never reaches
// this file: listOrders admits only Int and Float elements before
// compareResolves would ask.
//
// The index is `g.files.implMembers`, built by `resolveImplMembers` from every
// gen's `implOrder` and keyed on (receiver declaration, method name), with the
// interface recorded beside it. Keying on the declaration means the name the
// importing file binds the type under does not matter: `import
// widget.{Gadget as Doodad}` finds `impl Comparable for Gadget` the same way a
// plain import does (TestSiblingCompare_AnAliasedTypeImportOrdersARange).
//
// The INTERFACE clause is load-bearing. `implMemberKey` is only (declaration,
// method), so an inherent `impl Widget { pub fn compare(a, b): Ordering }`
// lands under the same key as `impl Comparable for Widget`'s member and both
// return `Ordering`. A lookup ignoring `iface` would pick one by index order.

// siblingCompareItem is a sibling file's `Comparable.compare` for one receiver:
// the unit that declares it and the function to call.
type siblingCompareItem struct {
	unit int
	fn   *fileFunc
}

// siblingCompareAt resolves `Comparable.compare` for receiver kind k in a
// SIBLING file, reporting whether exactly one file provides it.
//
// Declines, never refuses, so a receiver with no sibling impl keeps the
// refusal its consumer reports ("range over an unorderable element"), which
// names the operand.
//
// The SIGNATURE is verified against the call site's assumption, in the
// caller's own kinds: two parameters of the receiver's kind and a result that is
// the shared `Ordering` def by pointer. `f.params` belongs to the DECLARING
// package, so each is translated through `importKind` first: a mirror is a
// different pointer from the owner's def, so a raw `f.params[i] != k` is true
// for two spellings of one type.
func (g *gen) siblingCompareAt(k, ord kind, at ast.Node) (*siblingCompareItem, bool) {
	if g.files == nil || g.fileUnit < 0 {
		return nil, false
	}
	if k.tag != tagNamed || k.def == nil || k.def.decl == nil {
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
			// any accepted program. It DECLINES rather than choosing, because a
			// silent choice between two orderings is the one wrong answer this
			// file exists to prevent, and the consumer's own refusal already
			// names the operand.
			return nil, false
		}
		chosen = s
	}
	if chosen == nil {
		return nil, false
	}
	if !chosen.fn.lowerable() {
		// The implementation exists and this builder will not call it. Declining
		// keeps the consumer's own refusal, which names the OPERAND. That says
		// more than `chosen.fn.why`, which would name the sibling declaration
		// for a site the reader reached through a range literal.
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
