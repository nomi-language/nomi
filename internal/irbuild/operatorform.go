package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// operatorFamily is a family of uses of an operator interface one switch
// on the use's record lowers: `==` and `!=` (equality), the orderings
// (ordering), the arithmetic, bitwise and shift operators (arithmetic), a
// prefix `~~~` (bitwiseNot), and a qualified call of an operator
// interface's function (interfaceCall).
type operatorFamily string

const (
	familyEquality      operatorFamily = "equality"
	familyOrdering      operatorFamily = "ordering"
	familyArithmetic    operatorFamily = "arithmetic"
	familyBitwiseNot    operatorFamily = "bitwise not"
	familyInterfaceCall operatorFamily = "interface call"
)

// operatorAnswer is the checker's record of an operator or a call as the
// operator census reads it (operatorUseAt, calleeAt): two kinds of no
// record, and one answer for each OperatorForm (formAnswer).
type operatorAnswer uint8

const (
	// answerWritten: no record and no type, an operator the builder wrote
	// with no record carried onto it (carryOperatorUse).
	answerWritten operatorAnswer = iota
	// answerNone: no record and a type, an operator the checker's rule
	// leaves unanswered (a type with several blocks of the interface,
	// none selected).
	answerNone
)

// The answers of a record of each form (formAnswer). answerPerInstance
// is a per-instance record this instance does not resolve: every
// instance is entered with the checker's types for its type parameters
// (substFrame), and an operator over one resolves at them.
const (
	answerByImpl      = answerNone + operatorAnswer(analysis.OperatorByImpl)
	answerStructural  = answerNone + operatorAnswer(analysis.OperatorStructural)
	answerDispatched  = answerNone + operatorAnswer(analysis.OperatorDispatched)
	answerPerInstance = answerNone + operatorAnswer(analysis.OperatorPerInstance)
)

// formAnswer is the answer of a record of the form f.
func formAnswer(f analysis.OperatorForm) operatorAnswer { return answerNone + operatorAnswer(f) }

var operatorAnswerNames = [...]string{
	answerWritten: "no record (written by the builder)", answerNone: "no record (unanswered)",
	answerByImpl: "by an impl", answerStructural: "structural", answerDispatched: "dispatched",
	answerPerInstance: "per instance, unresolved",
}

// formArm is what a family's switch does with a record of one form: the
// arm that lowers the use (arm), or why the switch has none (none), in
// which case a use with that record declines and is counted
// (unreadAnswers).
//
// An arm that is abstractOnly lowers the use only in an abstract body
// (abstract.go): anywhere else the switch has none, and a use with that
// record declines and is counted under its form's class outside an
// abstract body (abstractOnlyClass).
type formArm struct {
	arm, none    string
	abstractOnly bool
}

// familySwitches is the function whose switch over the record's form
// lowers each family. TestEveryOperatorFormHasAnArm holds each switch to
// formArms: it reads every form formArms gives it an arm for, and no
// other.
var familySwitches = map[operatorFamily]string{
	familyEquality:      "equality",
	familyOrdering:      "ordering",
	familyArithmetic:    "arithmetic",
	familyBitwiseNot:    "bitwiseNot",
	familyInterfaceCall: "interfaceCall",
}

// perInstanceNone is why no switch has an arm for a per-instance record:
// it is read at the instance's types before the switch.
const perInstanceNone = "an instance reads a per-instance record at its own types (operatorUseAt, calleeAt); one it leaves unresolved names no impl"

// formArms gives, for each family, every OperatorForm
// (analysis.OperatorForms) an arm or the reason it has none
// (TestEveryOperatorFormHasAnArm).
var formArms = map[operatorFamily]map[analysis.OperatorForm]formArm{
	familyEquality: {
		analysis.OperatorByImpl:      {arm: "an impl the machine computes, natively or by shape; any other, a call of it (implEquality)"},
		analysis.OperatorStructural:  {arm: "a comparison by shape (shapeEquality)"},
		analysis.OperatorDispatched:  {arm: "two interface values compared by their run-time types (ir.ValIface)"},
		analysis.OperatorPerInstance: {none: perInstanceNone},
	},
	familyOrdering: {
		analysis.OperatorByImpl:      {arm: "an Int's, a Float's or a Bool's compare natively; any other, a call of the compare and a test of its rank (rankedCompare)"},
		analysis.OperatorStructural:  {arm: "a tuple's lexicographic chain (tupleRankedCompare)"},
		analysis.OperatorDispatched:  {arm: "values of a type parameter an abstract body leaves open, held as its Comparable bound: a call through the bound of compare and a test of its rank (boundComparePlan)", abstractOnly: true},
		analysis.OperatorPerInstance: {none: perInstanceNone},
	},
	familyArithmetic: {
		analysis.OperatorByImpl:      {arm: "the machine's operation (nativeArithmetic); any other impl, a call of it (implArithmetic)"},
		analysis.OperatorStructural:  {none: "no arithmetic interface is met by shape"},
		analysis.OperatorDispatched:  {none: "no arithmetic interface's function takes its operands as interface values"},
		analysis.OperatorPerInstance: {none: perInstanceNone},
	},
	familyBitwiseNot: {
		analysis.OperatorByImpl:      {arm: "Int's `~~~` natively; any other impl, a call of it (bitwiseNotImpl)"},
		analysis.OperatorStructural:  {none: "BitwiseNot is met by no shape"},
		analysis.OperatorDispatched:  {none: "BitwiseNot's function takes no interface value"},
		analysis.OperatorPerInstance: {none: perInstanceNone},
	},
	familyInterfaceCall: {
		analysis.OperatorByImpl:      {arm: "an impl the machine computes, as its operator (asOperator); any other, the recorded call"},
		analysis.OperatorStructural:  {arm: "Equatable's as `==` by shape, Hashable's as a mix or the Map key hash, Comparable's as a tuple's compare"},
		analysis.OperatorDispatched:  {arm: "Equatable's as `==` over interface values, Hashable's as the Map key hash or the dispatched call, Comparable's in an abstract body as a call through the bound (boundComparePlan)"},
		analysis.OperatorPerInstance: {none: perInstanceNone},
	},
}

