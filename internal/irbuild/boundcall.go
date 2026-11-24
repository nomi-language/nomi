package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Calls through a type parameter's bound, in an abstract body.
//
// An abstract body (abstract.go) holds each type parameter it leaves open
// as its bounds' interface, so a value of the type parameter is lowered as
// a value of that interface is. What the checker records about a use of
// the type parameter's operations is per instance
// (analysis.OperatorPerInstance, a CalleeInterface record for `T`): each
// instance resolves it at its own types (calleeAt, operatorUseAt). An
// abstract body, and an instance it asks for at its type parameters,
// resolves it at the type an open type parameter is held as
// (abstractRecordType, recordTypes), so one rule answers:
//
//   - where the checker answers the use at an interface value of the bound,
//     the use takes that answer's form, as it would over an interface value:
//     a call with one receiver is a dispatch on it (dispatchedCallPlan), and
//     a rendering is the erased one;
//   - where it answers nothing, because the function takes no value of the
//     type to select an implementation by (`T.from_sql(v)`, `T.make()`) or
//     takes two, the call is a call through the bound (ir.NewBoundCall):
//     the interface's function for some type, on dispatchedCallPlan's
//     route, since the record leaves the implementation to where it runs.
//
// The effect walk answers both by every implementation the program retains
// (vm's reach walk), so the rule chooses only the IR's form, never the
// answer. Nothing runs either: an abstract body is in a module marked
// abstract, which no machine runs and nothing encodes or links.
//
// A call through the bound names the interface's function by the symbol
// every implementation is recorded under (irIfaceMethodSym): the declaring
// unit's for a program's interface, the program's for a std interface it
// holds as a program interface (stdprogiface.go). The std interfaces
// stdiface.go's table holds (Display, Debug, Comparable) have no such
// symbol: each unit records its implementations under its own, so a use of
// one of them over a value held as an interface is always a call through
// the bound here, never a dispatch (boundStdPlan): `Debug.inspect(x)` and
// a Debug rendering of `x` (debugRender), `Comparable.compare(a, b)`
// (interfaceCall) and an ordering `a < b` (ordering), whose record at the
// bound is OperatorDispatched. The walk answers a call through the bound
// by every implementation recorded under any symbol of the function's
// name (vm's boundImpls), so every file's counts. A Display rendering is
// erased, and the walk reads it by every Display body the program has.

// boundCallPlan plans t, a call the checker records as an interface's
// function through a type parameter of an abstract body left open
// (openParam), as a call through the bound: the function the record names,
// for some type, with t's operands as written. Nil for any other call, for
// one whose function takes one receiver, which is dispatched on it
// (dispatchedCallPlan, whose route this is), for one that leaves a
// defaulted parameter out, whose default the implementation that runs
// chooses, and for a function of an interface whose implementations no one
// symbol names (the file comment). It reads the call's own record, which
// names the type parameter, and not the record at the bound (calleeAt).
func (bl *irScalarBuilder) boundCallPlan(t *ast.Call, args irQualArgs) *irQualPlan {
	c := bl.g.calleeOf(t)
	if c.Kind != analysis.CalleeInterface || !args.ok {
		return nil
	}
	if _, open := bl.g.openParam(c.For); !open {
		return nil
	}
	d := bl.g.ifaceOfQualifier(c.Owner)
	if d == nil && c.Owner.Kind == analysis.QualInterface && !c.Owner.Std.IsZero() {
		d = bl.g.stdDeclaredIface(c.Owner.Std.Module, c.Owner.Std.Name)
	}
	m := bl.g.recordedMethod(d, c)
	if m == nil {
		d, m = stdSharedMethod(d, c)
	}
	// A function with one receiver is dispatched on it, except one of an
	// interface each file records its implementations under a symbol of
	// its own for (stdiface.go's table), where a dispatch would reach this
	// file's alone.
	if m == nil || erasedReceiverGap(m) == "" && !d.stdShared || bl.g.leavesDefaults(t, len(m.params)) {
		return nil
	}
	result, recorded := bl.g.recordedKind(t)
	if !recorded {
		// A call the builder made for one the source writes (a pipe's
		// stage, a lambda's body copied for a capture) has the checker's
		// instantiated signature of its callee, at the callee's name.
		result = bl.g.checkedKind(t)
		// kindInvalid: sentinel — checkedKind's nothing-recorded answer.
		recorded = result != kindInvalid
	}
	if !recorded {
		return nil
	}
	return bl.g.boundPlan(d, m, result)
}

// boundPlan is the call through the bound of m, a function of interface d
// whose result has kind result: the interface's function for some type,
// named by the symbol this file records its implementations under
// (irIfaceMethodSym), or nil when none names them or the result is no
// value.
func (g *gen) boundPlan(d *ifaceDef, m *ifaceMethod, result kind) *irQualPlan {
	sym := g.irIfaceMethodSym(d, m)
	if sym == nil || !irCallableValueKind(result) && result != kindUnit {
		return nil
	}
	return &irQualPlan{token: m, name: d.nomi + "." + m.name, result: result, bound: true, sym: sym}
}

// stdSharedMethod is the interface of stdiface.go's table, and its
// function, that the record c names by the std interface and the name its
// record gives (Callee.Std): the table's declarations are its own, which
// no record names. d, and a nil function, for any other.
func stdSharedMethod(d *ifaceDef, c analysis.Callee) (*ifaceDef, *ifaceMethod) {
	for i, s := range stdIfaceSpecs {
		if c.Std.Module == s.module && c.Std.Owner == s.nomi {
			shared := stdIfaceDefs()[i]
			return shared, shared.methods[c.Std.Name]
		}
	}
	return d, nil
}

// boundComparePlan is, in an abstract body, the call through the bound of
// Comparable's compare that an ordering over values of a type parameter
// left open makes (the record at its bound is OperatorDispatched), or nil
// outside one.
func (g *gen) boundComparePlan() *irQualPlan {
	return g.boundStdPlan("Comparable", "compare")
}

// boundStdPlan is, in an abstract body, the call through the bound of
// method of iface, an interface of stdiface.go's table, whose
// implementations each file records under a symbol of its own, so that
// only a call through the bound, which the walk answers by every file's
// (vm's reach walk), reaches them all; nil outside an abstract body.
func (g *gen) boundStdPlan(iface, method string) *irQualPlan {
	if g.dc == nil || !g.dc.abstract {
		return nil
	}
	for i := range stdIfaceSpecs {
		if stdIfaceSpecs[i].nomi == iface {
			d := stdIfaceDefs()[i]
			m := d.methods[method]
			return g.boundPlan(d, m, m.result)
		}
	}
	return nil
}
