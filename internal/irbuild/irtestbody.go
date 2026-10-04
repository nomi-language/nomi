package irbuild

// A `test` declaration's body, retained as an `ir.Func` and declared in the
// module's test table.
//
// A test body is retained here rather than at `funcDecl` or `emitStdFunc`,
// so the VM's test runner has a graph to drive.
//
// # Where a test body is built
//
// The body is built into an `ir.Func` for `internal/vm` to run, once, after
// `Generate` has walked every module (irRetryWalkOnlyTestBodies), so the
// generator state it writes cannot change another body's lowering. A grouped
// case is built at its declaration, since its group is already known there.
//
// So the grammar here is wider than `irScalarBody`'s, and each widening is
// gated on `irScalarBuilder.inTest` rather than admitted generally. A test
// body is observed through its own hook (`irTestBodyObserved`), so it does
// not move the `fn`-body counts `irFuncObserved` reports.
//
// The four widenings:
//
//  1. COMPARISON (`ir.Compare`). An `assert` subject is a comparison in almost
//     every case in the corpus, so without this the reachable subset is empty.
//     See internal/ir/compare.go.
//  2. `and` / `or`. The shape is `ir.BeginShortCircuit`'s, shared, and the
//     ASYMMETRIC RECORDING RULE is the reason it appears here: `or` shows its
//     left operand always and `and` shows its left only when the left DECIDED.
//     That rule is only observable through a FAILING assertion, so it has no
//     coverage anywhere except a fixture that fails on purpose.
//  3. A raw or triple-quoted String literal, which a test body uses
//     (`01-foundations/raw_strings_test.nomi`,
//     `triple_quoted_strings_test.nomi`).
//  4. `assert` / `refute` as a statement, with the `values:` rows the report
//     prints (`ir.Assert`, `ir.Record`).
//
// # What is declined
//
//   - A case under a `tests` group: its boot, setup, pattern and clock are
//     irtestgroup.go's and irtestsetup.go's.
//   - A bare name bound by a pipe whose stages were not recorded declines. See
//     irpatternassert.go.
//   - `testing.check`: see irtestingcheck.go for what it declines.

