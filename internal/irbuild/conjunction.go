package irbuild

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Interface conjunctions (spec §12 *Interface conjunctions*).
//
// A value held as `Priced and Costed` is, in the VM, what a value held as
// one interface is (irExistentialKind): the concrete value, or the box a
// container or an `embeds` enum's value enters in (irboxed.go). A call
// through any member dispatches on the value's run-time type as it does
// through that member alone, so one representation serves both, and the
// conjunction adds no table.
//
// Its kind is tagIface with no iface and an interned comp whose parts are
// the members' existential kinds, sorted by name: the kind is the members,
// as the checker's ConjunctionType is, so `A and B` and `B and A` are one
// kind. irExistentialKind is false for it, since every reader of that
// predicate reads one interface's def; each place a conjunction is handled
// asks irConjKind.
//
// Entering a conjunction is entering each member at once: unboxed when the
// value's run-time type names every impl (irErases), or boxed once when
// each member boxes it under the same symbol, which names the value's
// static type (irBoxPlan), with every member's dispatch entries recorded
// under it. Widening to a member or to fewer members moves nothing: the
// value is a value of the wider kind already (relabels), and a call
// through a member relabels the receiver to that member's existential
// first (conjReceiver).

// conjKind is the kind of a value held as the conjunction of members, each
// an existential kind, or kindInvalid when one is not. A conjunction among
// members is spliced in, an interface written twice is kept once, and a
// single member left is that member's kind.
func (g *gen) conjKind(members []kind) kind {
	var flat []kind
	for _, m := range members {
		switch {
		case irConjKind(m):
			flat = append(flat, m.comp.parts...)
		case irExistentialKind(m):
			flat = append(flat, m)
		default:
			// kindInvalid: propagates — a kind CONSTRUCTOR; the member's own decline is reported where it was resolved.
			return kindInvalid
		}
	}
	slices.SortStableFunc(flat, func(a, b kind) int {
		return cmp.Compare(a.iface.nomi, b.iface.nomi)
	})
	flat = slices.Compact(flat)
	if len(flat) == 1 {
		return flat[0]
	}
	names := make([]string, len(flat))
	for i, m := range flat {
		names[i] = m.nomi()
	}
	return kind{tag: tagIface, comp: internComp(g, strings.Join(names, " and "), flat...)}
}

// irConjKind reports whether k is an interface conjunction's kind.
func irConjKind(k kind) bool {
	return k.tag == tagIface && k.iface == nil && k.comp != nil
}

// conjMembers is the existential kinds a value of kind k is held as: a
// conjunction's members, an interface's own kind, or nil.
func conjMembers(k kind) []kind {
	switch {
	case irConjKind(k):
		return k.comp.parts
	case irExistentialKind(k):
		return []kind{k}
	}
	return nil
}

// conjHolds reports whether a value of kind have, a conjunction, is a value
// of want, one of its members or a conjunction of some of them, unchanged.
func conjHolds(have, want kind) bool {
	if !irConjKind(have) {
		return false
	}
	wm := conjMembers(want)
	if wm == nil {
		return false
	}
	for _, w := range wm {
		if !slices.Contains(have.comp.parts, w) {
			return false
		}
	}
	return true
}

// projectConjunction is the kind of a value the checker typed as the
// conjunction t.
func (g *gen) projectConjunction(t *analysis.ConjunctionType) kind {
	members := make([]kind, len(t.Members))
	for i, m := range t.Members {
		members[i] = g.project(m)
	}
	return g.conjKind(members)
}

// conjTypeOf is the kind the annotation `A and B` names.
func (g *gen) conjTypeOf(ct *ast.ConjunctionType) kind {
	members := make([]kind, len(ct.Members))
	for i, m := range ct.Members {
		members[i] = g.typeOf(m)
	}
	return g.conjKind(members)
}

// conjValType is the VM type of a conjunction's values: an existential
// whose elements are its members' types, which accepts a value of any
// existential holding all of them (ir.ValType.Accepts).
func (g *gen) conjValType(k kind) *ir.ValType {
	members := make([]*ir.ValType, len(k.comp.parts))
	for i, m := range k.comp.parts {
		if members[i] = g.irValType(m); members[i] == nil {
			return nil
		}
	}
	return ir.NewConjunctionType(g.irTypes().Symbol(irConjTypeToken{k.comp}, k.comp.nomi), members)
}

