package irbuild

// The discarded statement.
//
// # Two spellings of one rule
//
// `irCopyDrop` is `_ = <src>`. The checker refuses a non-final expression
// statement whose value is not Unit and names two ways to satisfy the rule:
// `dbg` (a non-final `dbg` answers its operand) and an explicit discard.
// `_ = expr` and `_name = expr` are the same statement with the same delivery.
//
// So both are producers of one delivery, with no node, no field and no
// constructor of their own.
//
// # WHAT DECIDES WHETHER A DROP IS APPENDED, AND WHY IT IS A GRAPH QUESTION
//
// A call nobody reads IS the discarded statement: whether a call's destination
// is read is a def-use fact of the graph. Appending a Copy over a call would
// make the graph say something READS the call, which is false. The drop is
// therefore appended exactly when the value is NOT already a call, and
// `irTempDefinedByCall` is that question, asked of the graph with one scan
// rather than of the AST.
//
// # WHY THE TEST IS "IS IT A CALL" AND NOT "IS IT UNIT"
//
// A kind test (`k == kindUnit`) is a proxy that holds only while every Unit
// value comes from a call. `Unit` is also a value, so `_ = Unit` is reachable
// and needs its drop; a kind test would append nothing for it. The call test
// answers both positions: `io.print(x)` is a call, so it still gets no drop.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irStatementDrop appends the `_ = <value>` a non-final STATEMENT needs.
//
// WHY IT IS HERE AND NOT IN THE ARM THAT LOWERED THE VALUE: whether a
// statement's value is discarded is a fact about the statement's POSITION in
// the block, which `irScalarBuild`'s leading-statement loop holds and
// `bl.lower` does not.
//
// TWO PRODUCERS, and they are the front end's two spellings of
// one rule: a non-final `dbg`, and an explicit discard. Written over the GRAPH
// rather than over the node type so a third spelling needs no third case.
func (bl *irScalarBuilder) irStatementDrop(at ast.Node, src ir.Temp, k kind) {
	if irTempDefinedByCall(bl.f, src) {
		// The call IS the statement. See the file header.
		return
	}
	cp := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), src)
	bl.b.Append(cp)
	bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyDrop})
}

// irDiscardStmtUnit appends the discard STATEMENT's own Unit value.
//
// A binding statement's own value is Unit, so every non-final binding is
// followed by a Unit value and its drop. A discard binding has no `ir.Bind`
// and therefore needs its own owner for that value.
//
// SO IT IS IN THE GRAPH RATHER THAN IN A DELIVERY. `ir.ConstUnit`'s own doc
// line is "`rt.Unit{}`: the value of a statement", and that is exactly what
// this is: a value, and then the discard of it. Hiding it inside a second Copy
// spelling would add an `irCopyRole` describing a VALUE rather than a
// delivery, and it would need the `*ast.Binding` carried to the Copy for
// `g.irUnit`'s position — a node-keyed side list for a position the Copy
// already has.
//
// The position is the BINDING's.
func (bl *irScalarBuilder) irDiscardStmtUnit(at ast.Node) {
	u := ir.NewUnit(bl.g.irNodePos(at), bl.f.NewTemp())
	bl.b.Append(u)
	cp := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), u.Dst())
	bl.b.Append(cp)
	bl.side(cp.Dst(), irScalarSide{k: kindUnit, copy: irCopyDrop})
}

// irTempDefinedByCall reports whether t is an `ir.Call`'s destination.
//
// The def-use chain is not built, and one scan of one function answers it. A
// retained body is a handful of instructions in a handful of blocks, so the
// cost is a bounded scan.
func irTempDefinedByCall(f *ir.Func, t ir.Temp) bool {
	if t == ir.NoTemp {
		return false
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if c, isCall := in.(*ir.Call); isCall && c.Dst() == t {
				return true
			}
		}
	}
	return false
}