import (
	"fmt"
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irTestBodyObserved is a test-only hook, nil in production, called once per
// `test` declaration the builder lowers a body for: with the case's reported
// name and its retained `ir.Func`, or nil when the shape declined.
//
// It is a hook rather than a counter, for `irFuncObserved`'s reason: a caller
// that needs it runs whole lowerings through `Generate`, which hands back no
// gen.
//
// It is separate from `irFuncObserved` rather than a third origin on it,
// because that hook's contract is "once per function the builder builds a
// body for", and a test case is not one.
var irTestBodyObserved func(name string, f *ir.Func)

// irTestBody builds a grouped case's body into a retained `ir.Func` and
// declares it in this module's test table, or queues an ungrouped case for
// irRetryWalkOnlyTestBodies. It answers nil: the case is built for the VM.
//
// It DECLINES rather than refuses, exactly as `irScalarBuild` does: the caller
// reports the refusal with the decline's reason, and a second refusal here
// would name the same gap twice.
//
// It is called with the case's scope freshly pushed, before anything else
// binds the body's names, and the order is load-bearing. `bl.lower`'s
// `*ast.Ident` arm asks `g.lookup`, so a name already bound in the gen's
// scope would resolve to an `ir.Ref` naming it, which no machine can read.
// Building first means a body's own names come from `bl.bound`, the
// `ir.Bind` destinations this builder created, which is the def-use chain
// the graph carries.
func (g *gen) irTestBody(c testCaseDecl, name string) *irScalarPlan {
	p, group, st := g.irTestBodyBuild(c, name)
	if st == irTestBodyDeferred {
		// Observed, linted and declared by irRetryWalkOnlyTestBodies.
		return nil
	}
	var fn *ir.Func
	if st == irTestBodyBuilt {
		fn = p.fn
	}
	if irTestBodyObserved != nil {
		irTestBodyObserved(name, fn)
	}
	if st != irTestBodyBuilt {
		return nil
	}
	g.irTestBodyDeclare(len(g.irModule().Tests()), name, fn, group)
	irDeclineWhy = "a grouped test case, built for the VM only"
	return nil
}

// irTestBodyDeclare lints a retained body and declares it at position at of
// this module's test table.
func (g *gen) irTestBodyDeclare(at int, name string, fn *ir.Func, group ir.TestGroup) {
	if err := ir.Lint(fn); err != nil {
		// `irScalarLower`'s discipline: a malformed `ir.Func` is a producer
		// bug with no user input that reaches it, and recording a refusal
		// instead would let a wrong graph reach a consumer while the tally
		// reported a missing feature.
		panic(err)
	}
	g.irModule().DeclareTestAt(at, name, fn, group)
}

// irTestBodyStatus is what one case's inline build answered.
type irTestBodyStatus int

const (
	irTestBodyDeclined irTestBodyStatus = iota
	irTestBodyBuilt
	// irTestBodyDeferred is a case whose reader-shaped attempt declined and
	// whose VM-only attempt waits in gen.irTestRetries.
	irTestBodyDeferred
)

// irTestRetry is one ungrouped case waiting for its VM-only attempt. at is
// its position in the module's test table, counted when the walk reached it.
type irTestRetry struct {
	c     testCaseDecl
	name  string
	group ir.TestGroup
	at    int
	// retained is set once the attempt declared the body.
	retained bool
}

// irTestBodyBuild is the build itself, split from the declaration so the
// observation hook sees a declined attempt as nil.
func (g *gen) irTestBodyBuild(c testCaseDecl, name string) (*irScalarPlan, ir.TestGroup, irTestBodyStatus) {
	g.irDeclineOpen("test body: " + name)
	switch {
	case c.refusedBy != "":
		irDeclineNote("a case the builder refused: " + c.refusedBy)
		return nil, ir.TestGroup{}, irTestBodyDeclined
	case c.body == nil || len(c.body.Stmts) == 0:
		irDeclineNote("an empty test body")
		return nil, ir.TestGroup{}, irTestBodyDeclined
	case !emittableLine(c.line):
		irDeclineNote("a test on a synthesized line")
		return nil, ir.TestGroup{}, irTestBodyDeclined
	}
	group, ok := g.irTestGroup(c)
	if !ok {
		irDeclineNote("a `tests` group setup outside the retained shape")
		return nil, ir.TestGroup{}, irTestBodyDeclined
	}
	if irTestGrouped(c) {
		if p, ok := g.irTestBodyAttempt(c, name, group, irTestGrouped(c)); ok {
			return p, group, irTestBodyBuilt
		}
		return nil, ir.TestGroup{}, irTestBodyDeclined
	}
	// AN UNGROUPED CASE IS BUILT AFTER THE MODULE WALK IS COMPLETE. It resolves
	// types and calls, and resolving writes generator state: registering the
	// generic TEMPLATE's mirror of `shapes.Holder` in `g.types`, for one,
	// would make later lowering find that mirror instead of minting the
	// `Holder<Int>` instance. Built once `Generate` has walked every module,
	// nothing it writes can change another body's lowering, so there is
	// nothing to restore. TestIRTestBody_CorpusWalkRetryMovesNoSource is the guard.
	at := 0
	if g.irMod != nil {
		at = len(g.irMod.Tests())
	}
	g.irTestRetries = append(g.irTestRetries, irTestRetry{c: c, name: name, group: group, at: at})
	return nil, ir.TestGroup{}, irTestBodyDeferred
}

// irRetryWalkOnlyTestBodies runs the VM-only attempt of every case whose
// reader-shaped attempt declined, in declaration order, and declares each body
// it retains at the position the walk reached it.
//
// `Generate` calls it once every module of the program is walked, so the
// generator state the attempts write — types, instances, package-wide names,
// used packages, the error list — is read by nothing that emits. What they
// keep is IR: the module, its symbols, the `ir.Table` of type identities and
// the host keys, which the retained graph names and the VM links.
func (g *gen) irRetryWalkOnlyTestBodies() {
	g.irRetryWalkOnlyTestBodiesWith(nil)
}

// irRetryWalkOnlyTestBodiesWith is irRetryWalkOnlyTestBodies with a hook that
// sets up each case's context and answers its undo; a stdlib module's cases
// install their self type through it (stdtests.go).
func (g *gen) irRetryWalkOnlyTestBodiesWith(setup func(testCaseDecl) func()) {
	retries := g.irTestRetries
	g.irTestRetries = nil
	for i, r := range retries {
		g.irDeclineOpen("test body: " + r.name)
		g.at(r.c.line)
		undo := func() {}
		if setup != nil {
			undo = setup(r.c)
		}
		prevInTest, prevResult := g.inTest, g.result
		g.inTest, g.result = true, kindInvalid
		g.pushScope()
		p, ok := g.irTestBodyAttempt(r.c, r.name, r.group, true)
		g.popScope()
		g.inTest, g.result = prevInTest, prevResult
		undo()
		var fn *ir.Func
		if ok {
			fn = p.fn
		}
		if irTestBodyObserved != nil {
			irTestBodyObserved(r.name, fn)
		}
		if !ok {
			continue
		}
		// Earlier retries in this loop were declared before this one, and
		// every one of them preceded it in the file.
		at := r.at
		for _, prev := range retries[:i] {
			if prev.retained {
				at++
			}
		}
		retries[i].retained = true
		g.irTestBodyDeclare(at, r.name, fn, r.group)
	}
	// A retried body may have queued a generic function's instance, or a
	// generic type instance's impls, after the module flushed its own.
	g.flushInstanceQueues()
}

// flushInstanceQueues builds every queued generic function instance, generic
// type instance impl and method instance, until no queue grows.
func (g *gen) flushInstanceQueues() {
	for {
		impls, monos, methods, defaults := len(g.genericImplQueue), len(g.monoQueue), len(g.methodInstQueue), len(g.fieldDefaultQueue)
		g.flushMonoInstances()
		g.flushGenericImpls()
		g.flushMethodInsts()
		g.flushFieldDefaults()
		if impls == len(g.genericImplQueue) && monos == len(g.monoQueue) && methods == len(g.methodInstQueue) &&
			defaults == len(g.fieldDefaultQueue) {
			break
		}
	}
}

// instancesPending reports whether a queue holds work no flush has built:
// an instance another file's call asked this gen for after its own walk.
func (g *gen) instancesPending() bool {
	if g.genericImplsDone < len(g.genericImplQueue) {
		return true
	}
	for _, inst := range g.monoQueue {
		if !inst.emitted {
			return true
		}
	}
	for _, mi := range g.methodInstQueue {
		if !mi.built {
			return true
		}
	}
	for _, r := range g.fieldDefaultQueue {
		if !r.built {
			return true
		}
	}
	return false
}

// irFlushLateInstances builds the instances a file's call asked another
// file's gen for after that gen's own module walk had flushed, until no gen
// has any left. Building one can ask a third gen, or the first again, for
// more, hence the loop.
func irFlushLateInstances(gens []*gen) {
	for {
		built := false
		for _, g := range gens {
			if !g.instancesPending() {
				continue
			}
			built = true
			g.flushInstanceQueues()
		}
		if !built {
			return
		}
	}
}

// irTestBodyAttempt builds one case's body. walked admits what a body built
// for the VM only admits.
func (g *gen) irTestBodyAttempt(c testCaseDecl, name string, group ir.TestGroup, walked bool) (*irScalarPlan, bool) {
	at := g.irNodePos(c.body)
	fn := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: fn, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{},
		patternOK: true, at: c.body}
	sh.frame = newIRFuncFrame(fn)
	sh.entry = fn.NewBlock(at, "entry")
	// NO RESULT SLOT AND NO RESULT TEMPORARY, which is the one structural
	// difference from `irFuncShellFor`. A test body answers nothing: a
	// passing case is the ABSENCE of a failure and its only other exit is
	// the assertion's own, so there is no storage to declare. `sh.result` stays `ir.NoTemp` and nothing here reads it.
	bl := &irScalarBuilder{
		g: g, sh: sh, f: fn, b: sh.entry,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{},
		inTest: true, testApp: group.Boot != nil, testWalked: walked,
	}
	if scope := g.blockTypes[c.body]; scope != nil {
		// Types the body declares resolve by name for the extent of the
		// body, as they do for the front end (blocklocaltype.go).
		g.pushTypeScope(scope)
		defer g.popTypeScope()
	}
	// The body's deferred calls, and those of the `setup` frames around it,
	// run at the case's exit, most recent first: a setup's resource
	// stays alive for the case and is released after the body's own.
	bl.testDeferScope = &irDeferScope{block: c.body}
	// A `with` in the setup or the body holds to the case's end, which is
	// the activation's.
	bl.testWithScope = &irWithScope{root: true, saved: ir.NoTemp}
	if !bl.testSetupChain(c) {
		return nil, false
	}
	bl.testTail = true
	ok := bl.testStmts(c.body.Stmts)
	bl.testTail = false
	if !ok {
		return nil, false
	}
	bl.closeDefers(bl.testDeferScope, c.body.Stmts[len(c.body.Stmts)-1])
	// THE EXIT IS `ir.NewReturnUnit`, the valueless `Return` the
	// representation has always had and which no producer had built: every
	// retained `fn` returns its result slot. A test body answers nothing: a
	// passing case is the ABSENCE of a failure and its only other exit is
	// the assertion's own, so there is no storage to declare and nothing to
	// return. The machine answers
	// Unit, and what a test body's value IS has one answer: nothing reads it.
	bl.b.SetTerm(ir.NewReturnUnit(bl.lastPos(c)))
	return &irScalarPlan{fn: fn, result: kindUnit}, true
}

