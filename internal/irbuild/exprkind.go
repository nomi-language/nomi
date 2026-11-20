package irbuild

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// exprKindAgreement is the record an expression's lowered kind equals:
// its own type, or the type a record of the checker says the value has
// where lowerExact answers it.
type exprKindAgreement string

const (
	// exprKindOwnType: the type the checker recorded for the expression
	// (FileAnalysis.ExprTypes). A read the checker narrowed (irnarrow.go)
	// is recorded at the narrowed type, and agrees here.
	exprKindOwnType exprKindAgreement = "its own type"
	// exprKindSettled: an expression of Infallible (`todo`,
	// `unreachable`) the builder lowers at the type its position settled
	// (ExpectedTypes), as todoCheckedKind reads it, and a lambda whose body
	// only leaves (`continue`, `break`), recorded with an Infallible result
	// and lowered at the result its position takes.
	exprKindSettled exprKindAgreement = "Infallible, at its position's type"
	// exprKindConverted: an expression the builder lowers at the type its
	// Conversion converts it to (a lambda written where a wider function
	// type is expected).
	exprKindConverted exprKindAgreement = "at the type its Conversion records"
	// exprKindCoerced: an expression the builder lowers as the interface
	// value its Coercion records.
	exprKindCoerced exprKindAgreement = "at the interface its Coercion records"
	// exprKindBoolVariant: a value of a type Bool embeds (`True` and
	// `False` with no expected type are recorded at their own host types),
	// which the builder holds as the Bool it is a variant of.
	exprKindBoolVariant exprKindAgreement = "a Bool variant's value, held as the Bool"
)

// exprKindClass is a known way the kind the builder lowers an expression at
// and the type the checker recorded for it differ.
type exprKindClass string

const (
	exprKindHeldConcrete      exprKindClass = "held at a concrete kind where the record is an interface"
	exprKindCaptureView       exprKindClass = "a partial lowered at its callee's Iter parameter"
	exprKindLiteralAtPosition exprKindClass = "a literal recorded at wider element types than its items'"
)

// knownExprKindClasses is every exprKindClass verifyExprKind lets pass,
// with its cause. verifyExprKind fails on a disagreement of no listed
// class; TestIRGolden_RecordsMatchTheVM counts each class's distinct
// sites, and maxExprKindSites holds each count to a maximum that only goes
// down.
var knownExprKindClasses = map[exprKindClass]string{
	// The checker solved a generic call's type parameter at the interface
	// its position expects (`d: Display = Iter.loop(Set[1, 2], ...)` solves
	// the state at Display), and the builder instantiates the callee at its
	// arguments' kinds and converts the result where it is used.
	exprKindHeldConcrete: "the builder instantiates a generic call at its arguments' kinds",
	// `&Iter.sum(&1)` mapped over a List of Lists: the builder types the
	// partial's open slot at its callee's parameter (Iter<Int>), and the
	// checker types it at the List it is given and records no view of it.
	exprKindCaptureView: "the builder types a partial's open slot from its callee's parameter",
	// `{"a" => [1]}` checked against `Map<String, Iter<Int>>` is recorded at
	// that type, and `[fc1]` in `Vector[[fc1], [Fig1.Pt1, fc1]]` (fc1 a
	// Ring1, which Fig1 embeds) at List<Fig1>; the builder holds each
	// literal at its items' own kinds and converts it where it is used.
	exprKindLiteralAtPosition: "the checker records a literal at the element types its position takes",
}

// exprKindNoRecord is why an expression the builder lowers has no type in
// the checker's record to compare with.
type exprKindNoRecord string

const (
	noRecordUnchecked exprKindNoRecord = "code markUnchecked marks"
	noRecordPattern   exprKindNoRecord = "a pattern's literal lowered as a value"
	noRecordSynth     exprKindNoRecord = "a node on a synthesized line"
	noRecordPlaced    exprKindNoRecord = "a stand-in for a placed argument"
	noRecordCopy      exprKindNoRecord = "a copy of a checked expression"
	noRecordBuilt     exprKindNoRecord = "a node the builder writes"
)

// verifyExprKind fails, under substVerify, when the kind k lowerExact
// lowered n at is not what the checker's records say n's value is there:
// its own type (ExprTypes) under the current instance's frame, or the type
// a record that changes it gives (exprKindAgreement), and the difference is
// no known exprKindClass. A node with no record (one the builder wrote or
// copied, one of code markUnchecked marks) is not checked, and the census
// counts it by why it has none.
func (bl *irScalarBuilder) verifyExprKind(n ast.Node, k kind) {
	g := bl.g
	if !substVerify || g.fa == nil {
		return
	}
	fa := g.srcFA()
	if fa == nil {
		return
	}
	line, col := nodePos(n)
	note := func(class string) {
		exprKindCensus.note(g, class, line, col)
		stdExprKindCensus.note(g, class, line, col)
	}
	rec := fa.ExprTypes[n]
	if g.unchecked[n] || rec == nil {
		if exprKindCensus.counting() || stdExprKindCensus.counting() {
			note(exprKindNoRecordPrefix + string(bl.noRecordOf(n, fa)))
		}
		return
	}
	if agreement, agrees := g.exprKindAgreementOf(n, k, rec); agrees {
		note(exprKindAgreePrefix + string(agreement))
		return
	}
	class, identified := bl.exprKindClassOf(n, k, rec)
	if _, listed := knownExprKindClasses[class]; !identified || !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("the builder lowers this %s (`%s`) at %s, and the checker records it as %s (%s)",
			n.NodeType(), renderNode(n), k.nomi(), rec, g.project(rec).nomi()))
		return
	}
	note(string(class))
}

