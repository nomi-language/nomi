package irbuild

import (
	"fmt"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// operatorRoute is how the builder lowered an operator, whatever its
// record says (irScalarBuilder.op). An operator whose lowering declines
// has none.
type operatorRoute uint8

const (
	routeUnset operatorRoute = iota
	// routeNative is the machine's own operation: over scalars, ir.Arith,
	// ir.UnaryArith, ir.Concat, an ir.Compare of a scalar shape; over
	// Lists, the concatenation `List.concat` is the host operation of.
	routeNative
	// routeStructural compares by the operands' shape: an ir.Compare of a
	// container, struct or variant, a tuple part by part, Unit's or a
	// marker's constant answer.
	routeStructural
	// routeImpl calls an impl's function: the program's, std's, or one
	// chosen by kind.
	routeImpl
	// routeDispatched compares two interface values by their run-time
	// types (ir.ValIface).
	routeDispatched
)

// operatorAnswer is the checker's record of an operator as the verifier
// reads it (operatorUseAt): its OperatorForm, a by-impl record split by
// the route machineImpls gives the impl it names and else by whose impl
// it is, and two kinds of no record.
type operatorAnswer uint8

const (
	// answerWritten: no record and no type, an operator the builder wrote.
	answerWritten operatorAnswer = iota
	// answerNone: no record and a type, an operator the checker's rule
	// leaves unanswered (a type with several blocks of the interface,
	// none selected). No pair lists it.
	answerNone
	// answerMachineNative and answerMachineStructural: a by-impl record of
	// an impl the machine computes itself (machineImpls), natively or by
	// shape.
	answerMachineNative
	answerMachineStructural
	answerStdImpl
	answerDerivedImpl
	answerProgramImpl
	answerStructural
	answerDispatched
	// answerPerInstance: a per-instance record this instance does not
	// resolve, which no pair lists: every instance is entered with the
	// checker's types for its type parameters (substFrame), and an
	// operator over one resolves at them.
	answerPerInstance
)

var operatorAnswerNames = [...]string{
	answerWritten: "no record (written by the builder)", answerNone: "no record (unanswered)",
	answerMachineNative: "by an impl the machine computes natively", answerMachineStructural: "by an impl the machine computes by shape",
	answerStdImpl: "by std's impl", answerDerivedImpl: "by a derived impl", answerProgramImpl: "by the program's impl",
	answerStructural: "structural", answerDispatched: "dispatched", answerPerInstance: "per instance, unresolved",
}

var operatorRouteNames = [...]string{
	routeUnset: "no route", routeNative: "natively", routeStructural: "structurally",
	routeImpl: "through an impl", routeDispatched: "dispatched",
}

// operatorClass is one entry of the table verifyOperatorForm holds each
// operator lowering to. An agreed pair is what the record says; a pair
// by design stays as it is; any other class is a known disagreement,
// which TestIRGolden_RecordsMatchTheVM counts by distinct site and holds
// to a maximum that only goes down (maxOperatorFormSites).
type operatorClass struct {
	name     string
	agreed   bool
	byDesign bool
}

type operatorPair struct {
	answer operatorAnswer
	route  operatorRoute
}

// operatorPairs is every (record, route) pair an operator lowering may
// make. A pair not listed fails the run.
var operatorPairs = map[operatorPair]operatorClass{
	{answerStdImpl, routeImpl}:          {name: "std impl, called", agreed: true},
	{answerDerivedImpl, routeImpl}:      {name: "derived impl, called", agreed: true},
	{answerProgramImpl, routeImpl}:      {name: "program impl, called", agreed: true},
	{answerStructural, routeStructural}: {name: "structural, structural", agreed: true},
	{answerDispatched, routeDispatched}: {name: "dispatched, dispatched", agreed: true},
	// An impl the machine computes itself, lowered by the route
	// machineImpls gives it: Int's, Float's, Decimal's and String's
	// operators and Bool's derived ones natively, List's, Unit's and a
	// derived enum's Equatable by shape. Lowered by another route, or a
	// std or derived impl lowered by one of these two without a row, is
	// no listed pair.
	{answerMachineNative, routeNative}:         {name: "machine impl, native", agreed: true},
	{answerMachineStructural, routeStructural}: {name: "machine impl, structural", agreed: true},

	// Int arithmetic the builder writes over values it already holds
	// (tupleHashOf's hash mix).
	{answerWritten, routeNative}: {name: "written, native", byDesign: true},

	// `==` the builder writes to reuse compare (structuralEqualCall,
	// containerEqualCall).
	{answerWritten, routeStructural}: {name: "written, structural"},
}

// beginOperator starts lowering an operator: it answers the lowering an
// enclosing operator is in, which endOperator puts back.
func (bl *irScalarBuilder) beginOperator() operatorRoute {
	outer := bl.op
	bl.op = routeUnset
	return outer
}

// endOperator ends lowering the operator t, which ok says lowered, and
// holds the route it committed to against its record
// (verifyOperatorForm).
func (bl *irScalarBuilder) endOperator(t ast.Node, ok bool, outer operatorRoute) {
	if ok {
		bl.g.verifyOperatorForm(t, bl.op)
	}
	bl.op = outer
}

// verifyOperatorForm fails, under substVerify, when the builder lowered
// the operator t by a route its record (operatorUseAt) does not pair with
// in operatorPairs, or by no route at all; and counts the pair's class
// in the operator census when one is open.
func (g *gen) verifyOperatorForm(t ast.Node, route operatorRoute) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	line, col := nodePos(t)
	if route == routeUnset {
		g.calleeMismatch(line, col, "an operator's lowering commits to no route")
		return
	}
	answer := g.operatorAnswerOf(t)
	class, listed := operatorPairs[operatorPair{answer, route}]
	if !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("the builder lowers the operator %s, and the record answers it %s",
			operatorRouteNames[route], operatorAnswerNames[answer]))
		return
	}
	operatorCensusNote(g, class.name, line, col)
}

