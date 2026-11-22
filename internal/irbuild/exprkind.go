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
	// (settledKind), as trapKind reads it, and a lambda whose body
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

// recordedType is the type the checker recorded for n's value
// (FileAnalysis.ExprTypes), or for a node the builder made, the type it
// carries (carryType), and nil where there is none: a node the builder
// wrote or copied that carries none, or code markUnchecked marks. The
// builder reads an expression's kind through it (recordedKind) and
// verifyExprKind holds every other expression's lowered kind to it.
func (g *gen) recordedType(n ast.Node) analysis.Type {
	if g.fa == nil || g.unchecked[n] {
		return nil
	}
	if ty := g.carriedTypes[n]; ty != nil {
		return ty
	}
	fa := g.srcFA()
	if fa == nil {
		return nil
	}
	return fa.ExprTypes[n]
}

// carryType makes ty the type recordedType answers for n, a node the
// builder made, until done is called: the checker's type of the source
// node n stands for (a lambda a capture stands for takes the capture's,
// recordedType(capture)), which is projected under the instance frame n is
// lowered in, as that node's own record would be. A node that lives as
// long as the body it is in never calls done. A nil ty carries nothing.
func (g *gen) carryType(n ast.Node, ty analysis.Type) (done func()) {
	if ty == nil {
		return func() {}
	}
	if g.carriedTypes == nil {
		g.carriedTypes = map[ast.Node]analysis.Type{}
	}
	g.carriedTypes[n] = ty
	return func() { delete(g.carriedTypes, n) }
}

// carryRefResult makes the result of the function type the checker gave
// ref, a function used as a value (checkedRefFuncType), the type of call,
// the call of ref the body of that value's function makes, until done is
// called.
func (g *gen) carryRefResult(call *ast.Call, ref ast.Node) (done func()) {
	if ft := checkedRefFuncType(g.srcFA(), ref); ft != nil {
		return g.carryType(call, ft.Return)
	}
	return func() {}
}

// recordedKind is the kind of recordedType under the current instance's
// frame, and false where n has no record.
func (g *gen) recordedKind(n ast.Node) (kind, bool) {
	rec := g.recordedType(n)
	if rec == nil {
		return kind{}, false
	}
	return g.project(rec), true
}

// convertedKind is the kind the checker's records give n's value where it
// is used: the type its Conversion converts it to (a literal written where
// a wider type is expected, `(1, Pt{x: 5})` returned as an `(Int, Named)`),
// or else its own type (recordedKind), under the current instance's frame,
// and false where n has no record.
func (g *gen) convertedKind(n ast.Node) (kind, bool) {
	if g.recordedType(n) == nil {
		return kind{}, false
	}
	if c := g.srcFA().Conversions[n]; c != nil {
		return g.project(c.To), true
	}
	return g.recordedKind(n)
}

// settledKind is the kind of the type the checker settled for n, an
// expression of Infallible, from its position (FileAnalysis.ExpectedTypes,
// analysis's settleTodo: Bool for `not todo`, the other arm's type for an
// `if` arm), under the current instance's frame, and false where it settled
// none.
func (g *gen) settledKind(n ast.Node) (kind, bool) {
	if g.fa == nil {
		return kind{}, false
	}
	fa := g.srcFA()
	if fa == nil {
		return kind{}, false
	}
	ty, settled := fa.ExpectedTypes[n]
	if !settled {
		return kind{}, false
	}
	return g.project(ty), true
}

// valueKind is the kind the checker's records give n's value, for a
// construct whose kind is its parts' (a region, a block, a `concurrent`
// body): its own type (recordedKind), or for a value of Infallible the type
// its position settled (settledKind) where one was. ok is false where n has
// no record that names a kind (noteUnrecordedKind counts it under family):
// a block then takes its tail's kind, and a region or a `concurrent` body
// declines.
func (bl *irScalarBuilder) valueKind(family string, n ast.Node) (kind, bool) {
	k, recorded := bl.g.recordedKind(n)
	if settled, isSettled := bl.g.settledKind(n); recorded && k == kindNever && isSettled && irCallableValueKind(settled) {
		k = settled
	}
	// kindInvalid: lookup — a record no kind represents is counted as no record.
	if !recorded || k == kindInvalid {
		bl.noteUnrecordedKind(family, n)
		return kind{}, false
	}
	return k, true
}

// verifyExprKind fails, under substVerify, when the kind k lowerExact
// lowered n at is not what the checker's records say n's value is there:
// its own type (recordedType) under the current instance's frame, or the
// type a record that changes it gives (exprKindAgreement). A node with no
// record (one the builder wrote or copied, one of code markUnchecked marks)
// is not checked, and the census counts it by why it has none.
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
	rec := g.recordedType(n)
	if rec == nil {
		if exprKindCensus.counting() || stdExprKindCensus.counting() {
			note(exprKindNoRecordPrefix + string(bl.noRecordOf(n, fa)))
		}
		return
	}
	if agreement, agrees := g.exprKindAgreementOf(n, k, rec); agrees {
		note(exprKindAgreePrefix + string(agreement))
		return
	}
	g.calleeMismatch(line, col, fmt.Sprintf("the builder lowers this %s (`%s`) at %s, and the checker records it as %s (%s)",
		n.NodeType(), renderNode(n), k.nomi(), rec, g.project(rec).nomi()))
}

// The census counts an agreement and a site with no record as classes of
// their own, named with these prefixes.
const (
	exprKindAgreePrefix    = "agrees: "
	exprKindNoRecordPrefix = "no record: "
	// exprKindUnrecordedPrefix names a site of a construct whose kind the
	// builder reads from the checker's record (noteUnrecordedKind) that has
	// none: a block computes its kind from its tail, and a region, a
	// lambda and a `concurrent` body decline. Each such class is held, to a
	// maximum that only goes down.
	exprKindUnrecordedPrefix = "no kind record, "
)

// noteUnrecordedKind counts n, a construct of family (a region) whose kind
// the builder reads from the checker's record (recordedKind), where n has
// no record that names a kind (a node the builder wrote that carries none,
// code markUnchecked marks): by family and by why it has none (noRecordOf,
// or a record project maps to no kind).
func (bl *irScalarBuilder) noteUnrecordedKind(family string, n ast.Node) {
	g := bl.g
	if g.fa == nil || !(exprKindCensus.counting() || stdExprKindCensus.counting()) {
		return
	}
	fa := g.srcFA()
	if fa == nil {
		return
	}
	why := "a record that names no kind"
	if g.recordedType(n) == nil {
		why = string(bl.noRecordOf(n, fa))
	}
	class := exprKindUnrecordedPrefix + family + ": " + why
	line, col := nodePos(n)
	exprKindCensus.note(g, class, line, col)
	stdExprKindCensus.note(g, class, line, col)
}

// exprKindAgreementOf is the record k equals, and false for none.
func (g *gen) exprKindAgreementOf(n ast.Node, k kind, rec analysis.Type) (exprKindAgreement, bool) {
	fa := g.srcFA()
	own := g.project(rec)
	settled, isSettled := g.settledKind(n)
	switch {
	case own == k:
		return exprKindOwnType, true
	case own == kindNever && isSettled && fa.ExpectedTypes[n] != nil && settled == k:
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
