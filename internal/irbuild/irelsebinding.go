package irbuild

// `Pattern = value [else { ... }]`, ast.PatternBinding: a binding whose
// pattern can fail, with the branch that runs when it does.
//
// The value is evaluated once and held. The else is lowered before the
// pattern's names are bound, so a name it reads is the enclosing one even when
// the pattern rebinds that spelling. There are two shapes.
//
// LEAVING. The pattern is tested as a `case` arm tests it (patternValueTest).
// The success edge binds its names and the rest of the block continues there.
// The failure edge runs the else, every path of which leaves: `return`, or a
// signalling callback's `break` or `continue` (the checker requires it).
//
// FALLBACK. The pattern is one variant with a payload whose own pattern always
// matches (`Some(email)`, `Ok((w, h))`, `.Valid{addr, score}`), so an else path
// may produce a value instead of leaving. A slot of the payload's kind is
// declared; the variant is tested; the success edge writes the projected
// payload into the slot and the else's value paths write their fallback into
// it; both join, and the inner pattern binds from the slot. Each name is one
// binding whichever edge reached the join.
//
// A binding with no else has a pattern that always matches (the checker
// requires it): the success edge alone, its failure edge a NoMatch no value
// reaches.
//
// Inside an inline `Iter.loop` body (lp non-nil) an else path's `break v`,
// `return v` and `continue` are the loop's, as a loop guard's are
// (loopElsePath).

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternBindingStmt lowers one PatternBinding statement, leaving bl.b where
// the statements after it continue.
func (bl *irScalarBuilder) patternBindingStmt(t *ast.PatternBinding, lp *irLoopBuild) bool {
	if isNilNode(t.Value) || isNilNode(t.Pattern) {
		return false
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
	pos := bl.g.irNodePos(t)
	cont := bl.f.NewBlock(bl.g.irNodePos(t.Pattern), "pattern matched")
	if t.Else == nil {
		nomatch := bl.f.NewBlock(pos, "pattern mismatch")
		nomatch.Append(ir.NewNoMatch(pos))
		nomatch.SetTerm(ir.NewJump(pos, cont.ID()))
		if !bl.patternValueTest(t.Pattern, subj, sk, cont, nomatch) {
			irDeclineNote("a pattern binding outside the retained case tests: " + sk.nomi())
			return false
		}
		bl.b = cont
		return true
	}
	if fb, ok := bl.elseFallback(t.Pattern, sk); ok && !(bl.inTest && elseOnlyLeaves(t)) {
		return bl.fallbackBinding(t, subj, sk, fb, cont, lp)
	}
	failed := bl.f.NewBlock(bl.g.irNodePos(t.Else), "pattern mismatch")
	outer := bl.saveScope()
	bl.cloneScope()
	if !bl.patternValueTest(t.Pattern, subj, sk, cont, failed) {
		irDeclineNote("a pattern binding outside the retained case tests: " + sk.nomi())
		return false
	}
	matched := bl.saveScope()
	bl.restoreScope(outer)
	// Every path of the else leaves, so none reaches its exit; the exit
	// joins the continuation only to terminate.
	exit := bl.f.NewBlock(pos, "else exit")
	sig := irFuncSig{result: kindUnit}
	if bl.inTest {
		sig = irFuncSig{testArms: true}
	}
	if !bl.bindingElse(t, subj, sk, failed, exit, sig, lp) {
		return false
	}
	exit.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.restoreScope(matched)
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
	// record marks a struct-shaped variant, whose payload is the record of
	// its fields.
	record bool
}

// elseFallback reports whether pattern is one variant of the subject's enum
// with a payload a fallback can stand in for. The checker decided the inner
// pattern always matches; this reads the variant the IR projects.
func (bl *irScalarBuilder) elseFallback(pattern ast.Node, k kind) (irElseFallback, bool) {
	if k.tag != tagNamed || k.def == nil || !irRetainedEnumKind(k.def) {
		return irElseFallback{}, false
	}
	var head ast.TypeExpr
	var fb irElseFallback
	switch p := pattern.(type) {
	case *ast.EnumPattern:
		if p.Binding == "" && p.Payload == nil {
			return irElseFallback{}, false
		}
		head, fb.inner, fb.binding = p.Variant, p.Payload, p.Binding
		if _, wild := p.Payload.(*ast.WildcardPattern); wild {
			fb.inner = nil
		}
	case *ast.StructPattern:
		if p.TypeName == nil {
			return irElseFallback{}, false
		}
		fields := *p
		fields.TypeName = nil
		head, fb.inner = p.TypeName, &fields
	default:
		return irElseFallback{}, false
	}
	owner, member, ok := patternHead(head)
	if !ok || (owner != "" && !bl.g.declaredAs(owner, k.def) && !bl.g.preludeOwns(owner, k.def) && !bl.g.stdEnumOwns(owner, k.def)) {
		return irElseFallback{}, false
	}
	fb.v = k.def.variant(member)
	switch {
	case fb.v == nil || irEmbedsEnum(fb.v):
		return irElseFallback{}, false
	case fb.v.kind == "struct":
		fb.record = true
		// `.V{a, b}`: the record's fields bind through the pattern's own
		// fields. `.V(r)` binds the record whole.
		sp, fields := fb.inner.(*ast.StructPattern)
		if !fields && fb.inner != nil {
			return irElseFallback{}, false
		}
		for _, f := range structFields(sp) {
			matches := false
			for _, part := range fb.v.payloads {
				if part.nomi == f.Name {
					matches = bl.g.irPatternAlwaysMatches(f.Pattern, part.k)
				}
			}
			if !matches {
				return irElseFallback{}, false
			}
		}
	case len(fb.v.payloads) != 1:
		return irElseFallback{}, false
	case fb.v.kind == "embedded":
		// An `embeds` payload is bound by name only.
		if fb.inner != nil {
			return irElseFallback{}, false
		}
	case !bl.g.irPatternAlwaysMatches(fb.inner, fb.v.payloads[0].k):
		// `Ok(Some(x))`: no single payload to stand in for, so every path
		// of the else leaves and the pattern is tested whole.
		return irElseFallback{}, false
	}
	return fb, true
}

// irPatternAlwaysMatches reports whether p matches every value of kind k: it
// only binds, through tuples, struct and record fields and wrapping distincts.
func (g *gen) irPatternAlwaysMatches(p ast.Node, k kind) bool {
	switch v := p.(type) {
	case nil, *ast.WildcardPattern, *ast.IdentPattern:
		return true
	case *ast.TuplePattern:
		if !irRetainedTupleKind(k) || len(v.Patterns) != len(k.comp.parts) {
			return false
		}
		for i, c := range v.Patterns {
			if !g.irPatternAlwaysMatches(c, k.comp.parts[i]) {
				return false
			}
		}
		return true
	case *ast.StructPattern:
		switch {
		case irRetainedRecordKind(k):
			if v.TypeName != nil {
				return false
			}
		case k.tag != tagNamed || k.def == nil || !irRetainedStructKind(k.def):
			return false
		}
		for _, f := range v.Fields {
			fk, ok := irFieldKind(k, f.Name)
			if !ok || !g.irPatternAlwaysMatches(f.Pattern, fk) {
				return false
			}
		}
		return true
	case *ast.EnumPattern:
		return g.distinctSubPattern(v, k)
	}
	return false
}

// structFields is a struct pattern's fields, none for a nil pattern.
func structFields(p *ast.StructPattern) []ast.StructPatternField {
	if p == nil {
		return nil
	}
	return p.Fields
}

// irFieldKind is the kind of a record's or struct's field.
func irFieldKind(k kind, name string) (kind, bool) {
	if irRetainedRecordKind(k) {
		for i, n := range k.comp.names {
			if n == name {
				return k.comp.parts[i], true
			}
		}
		return kindInvalid, false
	}
	if k.tag == tagNamed && k.def != nil {
		if fd := k.def.field(name); fd != nil {
			return fd.k, true
		}
	}
	return kindInvalid, false
}

// fallbackBinding lowers the FALLBACK shape.
func (bl *irScalarBuilder) fallbackBinding(t *ast.PatternBinding, subj ir.Temp, sk kind, fb irElseFallback, cont *ir.Block, lp *irLoopBuild) bool {
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
	var payload ir.Temp
	var pk kind
	if fb.record {
		var ok bool
		if payload, pk, ok = bl.variantRecord(t.Pattern, subj, d, fb.v); !ok {
			return false
		}
	} else {
		payload, pk = bl.variantPayload(t.Pattern, subj, d, fb.v), fb.v.payloads[0].k
		if fb.v.kind == "embedded" && irWrappingDistinct(fb.v.embeds) {
			// The checker's payload for an `embeds` of a wrapping distinct
			// is the distinct's inner value.
			payload = bl.distinctProjection(t.Pattern, payload, pk, false, "irelsebinding.go fallbackBinding")
			pk = fb.v.embeds.inner
		}
	}
	ty := bl.g.irTypeOf(pk)
	if ty == nil {
		irDeclineNote("a binding else whose payload kind has no IR type: " + pk.nomi())
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
	ok := bl.bindingElse(t, subj, sk, failed, join, irFuncSig{result: pk}, lp)
	bl.sh.result = outer
	if !ok {
		return false
	}

	// The inner pattern binds from the slot, which both edges wrote.
	bl.b = join
	switch {
	case fb.binding != "":
		bl.patternBinding(t.Pattern, fb.binding, slot, pk)
		join.SetTerm(ir.NewJump(pos, cont.ID()))
	case fb.inner == nil:
		join.SetTerm(ir.NewJump(pos, cont.ID()))
	default:
		nomatch := bl.f.NewBlock(pos, "payload mismatch")
		nomatch.Append(ir.NewNoMatch(pos))
		nomatch.SetTerm(ir.NewJump(pos, cont.ID()))
		if !bl.patternValueTest(fb.inner, slot, pk, cont, nomatch) {
			irDeclineNote("a binding else payload pattern outside the retained case tests: " + pk.nomi())
			return false
		}
	}
	bl.b = cont
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
	scope := bl.saveScope()
	defer bl.restoreScope(scope)
	for i := range t.Else.Arms {
		bl.restoreScope(scope)
		bl.cloneScope()
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
	scope := bl.saveScope()
	defer bl.restoreScope(scope)
	bl.cloneScope()
	bl.b = arm
	lead, tail := []ast.Node(nil), body
	if block, ok := body.(*ast.Block); ok {
		defer bl.g.enterBlockTypes(block)()
		if lead, tail = bl.g.irScalarBlock(block, ""); tail == nil {
			return false
		}
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
	for _, s := range lead {
		if leaves {
			bl.loopWithScope(lp)
		} else {
			bl.withScope = ws
		}
		if !bl.leading([]ast.Node{s}) || bl.b != arm {
			irDeclineNote("an Iter.loop binding else whose statements branch")
			return false
		}
	}
	var target *ir.Block
	switch s := tail.(type) {
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
		if sig.result == kindUnit || sig.testArms {
			irDeclineNote("an Iter.loop binding else path that neither leaves nor falls back")
			return false
		}
		val, k, _, ok := bl.lowerWant(tail, sig.result)
		if !ok || k != sig.result || bl.b != arm {
			irDeclineNote("an Iter.loop binding else fallback outside a straight value of the payload kind")
			return false
		}
		bl.resultCopy(tail, val)
		bl.closeWithScope(ws, tail)
		target = exit
	}
	if target == exit {
		arm.SetTerm(ir.NewJump(bl.g.irNodePos(tail), target.ID()))
		return true
	}
	bl.loopJump(arm, tail, lp, target)
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

// irLexicalScope is the builder's name environment, saved around a region
// whose names do not escape.
type irLexicalScope struct {
	bound  map[string]ir.Temp
	boundK map[string]kind
	syms   map[string]*ir.Symbol
}

func (bl *irScalarBuilder) saveScope() irLexicalScope {
	return irLexicalScope{bl.bound, bl.boundK, bl.sh.syms}
}

func (bl *irScalarBuilder) restoreScope(s irLexicalScope) {
	bl.bound, bl.boundK, bl.sh.syms = s.bound, s.boundK, s.syms
}

// cloneScope gives the builder copies of its name tables, so the names bound
// from here are dropped when the saved tables are restored.
func (bl *irScalarBuilder) cloneScope() {
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bl.bound), maps.Clone(bl.boundK), maps.Clone(bl.sh.syms)
}
