package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// operatorFamily is a family of operators one switch on the operator's
// record lowers: `==` and `!=` (equality), the orderings (ordering), the
// arithmetic, bitwise and shift operators (arithmetic) and a prefix `~~~`
// (bitwiseNot).
type operatorFamily string

const (
	familyEquality   operatorFamily = "equality"
	familyOrdering   operatorFamily = "ordering"
	familyArithmetic operatorFamily = "arithmetic"
	familyBitwiseNot operatorFamily = "bitwise not"
)

// operatorAnswer is the checker's record of an operator as the operator
// census reads it (operatorUseAt): its OperatorForm, and two kinds of no
// record.
type operatorAnswer uint8

const (
	// answerWritten: no record and no type, an operator the builder wrote
	// with no record carried onto it (carryOperator).
	answerWritten operatorAnswer = iota
	// answerNone: no record and a type, an operator the checker's rule
	// leaves unanswered (a type with several blocks of the interface,
	// none selected).
	answerNone
	answerByImpl
	answerStructural
	answerDispatched
	// answerPerInstance: a per-instance record this instance does not
	// resolve: every instance is entered with the checker's types for its
	// type parameters (substFrame), and an operator over one resolves at
	// them.
	answerPerInstance
)

var operatorAnswerNames = [...]string{
	answerWritten: "no record (written by the builder)", answerNone: "no record (unanswered)",
	answerByImpl: "by an impl", answerStructural: "structural", answerDispatched: "dispatched",
	answerPerInstance: "per instance, unresolved",
}

// unreadAnswers are, for each family, the answers of an operator whose
// switch has no arm that reads them, which declines it: one the builder
// wrote with no record carried onto it (carryOperator), one nothing
// answers, one over a type parameter its instance does not resolve, an
// ordering over interface values, and an arithmetic operator or a `~~~`
// answered by shape or by dispatch. Each is a class of the operator
// census (noteUnreadOperator), held to a maximum that only goes down
// (maxOperatorFormSites).
var unreadAnswers = map[operatorFamily][]operatorAnswer{
	familyEquality:   {answerWritten, answerNone, answerPerInstance},
	familyOrdering:   {answerWritten, answerNone, answerPerInstance, answerDispatched},
	familyArithmetic: {answerWritten, answerNone, answerPerInstance, answerStructural, answerDispatched},
	familyBitwiseNot: {answerWritten, answerNone, answerPerInstance, answerStructural, answerDispatched},
}

// unreadClass is the operator census's class of an operator of the
// family f whose record has the answer a.
func unreadClass(f operatorFamily, a operatorAnswer) string {
	return string(f) + ", " + operatorAnswerNames[a]
}

// noteUnreadOperator counts, under substVerify, the declined operator t
// of the family f in the operator census when its record is one f's
// switch has no arm for (unreadAnswers), under that answer's class
// (unreadClass).
func (g *gen) noteUnreadOperator(t ast.Node, f operatorFamily) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	if a := g.operatorAnswerOf(t); slices.Contains(unreadAnswers[f], a) {
		line, col := nodePos(t)
		operatorCensus.note(g, unreadClass(f, a), line, col)
	}
}

// operatorAnswerOf is the record of the operator t at this instance, as
// the operator census classes it.
func (g *gen) operatorAnswerOf(t ast.Node) operatorAnswer {
	use, resolved := g.operatorUseAt(t)
	if use == nil {
		if _, typed := g.srcFA().ExprTypes[t]; resolved || typed {
			return answerNone
		}
		return answerWritten
	}
	switch use.Form {
	case analysis.OperatorByImpl:
		return answerByImpl
	case analysis.OperatorStructural:
		return answerStructural
	case analysis.OperatorDispatched:
		return answerDispatched
	}
	return answerPerInstance
}