// testStmts lowers a run of test-body statements, each seeing the ones after
// it for testStagesRead.
//
// Only the last statement of a run in the body's value position is itself in
// that position (testTail); every earlier one is an ordinary statement.
func (bl *irScalarBuilder) testStmts(stmts []ast.Node) bool {
	tail := bl.testTail
	defer func() { bl.testTail = tail }()
	for i, s := range stmts {
		bl.testAfter = stmts[i+1:]
		bl.testTail = tail && i == len(stmts)-1
		if !bl.testStmt(s) {
			irDeclineNote(fmt.Sprintf("a test-body statement outside the shape: %T", s))
			return false
		}
		if _, returned := s.(*ast.Return); returned {
			// The rest of the run cannot execute.
			return true
		}
	}
	return true
}

// lastPos is the position the exit is blamed on: the body's final statement,
// which is where control leaves a case that passed.
func (bl *irScalarBuilder) lastPos(c testCaseDecl) ir.Pos {
	last := c.body.Stmts[len(c.body.Stmts)-1]
	if line, _ := nodePos(last); emittableLine(line) {
		return bl.g.irNodePos(last)
	}
	return bl.g.irNodePos(c.body)
}

// testStmt lowers one statement of a test body, reporting whether it is in the
// shape.
//
// THREE FORMS, which is `irScalarBody`'s two plus the assertion. There is no
// final-expression form: a test body's value is discarded, so the last
// statement is an ordinary statement rather than the tail `irScalarBuild`
// unwraps.
func (bl *irScalarBuilder) testStmt(s ast.Node) bool {
	line, _ := nodePos(s)
	if !emittableLine(line) {
		return false
	}
	switch st := s.(type) {
	case *ast.Assertion:
		return bl.assertion(st)
	case *ast.PatternDestructure:
		return bl.patternAssert(st)
	case *ast.PatternBinding:
		return bl.patternBindingStmt(st, nil)
	case *ast.Binding:
		return bl.testBinding(st)
	case *ast.TupleDestructure:
		return bl.tupleBinding(st)
	case *ast.StructDestructure:
		return bl.structBinding(st)
	case *ast.With:
		return bl.withStmt(st, bl.testWithScope)
	case *ast.MapDestructure:
		return bl.mapBinding(st)
	case *ast.DistinctDestructure:
		return bl.distinctBinding(st)
	case *ast.FuncDef:
		return bl.nestedFunc(st)
	case *ast.If, *ast.Case:
		return bl.testRegion(st)
	case *ast.Block:
		return bl.testBlock(st)
	case *ast.Defer:
		return bl.deferCall(st, bl.testDeferScope)
	case *ast.ImportStmt:
		return bl.testImport(st)
	case *ast.Return:
		return bl.testReturn(st)
	case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias:
		// A block-local type declaration runs nothing: its values' layout
		// and identity come from the typeDef blocklocaltype.go resolved.
		return true
	case *ast.ExprStmt:
		if isNilNode(st.Expr) {
			return false
		}
		switch e := st.Expr.(type) {
		case *ast.If, *ast.Case:
			return bl.testRegion(st.Expr)
		case *ast.Block:
			return bl.testBlock(e)
		}
		val, k, _, ok := bl.lower(st.Expr)
		if !ok {
			return false
		}
		if bl.testTail && bl.testVerdict(st.Expr, val, k) {
			return true
		}
		// `irScalarBuild`'s expression-statement arm, including the `dbg`
		// drop. The drop is gated on `irTempDefinedByCall`; see irdiscard.go.
		bl.irStatementDrop(st.Expr, val, k)
		return true
	}
	irDeclineNote(fmt.Sprintf("a test-body statement form: %T", s))
	return false
}