// operatorAnswerOf is the record of the operator t at this instance, as
// verifyOperatorForm pairs it with a route.
func (g *gen) operatorAnswerOf(t ast.Node) operatorAnswer {
	fa := g.srcFA()
	use, resolved := g.operatorUseAt(t)
	if use == nil {
		if _, typed := fa.ExprTypes[t]; resolved || typed {
			return answerNone
		}
		return answerWritten
	}
	switch use.Form {
	case analysis.OperatorByImpl:
		switch g.machineRouteOf(use) {
		case routeNative:
			return answerMachineNative
		case routeStructural:
			return answerMachineStructural
		}
		switch {
		case use.Impl.Derived():
			return answerDerivedImpl
		case use.Impl != nil && !g.operatorImplStd(use.Impl).IsZero():
			return answerStdImpl
		}
		return answerProgramImpl
	case analysis.OperatorStructural:
		return answerStructural
	case analysis.OperatorDispatched:
		return answerDispatched
	}
	return answerPerInstance
}

// operatorCensus counts, while a test has it open, the distinct source
// sites of each class verifyOperatorForm finds. Only a program's
// lowering is counted (a gen with a std instance table), as substCensus
// counts: the shared std lowering runs once a process, or not at all
// when its bundle is read back.
var operatorCensus struct {
	sync.Mutex
	open  bool
	seen  map[string]bool
	sites map[string][]string
}

func operatorCensusNote(g *gen, class string, line, col int) {
	if g.stdInsts == nil || g.stdInsts.stdOwn {
		return
	}
	c := &operatorCensus
	c.Lock()
	defer c.Unlock()
	if !c.open {
		return
	}
	fa := g.srcFA()
	path := fa.FilePath
	if path == "" {
		path = fa.Origin
	}
	site := fmt.Sprintf("%s:%d:%d", path, line, col)
	key := class + "\x00" + site
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.sites[class] = append(c.sites[class], site)
}

// openOperatorCensus starts counting and answers a function that stops
// and returns the distinct sites by class.
func openOperatorCensus() func() map[string][]string {
	c := &operatorCensus
	c.Lock()
	c.open, c.seen, c.sites = true, map[string]bool{}, map[string][]string{}
	c.Unlock()
	return func() map[string][]string {
		c.Lock()
		defer c.Unlock()
		out := c.sites
		c.open, c.seen, c.sites = false, nil, nil
		return out
	}
}
