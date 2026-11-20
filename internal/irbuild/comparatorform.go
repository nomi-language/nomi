package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// comparatorPick is the `Comparable.compare` an operation that orders
// values without an operator orders them by (`Iter.sort`, `Iter.sort_by`'s
// key, a Range's walk and `contains`, a tuple's element): a program's impl
// function in this file (local), the callee symbol of a function another
// unit lowers (sibling: another file's impl, or the instance of a std
// generic container's impl), std's own function (std), or a tuple's
// lexicographic compare, which the builder writes (tuple,
// irtuplecompare.go). The zero pick is none.
type comparatorPick struct {
	local   *implItem
	sibling *ir.Symbol
	std     *stdFunc
	tuple   bool
}

// recordedComparator is the comparator the checker's record of
// `Comparable.compare(a, b)` at kt, the element's checker type, names
// (stdIfaceCallRecord), for elements of kind k: a tuple's structural
// compare, a program's impl function, the instance of a std generic
// container's impl at k, or std's own function. The zero pick where the
// record names none the builder can make a value of.
func (bl *irScalarBuilder) recordedComparator(k kind, kt analysis.Type) comparatorPick {
	r := bl.recordedIfaceFnAt(stdComparable, "compare", k, kt, []kind{k, k}, stdEnumKind(stdEnumOrdering))
	switch {
	case r.structural:
		if k.tag == tagTuple && bl.tupleComparable(k, kt) {
			return comparatorPick{tuple: true}
		}
	case r.local != nil:
		if comparesAt(r.local, k) {
			return comparatorPick{local: r.local}
		}
	case r.sym != nil:
		return comparatorPick{sibling: r.sym}
	case r.template != nil:
		return comparatorPick{sibling: bl.g.comparatorInstance(r.template, k, kt)}
	case r.std != nil:
		if compareSignature(r.std, k) && comparatorBody(r.std) {
			return comparatorPick{std: r.std}
		}
	}
	return comparatorPick{}
}

// comparesAt reports whether it is a `compare` of elements of kind k with
// a body the builder can retain.
func comparesAt(it *implItem, k kind) bool {
	return it != nil && it.impl != nil && it.impl.lowerable && it.lowerable && irImplSource(it) != nil &&
		len(it.params) == 2 && it.params[0] == k && it.params[1] == k && it.result == stdEnumKind(stdEnumOrdering)
}

// comparatorInstance is the callee symbol of the instance at k of f, a
// std generic container's `Comparable.compare` (`impl Comparable for
// List<T>` at `List<Int>`), entered with kt's types, or nil.
func (g *gen) comparatorInstance(f *stdFunc, k kind, kt analysis.Type) *ir.Symbol {
	if f == nil || !stdGenericTemplateUsable(f) {
		return nil
	}
	ord := stdEnumKind(stdEnumOrdering)
	inst := g.stdRecvInstance(f, "", k, kt, false)
	if inst == nil || len(inst.view.params) != 2 || inst.view.params[0] != k || inst.view.params[1] != k || inst.view.result != ord {
		return nil
	}
	return inst.sym
}

// comparatorBody reports whether f, std's `Comparable.compare` at an
// element, is one a comparator can be made of: a retained body, or a host
// a forwarding body calls (irHostForward).
func comparatorBody(f *stdFunc) bool {
	return f != nil && (f.irBody != nil || f.rtCall != "" && irScalarHost(f))
}

// comparatorClass is how the comparator of a site was chosen.
type comparatorClass string

const (
	// comparatorFromRecord: from the checker's record at the element's
	// type (recordedComparator).
	comparatorFromRecord comparatorClass = "from the record"
	// comparatorNoType: by the element's kind, which has no checker type
	// where the builder asks for its comparator.
	comparatorNoType comparatorClass = "no checker type"
)

// comparatorClasses is every comparatorClass, with whether it is the
// record's. TestIRGolden_RecordsMatchTheVM counts the distinct sites of
// each class that is not, and maxComparatorSites holds each count to a
// maximum that only goes down.
var comparatorClasses = map[comparatorClass]bool{
	comparatorFromRecord: true,
	comparatorNoType:     false,
}

// noteComparator counts, under substVerify, the site at of a comparator
// chosen for an element whose checker type is kt in the comparator
// census, by how it was chosen. A class comparatorClasses does not list
// fails the lowering.
func (bl *irScalarBuilder) noteComparator(at ast.Node, kt analysis.Type) {
	g := bl.g
	if !substVerify || at == nil || g.srcFA() == nil {
		return
	}
	line, col := nodePos(at)
	class := comparatorFromRecord
	if kt == nil {
		class = comparatorNoType
	}
	if _, listed := comparatorClasses[class]; !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("a comparator is chosen %q, which no class lists", class))
		return
	}
	comparatorCensus.note(g, string(class), line, col)
}