// testReturn lowers `return` in a test body: it ends the case. Its value, if
// any, is the body's final value (testVerdict), and a Return runs every
// pending deferred call on the way out, as it does for a function.
//
// Nothing after the return runs, so the statements after it in its run are
// not lowered (testStmts stops), and the builder continues in a fresh block
// no edge reaches: an enclosing arm's jump or the body's exit terminates it.
func (bl *irScalarBuilder) testReturn(r *ast.Return) bool {
	if bl.recording > 0 {
		irDeclineNote("a return in a test body the builder reads back")
		return false
	}
	if !isNilNode(r.Value) {
		val, k, _, ok := bl.lower(r.Value)
		if !ok {
			return false
		}
		if !bl.testVerdict(r.Value, val, k) {
			bl.irStatementDrop(r.Value, val, k)
		}
	}
	pos := bl.g.irNodePos(r)
	bl.b.SetTerm(ir.NewReturnUnit(pos))
	bl.b = bl.f.NewBlock(pos, "after-return")
	return true
}

// testVerdict makes a value-position statement's value (testTail) the case's
// verdict when its type is `Result<T, AssertionFailure>`: an `Err` fails the case with that
// failure's own report, as a failed `assert` would. A final statement that is
// itself an assertion never reaches here, so `refute testing.check(e)` passes.
//
// The exit is an `ir.Try` whose boundary is the test body: the VM's runner
// reads an AssertionFailure a try carried out of a test body back into the
// failure's report (internal/vm/testrun.go), the path `try testing.check(e)`
// already takes. Only an `Err` whose payload is an AssertionFailure fails the
// case, and this type is the only one whose `Err` carries one.
func (bl *irScalarBuilder) testVerdict(at ast.Node, val ir.Temp, k kind) bool {
	failure, anchored := assertionFailureKind()
	d := k.def
	spec := preludeSpecFor("std/results", "Result")
	if !anchored || spec == nil || d == nil || d.preludeOf == nil || d.preludeOf.spec != spec || len(d.variants) != 2 {
		return false
	}
	errVariant := &d.variants[len(d.variants)-1]
	if len(errVariant.payloads) != 1 || errVariant.payloads[0].k != failure {
		return false
	}
	bl.b.Append(ir.NewTry(bl.g.irNodePos(at), val, renderNode(at)))
	return true
}

