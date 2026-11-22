package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A qualified call reaches the function it calls through an intrinsic arm
// of call that its record names (intrinsicArm), typeQualCall's helpers and
// qualImplPlan's routes. Of the helpers and routes, qualImplPlan's first
// two, stdRecvOwnerPlan and ifaceContainerCall read the call's record
// (recordedCallPlan, dispatchedCallPlan, recordedImplPlan); the others
// decide by what the builder spells or holds: an owner's name, a
// name-keyed table, an operand's kind. callRouteCensus counts the distinct
// sites each decision answers, split by where the call comes from (the
// source, a derive, the builder), so that a route the record makes
// unneeded is seen to reach 0 and deleted (TestIRGolden_RecordsMatchTheVM,
// maxCallRouteSites).

// callRoute is a decision that chose the function a call reaches.
type callRoute string

const (
	// qualImplPlan's routes, in the order it tries them.
	viaByRecord      callRoute = "qualImplPlan 1, recordedCallPlan: the function the record names"
	viaByDispatch    callRoute = "qualImplPlan 2, dispatchedCallPlan: the interface function the record names"
	viaConcreteIface callRoute = "qualImplPlan 3, concreteInterfacePlan: the interface the record names, the receiver's kind"
	viaStdIface      callRoute = "qualImplPlan 4, stdIfacePlan: std's index by spelling and receiver kind"

	// inspectCall's renderings (debugRender): by the form of the call's
	// record (the machine's rendering where the impl it names has a row
	// in machineImpls, a call of another unit's or std's body, an
	// interface value's implementors, a function's constant, a value's
	// shape), and by the operand's kind where the call has no record at
	// the instance's types. A record naming this unit's own body is planned
	// by qualImplPlan's first route. No program the census runs reaches
	// viaDebugNoRecord; `nomi test tests` and the REPL do, in std's Debug
	// of a Range an Iter's sources name (nestedDebugImpls, which
	// instantiates it with no checker type) and in a derived Debug lowered
	// at an expansive member's instance, whose frame has no checker type.
	viaDebugRecord   callRoute = "typeQualCall, inspectCall: a rendering by the form of the call's record"
	viaDebugNoRecord callRoute = "typeQualCall, inspectCall: a rendering by the operand's kind, with no record at the instance's types"
	// typeQualCall's helpers that read the call's record.
	viaIfaceContainer     callRoute = "typeQualCall, ifaceContainerCall: the std container impl the record names"
	viaStdRecvOwnerRecord callRoute = "typeQualCall, stdRecvOwnerPlan: the program's function the record names"
	// The std interface default a call's record names
	// (recordedStdDefault).
	viaStdIfaceDefault callRoute = "typeQualCall, stdIfaceDefaultCall: the std interface default the record names"

	// stdPick choosing among several std overloads by the operands' kinds
	// in stdIfacePlan, the one call route that still picks.
	viaPickStdIface callRoute = "stdPick among overloads by operand kinds, in stdIfacePlan"
)

// callRoutes is every callRoute, with whether it is held to a maximum
// (maxCallRouteSites): every route but those that read the record. A
// route it does not list fails the lowering.
var callRoutes = map[callRoute]bool{
	viaByRecord:           false,
	viaByDispatch:         false,
	viaConcreteIface:      true,
	viaStdIface:           true,
	viaDebugRecord:        false,
	viaDebugNoRecord:      true,
	viaIfaceContainer:     false,
	viaStdRecvOwnerRecord: false,
	viaStdIfaceDefault:    false,
	viaPickStdIface:       true,
}

// A call route census class is its route and where the call comes from:
// written in the source, written by the front end for a derive (on a
// synthesized line), or made by the builder.
const (
	callWritten = " (written)"
	callDerived = " (derived)"
	callMade    = " (made)"
)

// callOrigins is every origin a call route census class names.
var callOrigins = []string{callWritten, callDerived, callMade}

// noteCallRoute counts, under substVerify, the site of t under route: as
// written or derived when the checker recorded t (FileAnalysis.Calls), and
// as made otherwise (a call the builder wrote, carrying a record or none,
// and a copy of a written call). A route callRoutes does not list fails
// the lowering.
func (g *gen) noteCallRoute(t *ast.Call, route callRoute) {
	if !substVerify || t == nil || !(callRouteCensus.counting() || stdCallRouteCensus.counting()) {
		return
	}
	fa := g.srcFA()
	if fa == nil {
		return
	}
	line, col := nodePos(t)
	if _, listed := callRoutes[route]; !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("a call is routed by %q, which no route lists", route))
		return
	}
	class := string(route) + callMade
	switch {
	case fa.Calls[t] == nil:
	case analysis.IsSynthesizedLine(line):
		class = string(route) + callDerived
	default:
		class = string(route) + callWritten
	}
	stdCallRouteCensus.note(g, class, line, col)
	if strings.HasPrefix(fa.Origin, "std/") {
		// A std function's body lowered in a program's gen (an instance,
		// a module lowered with each program) is lowered there only when
		// the artifact cache holds no lowering of it, so its sites would
		// make a count depend on the cache.
		return
	}
	callRouteCensus.note(g, class, line, col)
}

// planned counts p, when there is one, under route, and answers it.
func (bl *irScalarBuilder) planned(t *ast.Call, route callRoute, p *irQualPlan) *irQualPlan {
	if p != nil {
		bl.g.noteCallRoute(t, route)
	}
	return p
}

// pickedByKinds is stdPick for the call t, counted under route when the
// operands' kinds choose among several functions.
func (bl *irScalarBuilder) pickedByKinds(t *ast.Call, route callRoute, fs []*stdFunc, kinds []kind) *stdFunc {
	f := stdPick(fs, kinds)
	if f != nil && len(fs) > 1 {
		bl.g.noteCallRoute(t, route)
	}
	return f
}
