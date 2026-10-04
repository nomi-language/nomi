package irbuild

// Inline `Iter.loop` in retained named-function bodies.
//
// The graph follows `Iter.loop`'s semantics. The seed is lowered in the enclosing
// block and bound to the state; the answer slot is declared there too, and
// the block jumps to the loop head. The head binds the callback parameter
// from the state and declares the tail slot. The body is straight-line
// statements, guards and a tail expression:
//
//	if c { break v }     writes the answer and jumps to the exit
//	if c { return v }    writes the state and jumps back to the head
//	if c { continue }    jumps back to the head; the state is unchanged
//	tail expression      writes the tail slot, then the state, and jumps back
//
// A `case` tail writes the tail slot from each value arm and joins; an arm
// whose body is `break v` writes the answer and leaves for the exit, and the
// structured reader spells it as `break L` before the arm's own `break`. The
// join writes the state and jumps back.
//
// The state is an `ir.Bind` that later `ir.Copy`s overwrite, spelled in Go
// through the `irBindFresh` and `irCopyInto` pair. The VM executes the cycle
// with ordinary register writes.
//
// The structured reader spells the region as a labelled `for`. Loops in
// lambdas, test bodies, `once` initializers and assertion subjects, stateless
// and destructuring callbacks, empty seeds the checker did not solve, bare `break`, a final
// `break`, `continue` or `return`, `if` tails, `break` below a case arm's
// own body, and bodies whose statements branch are declined.

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
	// discarded is set when the loop's value is dropped, which is where a
	// bare `break` is admitted: it answers Unit (spec: `break` alone is
	// `break Unit` in a loop), and a dropped answer is never read.
	discarded bool
	// bare is set once a bare `break` was lowered.
	bare bool
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
	// A VM-only test body (one the read-back attempt declined) admits a loop
	// as a named function's body does.
	walked := bl.inTest
	if (!bl.loopsOK && !walked) || bl.parent != nil || (bl.recording > 0 && !walked) || !bl.g.iterOwns("Iter") {
		return no("an Iter.loop outside a retained named-function body")
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
	if isLambda && len(lam.Params) == 0 {
		return bl.statelessLoop(lam)
	}
	if !isLambda || len(lam.Params) != 1 {
		return no("an Iter.loop callback that is not a one-parameter lambda literal")
	}
	p := lam.Params[0]
	if p.Default == nil || p.Destructure != nil || ast.IsDiscardName(p.Name) {
		return no("an Iter.loop callback without a named, seeded state")
	}
	seed, sk, _, ok := bl.lower(p.Default)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	switch sk.tag {
	case tagEmptyList, tagEmptyMap, tagEmptySet:
		// An untyped empty seed takes the kind the checker solved for the
		// parameter and is coerced to it.
		_, solved := bl.g.inferredParamKind(p)
		// kindInvalid: lookup — asks whether the checker solved the seed's kind; a miss declines retention.
		if solved == kindInvalid {
			return no("an Iter.loop seed whose element type the checker did not solve")
		}
		if seed, sk, ok = bl.coerceEmpty(p.Default, seed, sk, solved); !ok {
			return no("an Iter.loop seed outside the retained empty-container coercions")
		}
	}
	if !irRetainedValueKind(sk) || (p.TypeAnnotation != nil && bl.g.typeOf(p.TypeAnnotation) != sk) {
		return no("an Iter.loop state outside the retained value domain: " + sk.nomi())
	}
	ty := bl.g.irTypeOf(sk)
	if ty == nil {
		return no("an Iter.loop state without an IR type: " + sk.nomi())
	}
	pos := bl.g.irNodePos(t)
	state := ir.NewBind(pos, bl.f.NewTemp(), seed, ir.NewSymbol(p.Name))
	bl.b.Append(state)
	bl.side(state.Dst(), irScalarSide{k: sk})
	answer := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, answer, ty))
	bl.side(answer, irScalarSide{k: sk})
	lp := &irLoopBuild{state: state.Dst(), answer: answer, k: sk,
		head: bl.f.NewBlock(pos, "loop head"), exit: bl.f.NewBlock(pos, "loop exit"),
		discarded: bl.discarded == ast.Node(t), scope: ir.NoTemp}
	// Nothing inside the callback is the discarded statement.
	prevDiscarded := bl.discarded
	bl.discarded = nil
	defer func() { bl.discarded = prevDiscarded }()
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
	sym := ir.NewSymbol(p.Name)
	bl.sh.syms[p.Name] = sym
	param := ir.NewBind(bl.g.irNodePos(lam), bl.f.NewTemp(), state.Dst(), sym)
	bl.b.Append(param)
	bl.side(param.Dst(), irScalarSide{k: sk})
	bl.bound[p.Name], bl.boundK[p.Name] = param.Dst(), sk
	tail := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(bl.g.irNodePos(lam), tail, ty))
	bl.side(tail, irScalarSide{k: sk})

	lead, final := bl.g.irScalarBlock(lam.Body, "")
	if final == nil {
		return no("an Iter.loop body outside the statement shape: " + irDeclineBodyWhy)
	}
	if !bl.loopBody(lead, final, lp, tail) {
		return ir.NoTemp, kindInvalid, false, false
	}
	if !lp.broke {
		return no("an Iter.loop without a break")
	}
	bl.b = lp.exit
	// After the `for` closes, the cursor returns to the loop call's line.
	bl.loops++
	if lp.bare {
		// A bare `break` left the answer unwritten on its path. The loop's
		// value is dropped, so it answers Unit, the bare break's value.
		u := ir.NewUnit(pos, bl.f.NewTemp())
		bl.b.Append(u)
		bl.side(u.Dst(), irScalarSide{k: kindUnit})
		return u.Dst(), kindUnit, true, true
	}
	return answer, sk, true, true
}