// testRegion lowers an `if` or `case` STATEMENT in a test body: the shared
// branch builder, with each arm's statements lowered as test statements, so
// an arm may assert, bind and print. The statement's value is discarded, so an
// `if` may omit its `else` and the arms need not agree on a kind.
//
// Only in a body built for the VM only: the test-body reader spells one
// straight block. A region inside an assertion subject is the subject's own
// lowering.
func (bl *irScalarBuilder) testRegion(n ast.Node) bool {
	if bl.recording > 0 {
		irDeclineNote("an `if` or `case` statement in a test body the builder reads back")
		return false
	}
	sig := irFuncSig{testArms: true}
	var ok bool
	switch t := n.(type) {
	case *ast.If:
		_, ok = bl.ifRegion(t, sig)
	case *ast.Case:
		_, ok = bl.caseRegion(t, sig)
	}
	return ok
}

// testBlock lowers a block statement in a test body: its statements are test
// statements in their own lexical scope, its deferred calls run at its exit,
// and a `with` in it holds for the block alone.
func (bl *irScalarBuilder) testBlock(block *ast.Block) bool {
	if bl.recording > 0 || bl.g.blockTypes[block] != nil {
		irDeclineNote("a test-body block with declared types, the open block, or in a body the builder reads back")
		return false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defs, stages, after, outer, outerWith := bl.testDefs, bl.testStages, bl.testAfter, bl.testDeferScope, bl.testWithScope
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	bl.testDefs, bl.testStages = maps.Clone(defs), maps.Clone(stages)
	defer func() {
		bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms
		bl.testDefs, bl.testStages, bl.testAfter, bl.testDeferScope, bl.testWithScope = defs, stages, after, outer, outerWith
	}()
	scope := &irDeferScope{block: block}
	bl.testDeferScope = scope
	ws := &irWithScope{saved: ir.NoTemp}
	bl.testWithScope = ws
	if !bl.testStmts(block.Stmts) {
		return false
	}
	bl.closeDefers(scope, block)
	bl.closeWithScope(ws, block)
	return true
}

// testArm lowers one arm of a test-body statement region into the current
// block and jumps to exit. The arm is its own lexical scope:
// names it binds, and their "defined as:" records, end with it.
func (bl *irScalarBuilder) testArm(exit *ir.Block, body ast.Node) (kind, bool) {
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defs, stages, after, outer, outerWith := bl.testDefs, bl.testStages, bl.testAfter, bl.testDeferScope, bl.testWithScope
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	bl.testDefs, bl.testStages = maps.Clone(defs), maps.Clone(stages)
	defer func() {
		bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms
		bl.testDefs, bl.testStages, bl.testAfter, bl.testDeferScope, bl.testWithScope = defs, stages, after, outer, outerWith
	}()
	// The arm is a lexical scope: its deferred calls run at its exit, and a
	// `with` in it holds to its exit.
	scope := &irDeferScope{block: nil}
	bl.testDeferScope = scope
	ws := &irWithScope{saved: ir.NoTemp}
	bl.testWithScope = ws
	var stmts []ast.Node
	switch t := body.(type) {
	case *ast.Block:
		if bl.g.blockTypes[t] != nil {
			irDeclineNote("a test-body arm block with declared types, or the open block")
			return kindInvalid, false
		}
		stmts = t.Stmts
	case *ast.Assertion, *ast.PatternDestructure, *ast.Binding, *ast.If, *ast.Case, *ast.ExprStmt,
		// An arm that ends the case, `Err(_) -> return`, as a test statement does.
		*ast.Return:
		stmts = []ast.Node{t}
	default:
		// A case arm's expression body, lowered as the statement it is.
		line, col := nodePos(body)
		stmts = []ast.Node{&ast.ExprStmt{Expr: body, Line: line, Col: col}}
	}
	if !bl.testStmts(stmts) {
		return kindInvalid, false
	}
	bl.closeDefers(scope, body)
	bl.closeWithScope(ws, body)
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(body), exit.ID()))
	return kindUnit, true
}

