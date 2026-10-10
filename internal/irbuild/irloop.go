package irbuild

// Inline `Iter.loop` in any retained body.
//
// The graph follows `Iter.loop`'s semantics. The seed is lowered in the enclosing
// block and bound to the state; the answer slot is declared there too, and
// the block jumps to the loop head. The head binds the callback parameter
// from the state and declares the tail slot. The body is statements, guards
// and a tail expression:
//
//	if c { break v }     writes the answer and jumps to the exit
//	if c { return v }    writes the state and jumps back to the head
//	if c { continue }    jumps back to the head; the state is unchanged
//	tail expression      writes the tail slot, then the state, and jumps back
//
// While the body is lowered the builder's `loop` is set, so every `break`,
// `return` and `continue` in it is the loop's: a guard anywhere in the body
// is one of the above, and an `if` or `case` arm ending in one leaves the
// iteration the same way while the region's other arms join. A `case` or `if`
// tail writes the tail slot from each value arm; the join writes the state
// and jumps back.
//
// The state is an `ir.Bind` that later `ir.Copy`s overwrite. The VM executes
// the cycle with ordinary register writes.
//
// A loop in a lambda lowers in the lambda's own function, as one in a named
// function does; the lambda's own `return`, `break` and `continue` are not
// reachable from the callback, since `loop` is set while it lowers. A loop
// in a `once` initializer, a boot body or a default accessor lowers the same
// way, in that body's own function. Empty seeds the checker did not solve
// are declined.
//
// A destructuring state (`|(n, acc): (Int, Int) = (5, 1)|`) binds the
// pattern's names at the head from the state, each iteration, as a pattern
// binding with no `else` does. The checker admits only an irrefutable
// pattern there, so the mismatch path is unreachable; it traps as that
// binding's does.
//
// A bare `break` stops with the current state: it writes the state into the
// answer and jumps to the exit, as `break <state>` would.
//
// A loop with no `break` (analysis.LoopNeverBreaks) never reaches its exit.
// irScalarBlock makes it the tail of its block, dropping the statements after
// it, and endlessLoop lowers it there, ending the exit in a jump to itself.
//
// A stateless loop (`Iter.loop(|| …)`) has state Unit: it lowers the same
// way with a Unit seed and binds no name in the body. Its tail may be a
// statement, an `if` with no `else` included, whose Unit value leaves the
// state as it was.

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irLoopBuild is the storage and blocks one loop's control statements target.
type irLoopBuild struct {
	state, answer ir.Temp
	head, exit    *ir.Block
	k             kind
	broke         bool
	// scope is the running scope read at the head when the callback's body
	// holds a `with`, or ir.NoTemp: every jump back to the head or out to the
	// exit restores it, so an override ends with the iteration that made it.
	scope ir.Temp
}

// irBodyWrites reports whether a callback body holds a `with` statement in
// its own statements or a block nested in them, not counting the bodies of
// lambdas it builds, which are activations of their own.
func irBodyWrites(body ast.Node) bool {
	found := false
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if found || isNilNode(n) {
			return
		}
		switch t := n.(type) {
		case *ast.With:
			found = true
		case *ast.Lambda, *ast.FuncDef:
			return
		case *ast.Block:
			for _, s := range t.Stmts {
				walk(s)
			}
		case *ast.ExprStmt:
			walk(t.Expr)
		case *ast.If:
			walk(t.Then)
			walk(t.Else)
		case *ast.Case:
			for _, br := range t.Branches {
				walk(br.Body)
			}
		case *ast.PatternBinding:
			walk(t.Else)
		}
	}
	walk(body)
	return found
}

// loopWithScope opens the scope the next `leading` run of a loop body lowers
// its `with` statements in: the loop's jumps restore what they replace.
func (bl *irScalarBuilder) loopWithScope(lp *irLoopBuild) {
	if lp != nil && lp.scope != ir.NoTemp {
		bl.openWithScope(true)
	}
}

// loopJump ends block b with a jump to target, restoring the scope read at
// the loop's head first when the body holds a `with`.
func (bl *irScalarBuilder) loopJump(b *ir.Block, at ast.Node, lp *irLoopBuild, target *ir.Block) {
	if lp.scope != ir.NoTemp {
		b.Append(ir.NewStoreScope(bl.g.irNodePos(at), bl.g.irTypes().Symbol(irScopeHandle{}, "$scope"), lp.scope))
	}
	b.SetTerm(ir.NewJump(bl.g.irNodePos(at), target.ID()))
}