// loopBody lowers a loop callback's statements: guards, straight
// statements, and a tail. A final `return v` is the next state, as a tail
// value is; a final `if c { break v } else { e }` is the guard
// `if c { break v }` followed by e.
func (bl *irScalarBuilder) loopBody(lead []ast.Node, final ast.Node, lp *irLoopBuild, tail ir.Temp) bool {
	for _, s := range lead {
		if guard, isIf := s.(*ast.If); isIf {
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
		block := bl.b
		bl.loopWithScope(lp)
		if !bl.leading([]ast.Node{s}) {
			return false
		}
		if bl.b != block {
			irDeclineNote("an Iter.loop body statement that branches")
			return false
		}
	}
	switch f := final.(type) {
	case *ast.Break:
		// `break v` ending the body: the loop's answer, on this iteration.
		arm := bl.b
		if !bl.loopBreakValue(f, lp, arm) {
			irDeclineNote("an Iter.loop final `break` without a value of the state type")
			return false
		}
		bl.loopJump(arm, f, lp, lp.exit)
		lp.broke = true
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

// statelessLoop lowers `Iter.loop(|| { ...; break v })`: a callback with no
// state whose body is straight statements ending in `break`. It runs once
// and its break value (Unit for a bare `break`) is the loop's answer.
func (bl *irScalarBuilder) statelessLoop(lam *ast.Lambda) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	lead, final := bl.g.irScalarBlock(lam.Body, "")
	b, isBreak := final.(*ast.Break)
	if final == nil || !isBreak {
		return no("a stateless Iter.loop whose body does not end in `break`")
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	// The body runs once in the enclosing activation, so a `with` in it is
	// restored after its `break` value is computed.
	lp := &irLoopBuild{scope: ir.NoTemp}
	if irBodyWrites(lam.Body) {
		lp.scope = bl.f.NewTemp()
		bl.f.SetType(lp.scope, ir.NewHandleType(bl.g.irTypes().Symbol(irScopeHandle{}, "Scope")))
		bl.b.Append(ir.NewRefScope(bl.g.irNodePos(lam), lp.scope, bl.g.irTypes().Symbol(irScopeHandle{}, "$scope")))
	}
	restore := func() {
		if lp.scope != ir.NoTemp {
			bl.b.Append(ir.NewStoreScope(bl.g.irNodePos(b), bl.g.irTypes().Symbol(irScopeHandle{}, "$scope"), lp.scope))
		}
	}
	for _, s := range lead {
		block := bl.b
		bl.loopWithScope(lp)
		if _, isIf := s.(*ast.If); isIf || !bl.leading([]ast.Node{s}) || bl.b != block {
			return no("a stateless Iter.loop body statement that branches")
		}
	}
	if isNilNode(b.Value) {
		u := ir.NewUnit(bl.g.irNodePos(b), bl.f.NewTemp())
		bl.b.Append(u)
		bl.side(u.Dst(), irScalarSide{k: kindUnit})
		restore()
		return u.Dst(), kindUnit, true, true
	}
	v, k, mobile, ok := bl.lower(b.Value)
	if !ok || !irRetainedValueKind(k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	if lp.scope != ir.NoTemp && !mobile {
		// The value is held before the scope it was computed in ends.
		c := ir.NewCopy(bl.g.irNodePos(b.Value), bl.f.NewTemp(), v)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyForce})
		v = c.Dst()
	}
	restore()
	return v, k, mobile, true
}

// loopTail lowers a straight tail expression: it writes the tail slot, then
// the state, and jumps back to the head.
func (bl *irScalarBuilder) loopTail(final ast.Node, lp *irLoopBuild, tail ir.Temp) bool {
	switch final.(type) {
	case *ast.Return, *ast.Break, *ast.Continue, *ast.If:
		irDeclineNote("an Iter.loop body without a straight tail expression")
		return false
	}
	block := bl.b
	val, k, _, ok := bl.lower(final)
	if !ok {
		return false
	}
	if bl.b != block || k != lp.k {
		irDeclineNote("an Iter.loop tail that branches or is not the state type")
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
	outer, outerLoop := bl.sh.result, bl.loopArms
	bl.sh.result, bl.loopArms = tail, lp
	k, ok := bl.caseRegion(c, irFuncSig{result: lp.k})
	bl.sh.result, bl.loopArms = outer, outerLoop
	if !ok || k != lp.k {
		return false
	}
	bl.loopCopy(c, lp.state, tail)
	bl.loopJump(bl.b, c, lp, lp.head)
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
	bl.loopCopy(t, lp.state, tail)
	bl.loopJump(bl.b, t, lp, lp.head)
	return true
}

// loopBreakArm lowers a case arm's `break v` in a loop's case tail.
func (bl *irScalarBuilder) loopBreakArm(b *ast.Break, lp *irLoopBuild, arm *ir.Block) bool {
	if !bl.loopBreakValue(b, lp, arm) {
		return false
	}
	bl.loopJump(arm, b, lp, lp.exit)
	lp.broke = true
	return true
}

// loopGuard lowers one `if c { ...; break v | return v | continue }`
// statement inside a loop body. The false edge continues the body.
func (bl *irScalarBuilder) loopGuard(t *ast.If, lp *irLoopBuild) bool {
	if t.Else != nil || isNilNode(t.Cond) || t.Then == nil {
		return false
	}
	lead, exit := bl.g.irScalarBlock(t.Then, "")
	if exit == nil {
		return false
	}
	block := bl.b
	cond, ck, _, ok := bl.lower(t.Cond)
	if !ok || bl.b != block || (t.CondPattern == nil && ck != kindBool) {
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
		bl.loopWithScope(lp)
		if !bl.leading([]ast.Node{s}) || bl.b != arm {
			return false
		}
	}
	var target *ir.Block
	switch s := exit.(type) {
	case *ast.Break:
		if !bl.loopBreakValue(s, lp, arm) {
			return false
		}
		target, lp.broke = lp.exit, true
	case *ast.Return:
		if !bl.loopValue(s.Value, lp.state, lp.k, arm) {
			return false
		}
		target = lp.head
	case *ast.Continue:
		target = lp.head
	default:
		return false
	}
	bl.loopJump(arm, exit, lp, target)
	bl.b = next
	bl.irDiscardStmtUnit(t)
	return true
}

// loopBreakValue writes a `break`'s value into the loop's answer. A bare
// `break` answers Unit, which only a loop whose value is dropped admits: the
// answer slot holds the state type and nothing reads it.
func (bl *irScalarBuilder) loopBreakValue(b *ast.Break, lp *irLoopBuild, arm *ir.Block) bool {
	if isNilNode(b.Value) {
		if !lp.discarded {
			irDeclineNote("a bare `break` in an Iter.loop whose value is used")
			return false
		}
		lp.bare = true
		return bl.b == arm
	}
	return bl.loopValue(b.Value, lp.answer, lp.k, arm)
}

// loopValue lowers a `break` or `return` value of the state type and writes it
// into dst.
func (bl *irScalarBuilder) loopValue(value ast.Node, dst ir.Temp, k kind, arm *ir.Block) bool {
	if isNilNode(value) {
		return false
	}
	v, vk, _, ok := bl.lower(value)
	if !ok || vk != k || bl.b != arm {
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