// testBinding lowers `name = expr` inside a test body.
//
// An annotation is a coercion target with its own refusal paths, which a body
// the Go reader reads back declines and a VM-only body takes as a named
// function's body does. Repeated names also decline until assertion metadata
// follows the latest binding.
func (bl *irScalarBuilder) testBinding(b *ast.Binding) bool {
	if bl.inTest && ast.IsDiscardName(b.Name) {
		// `_ = expr` (or `_x: T = expr`) in a VM-only body: the value and its drop, as a named
		// function's body lowers it.
		src, k, ok := bl.bindingExpression(b)
		if !ok {
			return false
		}
		bl.irStatementDrop(b, src, k)
		bl.irDiscardStmtUnit(b)
		return true
	}
	if a, isAssert := b.Value.(*ast.Assertion); isAssert && b.TypeAnnotation == nil && !ast.IsDiscardName(b.Name) {
		return bl.testAssertionBinding(b, a)
	}
	if (b.TypeAnnotation != nil && !bl.inTest) || ast.IsDiscardName(b.Name) {
		irDeclineNote("an annotated or discarded test-body binding")
		return false
	}
	// A VM-only body may rebind a name it bound itself, as a named function's
	// body may: the new binding is a fresh identity, reads in its initializer
	// see the previous one, and a later assertion's "defined as:" block
	// follows the latest binding (testDefs is overwritten below). Shadowing a
	// module-level name still declines.
	_, shadows := bl.g.lookup(b.Name)
	rebind := bl.bound[b.Name] != ir.NoTemp
	if shadows || (rebind && !bl.inTest) {
		irDeclineNote("a test-body binding that shadows or rebinds: " + b.Name)
		return false
	}
	// A VM-only body's annotation is a coercion target exactly as in a named
	// function's body; bindingExpression is that path. A block-valued
	// binding takes a named function's typed block delivery.
	if rebind {
		// The previous binding's pipeline rows describe a value no later
		// assertion can name.
		delete(bl.testStages, b.Name)
	}
	var src ir.Temp
	var k kind
	var ok bool
	_, block := b.Value.(*ast.Block)
	_, conditional := b.Value.(*ast.If)
	_, match := b.Value.(*ast.Case)
	if prefixes := pipeStagePrefixes(b.Value); b.TypeAnnotation == nil && prefixes != nil && bl.testStagesRead(b.Name) {
		src, k, ok = bl.testPipeStages(b.Name, prefixes)
	} else if (block || conditional || match) && bl.inTest {
		// The checker's binding type is the region's destination, so an arm
		// answering `[]` takes the other arm's list type.
		src, k, ok = bl.bindingValue(b)
	} else {
		src, k, ok = bl.bindingExpression(b)
	}
	if !ok {
		return false
	}
	sym := bl.sh.localSym(b.Name)
	if rebind {
		// Reads in the initializer kept the previous identity; later reads
		// see this one.
		sym = ir.NewSymbol(b.Name)
		bl.sh.syms[b.Name] = sym
	}
	bind := ir.NewBind(bl.g.irPos(b.Line, b.Col), bl.f.NewTemp(), src, sym)
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: k})
	bl.bound[b.Name], bl.boundK[b.Name] = bind.Dst(), k
	if bl.testDefs == nil {
		bl.testDefs = map[string]*ast.Binding{}
	}
	bl.testDefs[b.Name] = b
	return true
}

// testAssertionBinding lowers `name = assert e` / `name = refute e`: the
// assertion, then a binding of its value, which is the judged subject.
func (bl *irScalarBuilder) testAssertionBinding(b *ast.Binding, a *ast.Assertion) bool {
	if _, shadows := bl.g.lookup(b.Name); shadows || bl.bound[b.Name] != ir.NoTemp {
		irDeclineNote("a test-body binding that shadows or rebinds: " + b.Name)
		return false
	}
	subj, k, ok := bl.assertionValue(a)
	if !ok {
		return false
	}
	bind := ir.NewBind(bl.g.irPos(b.Line, b.Col), bl.f.NewTemp(), subj, bl.sh.localSym(b.Name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: k})
	bl.bound[b.Name], bl.boundK[b.Name] = bind.Dst(), k
	if bl.testDefs == nil {
		bl.testDefs = map[string]*ast.Binding{}
	}
	bl.testDefs[b.Name] = b
	return true
}

// testStagesRead reports whether a later statement of the test body names
// this binding bare, which is when its report prints the binding's
// `pipeline values:` block.
func (bl *irScalarBuilder) testStagesRead(name string) bool {
	for _, s := range bl.testAfter {
		if assertsBareName(s, name) {
			return true
		}
	}
	return false
}

// testPipeStages lowers a pipe-valued binding one cumulative prefix at a time
// and keeps each prefix's value for the binding's `pipeline values:` block.
// Each later stage is the pipe with its left operand replaced by the previous
// prefix's held value, so every prefix is evaluated once. A chain whose stage
// list is not a function of its spine declines.
func (bl *irScalarBuilder) testPipeStages(name string, prefixes []ast.Node) (ir.Temp, kind, bool) {
	if !pipeStagesRecordable(prefixes) {
		irDeclineNote("a defined-as block over a pipe whose stages are not its spine")
		return ir.NoTemp, kindInvalid, false
	}
	cur, k, stages, ok := bl.pipeStageValues(prefixes)
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	if bl.testStages == nil {
		bl.testStages = map[string][]ir.AssertStage{}
	}
	bl.testStages[name] = stages
	return cur, k, true
}