// The census counts an agreement and a site with no record as classes of
// their own, named with these prefixes, beside the classes it holds.
const (
	exprKindAgreePrefix    = "agrees: "
	exprKindNoRecordPrefix = "no record: "
)

// exprKindAgreementOf is the record k equals, and false for none.
func (g *gen) exprKindAgreementOf(n ast.Node, k kind, rec analysis.Type) (exprKindAgreement, bool) {
	fa := g.srcFA()
	own := g.project(rec)
	switch {
	case own == k:
		return exprKindOwnType, true
	case own == kindNever && fa.ExpectedTypes[n] != nil && g.project(fa.ExpectedTypes[n]) == k:
		return exprKindSettled, true
	case own.tag == tagFunc && k.tag == tagFunc && funcResult(own) == kindNever && slices.Equal(funcParams(own), funcParams(k)):
		return exprKindSettled, true
	}
	if c := fa.Conversions[n]; c != nil && g.project(c.To) == k {
		return exprKindConverted, true
	}
	if c := fa.Coercions[n]; c != nil && g.project(c.Into()) == k {
		return exprKindCoerced, true
	}
	if k == kindBool && boolEmbeds(rec) {
		return exprKindBoolVariant, true
	}
	return "", false
}

// boolEmbeds reports whether t is one of the types Bool embeds as its
// variants (`True`, `False`), as the checker's Bool declares them.
func boolEmbeds(t analysis.Type) bool {
	bt, isEnum := analysis.TypeBool.(*analysis.EnumType)
	if !isEnum {
		return false
	}
	for _, v := range bt.Variants {
		if v.Embedded != nil && analysis.TypesEqual(v.Embedded, t) {
			return true
		}
	}
	return false
}

// exprKindClassOf is the class of a disagreement between k and rec,
// identified by what the builder is lowering and what the records hold, and
// false for none.
func (bl *irScalarBuilder) exprKindClassOf(n ast.Node, k kind, rec analysis.Type) (exprKindClass, bool) {
	g := bl.g
	own := g.project(rec)
	// kindInvalid: lookup — a record project maps to no kind is of no class.
	if own == kindInvalid {
		return "", false
	}
	if own.tag == tagIface && k.tag != tagIface {
		return exprKindHeldConcrete, true
	}
	if partialAtIterSlots(n, k, own) {
		return exprKindCaptureView, true
	}
	if isLiteral(n) && sameCollection(k, own) {
		return exprKindLiteralAtPosition, true
	}
	return "", false
}

// partialAtIterSlots reports whether n is a partial capture (`&Iter.sum(&1)`,
// a hole that is an open argument) lowered at k, a function kind that
// differs from own, the checker's, only in parameters k takes as an Iter.
func partialAtIterSlots(n ast.Node, k, own kind) bool {
	c, isCapture := n.(*ast.Capture)
	if !isCapture || k.tag != tagFunc || own.tag != tagFunc || funcResult(k) != funcResult(own) {
		return false
	}
	partial := false
	for _, h := range c.Holes() {
		_, open := h.(*ast.Placeholder)
		partial = partial || open
	}
	ks, owns := funcParams(k), funcParams(own)
	if !partial || len(ks) != len(owns) {
		return false
	}
	for i := range ks {
		if ks[i] != owns[i] && ks[i].tag != tagSeq {
			return false
		}
	}
	return true
}

// sameCollection reports whether k and own are one shape at other element
// kinds: two tuples, records, Lists or Maps of as many parts, or two
// instances of one std container (stdRecvParts: a Vector, a Set).
func sameCollection(k, own kind) bool {
	if k.tag == own.tag && k.comp != nil && own.comp != nil && len(k.comp.parts) == len(own.comp.parts) {
		return true
	}
	kh, kargs, kok := stdRecvParts(k)
	oh, oargs, ook := stdRecvParts(own)
	return kok && ook && kh == oh && len(kargs) == len(oargs)
}

// isLiteral reports whether n is a collection, tuple or record literal.
func isLiteral(n ast.Node) bool {
	switch n.(type) {
	case *ast.ListLit, *ast.MapLit, *ast.TupleLit, *ast.StructLit:
		return true
	}
	return false
}

// noRecordOf is why n, which has no type in the checker's record, has
// none.
func (bl *irScalarBuilder) noRecordOf(n ast.Node, fa *analysis.FileAnalysis) exprKindNoRecord {
	line, _ := nodePos(n)
	_, placed := bl.placed[n]
	_, pattern := fa.PatternTypes[n]
	switch {
	case bl.g.unchecked[n]:
		return noRecordUnchecked
	case placed:
		return noRecordPlaced
	case pattern:
		return noRecordPattern
	case analysis.IsSynthesizedLine(line):
		return noRecordSynth
	case bl.g.recordedAt(n):
		return noRecordCopy
	}
	return noRecordBuilt
}

// recordedAt reports whether the checker recorded a type for an expression
// of n's node type at n's position: n is a copy of a checked expression.
func (g *gen) recordedAt(n ast.Node) bool {
	fa := g.srcFA()
	if g.exprTypesFA != fa {
		g.exprTypesFA = fa
		g.exprTypesAt = make(map[irCoercionPos]bool, len(fa.ExprTypes))
		for m := range fa.ExprTypes {
			if !isNilNode(m) {
				line, col := nodePos(m)
				g.exprTypesAt[irCoercionPos{line, col, m.NodeType()}] = true
			}
		}
	}
	line, col := nodePos(n)
	return g.exprTypesAt[irCoercionPos{line, col, n.NodeType()}]
}
