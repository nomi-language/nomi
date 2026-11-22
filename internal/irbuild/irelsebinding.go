package irbuild

// `Pattern = value [else { ... }]`, ast.PatternBinding: a binding whose
// pattern can fail, with the branch that runs when it does.
//
// The value is evaluated once and held. The else is lowered before the
// pattern's names are bound, so a name it reads is the enclosing one even when
// the pattern rebinds that spelling. There are two shapes.
//
// LEAVING. The pattern is tested as a `case` arm tests it (patternTest).
// The success edge binds its names and the rest of the block continues there.
// The failure edge runs the else, every path of which leaves: `return`, or a
// signalling callback's `break` or `continue` (the checker requires it).
//
// FALLBACK. The pattern is one variant with a payload whose own pattern always
// matches (`Some(email)`, `Ok((w, h))`, `.Valid{addr:, score:}`), so an else path
// may produce a value instead of leaving. A slot of the payload's kind is
// declared; the variant is tested; the success edge writes the projected
// payload into the slot and the else's value paths write their fallback into
// it; both join, and the inner pattern binds from the slot. Each name is one
// binding whichever edge reached the join.
//
// A binding with no else has a pattern that always matches (the checker
// requires it): the success edge alone, its failure edge a NoMatch no value
// reaches. A destructuring statement, `(a, _) = v`, `{x:, y: b} = v`,
// `Meters(n) = d` or `Box.Only(x) = b` over a one-variant enum, is such a
// binding.
//
// Inside an inline `Iter.loop` body (lp non-nil) an else path's `break v`,
// `return v` and `continue` are the loop's, as a loop guard's are
// (loopElsePath).

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternBindingStmt lowers one PatternBinding statement, leaving bl.b where
// the statements after it continue.
func (bl *irScalarBuilder) patternBindingStmt(t *ast.PatternBinding, lp *irLoopBuild) bool {
	if isNilNode(t.Value) || isNilNode(t.Pattern) {
		panic("irbuild: patternBindingStmt: the parser always sets a PatternBinding's Value and Pattern")
	}
	subj, sk, _, ok := bl.lower(t.Value)
	if !ok {
		return false
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		c := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: sk, copy: irCopyHold})
		subj = c.Dst()
	}
	if t.Else == nil {
		return bl.bindPattern(t.Pattern, subj, sk)
	}
	if fb, fallsBack := bl.elseFallback(t, sk); fallsBack && !(bl.inTest && elseOnlyLeaves(t)) {
		return bl.fallbackBinding(t, subj, sk, fb, lp)
	}
	pos := bl.g.irNodePos(t)
	failed := bl.f.NewBlock(bl.g.irNodePos(t.Else), "pattern mismatch")
	if !bl.patternTest(t.Pattern, subj, sk, failed) {
		return false
	}
	cont := bl.b
	// Every path of the else leaves, so none reaches its exit; the exit
	// joins the continuation only to terminate.
	exit := bl.f.NewBlock(pos, "else exit")
	sig := irFuncSig{result: kindUnit, leaves: true}
	if bl.inTest {
		sig = irFuncSig{testArms: true, leaves: true}
	}
	if !bl.bindingElse(t, subj, sk, failed, exit, sig, lp) {
		return false
	}
	exit.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = cont
	return true
}

// irElseFallback is the variant a fallback-shaped pattern names: the payload
// a fallback stands in for, and how the pattern binds from it.
type irElseFallback struct {
	v *variantDef
	// inner is the pattern the payload binds through; nil with binding set
	// for the `V(name)` fast path, and nil with binding empty for `V(_)`.
	inner   ast.Node
	binding string
}

// elseFallback reports whether a path of t's `else` may fall back to the
// payload of the one variant its pattern names, which the checker recorded
// (BindingFallbacks), and reads the variant the IR projects. A struct
// variant's fields (`.V{a:, b:}`) are matched as the record they are,
// which the record says always matches.
func (bl *irScalarBuilder) elseFallback(t *ast.PatternBinding, k kind) (irElseFallback, bool) {
	if payload, _ := bl.g.srcFA().BindingFallback(t); payload == nil {
		return irElseFallback{}, false
	}
	var head ast.TypeExpr
	var fb irElseFallback
	switch p := t.Pattern.(type) {
	case *ast.EnumPattern:
		head, fb.inner, fb.binding = p.Variant, p.Payload, p.Binding
		if _, wild := p.Payload.(*ast.WildcardPattern); wild {
			fb.inner = nil
		}
	case *ast.StructPattern:
		fields := *p
		fields.TypeName = nil
		head, fb.inner = p.TypeName, bl.g.carryPatternAnswer(&fields, true)
	default:
		panic("irbuild: elseFallback: the checker records a fallback only for a variant pattern, not a " + t.Pattern.NodeType())
	}
	member, named := bl.g.patternVariant(head)
	if !named || k.tag != tagNamed || k.def == nil || !irRetainedEnumKind(k.def) {
		panic("irbuild: elseFallback: the checker records a fallback only for a pattern naming a variant of the subject's enum, over " + k.nomi())
	}
	fb.v = k.def.variant(member)
	switch {
	case fb.v == nil:
		panic("irbuild: elseFallback: " + k.nomi() + " has no variant " + member)
	case fb.v.kind != "struct" && len(fb.v.payloads) != 1:
		panic("irbuild: elseFallback: the checker records a fallback only for a variant with one payload, not " + member)
	}
	return fb, true
}