// pipeStageValues lowers a recordable pipe one cumulative prefix at a time and
// answers the whole pipe's value with each prefix's held value.
func (bl *irScalarBuilder) pipeStageValues(prefixes []ast.Node) (ir.Temp, kind, []ir.AssertStage, bool) {
	var cur ir.Temp
	var k kind
	stages := make([]ir.AssertStage, 0, len(prefixes))
	for i, node := range prefixes {
		var v ir.Temp
		var mobile, ok bool
		if i == 0 {
			v, k, mobile, ok = bl.lower(node)
		} else {
			pipe := node.(*ast.Binary)
			line, col := nodePos(pipe.Left)
			hole := &ast.Ident{Name: fmt.Sprintf("|stage %d", cur), Line: line, Col: col}
			bl.bound[hole.Name], bl.boundK[hole.Name] = cur, k
			spliced := *pipe
			spliced.Left = hole
			v, k, mobile, ok = bl.pipe(&spliced)
			delete(bl.bound, hole.Name)
			delete(bl.boundK, hole.Name)
		}
		if !ok {
			return ir.NoTemp, kindInvalid, nil, false
		}
		if !mobile {
			// Each prefix is held once, before the next stage reads it.
			c := ir.NewCopy(bl.g.irNodePos(node), bl.f.NewTemp(), v)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyForce})
			v = c.Dst()
		}
		cur = v
		stages = append(stages, ir.AssertStage{Text: renderNode(node), Val: v})
	}
	return cur, k, stages, true
}

// assertion lowers `assert e` / `refute e` in statement position: the rows the
// report will print, then the judgement.
//
// THE ROWS ARE BUILT BY THE SUBJECT'S OWN LOWERING, not here. A comparison,
// a call's arguments and `irLogical`'s two branches each record their own
// operands, because which operands are worth showing is a property of the OPERATOR and
// not of the assertion. `bl.recording` is this builder's `g.traceVar`: the
// flag that says a subject is being lowered and the rows are live.
//
// A BARE BOUND NAME IS DECLINED, and it is the one decline that is about the
// REPORT rather than about the subject. See the file header.
func (bl *irScalarBuilder) assertion(t *ast.Assertion) bool {
	_, _, ok := bl.assertionValue(t)
	return ok
}

// assertionValue is assertion answering the subject's temporary, which is the
// assertion's value: `truth = assert True` binds True.
func (bl *irScalarBuilder) assertionValue(t *ast.Assertion) (ir.Temp, kind, bool) {
	if t.Check || isNilNode(t.Expr) {
		return ir.NoTemp, kindInvalid, false
	}
	bare, isBare := t.Expr.(*ast.Ident)
	if isBare {
		// A bare name this body bound. Its report carries the binding's
		// "defined as:" block, which the test-body reader does not spell, so
		// the body is retained for the VM only. Any other bare name declines.
		if _, local := bl.bound[bare.Name]; !local {
			return ir.NoTemp, kindInvalid, false
		}
	}
	kw := ir.KeywordAssert
	if t.Refute {
		kw = ir.KeywordRefute
	}
	witnessRow := bl.witnessRow
	bl.witnessRow = false
	bl.recording++
	subj, k, mobile, ok := bl.lower(t.Expr)
	bl.recording--
	if bl.witnessRow {
		irDeclineNote("an assertion row over a Type witness")
		return ir.NoTemp, kindInvalid, false
	}
	bl.witnessRow = witnessRow
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	// A `Maybe`/`Result` or `Assertable` subject is a different judgement —
	// a tag comparison or the value's own `failure` impl — and
	// `rt.AssertionSite` has a separate entry point for each. The VM reads
	// the shape off the value; an Assertable's verdict is its impl's answer,
	// called below. The test-body reader spells only JudgeBool.
	shape := irAssertShapeKind(k)
	var assertable *implItem
	if k != kindBool && !shape {
		if assertable = bl.assertableFailure(k); assertable == nil {
			irDeclineNote("an assertion subject that is not a Bool, Maybe, Result or retained Assertable")
			return ir.NoTemp, kindInvalid, false
		}
	}
	if !mobile {
		// The subject is named twice in the Go the reader spells: once in
		// `JudgeBool`'s argument list and once in the statement's own
		// `_ = <subject>` discard, so an impure one must be in a temporary.
		//
		// AFTER THE ROWS AND BEFORE THE `ir.Assert`: the records are built
		// inside the subject's own lowering, and the copy holds the value
		// that lowering answered.
		c := ir.NewCopy(bl.g.irNodePos(t.Expr), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyForce})
		subj = c.Dst()
	}
	a := ir.NewAssert(bl.g.irPos(t.Line, t.Col), ir.NoTemp, subj, kw, renderNode(t.Expr))
	if assertable != nil {
		a.WithAnswer(bl.assertableAnswer(t.Expr, assertable, subj))
	}
	if isBare {
		def, ok := bl.assertDefinedAs(bare, subj)
		if !ok {
			return ir.NoTemp, kindInvalid, false
		}
		if def != nil {
			a.WithBinding(*def)
		}
	}
	bl.b.Append(a)
	return subj, k, true
}