// iterLoop lowers `Iter.loop(|p = seed| body)` and answers its answer slot.
func (bl *irScalarBuilder) iterLoop(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	// The loop's blocks go into the builder's own function, whatever body
	// that function is: a named function, a test, a `once` initializer, a
	// boot, a default accessor, or a lambda nested in any of them. Inside an
	// assertion subject only a test body records the loop as one row.
	root := bl
	for root.parent != nil {
		root = root.parent
	}
	if (bl.recording > 0 && !root.inTest) || !bl.g.iterOwns("Iter") {
		return no("an Iter.loop inside a recorded assertion subject outside a test")
	}
	if bl.recording > 0 {
		// Inside a VM-only assertion subject the loop is one row, the
		// answer the enclosing operand records; the callback's seed and body
		// record nothing, as nothing is recorded inside a lambda.
		saved := bl.recording
		bl.recording = 0
		defer func() { bl.recording = saved }()
	}
	if len(t.Args) != 1 {
		return no("an Iter.loop call without one callback")
	}
	lam, isLambda := t.Args[0].(*ast.Lambda)
	if !isLambda || len(lam.Params) > 1 {
		return no("an Iter.loop callback that is not a lambda literal of at most one parameter")
	}
	stateless := len(lam.Params) == 0
	seed, sk, stateName := ir.NoTemp, kindUnit, "$state"
	var p *ast.Param
	if stateless {
		// A stateless loop (`Iter.loop(|| …)`) has state Unit: its seed is
		// Unit and its callback binds no name.
		u := ir.NewUnit(bl.g.irNodePos(lam), bl.f.NewTemp())
		bl.b.Append(u)
		bl.side(u.Dst(), irScalarSide{k: kindUnit})
		seed = u.Dst()
	} else {
		p = &lam.Params[0]
		if p.Default == nil {
			return no("an Iter.loop callback without a seeded state")
		}
		if p.Destructure == nil {
			stateName = p.Name
		}
		var ok bool
		if seed, sk, _, ok = bl.lower(p.Default); !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	switch sk.tag {
	case tagEmptyList, tagEmptyMap, tagEmptySet:
		// An untyped empty seed takes the kind the checker solved for the
		// parameter and is coerced to it.
		var ok bool
		var solved kind
		if p.Destructure != nil {
			solved = bl.g.irParamPatternKind(*p)
		} else {
			_, solved = bl.g.inferredParamKind(*p)
		}
		// kindInvalid: lookup — asks whether the checker solved the seed's kind; a miss declines retention.
		if solved == kindInvalid {
			return no("an Iter.loop seed whose element type the checker did not solve")
		}
		if seed, sk, ok = bl.coerceEmpty(p.Default, seed, sk, solved); !ok {
			return no("an Iter.loop seed outside the retained empty-container coercions")
		}
	}
	// A Unit state (`|u: Unit = Unit|`) is a loop run for its effects.
	if (!irRetainedValueKind(sk) && sk != kindUnit) || (p != nil && p.TypeAnnotation != nil && bl.g.typeOf(p.TypeAnnotation) != sk) {
		return no("an Iter.loop state outside the retained value domain: " + sk.nomi())
	}
	ty := bl.g.irTypeOf(sk)
	if ty == nil {
		return no("an Iter.loop state without an IR type: " + sk.nomi())
	}
	pos := bl.g.irNodePos(t)
	state := ir.NewBind(pos, bl.f.NewTemp(), seed, ir.NewSymbol(stateName))
	bl.b.Append(state)
	bl.side(state.Dst(), irScalarSide{k: sk})
	answer := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, answer, ty))
	bl.side(answer, irScalarSide{k: sk})
	lp := &irLoopBuild{state: state.Dst(), answer: answer, k: sk,
		head: bl.f.NewBlock(pos, "loop head"), exit: bl.f.NewBlock(pos, "loop exit"),
		scope: ir.NoTemp}
	bl.b.SetTerm(ir.NewJump(pos, lp.head.ID()))

	// The callback body is its own lexical scope.
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.b = lp.head
	if irBodyWrites(lam.Body) {
		lp.scope = bl.f.NewTemp()
		bl.f.SetType(lp.scope, ir.NewHandleType(bl.g.irTypes().Symbol(irScopeHandle{}, "Scope")))
		bl.b.Append(ir.NewRefScope(bl.g.irNodePos(lam), lp.scope, bl.g.irTypes().Symbol(irScopeHandle{}, "$scope")))
	}
	if p != nil && p.Destructure != nil {
		// `|(n, acc) = (5, 1)|`: the pattern's names, read off this
		// iteration's state.
		at := bl.g.irNodePos(p.Destructure)
		held := ir.NewBind(at, bl.f.NewTemp(), state.Dst(), ir.NewSymbol("$state"))
		bl.b.Append(held)
		bl.side(held.Dst(), irScalarSide{k: sk})
		cont := bl.f.NewBlock(at, "loop state bound")
		nomatch := bl.f.NewBlock(at, "loop state mismatch")
		nomatch.Append(ir.NewNoMatch(at))
		nomatch.SetTerm(ir.NewJump(at, cont.ID()))
		if !bl.patternValueTest(p.Destructure, held.Dst(), sk, cont, nomatch) {
			return no("an Iter.loop state pattern outside the retained pattern tests: " + sk.nomi())
		}
		bl.b = cont
	} else if p != nil && !ast.IsDiscardName(p.Name) {
		// A discard name (`|_u: Unit = Unit|`) binds nothing in the body.
		sym := ir.NewSymbol(p.Name)
		bl.sh.syms[p.Name] = sym
		param := ir.NewBind(bl.g.irNodePos(lam), bl.f.NewTemp(), state.Dst(), sym)
		bl.b.Append(param)
		bl.side(param.Dst(), irScalarSide{k: sk})
		bl.bound[p.Name], bl.boundK[p.Name] = param.Dst(), sk
	}
	tail := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(bl.g.irNodePos(lam), tail, ty))
	bl.side(tail, irScalarSide{k: sk})

	defer bl.g.enterBlockTypes(lam.Body)()
	lead, final := bl.g.irScalarBlock(lam.Body, "")
	if final == nil {
		return no("an Iter.loop body outside the statement shape: " + irDeclineBodyWhy)
	}
	outerLoop := bl.loop
	bl.loop = lp
	lowered := bl.loopBody(lead, final, lp, tail)
	bl.loop = outerLoop
	if !lowered {
		return ir.NoTemp, kindInvalid, false, false
	}
	if !lp.broke && bl.endless != t {
		// Nothing reaches the exit, and whatever reads the loop's value
		// would be lowered into it. endlessLoop admits the loop only where
		// nothing after it is lowered.
		return no("an Iter.loop without a break, in a position that reads its value")
	}
	bl.b = lp.exit
	return answer, sk, true, true
}