// fallbackBinding lowers the FALLBACK shape.
func (bl *irScalarBuilder) fallbackBinding(t *ast.PatternBinding, subj ir.Temp, sk kind, fb irElseFallback, lp *irLoopBuild) bool {
	pos := bl.g.irNodePos(t)
	d := sk.def
	matched := bl.f.NewBlock(bl.g.irNodePos(t.Pattern), "variant matched")
	failed := bl.f.NewBlock(bl.g.irNodePos(t.Else), "pattern mismatch")
	join := bl.f.NewBlock(pos, "else join")
	match := bl.variantTest(t.Pattern, subj, d, fb.v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	head := bl.b
	// The payload is read on the success edge, which decides the slot's kind.
	bl.b = matched
	payload, pk, ok := bl.variantPatternValue(t.Pattern, subj, d, fb.v)
	if !ok {
		return false
	}
	ty := bl.g.irTypeOf(pk)
	if ty == nil {
		bl.g.dc.note("a binding else whose payload kind has no IR type: " + pk.nomi())
		return false
	}
	slot := bl.f.NewTemp()
	head.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: pk})
	head.SetTerm(ir.NewBranch(bl.g.irNodePos(t.Pattern), match.Dst(), matched.ID(), failed.ID()))
	bl.b.Append(ir.NewCopy(bl.g.irNodePos(t.Pattern), slot, payload))
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(t.Pattern), join.ID()))

	outer := bl.sh.result
	bl.sh.result = slot
	ok = bl.bindingElse(t, subj, sk, failed, join, irFuncSig{result: pk}, lp)
	bl.sh.result = outer
	if !ok {
		return false
	}

	// The inner pattern binds from the slot, which both edges wrote.
	bl.b = join
	switch {
	case fb.binding != "":
		bl.patternBinding(t.Pattern, fb.binding, slot, pk)
	case fb.inner != nil:
		return bl.bindPattern(fb.inner, slot, pk)
	}
	return true
}

// bindingElse lowers the else into failed: the block, or the arms tested
// against subj as a `case`'s are. A path that produces a value writes
// bl.sh.result and jumps to exit.
func (bl *irScalarBuilder) bindingElse(t *ast.PatternBinding, subj ir.Temp, sk kind, failed, exit *ir.Block, sig irFuncSig, lp *irLoopBuild) bool {
	prev := bl.b
	defer func() { bl.b = prev }()
	if block := t.Else.Block; block != nil {
		return bl.elsePath(failed, exit, block, sig, lp)
	}
	bl.b = failed
	nomatch := bl.f.NewBlock(bl.g.irNodePos(t.Else), "nomatch")
	nomatch.Append(ir.NewNoMatch(bl.g.irNodePos(t.Else)))
	nomatch.SetTerm(ir.NewJump(bl.g.irNodePos(t.Else), exit.ID()))
	for i := range t.Else.Arms {
		br := &t.Else.Arms[i]
		last := i == len(t.Else.Arms)-1
		arm, next, ok := bl.caseArmTest(t.Else, br, false, subj, sk, last, nomatch)
		if !ok {
			return false
		}
		if !bl.elsePath(arm, exit, br.Body, sig, lp) {
			return false
		}
		if next == nil {
			break
		}
	}
	return true
}

// elsePath lowers one path of an else, a block or an arm's body, into arm.
func (bl *irScalarBuilder) elsePath(arm, exit *ir.Block, body ast.Node, sig irFuncSig, lp *irLoopBuild) bool {
	if lp != nil {
		return bl.loopElsePath(arm, exit, body, sig, lp)
	}
	_, ok := bl.armInto(arm, exit, body, sig)
	return ok
}