// record builds one `values:` row, if a subject is being lowered.
//
// `gen.recordOperand`'s counterpart, and the two differ in ONE thing: the
// builder renders the operand at build time into Go text
// (`gen.inspectCode`) and holds that text in the Record's temporary, while
// this holds the OPERAND ITSELF and leaves the rendering to the consumer.
// `ir.Record.Val`'s doc records both readings; the node's meaning is "the
// observed value" and a Temp naming its rendering is the Go reader's own
// realization, the way every entry in `irScalarSide` is.
//
// THE SUPPRESSION IS A REQUEST AND NOT AN ANSWER, which is `ir.Record`'s own
// rule: `rt.RecordOperand` applies "an operand that reads exactly like its
// value explains nothing", so `assert 1 == 2` prints no rows and this producer
// does not decide that.
func (bl *irScalarBuilder) record(at ast.Node, val ir.Temp, k kind, suppressRedundantLiteral bool) {
	if bl.recording == 0 || isNilNode(at) || val == ir.NoTemp {
		return
	}
	if _, witness := typeWitnessArg(k); witness {
		bl.witnessRow = true
		return
	}
	// THE ROW'S GO RENDERING IS THE READER'S, AND THE KIND DECIDES WHETHER
	// THE READER CAN SPELL IT WITHOUT MINTING AT BUILD TIME.
	//
	// `bl.g.inspector(k)` is not a pure query: for a NAMED kind it reaches
	// `namedInspector`, which mints `nomiInspect_X_N` through `gen.uniq` and
	// registers an inspector for the module to emit. `gen.uniq` is one
	// counter for every generated identifier, so asking it during the build
	// would renumber every later identifier in the file, breaking this
	// type's rule that it mints no Go identifier.
	//
	// SO THE CHECK IS A CLOSED PREDICATE OVER THE KIND and touches nothing.
	// It is narrower than `inspector` — a named type's row IS renderable,
	// and this declines the reader for it rather than claiming otherwise.
	// Retention is unaffected either way: `internal/vm` renders with
	// `rt.RowText`, which is total.
	r := ir.NewRecordOperand(bl.g.irNodePos(at), val, renderNode(at), suppressRedundantLiteral)
	bl.b.Append(r)
}

// recordCallArgs builds the `values:` rows a CALL inside an assertion subject
// contributes: `gen.recordCallArgs`.
//
// THE LITERAL RULE IS THE WHOLE CONTENT OF IT AND IT IS NOT SYMMETRIC. An
// ordinary call's literal arguments are noise — `assert double(3) == 7`
// explains nothing by printing `3 = 3` — while a call returning a BOOL is a
// PREDICATE whose literal inputs are precisely what the reader wants:
// `assert even?(5)` shows `5 = 5`. The builder decides it from the callee's
// declared return type.
//
// `skipAssertionArg` IS CALLED RATHER THAN RESTATED, so the classification of
// a lambda, a block and a type name has one home.
// recordCallSlots is recordCallArgs for a call whose written arguments were
// placed by argSlotPlan: `recordAssertionCallSlots` exactly. Positional
// arguments come first, each against the slot at its own position, except that
// a last positional lambda or block that moved reads the last slot; then named
// arguments in written order, each against its parameter's slot. A slot no
// written argument filled holds a default the callee supplies, and records
// nothing. vals is indexed by slot.
//
// A moved bare function name (`one_skip(double)`) therefore reads its OWN
// position's slot: that slot is a
// default, so the name records no row.
func (bl *irScalarBuilder) recordCallSlots(args []ast.Node, plan argPlan, names []string, vals []ir.Temp, params []kind, result kind) {
	if bl.recording == 0 {
		return
	}
	written := make(map[int]bool, len(plan.slots))
	for _, slot := range plan.slots {
		written[slot] = true
	}
	var positional []ast.Node
	var named []*ast.NamedArg
	for _, a := range args {
		if na, ok := a.(*ast.NamedArg); ok {
			named = append(named, na)
		} else {
			positional = append(positional, a)
		}
	}
	includeLiterals := result == kindBool
	recordAt := func(arg ast.Node, slot int) {
		if slot < 0 || slot >= len(vals) || slot >= len(params) || !written[slot] || skipAssertionArg(arg, includeLiterals) {
			return
		}
		bl.record(arg, vals[slot], params[slot], !includeLiterals)
	}
	for i, arg := range positional {
		slot := i
		if i == len(positional)-1 && i != len(params)-1 && len(positional) <= len(params) {
			switch arg.(type) {
			case *ast.Lambda, *ast.Block, *ast.FieldAccessor:
				slot = len(params) - 1
			}
		}
		recordAt(arg, slot)
	}
	for _, na := range named {
		slot := -1
		for j, name := range names {
			if name == na.Name {
				slot = j
			}
		}
		recordAt(na.Value, slot)
	}
}

func (bl *irScalarBuilder) recordCallArgs(args []ast.Node, vals []ir.Temp, params []kind, result kind) {
	if bl.recording == 0 {
		return
	}
	includeLiterals := result == kindBool
	for i, arg := range args {
		if i >= len(vals) || i >= len(params) || skipAssertionArg(arg, includeLiterals) {
			continue
		}
		bl.record(arg, vals[i], params[i], !includeLiterals)
	}
}