// unreadAnswers are, for each family, the answers of an operator or a
// call whose switch has no arm that reads them, which declines it: one the
// builder wrote with no record carried onto it (carryOperatorUse), one
// nothing answers, and each form formArms gives no arm. Each is a class
// of the operator census (noteUnreadOperator, noteUnreadCall), held to a
// maximum that only goes down (maxOperatorFormSites).
var unreadAnswers = func() map[operatorFamily][]operatorAnswer {
	out := map[operatorFamily][]operatorAnswer{}
	for f, arms := range formArms {
		unread := []operatorAnswer{answerWritten, answerNone}
		for _, form := range analysis.OperatorForms() {
			if arms[form].none != "" {
				unread = append(unread, formAnswer(form))
			}
		}
		out[f] = unread
	}
	return out
}()

// unreadClass is the operator census's class of an operator of the
// family f whose record has the answer a.
func unreadClass(f operatorFamily, a operatorAnswer) string {
	return string(f) + ", " + operatorAnswerNames[a]
}

// abstractOnlyClass is the operator census's class of an operator of the
// family f whose record has the answer a, outside an abstract body, where
// the arm f's switch has for a lowers nothing (formArm.abstractOnly).
func abstractOnlyClass(f operatorFamily, a operatorAnswer) string {
	return unreadClass(f, a) + ", outside an abstract body"
}

// abstractOnlyAnswers are, for each family, the answers whose arm lowers a
// use only in an abstract body (formArm.abstractOnly).
var abstractOnlyAnswers = func() map[operatorFamily][]operatorAnswer {
	out := map[operatorFamily][]operatorAnswer{}
	for f, arms := range formArms {
		for _, form := range analysis.OperatorForms() {
			if arms[form].abstractOnly {
				out[f] = append(out[f], formAnswer(form))
			}
		}
	}
	return out
}()

// noteUnreadOperator counts, under substVerify, the declined operator t
// of the family f in the operator census when its record is one f's
// switch has no arm for (unreadAnswers), under that answer's class
// (unreadClass).
func (g *gen) noteUnreadOperator(t ast.Node, f operatorFamily) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	use, resolved := g.operatorUseAt(t)
	var form analysis.OperatorForm
	if use != nil {
		form = use.Form
	}
	g.noteUnread(t, f, g.answerOf(t, form, resolved))
}

// noteUnreadCall counts, under substVerify, the declined call t, of an
// operator interface's function whose record at this instance is c, as
// noteUnreadOperator counts an operator.
func (g *gen) noteUnreadCall(t *ast.Call, c analysis.Callee) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	g.noteUnread(t, familyInterfaceCall, g.answerOf(t, c.Form, false))
}

// noteUnread counts t, of the family f, under the class of its answer a
// when f's switch has no arm for a, or has one only in an abstract body
// and t is outside one.
func (g *gen) noteUnread(t ast.Node, f operatorFamily, a operatorAnswer) {
	line, col := nodePos(t)
	switch {
	case slices.Contains(unreadAnswers[f], a):
		operatorCensus.note(g, unreadClass(f, a), line, col)
	case slices.Contains(abstractOnlyAnswers[f], a) && (g.dc == nil || !g.dc.abstract):
		operatorCensus.note(g, abstractOnlyClass(f, a), line, col)
	}
}

// answerOf is the answer the record form of the operator or call t gives,
// as the operator census classes it; resolved says an instance's types
// were asked for a record they left unanswered.
func (g *gen) answerOf(t ast.Node, form analysis.OperatorForm, resolved bool) operatorAnswer {
	if form != 0 {
		return formAnswer(form)
	}
	if _, typed := g.srcFA().ExprTypes[t]; resolved || typed {
		return answerNone
	}
	return answerWritten
}
