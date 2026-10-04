package irbuild

// The two assertion subjects that are not a Bool.
//
// The judgement is read off the value: a `Maybe`/`Result`
// is a SHAPE subject (`assert` needs Some/Ok, `refute` the other variant, and
// the assertion's value is the subject either way), and any other non-Bool is
// an `Assertable` whose own `failure` impl answers the verdict. The VM reads
// the shape off the value (internal/vm's judge). What it cannot do is
// select the `Assertable.failure` impl, because that is this builder's static
// dispatch; so the builder calls the impl after the subject, and the
// `ir.Assert` carries the
// answer (`ir.Assert.WithAnswer`).

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// fnAssertion reports whether the assertion keyword at line:col is inside an
// ordinary `fn` or a `concurrent` block the checker names as its boundary,
// where a failure is that activation's `Err(AssertionFailure)`: a
// `concurrent` body is its own ir.Func, so the failure ends the block as a
// `try` does and the block's scope exit cancels its in-flight tasks. The
// classification is the checker's (`assertionBoundary`); a lambda boundary
// declines rather than being lowered as though the enclosing function were
// the boundary.
func (bl *irScalarBuilder) fnAssertion(line, col int) bool {
	boundary, ok := bl.g.assertionBoundary(line, col)
	inBlock := boundary == "concurrent" && bl.tryBoundary == "concurrent"
	if !ok || bl.inTest || !(strings.HasPrefix(boundary, "fn ") || inBlock) {
		irDeclineNote("an assertion whose boundary is not an ordinary fn or a concurrent block")
		return false
	}
	return true
}

// irAssertShapeKind reports whether k is a retained prelude Maybe or Result,
// the kinds an assertion judges by shape.
func irAssertShapeKind(k kind) bool {
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || !irRetainedEnumKind(k.def) {
		return false
	}
	spec := k.def.preludeOf.spec
	return spec == preludeSpecFor("std/maybe", "Maybe") || spec == preludeSpecFor("std/results", "Result")
}

// assertableFailure is this module's retained `Assertable.failure` impl for
// subject kind k, or nil.
func (bl *irScalarBuilder) assertableFailure(k kind) *implItem {
	if k.tag != tagNamed || k.def == nil {
		return nil
	}
	d := bl.g.implsByIface["Assertable"][k]
	if d == nil || !d.lowerable {
		return nil
	}
	it := d.items["failure"]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 1 || it.params[0] != k {
		return nil
	}
	r := it.result
	if r.tag != tagNamed || r.def == nil || r.def.preludeOf == nil ||
		r.def.preludeOf.spec != preludeSpecFor("std/maybe", "Maybe") {
		return nil
	}
	return it
}

// assertableAnswer calls the subject's `Assertable.failure` impl and answers
// the `Maybe<AssertionDetails>` it returns. It runs after the subject and its
// rows, with recording off, so the impl's own operands are never rows.
func (bl *irScalarBuilder) assertableAnswer(at ast.Node, it *implItem, subj ir.Temp) ir.Temp {
	c := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym(it, "Assertable.failure"), subj)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: it.result, deferrable: true})
	return c.Dst()
}