// irConjTypeToken identifies the symbol a conjunction's VM type names.
type irConjTypeToken struct{ c *compKind }

// conjErases reports whether a value of kind have enters the conjunction
// want by erasure, as irErases answers for one interface: a value whose
// run-time type names an impl of each member. Such a value may still enter
// boxed (conjBoxPlan). An interface value enters by conjHolds instead.
func (g *gen) conjErases(want, have kind) bool {
	for _, m := range want.comp.parts {
		if !g.irErases(m, have) {
			return false
		}
	}
	return true
}

// conjBoxPlan is the plan that boxes have into the conjunction want, with
// impls the checker recorded for each member (nil where it recorded none):
// one box, named by the first member plan that boxes it, carrying every
// member's dispatch entries under that name. It is nil when no member boxes
// the value.
func (g *gen) conjBoxPlan(want, have kind, impls []*ast.ImplBlock) *irBoxPlan {
	var plans []*irBoxPlan
	for i, m := range want.comp.parts {
		var impl *ast.ImplBlock
		if i < len(impls) {
			impl = impls[i]
		}
		plans = append(plans, g.irBoxPlanFor(m, have, impl))
	}
	// A member that takes the value unboxed takes the box too: dispatch,
	// equality and rendering see through it (rt.Unbox). Each member that
	// boxes it has its entries recorded under the one box's name.
	var out *irBoxPlan
	for _, q := range plans {
		if q == nil {
			continue
		}
		if out == nil {
			out = &irBoxPlan{typ: q.typ}
		}
		for _, e := range q.impls {
			e.typ = out.typ
			out.impls = append(out.impls, e)
		}
	}
	return out
}

// intoConjunction makes v, a value of kind have, a value of the conjunction
// want, as intoExistential makes one of an interface. rec is the checker's
// record of the coercion, nil inside a conversion it records as a whole.
func (bl *irScalarBuilder) intoConjunction(at ast.Node, v ir.Temp, want, have kind, rec *analysis.Coercion) (ir.Temp, irCoercion) {
	if irConjKind(have) || irExistentialKind(have) {
		if conjHolds(have, want) {
			return bl.relabel(at, v, want), irCoercedUnboxed
		}
		return v, irNotCoerced
	}
	var impls []*ast.ImplBlock
	if rec != nil && rec.Conj != nil && bl.g.project(rec.Conj) == want {
		// The record's members project to want's parts in their order:
		// both are sorted by name.
		impls = rec.Impls
	}
	if p := bl.g.conjBoxPlan(want, have, impls); p != nil {
		return bl.eraseBoxed(at, v, want, p), irCoercedBoxed
	}
	if bl.g.conjErases(want, have) {
		return v, irCoercedUnboxed
	}
	return v, irNotCoerced
}

// conjReceiver is the receiver of a call through the interface d, of kind
// recv, as a value of d's existential: a conjunction holding d relabelled
// to it. ok is false when recv is no conjunction holding d.
func (bl *irScalarBuilder) conjReceiver(at ast.Node, v ir.Temp, recv kind, d *ifaceDef) (ir.Temp, kind, bool) {
	if !irConjKind(recv) {
		return v, recv, false
	}
	for _, m := range recv.comp.parts {
		if m.iface == d || (m.iface != nil && m.iface.instOf != nil && m.iface.instOf == d.decl) {
			return bl.relabel(at, v, m), m, true
		}
	}
	return v, recv, false
}

// recordedConj is the checker's record of a value of kind have entering
// the conjunction want at the expression at: the coercion of the expression
// itself, or a part of its conversion. Nil when it recorded none there.
func (g *gen) recordedConj(at ast.Node, want, have kind) *analysis.Coercion {
	if rec := g.coercionAt(at); rec != nil && rec.Conj != nil && g.project(rec.Conj) == want {
		return rec
	}
	if rec := g.conversionAt(at); rec != nil {
		for _, b := range rec.Boxes {
			if b.Conj != nil && g.project(b.Conj) == want && g.project(b.Value) == have {
				return b
			}
		}
	}
	return nil
}

// irHeldKind reports whether k is the kind of a value held as an interface
// value: one interface's existential or a conjunction's. Both are the
// concrete value in the VM, so a position that holds, compares, hashes or
// renders one holds the other the same way.
func irHeldKind(k kind) bool {
	return irExistentialKind(k) || irConjKind(k)
}
