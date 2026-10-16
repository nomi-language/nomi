package irbuild

// `subtests rows, pattern { body }` (ir.Subtests).
//
// The rows lower to a sequence, the view an `Iter` call takes over its source
// (seqView), at the row type the checker recorded
// (analysis.FileAnalysis.SubtestsRowTypes). The body lowers to its own
// function of one parameter, the row, which binds the pattern as a test's
// pattern binds its setup value (testDestructure) and runs the body as test
// statements: an assertion there is a statement assertion, whose failure
// leaves the row's activation as a test case's does. The function captures
// what the body reads from the enclosing body, as a lambda does.
//
// The VM runs the function once per row, catches each row's failure, and
// delivers the combined failure the way an assertion in the enclosing body
// would (internal/vm/subtests.go). So where the statement may stand is where
// a statement assertion may: a test body, or an ordinary fn the checker
// names as the boundary (fnAssertion).

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// isSubtestsRowBoundary reports whether an assertion's checker boundary is a
// `subtests` row.
func isSubtestsRowBoundary(boundary string) bool {
	return boundary == analysis.SubtestsRowBoundary
}

// subtests lowers the statement in the current block.
func (bl *irScalarBuilder) subtests(t *ast.Subtests) bool {
	if bl.recording > 0 || (bl.inTest && !bl.testWalked) {
		irDeclineNote("a `subtests` statement in a body the builder reads back")
		return false
	}
	if bl.subtestsRow {
		irDeclineNote("a `subtests` statement inside a `subtests` row")
		return false
	}
	ty, found := bl.g.srcFA().SubtestsRowTypes[t]
	if !found {
		irDeclineNote("a `subtests` statement whose row type the checker did not record")
		return false
	}
	rowK := bl.g.project(ty)
	// kindInvalid: lookup — asks whether the checker's row type has a representation; the decline below names the statement.
	if rowK == kindInvalid || (!irCallableValueKind(rowK) && rowK != kindUnit) {
		irDeclineNote("a `subtests` row type outside the domain")
		return false
	}
	rows, rk, _, ok := bl.lower(t.Rows)
	if !ok {
		return false
	}
	seq, ok := bl.seqView(t.Rows, rows, rk, rowK)
	if !ok {
		irDeclineNote("`subtests` rows the builder cannot view as a sequence: " + rk.nomi())
		irDeclineAtNode(t.Rows)
		return false
	}
	row, ok := bl.subtestsRowFunc(t, rowK)
	if !ok {
		return false
	}
	bl.b.Append(ir.NewSubtests(bl.g.irPos(t.Line, t.Col), seq, row, renderNode(t.Rows)))
	return true
}

// subtestsRowFunc builds the row function and answers the function value
// that closes over what the body captures.
func (bl *irScalarBuilder) subtestsRowFunc(t *ast.Subtests, rowK kind) (ir.Temp, bool) {
	body := t.Body
	if body == nil || len(body.Stmts) == 0 {
		irDeclineNote("a `subtests` statement with an empty body")
		return ir.NoTemp, false
	}
	at := bl.g.irNodePos(body)
	f := ir.NewFunc(at, "row")
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{},
		patternOK: true, at: body}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{
		g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{},
		inTest: true, testApp: bl.testApp, testWalked: true,
		root: body, subtestsRow: true, deferOK: true,
	}
	defer bl.g.enterBlockTypes(body)()
	param := bl.g.irAddParam(f, sh.localSym("|row"), rowK)
	if !child.testDestructure(t.Pattern, param, rowK) {
		return ir.NoTemp, false
	}
	child.testDeferScope = &irDeferScope{block: body}
	child.testWithScope = &irWithScope{root: true, saved: ir.NoTemp}
	// A final statement whose value is a `Result<_, AssertionFailure>` is
	// the row's verdict, as it is a case's (testVerdict).
	child.testTail = true
	if !child.testStmts(body.Stmts) {
		return ir.NoTemp, false
	}
	last := body.Stmts[len(body.Stmts)-1]
	child.closeDefers(child.testDeferScope, last)
	end := at
	if line, _ := nodePos(last); emittableLine(line) {
		end = bl.g.irNodePos(last)
	}
	child.b.SetTerm(ir.NewReturnUnit(end))
	if err := bl.g.irLint(f); err != nil {
		irDeclineNote("a `subtests` row graph that does not lint: " + err.Error())
		return ir.NoTemp, false
	}
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f, child.captures...)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: funcKindIn(bl.g, []kind{rowK}, kindUnit)})
	return n.Dst(), true
}