// loopBody lowers a loop callback's statements: guards, straight
// statements, and a tail. A final `return v` is the next state, as a tail
// value is; a final `if c { break v } else { e }` is the guard
// `if c { break v }` followed by e.
func (bl *irScalarBuilder) loopBody(lead []ast.Node, final ast.Node, lp *irLoopBuild, tail ir.Temp) bool {
	for _, s := range lead {
		if guard, isIf := s.(*ast.If); isIf && loopGuardShape(bl.g, guard) {
			if !bl.loopGuard(guard, lp) {
				irDeclineNote("an Iter.loop guard outside `if c { break v | return v | continue }`")
				return false
			}
			continue
		}
		if pb, ok := s.(*ast.PatternBinding); ok {
			// Its else's `break`, `return` and `continue` are the loop's.
			if !bl.patternBindingStmt(pb, lp) {
				return false
			}
			continue
		}
		// A region in the statement (`x = case c { 0 -> continue  _ -> 1
		// }`) may branch: an arm ending in `break v`, `return v` or
		// `continue` leaves the iteration, and the others join.
		bl.loopWithScope(lp)
		if !bl.leading([]ast.Node{s}) {
			return false
		}
	}
	// A block whose tail leaves ending the body: its statements, then its
	// tail as the body's.
	final, restore, ok := bl.leavingBlocks(final)
	defer restore()
	if !ok {
		return false
	}
	if endless, ok := bl.endlessLoop(final); endless {
		// An inner `Iter.loop` with no `break` ending the body: this
		// iteration never ends, and writes no next state.
		return ok
	}
	switch f := final.(type) {
	case *ast.Break:
		// `break v` ending the body: the loop's answer, on this iteration.
		if !bl.loopBreakValue(f, lp) {
			irDeclineNote("an Iter.loop final `break` without a value of the state type")
			return false
		}
		bl.loopJump(bl.b, f, lp, lp.exit)
		lp.broke = true
		return true
	case *ast.Continue:
		// `if c { break v } else { continue }`: round again with the state
		// unchanged.
		bl.loopJump(bl.b, f, lp, lp.head)
		return true
	case *ast.Case:
		if !bl.loopCase(f, lp, tail) {
			irDeclineNote("an Iter.loop case tail outside value and `break v` arms")
			return false
		}
		return true
	case *ast.Return:
		if isNilNode(f.Value) {
			return false
		}
		return bl.loopTail(f.Value, lp, tail)
	case *ast.If:
		if f.Else == nil && lp.k == kindUnit {
			// An `if` without `else` ending a Unit-state body, as a
			// stateless loop's may: a statement, then the Unit tail.
			return bl.loopBody([]ast.Node{f}, &ast.TypeIdent{Name: "Unit", Line: f.Line, Col: f.Col}, lp, tail)
		}
		if f.Else == nil || f.CondPattern != nil || f.Then == nil {
			break
		}
		if _, exit := bl.g.irScalarBlock(f.Then, ""); exit == nil {
			return false
		} else {
			switch exit.(type) {
			case *ast.Break, *ast.Return, *ast.Continue:
			default:
				// `if c { a } else { b }` as the next state: a value
				// region writing the tail slot, as a `case` tail is.
				return bl.loopIf(f, lp, tail)
			}
		}
		guard := *f
		guard.Else = nil
		if !bl.loopGuard(&guard, lp) {
			irDeclineNote("an Iter.loop guard outside `if c { break v | return v | continue }`")
			return false
		}
		switch e := f.Else.(type) {
		case *ast.Block:
			defer bl.g.enterBlockTypes(e)()
			elseLead, elseFinal := bl.g.irScalarBlock(e, "")
			if elseFinal == nil {
				return false
			}
			return bl.loopBody(elseLead, elseFinal, lp, tail)
		case *ast.If:
			return bl.loopBody(nil, e, lp, tail)
		}
		return false
	}
	return bl.loopTail(final, lp, tail)
}