// loopElsePath lowers an else path inside an inline `Iter.loop` body: straight
// statements, then the loop's `break v`, `return v` (the next state) or
// `continue`, as loopGuard lowers a guard's arm, or a fallback value.
func (bl *irScalarBuilder) loopElsePath(arm, exit *ir.Block, body ast.Node, sig irFuncSig, lp *irLoopBuild) bool {
	prev := bl.b
	defer func() { bl.b = prev }()
	bl.b = arm
	lead, tail := []ast.Node(nil), body
	if block, ok := body.(*ast.Block); ok {
		defer bl.g.enterBlockTypes(block)()
		lead, tail = bl.g.irScalarBlock(block)
	}
	// A path that leaves for the head or the exit restores the loop's scope
	// on the way out, so a `with` in it ends there. A fallback path rejoins
	// the body: it is a block scope of its own, read before its first `with`
	// and restored after its value is written.
	_, isBreak := tail.(*ast.Break)
	_, isReturn := tail.(*ast.Return)
	_, isContinue := tail.(*ast.Continue)
	leaves := isBreak || isReturn || isContinue
	var ws *irWithScope
	if !leaves {
		outer := bl.withScope
		defer func() { bl.withScope = outer }()
		ws = bl.openWithScope(false)
	}
	var pathDefers *irDeferScope
	if leaves {
		// A leaving path is a block of its own, as a loop guard's arm is: a
		// `defer` in it runs as it leaves the iteration (loopJump).
		pathDefers = &irDeferScope{outer: bl.openDefers}
		bl.openDefers = pathDefers
		defer func() { bl.openDefers = pathDefers.outer }()
	}
	for _, s := range lead {
		if leaves {
			bl.loopWithScope(lp)
			bl.deferScope = pathDefers
		} else {
			bl.withScope = ws
		}
		// A statement may branch and join; the path goes on from its join.
		if !bl.leading([]ast.Node{s}) {
			return false
		}
	}
	var target *ir.Block
	switch s := bl.g.asTodo(tail).(type) {
	case *ast.Break:
		if !bl.loopBreakValue(s, lp) {
			return false
		}
		target, lp.broke = lp.exit, true
	case *ast.Return:
		if !bl.loopValue(s, s.Value, lp.state, lp.k) {
			return false
		}
		target = lp.head
	case *ast.Continue:
		target = lp.head
	case *ast.Todo, *ast.Unreachable:
		// The trap ends the path: it writes no fallback and reaches
		// neither the head nor the exit.
		if _, _, _, ok := bl.lowerNever(tail); !ok {
			return false
		}
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(tail), bl.b.ID()))
		return true
	case *ast.If, *ast.Case:
		// A region tail: each of its arms leaves as a loop arm does
		// (armInto) or writes the fallback into the payload slot.
		if sig.testArms {
			bl.g.dc.note("an Iter.loop binding else in a test body whose tail is a region")
			return false
		}
		var k kind
		var ok bool
		if t, isIf := s.(*ast.If); isIf {
			k, ok = bl.ifRegion(t, sig)
		} else {
			k, ok = bl.caseRegion(s.(*ast.Case), sig)
		}
		if !ok {
			return false
		}
		if k == kindDiverged || !ir.Reachable(bl.f)[bl.b.ID()] {
			// Every arm left the iteration, so the region's exit is
			// unreachable and the path writes nothing.
			bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(tail), bl.b.ID()))
			return true
		}
		// The region wrote the fallback, Unit for a Unit payload
		// (`Ok(v) = r else Unit` over a `Result<Unit, E>`). An else that
		// is not a fallback leaves on every path, which the checker
		// requires.
		if sig.leaves {
			bl.g.dc.note("an Iter.loop binding else path that neither leaves nor falls back")
			return false
		}
		bl.closeWithScope(ws, tail)
		target = exit
	default:
		if sig.leaves {
			bl.g.dc.note("an Iter.loop binding else path that neither leaves nor falls back")
			return false
		}
		val, _, _, ok := bl.lowerWant(tail, sig.result)
		if !ok {
			return false
		}
		bl.resultCopy(tail, val)
		bl.closeWithScope(ws, tail)
		target = exit
	}
	if target == exit {
		// The value may branch (`a and b`); the jump leaves from its join.
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(tail), target.ID()))
		return true
	}
	// A `break` or `return` value may branch (`a and b`); the jump leaves
	// from its join.
	bl.loopJump(bl.b, tail, lp, target)
	return true
}

// elseOnlyLeaves reports whether every path of t's else ends, as written, in
// `return`, `break` or `continue`. A test body lowers such an else as test
// statements, whose `return` ends the case.
func elseOnlyLeaves(t *ast.PatternBinding) bool {
	leaves := func(n ast.Node) bool {
		if block, ok := n.(*ast.Block); ok {
			if len(block.Stmts) == 0 {
				return false
			}
			n = block.Stmts[len(block.Stmts)-1]
			if es, ok := n.(*ast.ExprStmt); ok {
				n = es.Expr
			}
		}
		switch n.(type) {
		case *ast.Return, *ast.Break, *ast.Continue:
			return true
		}
		return false
	}
	if t.Else.Block != nil {
		return leaves(t.Else.Block)
	}
	for _, arm := range t.Else.Arms {
		if !leaves(arm.Body) {
			return false
		}
	}
	return true
}