// loopTail lowers a tail expression: it writes the tail slot, then
// the state, and jumps back to the head.
func (bl *irScalarBuilder) loopTail(final ast.Node, lp *irLoopBuild, tail ir.Temp) bool {
	switch final.(type) {
	case *ast.Return, *ast.Break, *ast.Continue, *ast.If:
		irDeclineNote("an Iter.loop body without a straight tail expression")
		return false
	}
	// A region in the tail (`f(case c { 0 -> continue  _ -> 1 })`) joins
	// before the writes; its arms that leave the iteration never reach them.
	val, k, _, ok := bl.lower(final)
	if !ok {
		return false
	}
	if k != lp.k {
		irDeclineNote("an Iter.loop tail that is not the state type")
		return false
	}
	bl.loopCopy(final, tail, val)
	bl.loopCopy(final, lp.state, tail)
	bl.loopJump(bl.b, final, lp, lp.head)
	return true
}

// loopCase lowers a `case` tail into the tail slot: each value arm writes it
// and joins, and a `break v` arm writes the answer and
// leaves for the exit. The join advances the state and jumps back to the head.
func (bl *irScalarBuilder) loopCase(c *ast.Case, lp *irLoopBuild, tail ir.Temp) bool {
	outer := bl.sh.result
	bl.sh.result = tail
	k, ok := bl.caseRegion(c, irFuncSig{result: lp.k})
	bl.sh.result = outer
	if !ok || k != lp.k {
		return false
	}
	bl.loopAdvance(c, lp, tail)
	return true
}

// loopIf lowers an `if` tail whose arms are values into the tail slot; the
// join advances the state and jumps back to the head.
func (bl *irScalarBuilder) loopIf(t *ast.If, lp *irLoopBuild, tail ir.Temp) bool {
	outer := bl.sh.result
	bl.sh.result = tail
	k, ok := bl.ifRegion(t, irFuncSig{result: lp.k})
	bl.sh.result = outer
	if !ok || k != lp.k {
		irDeclineNote("an Iter.loop `if` tail outside value arms of the state type")
		return false
	}
	bl.loopAdvance(t, lp, tail)
	return true
}

// loopAdvance closes a region tail's join: the tail slot becomes the next
// state and the iteration goes round. When every arm of the region left the
// iteration (`case c { True -> break n  False -> continue }`), no arm wrote
// the tail slot and no path reaches the join, so the join reads nothing and
// jumps to itself, as exitReturn leaves an unreachable function exit.
func (bl *irScalarBuilder) loopAdvance(at ast.Node, lp *irLoopBuild, tail ir.Temp) {
	if !ir.Reachable(bl.f)[bl.b.ID()] {
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(at), bl.b.ID()))
		return
	}
	bl.loopCopy(at, lp.state, tail)
	bl.loopJump(bl.b, at, lp, lp.head)
}

// loopGuardShape reports whether t is a loop guard: an `if` with no `else`
// whose then-block ends in `break`, `return` or `continue`.
func loopGuardShape(g *gen, t *ast.If) bool {
	if t.Else != nil || t.Then == nil {
		return false
	}
	_, exit := g.irScalarBlock(t.Then, "")
	switch exit.(type) {
	case *ast.Break, *ast.Return, *ast.Continue:
		return true
	}
	return false
}

// loopBreakArm lowers a case arm's `break v` in a loop's case tail.
func (bl *irScalarBuilder) loopBreakArm(b *ast.Break, lp *irLoopBuild) bool {
	if !bl.loopBreakValue(b, lp) {
		return false
	}
	bl.loopJump(bl.b, b, lp, lp.exit)
	lp.broke = true
	return true
}

// loopGuard lowers one `if c { ...; break v | return v | continue }`
// statement inside a loop body. The false edge continues the body.
func (bl *irScalarBuilder) loopGuard(t *ast.If, lp *irLoopBuild) bool {
	if t.Else != nil || isNilNode(t.Cond) || t.Then == nil {
		return false
	}
	defer bl.g.enterBlockTypes(t.Then)()
	lead, exit := bl.g.irScalarBlock(t.Then, "")
	if exit == nil {
		return false
	}
	// The condition may branch (`a and b`, a `case` operand); the guard
	// tests it at the block where its value is joined.
	cond, ck, _, ok := bl.lower(t.Cond)
	if !ok || (t.CondPattern == nil && ck != kindBool) {
		return false
	}
	arm := bl.f.NewBlock(bl.g.irNodePos(t.Then), "loop guard")
	next := bl.f.NewBlock(bl.g.irNodePos(t), "next")
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	if t.CondPattern != nil {
		// `if Some(v) = e { break v }`: the pattern's test, binding its
		// names on the arm's path, as a pattern `if` region does.
		if !bl.patternValueTest(t.CondPattern, cond, ck, arm, next) {
			return false
		}
	} else {
		br := ir.NewBranch(bl.g.irNodePos(t.Cond), cond, arm.ID(), next.ID())
		bl.b.SetTerm(br)
	}
	bl.b = arm
	for _, s := range lead {
		// A statement in the arm may branch and join; the arm goes on from
		// its join.
		bl.loopWithScope(lp)
		if !bl.leading([]ast.Node{s}) {
			return false
		}
	}
	var target *ir.Block
	switch s := exit.(type) {
	case *ast.Break:
		if !bl.loopBreakValue(s, lp) {
			return false
		}
		target, lp.broke = lp.exit, true
	case *ast.Return:
		if !bl.loopValue(s.Value, lp.state, lp.k) {
			return false
		}
		target = lp.head
	case *ast.Continue:
		target = lp.head
	default:
		return false
	}
	bl.loopJump(bl.b, exit, lp, target)
	bl.b = next
	bl.irDiscardStmtUnit(t)
	return true
}

// loopBreakValue writes a `break`'s value into the loop's answer, in the
// current block. A bare `break` stops with the current state: the state is
// written only where an iteration ends, so here it still holds the value the
// callback's parameter was bound from.
func (bl *irScalarBuilder) loopBreakValue(b *ast.Break, lp *irLoopBuild) bool {
	if isNilNode(b.Value) {
		bl.loopCopy(b, lp.answer, lp.state)
		return true
	}
	return bl.loopValue(b.Value, lp.answer, lp.k)
}

// loopValue lowers a `break` or `return` value of the state type and writes it
// into dst. The value may branch (`a and b`); the write is at its join, the
// current block afterwards, and the caller jumps from there.
func (bl *irScalarBuilder) loopValue(value ast.Node, dst ir.Temp, k kind) bool {
	if isNilNode(value) {
		return false
	}
	v, vk, _, ok := bl.lower(value)
	if !ok || vk != k {
		return false
	}
	bl.loopCopy(value, dst, v)
	return true
}

// loopCopy writes existing loop storage, recording the completed operand
// cursor for the write's line directive.
func (bl *irScalarBuilder) loopCopy(at ast.Node, dst, src ir.Temp) {
	cp := ir.NewCopy(bl.g.irNodePos(at), dst, src)
	bl.b.Append(cp)
}
